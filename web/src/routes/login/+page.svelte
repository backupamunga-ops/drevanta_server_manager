<script lang="ts">
        import { goto } from '$app/navigation';
        import { ApiError, fetchAccessStatus, fetchSession, postJSON, type LoginResult } from '$lib/api';
        import { accessLabelTitle } from '$lib/access.js';

        // Two-step sign-in: credentials first, then the authenticator-code
        // screen. The backend drives this — a verified password with MFA on
        // answers 401 totp_required, which moves us to the second screen
        // (it is a challenge, not an error).
        type Step = 'credentials' | 'mfa';
        let step = $state<Step>('credentials');
        let username = $state('');
        let password = $state('');
        let totp = $state('');
        let showPassword = $state(false);
        let busy = $state(false);
        let error = $state('');

        // Trusted access mode from the daemon (never the browser URL): the
        // subtitle is honest about how this console is being reached.
        let accessTitle = $state('');
        $effect(() => {
                fetchAccessStatus()
                        .then((a) => (accessTitle = accessLabelTitle(a)))
                        .catch(() => (accessTitle = ''));
        });

        // Focus the code field when the second screen appears.
        let codeInput = $state<HTMLInputElement | null>(null);
        $effect(() => {
                if (step === 'mfa') codeInput?.focus();
        });

        function backToCredentials() {
                step = 'credentials';
                totp = '';
                error = '';
        }

        async function submit(e: SubmitEvent) {
                e.preventDefault();
                busy = true;
                error = '';
                try {
                        const res = await postJSON<LoginResult>('/api/v1/auth/login', {
                                username,
                                password,
                                totp: totp || undefined
                        });
                        if (res.scope === 'onboarding') {
                                await goto('/onboarding');
                        } else {
                                await goto('/');
                        }
                } catch (err) {
                        if (err instanceof ApiError) {
                                if (err.code === 'totp_required') {
                                        // Password verified — advance to the code screen.
                                        step = 'mfa';
                                        totp = '';
                                } else if (err.code === 'instance_unclaimed') {
                                        error = 'No administrator exists yet. Use the claim token from the installer via the onboarding wizard.';
                                } else {
                                        // invalid_credentials / totp_invalid / recovery_invalid /
                                        // rate_limited — show on whichever step we are on.
                                        error = err.message;
                                }
                        } else {
                                error = String(err);
                        }
                } finally {
                        busy = false;
                }
        }

        // Already signed in? Route to the right place. A fresh (unclaimed)
        // instance belongs to the onboarding wizard.
        $effect(() => {
                fetchSession()
                        .then((s) => {
                                if (s.instance_status === 'unclaimed') {
                                        goto('/onboarding', { replaceState: true });
                                } else if (s.authenticated && s.scope === 'full') {
                                        goto('/', { replaceState: true });
                                } else if (s.authenticated) {
                                        goto('/onboarding', { replaceState: true });
                                }
                        })
                        .catch(() => undefined);
        });
</script>

<svelte:head>
        <title>Sign in — Drevanta</title>
</svelte:head>

<div class="auth-wrap">
        <div class="auth-card">
                {#if step === 'credentials'}
                        <h1 class="auth-title">Sign in</h1>
                        <p class="auth-sub">{accessTitle || "Access is limited to the instance administrator."}</p>

                        {#if error}
                                <div class="error" role="alert">{error}</div>
                        {/if}

                        <form class="form-col" onsubmit={submit}>
                                <div class="field">
                                        <label for="username">Username</label>
                                        <input id="username" name="username" autocomplete="username" required bind:value={username} />
                                </div>
                                <div class="field">
                                        <label for="password">Password</label>
                                        <div class="pw-wrap">
                                                <input
                                                        id="password"
                                                        name="password"
                                                        type={showPassword ? 'text' : 'password'}
                                                        autocomplete="current-password"
                                                        required
                                                        bind:value={password}
                                                />
                                                <button
                                                        type="button"
                                                        class="pw-toggle"
                                                        aria-pressed={showPassword}
                                                        aria-label={showPassword ? 'Hide password' : 'Show password'}
                                                        onclick={() => (showPassword = !showPassword)}
                                                >{showPassword ? 'Hide' : 'Show'}</button>
                                        </div>
                                </div>
                                <button class="btn-primary" type="submit" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</button>
                        </form>
                {:else}
                        <h1 class="auth-title">Two-step verification</h1>
                        <p class="auth-sub">Enter the 6-digit code from your authenticator app for <strong>{username}</strong>, or one of your single-use recovery codes.</p>

                        {#if error}
                                <div class="error" role="alert">{error}</div>
                        {/if}

                        <form class="form-col" onsubmit={submit}>
                                <div class="field">
                                        <label for="totp">Authenticator code</label>
                                        <input
                                                id="totp"
                                                name="totp"
                                                bind:this={codeInput}
                                                bind:value={totp}
                                                inputmode="numeric"
                                                autocomplete="one-time-code"
                                                pattern="[0-9]*"
                                                maxlength="12"
                                                placeholder="123456 or recovery code"
                                        />
                                        <span class="hint">6-digit code, or one of your single-use recovery codes.</span>
                                </div>
                                <button class="btn-primary" type="submit" disabled={busy}>{busy ? 'Verifying…' : 'Verify and sign in'}</button>
                                <button class="btn-ghost" type="button" onclick={backToCredentials}>Back</button>
                        </form>
                {/if}
        </div>
</div>

<style>
        .form-col {
                display: flex;
                flex-direction: column;
                gap: 14px;
        }

        .pw-wrap {
                position: relative;
        }

        /* Scoped compound selector wins over the global .field input rule.
           width:100% — .pw-wrap is a plain div, not a flex container, so the
           input would otherwise keep its intrinsic ~20ch width. */
        .pw-wrap input {
                width: 100%;
                padding-right: 64px;
        }

        .pw-toggle {
                position: absolute;
                right: 8px;
                top: 50%;
                transform: translateY(-50%);
                background: transparent;
                border: none;
                color: var(--text-dim);
                font-size: 12px;
                text-transform: uppercase;
                letter-spacing: 0.06em;
                cursor: pointer;
                padding: 4px 6px;
        }

        .pw-toggle:hover {
                color: var(--text);
        }

        .pw-toggle:focus-visible {
                outline: 2px solid var(--accent);
                outline-offset: 2px;
        }
</style>
