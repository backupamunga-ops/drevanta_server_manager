<script lang="ts">
        import { goto } from '$app/navigation';
        import { ApiError, fetchAccessStatus, fetchSession, postJSON, type LoginResult } from '$lib/api';
        import { accessLabelTitle } from '$lib/access.js';

        let username = $state('');
        let password = $state('');
        let totp = $state('');
        let totpRequired = $state(false);
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
                                        totpRequired = true;
                                        error = 'This account has MFA enabled — enter the 6-digit code from your authenticator app.';
                                } else if (err.code === 'instance_unclaimed') {
                                        error = 'No administrator exists yet. Use the claim token from the installer via the onboarding wizard.';
                                } else {
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
                                <input id="password" name="password" type="password" autocomplete="current-password" required bind:value={password} />
                        </div>
                        {#if totpRequired}
                                <div class="field">
                                        <label for="totp">Authenticator code</label>
                                        <input id="totp" name="totp" inputmode="numeric" pattern="[0-9]*" maxlength="12" placeholder="123456 or recovery code" bind:value={totp} />
                                        <span class="hint">6-digit code, or one of your single-use recovery codes.</span>
                                </div>
                        {/if}
                        <button class="btn-primary" type="submit" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</button>
                </form>
        </div>
</div>

<style>
        .form-col {
                display: flex;
                flex-direction: column;
                gap: 14px;
        }
</style>
