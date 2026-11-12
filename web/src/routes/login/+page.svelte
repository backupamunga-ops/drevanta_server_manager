<script lang="ts">
        import { goto } from '$app/navigation';
        import {
                ApiError,
                changeAccountPassword,
                fetchAccessStatus,
                fetchSession,
                forgotPassword,
                postJSON,
                resetPassword,
                recoveryLogin,
                type LoginResult
        } from '$lib/api';
        import { accessLabelTitle } from '$lib/access.js';

        // Sign-in steps:
        //   credentials -> mfa            (the normal two-step flow)
        //   credentials -> recovery       (username + single-use recovery code)
        //   recovery    -> newpass        (limited session: set a new password)
        //   credentials -> forgot         (email reset link request)
        //   hash #reset= -> reset         (arriving from the email link)
        type Step = 'credentials' | 'mfa' | 'recovery' | 'forgot' | 'newpass' | 'reset';
        let step = $state<Step>('credentials');
        let username = $state('');
        let password = $state('');
        let totp = $state('');
        let showPassword = $state(false);
        let busy = $state(false);
        let error = $state('');
        let info = $state('');

        // Recovery-code sign-in + forced password set.
        let recIdentifier = $state('');
        let recCode = $state('');
        let newPw = $state('');
        let newPw2 = $state('');

        // Forgot-password request.
        let forgotIdent = $state('');
        let forgotDone = $state(false);

        // Hash reset link.
        let resetToken = $state('');
        let resetPw = $state('');
        let resetPw2 = $state('');

        const newPwErr = $derived.by(() => {
                if (newPw !== '' && newPw.length < 12) return 'Use at least 12 characters.';
                if (newPw2 !== '' && newPw2 !== newPw) return 'Passwords do not match yet.';
                return '';
        });
        const resetPwErr = $derived.by(() => {
                if (resetPw !== '' && resetPw.length < 12) return 'Use at least 12 characters.';
                if (resetPw2 !== '' && resetPw2 !== resetPw) return 'Passwords do not match yet.';
                return '';
        });

        // The pending two-step sign-in. sessionStorage (NOT localStorage:
        // a new tab/window must start fresh) survives the page reload that
        // mobile browsers trigger when the user switches to their
        // authenticator app and back.
        const MFA_STORE_KEY = 'drevanta_mfa_signin';
        let challenge = $state('');

        function loadMfaStore(): { challenge: string; username: string } | null {
                try {
                        const raw = sessionStorage.getItem(MFA_STORE_KEY);
                        if (!raw) return null;
                        const parsed = JSON.parse(raw) as { challenge?: string; username?: string };
                        if (!parsed?.challenge) return null;
                        return { challenge: parsed.challenge, username: parsed.username ?? '' };
                } catch {
                        return null;
                }
        }

        function storeMfa(m: { challenge: string; username: string }) {
                try {
                        sessionStorage.setItem(MFA_STORE_KEY, JSON.stringify(m));
                } catch {
                        /* private mode etc. — the flow still works without the reload guard */
                }
        }

        function clearMfa() {
                try {
                        sessionStorage.removeItem(MFA_STORE_KEY);
                } catch {
                        /* ignore */
                }
        }

        // Restore a pending two-step sign-in after a reload.
        $effect(() => {
                const saved = loadMfaStore();
                if (saved) {
                        username = saved.username;
                        challenge = saved.challenge;
                        step = 'mfa';
                }
        });

        // Arriving from a password-reset email: /login#reset=<token>.
        // Handles BOTH a fresh load and a same-document hash change (the
        // browser is often already sitting on /login when the link is
        // opened — then no reload happens and only hashchange fires).
        $effect(() => {
                const apply = () => {
                        const m = location.hash.match(/reset=([A-Za-z0-9_-]+)/);
                        if (m) {
                                resetToken = m[1];
                                step = 'reset';
                        }
                };
                apply();
                window.addEventListener('hashchange', apply);
                return () => window.removeEventListener('hashchange', apply);
        });

        // Trusted access mode from the daemon (never the browser URL): the
        // subtitle is honest about how this console is being reached.
        let accessTitle = $state('');
        $effect(() => {
                fetchAccessStatus()
                        .then((a) => (accessTitle = accessLabelTitle(a)))
                        .catch(() => (accessTitle = ''));
        });

        // Focus the primary field whenever the step changes.
        let codeInput = $state<HTMLInputElement | null>(null);
        let recIdentInput = $state<HTMLInputElement | null>(null);
        let forgotInput = $state<HTMLInputElement | null>(null);
        let newPwInput = $state<HTMLInputElement | null>(null);
        let resetPwInput = $state<HTMLInputElement | null>(null);
        $effect(() => {
                if (step === 'mfa') codeInput?.focus();
                if (step === 'recovery') recIdentInput?.focus();
                if (step === 'forgot') forgotInput?.focus();
                if (step === 'newpass') newPwInput?.focus();
                if (step === 'reset') resetPwInput?.focus();
        });

        function toCredentials(msg = '') {
                clearMfa();
                challenge = '';
                step = 'credentials';
                totp = '';
                error = msg;
                info = '';
        }

        async function routeAfterLogin(res: LoginResult) {
                if (res.scope === 'onboarding') {
                        await goto('/onboarding');
                } else {
                        await goto('/');
                }
        }

        async function submit(e: SubmitEvent) {
                e.preventDefault();
                busy = true;
                error = '';
                info = '';
                try {
                        if (step === 'mfa') {
                                const res = await postJSON<LoginResult>('/api/v1/auth/login/mfa', {
                                        challenge,
                                        code: totp
                                });
                                clearMfa();
                                challenge = '';
                                await routeAfterLogin(res);
                                return;
                        }
                        if (step === 'recovery') {
                                const res = await recoveryLogin(recIdentifier.trim(), recCode.trim());
                                username = res.user.username;
                                recCode = '';
                                step = 'newpass';
                                return;
                        }
                        if (step === 'forgot') {
                                const res = await forgotPassword(forgotIdent.trim());
                                info = res.message;
                                forgotDone = true;
                                return;
                        }
                        if (step === 'newpass') {
                                if (newPwErr !== '') {
                                        error = newPwErr;
                                        return;
                                }
                                const res = await changeAccountPassword('', newPw);
                                void res;
                                await goto('/');
                                return;
                        }
                        if (step === 'reset') {
                                if (resetPwErr !== '') {
                                        error = resetPwErr;
                                        return;
                                }
                                await resetPassword(resetToken, resetPw);
                                info = '';
                                resetToken = '';
                                resetPw = '';
                                resetPw2 = '';
                                step = 'credentials';
                                info = 'Password reset. Sign in with your new password.';
                                return;
                        }
                        const res = await postJSON<LoginResult>('/api/v1/auth/login', {
                                username,
                                password,
                                totp: totp || undefined
                        });
                        clearMfa();
                        await routeAfterLogin(res);
                } catch (err) {
                        if (err instanceof ApiError) {
                                if (err.code === 'totp_required') {
                                        // Password verified — advance to the code screen.
                                        challenge =
                                                typeof err.data?.['mfa_challenge'] === 'string'
                                                        ? (err.data['mfa_challenge'] as string)
                                                        : '';
                                        if (challenge) {
                                                storeMfa({
                                                        challenge,
                                                        username:
                                                                typeof err.data?.['username'] === 'string'
                                                                        ? (err.data['username'] as string)
                                                                        : username
                                                });
                                        }
                                        step = 'mfa';
                                        totp = '';
                                } else if (
                                        step === 'mfa' &&
                                        (err.code === 'challenge_invalid' || err.code === 'challenge_exhausted')
                                ) {
                                        toCredentials(err.message);
                                } else if (err.code === 'instance_unclaimed') {
                                        error =
                                                'No administrator exists yet. Use the claim token from the installer via the onboarding wizard.';
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
        // instance belongs to the onboarding wizard. Mid-flow steps are
        // skipped: the recovery-code flow holds an authenticated LIMITED
        // session whose only destination is the forced password screen —
        // redirecting it anywhere else broke the flow (and a reload of
        // that state resumes the password step instead).
        $effect(() => {
                if (step === 'reset' || step === 'newpass' || step === 'recovery') return;
                fetchSession()
                        .then((s) => {
                                if (s.instance_status === 'unclaimed') {
                                        goto('/onboarding', { replaceState: true });
                                } else if (s.authenticated && s.scope === 'full') {
                                        goto('/', { replaceState: true });
                                } else if (s.authenticated && s.scope === 'recovery') {
                                        step = 'newpass';
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
                        <p class="auth-sub">{accessTitle || 'Access is limited to the instance administrator.'}</p>

                        {#if error}
                                <div class="error" role="alert">{error}</div>
                        {/if}
                        {#if info}
                                <div class="notice" role="status">{info}</div>
                        {/if}

                        <form class="form-col" onsubmit={submit}>
                                <div class="field">
                                        <label for="username">Username or email</label>
                                        <input id="username" name="username" autocomplete="username" autocapitalize="off" spellcheck="false" required placeholder="username or you@example.com" bind:value={username} />
                                        <span class="hint">The administrator username, or the account email.</span>
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
                        <div class="alt-links">
                                <button class="linkbtn" type="button" onclick={() => { step = 'forgot'; error = ''; info = ''; }}>Forgot password?</button>
                                <button class="linkbtn" type="button" onclick={() => { step = 'recovery'; error = ''; info = ''; }}>Use a recovery code instead</button>
                        </div>
                {:else if step === 'mfa'}
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
                                <button class="btn-ghost" type="button" onclick={() => toCredentials()}>Back</button>
                        </form>
                {:else if step === 'recovery'}
                        <h1 class="auth-title">Recovery-code sign-in</h1>
                        <p class="auth-sub">Locked out of your password? Sign in with one of the single-use recovery codes you saved when MFA was enabled. You will choose a new password right after.</p>

                        {#if error}
                                <div class="error" role="alert">{error}</div>
                        {/if}

                        <form class="form-col" onsubmit={submit}>
                                <div class="field">
                                        <label for="rec-username">Username or email</label>
                                        <input id="rec-username" bind:this={recIdentInput} bind:value={recIdentifier} autocapitalize="off" spellcheck="false" required />
                                </div>
                                <div class="field">
                                        <label for="rec-code">Recovery code</label>
                                        <input id="rec-code" class="mono" bind:value={recCode} autocomplete="off" spellcheck="false" required placeholder="xxxxx-xxxxx" />
                                        <span class="hint">Each code works once. This replaces the password and the 6-digit code for this one sign-in.</span>
                                </div>
                                <button class="btn-primary" type="submit" disabled={busy}>{busy ? 'Checking…' : 'Continue'}</button>
                                <button class="btn-ghost" type="button" onclick={() => toCredentials()}>Back to sign in</button>
                        </form>
                {:else if step === 'forgot'}
                        <h1 class="auth-title">Forgot password</h1>
                        <p class="auth-sub">Enter your username or email. If the account has an email and mail delivery is configured, a single-use reset link is sent there.</p>

                        {#if error}
                                <div class="error" role="alert">{error}</div>
                        {/if}
                        {#if info}
                                <div class="notice" role="status">{info}</div>
                        {/if}

                        {#if !forgotDone}
                                <form class="form-col" onsubmit={submit}>
                                        <div class="field">
                                                <label for="forgot-ident">Username or email</label>
                                                <input id="forgot-ident" bind:this={forgotInput} bind:value={forgotIdent} autocapitalize="off" spellcheck="false" required />
                                        </div>
                                        <button class="btn-primary" type="submit" disabled={busy}>{busy ? 'Sending…' : 'Send reset link'}</button>
                                        <button class="btn-ghost" type="button" onclick={() => toCredentials()}>Back to sign in</button>
                                </form>
                        {:else}
                                <button class="btn-ghost" type="button" onclick={() => toCredentials()}>Back to sign in</button>
                        {/if}
                {:else if step === 'newpass'}
                        <h1 class="auth-title">Set a new password</h1>
                        <p class="auth-sub">Signed in with recovery code <strong>{username ? `for ${username}` : ''}</strong> — choose a new password to finish and secure the account again.</p>

                        {#if error}
                                <div class="error" role="alert">{error}</div>
                        {/if}

                        <form class="form-col" onsubmit={submit}>
                                <div class="field">
                                        <label for="new-pw">New password</label>
                                        <input id="new-pw" type="password" autocomplete="new-password" maxlength="128" bind:this={newPwInput} bind:value={newPw} />
                                </div>
                                <div class="field">
                                        <label for="new-pw2">Confirm new password</label>
                                        <input id="new-pw2" type="password" autocomplete="new-password" maxlength="128" bind:value={newPw2} />
                                        {#if newPwErr !== ''}<span class="hint error-hint">{newPwErr}</span>{/if}
                                </div>
                                <button class="btn-primary" type="submit" disabled={busy || newPw === '' || newPw2 === ''}>
                                        {busy ? 'Saving…' : 'Save and continue'}
                                </button>
                        </form>
                {:else if step === 'reset'}
                        <h1 class="auth-title">Choose a new password</h1>
                        <p class="auth-sub">This reset link is single-use and expires on its own. After the reset, every existing session is signed out.</p>

                        {#if error}
                                <div class="error" role="alert">{error}</div>
                        {/if}

                        <form class="form-col" onsubmit={submit}>
                                <div class="field">
                                        <label for="reset-pw">New password</label>
                                        <input id="reset-pw" type="password" autocomplete="new-password" maxlength="128" bind:this={resetPwInput} bind:value={resetPw} />
                                </div>
                                <div class="field">
                                        <label for="reset-pw2">Confirm new password</label>
                                        <input id="reset-pw2" type="password" autocomplete="new-password" maxlength="128" bind:value={resetPw2} />
                                        {#if resetPwErr !== ''}<span class="hint error-hint">{resetPwErr}</span>{/if}
                                </div>
                                <button class="btn-primary" type="submit" disabled={busy || resetPw === '' || resetPw2 === ''}>
                                        {busy ? 'Resetting…' : 'Reset password'}
                                </button>
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

        .alt-links {
                display: flex;
                justify-content: space-between;
                gap: 10px;
                flex-wrap: wrap;
        }

        .linkbtn {
                background: transparent;
                border: none;
                color: var(--text-dim);
                font-size: 13px;
                cursor: pointer;
                padding: 2px 0;
                text-decoration: underline;
                text-underline-offset: 3px;
        }

        .linkbtn:hover {
                color: var(--text);
        }

        .linkbtn:focus-visible {
                outline: 2px solid var(--accent);
                outline-offset: 2px;
        }

        .notice {
                background: rgba(52, 211, 153, 0.08);
                border: 1px solid rgba(52, 211, 153, 0.4);
                color: var(--accent);
                border-radius: 10px;
                padding: 10px 14px;
                font-size: 13px;
                margin-bottom: 16px;
        }

        .mono {
                font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
                letter-spacing: 0.06em;
        }

        .error-hint {
                color: var(--accent-crit);
        }
</style>
