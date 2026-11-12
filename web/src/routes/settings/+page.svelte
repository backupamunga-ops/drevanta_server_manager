<script lang="ts">
        import { goto } from '$app/navigation';
        import {
                ApiError,
                accountMfaSetup,
                accountMfaVerify,
                applyDomain,
                changeAccountPassword,
                checkDomain,
                fetchAccessStatus,
                fetchAccountMfa,
                fetchAccountProfile,
                fetchSession,
                fetchSmtp,
                removeDomain,
                saveSmtp,
                setAccountEmail,
                testSmtp,
                type AccessStatus,
                type AccountMfaStatus,
                type AccountProfile,
                type DomainCheckResult,
                type MfaSetup,
                type SmtpStatus
        } from '$lib/api';
        import { domainHint } from '$lib/access.js';
        import { qrSvg as renderQrCode } from '$lib/qr.js';

        type SessionInfoShape = Awaited<ReturnType<typeof fetchSession>>;

        let session = $state<SessionInfoShape | null>(null);
        let mfa = $state<AccountMfaStatus | null>(null);
        let access = $state<AccessStatus | null>(null);
        let loadError = $state('');

        // Account: email binding + password change.
        let profile = $state<AccountProfile | null>(null);
        let emailInput = $state('');
        let emailBusy = $state(false);
        let emailMsg = $state('');
        let emailSaveError = $state('');
        let pwCurrent = $state('');
        let pwNew = $state('');
        let pwConfirm = $state('');
        let pwBusy = $state(false);
        let pwMsg = $state('');
        let pwError = $state('');

        // Email delivery (SMTP): used for password-reset links and test mail.
        let smtp = $state<SmtpStatus | null>(null);
        let smtpEnabled = $state(false);
        let smtpHost = $state('');
        let smtpPort = $state(587);
        let smtpUser = $state('');
        let smtpPass = $state('');
        let smtpFrom = $state('');
        let smtpBusy = $state(false);
        let smtpMsg = $state('');
        let smtpError = $state('');
        let smtpTestBusy = $state(false);
        let smtpTestMsg = $state('');

        // Account Security: enable-MFA flow (same shape as onboarding).
        let mfaStage = $state<'idle' | 'setup' | 'verify' | 'codes'>('idle');
        let mfaSecret = $state('');
        let qrSvg = $state('');
        let mfaCode = $state('');
        let mfaBusy = $state(false);
        let mfaError = $state('');
        let recoveryCodes = $state<string[]>([]);
        let codesSaved = $state(false);

        // Access: custom-domain management (IP HTTPS is the default and is
        // never disturbed — the backend rolls the domain back on any failure).
        let domainInput = $state('');
        let domainTouched = $state(false);
        let domainBusy = $state(false);
        let domainStage = $state<'input' | 'checking' | 'applying'>('input');
        let domainMsg = $state('');
        let domainError = $state('');
        let removeBusy = $state(false);

        const domainErr = $derived(domainHint(domainInput));
        const currentDomain = $derived(access?.https?.domain?.host ?? '');

        const EMAIL_RE = /^[^@\s]+@[^@\s]+\.[^@\s]{2,}$/;
        // Empty is valid here: clearing the email is an explicit action.
        const emailErr = $derived.by(() => {
                const e = emailInput.trim();
                if (e === '') return '';
                if (e.length > 254) return 'Keep it to 254 characters or fewer.';
                if (!EMAIL_RE.test(e)) return 'That doesn\u2019t look like an email address.';
                return '';
        });
        const pwErr = $derived.by(() => {
                if (pwNew !== '' && pwNew.length < 12) return 'Use at least 12 characters.';
                if (pwConfirm !== '' && pwConfirm !== pwNew) return 'Passwords do not match yet.';
                return '';
        });

        $effect(() => {
                let cancelled = false;
                fetchSession()
                        .then((s) => {
                                if (cancelled) return;
                                session = s;
                                if (!s.authenticated) {
                                        goto('/login', { replaceState: true });
                                } else if (s.scope !== 'full') {
                                        goto('/onboarding', { replaceState: true });
                                } else {
                                        void reload();
                                }
                        })
                        .catch(() => {
                                if (!cancelled) goto('/login', { replaceState: true });
                        });
                return () => {
                        cancelled = true;
                };
        });

        async function reload() {
                loadError = '';
                try {
                        const [m, a, p, mail] = await Promise.all([
                                fetchAccountMfa(),
                                fetchAccessStatus(),
                                fetchAccountProfile(),
                                fetchSmtp()
                        ]);
                        mfa = m;
                        access = a;
                        profile = p;
                        emailInput = p.email;
                        smtp = mail;
                        smtpEnabled = mail.enabled;
                        smtpHost = mail.host;
                        smtpPort = mail.port || 587;
                        smtpUser = mail.username;
                        smtpFrom = mail.from_addr;
                        smtpPass = '';
                } catch (err) {
                        loadError = err instanceof Error ? err.message : String(err);
                }
        }

        async function submitEmail(e: SubmitEvent) {
                e.preventDefault();
                emailSaveError = '';
                emailMsg = '';
                if (emailErr !== '') return;
                emailBusy = true;
                try {
                        const res = await setAccountEmail(emailInput.trim());
                        emailMsg = res.email === '' ? 'Email removed — sign in with the username.' : `Email saved: ${res.email}`;
                        await reload();
                } catch (err) {
                        emailSaveError =
                                err instanceof ApiError && err.code === 'email_taken'
                                        ? 'That email is already in use.'
                                        : err instanceof ApiError
                                                ? err.message
                                                : String(err);
                } finally {
                        emailBusy = false;
                }
        }

        async function submitPassword(e: SubmitEvent) {
                e.preventDefault();
                pwError = '';
                pwMsg = '';
                if (pwErr !== '') return;
                pwBusy = true;
                try {
                        const res = await changeAccountPassword(pwCurrent, pwNew);
                        pwMsg =
                                res.other_sessions_ended > 0
                                        ? `Password changed. ${res.other_sessions_ended} other session${res.other_sessions_ended === 1 ? '' : 's'} ended.`
                                        : 'Password changed.';
                        pwCurrent = '';
                        pwNew = '';
                        pwConfirm = '';
                } catch (err) {
                        pwError =
                                err instanceof ApiError && err.code === 'invalid_credentials'
                                        ? 'The current password is incorrect.'
                                        : err instanceof ApiError
                                                ? err.message
                                                : String(err);
                } finally {
                        pwBusy = false;
                }
        }

        async function submitSmtp(e: SubmitEvent) {
                e.preventDefault();
                smtpError = '';
                smtpMsg = '';
                try {
                        await saveSmtp({
                                enabled: smtpEnabled,
                                host: smtpHost.trim(),
                                port: Number(smtpPort) || 587,
                                username: smtpUser.trim(),
                                password: smtpPass,
                                from_addr: smtpFrom.trim()
                        });
                        smtpMsg = 'Mail delivery settings saved.';
                        await reload();
                } catch (err) {
                        smtpError = err instanceof ApiError ? err.message : String(err);
                }
        }

        async function sendSmtpTest() {
                smtpTestMsg = '';
                smtpError = '';
                smtpTestBusy = true;
                try {
                        const res = await testSmtp();
                        smtpTestMsg = res.ok
                                ? `Test message sent to ${res.ok ? smtpFrom.trim() || 'the From address' : ''}. Check the inbox.`
                                : `Send failed: ${res.error ?? 'unknown error'}`;
                } catch (err) {
                        smtpTestMsg = `Send failed: ${err instanceof ApiError ? err.message : String(err)}`;
                } finally {
                        smtpTestBusy = false;
                }
        }

        async function beginMfaSetup() {
                mfaBusy = true;
                mfaError = '';
                try {
                        const res: MfaSetup = await accountMfaSetup();
                        mfaSecret = res.secret;
                        qrSvg = await renderQrCode(res.otpauth_uri);
                        mfaCode = '';
                        mfaStage = 'setup';
                } catch (err) {
                        mfaError = err instanceof ApiError ? err.message : String(err);
                } finally {
                        mfaBusy = false;
                }
        }

        async function submitMfaVerify(e: SubmitEvent) {
                e.preventDefault();
                mfaBusy = true;
                mfaError = '';
                try {
                        const res = await accountMfaVerify(mfaCode.trim());
                        recoveryCodes = res.recovery_codes;
                        codesSaved = false;
                        mfaStage = 'codes';
                } catch (err) {
                        mfaError =
                                err instanceof ApiError && err.code === 'totp_invalid'
                                        ? 'That code did not match. Check the clock on this device and try the current code.'
                                        : err instanceof ApiError
                                                ? err.message
                                                : String(err);
                } finally {
                        mfaBusy = false;
                }
        }

        async function finishMfaEnable() {
                if (!codesSaved) return;
                mfaStage = 'idle';
                await reload();
        }

        async function submitDomainCheck() {
                domainTouched = true;
                domainError = '';
                domainMsg = '';
                if (domainErr !== '') return;
                domainBusy = true;
                domainStage = 'checking';
                try {
                        const res: DomainCheckResult = await checkDomain(domainInput.trim());
                        domainMsg = res.message;
                        domainStage = 'input';
                } catch (err) {
                        domainError = err instanceof ApiError ? err.message : String(err);
                        domainStage = 'input';
                } finally {
                        domainBusy = false;
                }
        }

        async function submitDomainApply() {
                domainTouched = true;
                domainError = '';
                domainMsg = '';
                if (domainErr !== '') return;
                domainBusy = true;
                domainStage = 'applying';
                try {
                        await applyDomain(domainInput.trim());
                        domainInput = '';
                        domainTouched = false;
                        domainStage = 'input';
                        await reload();
                } catch (err) {
                        domainError = err instanceof ApiError ? err.message : String(err);
                        domainStage = 'input';
                } finally {
                        domainBusy = false;
                }
        }

        async function doRemoveDomain() {
                removeBusy = true;
                domainError = '';
                try {
                        await removeDomain();
                        await reload();
                } catch (err) {
                        domainError = err instanceof ApiError ? err.message : String(err);
                } finally {
                        removeBusy = false;
                }
        }

        function copyCodes() {
                void navigator.clipboard.writeText(recoveryCodes.join('\n')).catch(() => undefined);
        }
</script>

<svelte:head>
        <title>Settings — Drevanta</title>
</svelte:head>

{#if session?.authenticated && session.scope === 'full'}
        <div class="settings">
                <div class="page-head">
                        <h1>Settings</h1>
                        <a class="back" href="/">Back to console</a>
                </div>

                {#if loadError}
                        <div class="error" role="alert">{loadError}</div>
                {/if}

                <section class="card" aria-labelledby="profile-title">
                        <h2 id="profile-title">Account</h2>
                        {#if profile === null}
                                <p class="muted">loading…</p>
                        {:else}
                                <p class="muted">
                                        Signed in as <strong>{profile.username}</strong>. Sign-in works with the username
                                        <strong>or</strong> the email below.
                                </p>

                                <form class="stack" onsubmit={submitEmail}>
                                        <div class="field">
                                                <label for="set-email">Email</label>
                                                <input
                                                        id="set-email"
                                                        type="email"
                                                        placeholder="you@example.com"
                                                        autocomplete="email"
                                                        spellcheck="false"
                                                        maxlength="254"
                                                        aria-invalid={emailErr !== ''}
                                                        bind:value={emailInput}
                                                />
                                                {#if emailErr !== ''}<p class="error" role="alert">{emailErr}</p>{/if}
                                        </div>
                                        {#if emailMsg}<p class="ok-line">{emailMsg}</p>{/if}
                                        {#if emailSaveError}<p class="error" role="alert">{emailSaveError}</p>{/if}
                                        <div class="row">
                                                <button class="btn-primary" type="submit" disabled={emailBusy || emailErr !== ''}>
                                                        {emailBusy ? 'Saving…' : 'Save email'}
                                                </button>
                                        </div>
                                </form>

                                <form class="stack" onsubmit={submitPassword}>
                                        <div class="field">
                                                <label for="pw-current">Current password</label>
                                                <input id="pw-current" type="password" autocomplete="current-password" bind:value={pwCurrent} />
                                        </div>
                                        <div class="field">
                                                <label for="pw-new">New password</label>
                                                <input id="pw-new" type="password" autocomplete="new-password" maxlength="128" bind:value={pwNew} />
                                        </div>
                                        <div class="field">
                                                <label for="pw-confirm">Confirm new password</label>
                                                <input id="pw-confirm" type="password" autocomplete="new-password" maxlength="128" bind:value={pwConfirm} />
                                                {#if pwErr !== ''}<p class="error" role="alert">{pwErr}</p>{/if}
                                        </div>
                                        <p class="muted">Changing the password signs out every other device.</p>
                                        {#if pwMsg}<p class="ok-line">{pwMsg}</p>{/if}
                                        {#if pwError}<p class="error" role="alert">{pwError}</p>{/if}
                                        <div class="row">
                                                <button
                                                        class="btn-primary"
                                                        type="submit"
                                                        disabled={pwBusy || pwCurrent === '' || pwNew === '' || pwConfirm === '' || pwErr !== ''}
                                                >
                                                        {pwBusy ? 'Changing…' : 'Change password'}
                                                </button>
                                        </div>
                                </form>
                        {/if}
                </section>

                <section class="card" aria-labelledby="as-title">
                        <h2 id="as-title">Account security</h2>
                        {#if mfa === null}
                                <p class="muted">loading…</p>
                        {:else if mfa.mfa_enabled || mfaStage === 'codes'}
                                {#if mfaStage === 'codes'}
                                        <p class="muted">
                                                MFA is enabled. Store these single-use recovery codes somewhere safe — they are shown
                                                <strong>only this once</strong>.
                                        </p>
                                        <div class="codes-grid">
                                                {#each recoveryCodes as c (c)}
                                                        <span class="code-chip">{c}</span>
                                                {/each}
                                        </div>
                                        <button class="btn-secondary" type="button" onclick={copyCodes}>Copy all codes</button>
                                        <label class="check-row">
                                                <input type="checkbox" bind:checked={codesSaved} />
                                                <span>I have stored these codes somewhere safe.</span>
                                        </label>
                                        <button class="btn-primary" type="button" onclick={finishMfaEnable} disabled={!codesSaved}>Done</button>
                                {:else}
                                        <p class="ok-line">
                                                <span class="pill pill-ok">MFA enabled</span>
                                                two-factor sign-in is active · {mfa.recovery_codes_unused} unused recovery code{mfa.recovery_codes_unused === 1 ? '' : 's'}
                                        </p>
                                {/if}
                        {:else if mfaStage === 'setup'}
                                <p class="muted">
                                        Scan the code with your authenticator app, or enter the secret manually, then confirm
                                        with the current 6-digit code.
                                </p>
                                <div class="qr">{@html qrSvg}</div>
                                <div class="field">
                                        <label for="acc-secret">Secret (manual entry)</label>
                                        <input id="acc-secret" class="mono" readonly value={mfaSecret} onclick={(e) => (e.currentTarget as HTMLInputElement).select()} />
                                </div>
                                <form class="stack" onsubmit={submitMfaVerify}>
                                        <div class="field">
                                                <label for="acc-code">6-digit code</label>
                                                <input id="acc-code" class="mono" inputmode="numeric" maxlength={6} placeholder="123456" autocomplete="one-time-code" bind:value={mfaCode} />
                                        </div>
                                        {#if mfaError}<p class="error" role="alert">{mfaError}</p>{/if}
                                        <div class="row">
                                                <button class="btn-primary" type="submit" disabled={mfaBusy || mfaCode.trim().length !== 6}>
                                                        {mfaBusy ? 'Verifying…' : 'Verify and enable MFA'}
                                                </button>
                                                <button class="btn-secondary" type="button" onclick={() => (mfaStage = 'idle')} disabled={mfaBusy}>Cancel</button>
                                        </div>
                                </form>
                        {:else}
                                <p class="warn-line">
                                        <span class="pill pill-warn">MFA off</span>
                                        your account is protected by password only. Recovery codes are issued when you enable it.
                                </p>
                                {#if mfaError}<p class="error" role="alert">{mfaError}</p>{/if}
                                <button class="btn-primary" type="button" onclick={beginMfaSetup} disabled={mfaBusy}>
                                        {mfaBusy ? 'Preparing…' : 'Enable MFA'}
                                </button>
                        {/if}
                </section>

                <section class="card" aria-labelledby="smtp-title">
                        <h2 id="smtp-title">Email delivery (SMTP)</h2>
                        {#if smtp === null}
                                <p class="muted">loading…</p>
                        {:else}
                                <p class="muted">
                                        Used for password-reset links. Point it at your own mailbox provider or relay;
                                        the password is stored server-side and never shown again.
                                </p>
                                <form class="stack" onsubmit={submitSmtp}>
                                        <label class="check-row">
                                                <input type="checkbox" bind:checked={smtpEnabled} />
                                                <span>Enable email delivery</span>
                                        </label>
                                        <div class="row">
                                                <div class="field" style="flex:2">
                                                        <label for="smtp-host">SMTP host</label>
                                                        <input id="smtp-host" class="mono" type="text" placeholder="smtp.example.com" bind:value={smtpHost} />
                                                </div>
                                                <div class="field" style="flex:1">
                                                        <label for="smtp-port">Port</label>
                                                        <input id="smtp-port" class="mono" type="number" min="1" max="65535" bind:value={smtpPort} />
                                                </div>
                                        </div>
                                        <div class="field">
                                                <label for="smtp-user">SMTP username {#if smtp.password_set}<span class="muted"> · password saved</span>{/if}</label>
                                                <input id="smtp-user" class="mono" type="text" autocomplete="off" bind:value={smtpUser} />
                                        </div>
                                        <div class="field">
                                                <label for="smtp-pass">SMTP password {#if smtp.password_set}<span class="muted"> · leave blank to keep</span>{/if}</label>
                                                <input id="smtp-pass" type="password" autocomplete="new-password" placeholder="{smtp.password_set ? '••••••••' : ''}" bind:value={smtpPass} />
                                        </div>
                                        <div class="field">
                                                <label for="smtp-from">From address</label>
                                                <input id="smtp-from" type="email" placeholder="drevanta@example.com" bind:value={smtpFrom} />
                                        </div>
                                        {#if smtpMsg}<p class="ok-line">{smtpMsg}</p>{/if}
                                        {#if smtpTestMsg}<p class="muted">{smtpTestMsg}</p>{/if}
                                        {#if smtpError}<p class="error" role="alert">{smtpError}</p>{/if}
                                        <div class="row">
                                                <button class="btn-primary" type="submit" disabled={smtpBusy}>
                                                        {smtpBusy ? 'Saving…' : 'Save mail settings'}
                                                </button>
                                                <button class="btn-secondary" type="button" onclick={sendSmtpTest} disabled={smtpTestBusy}>
                                                        {smtpTestBusy ? 'Sending…' : 'Send test message'}
                                                </button>
                                        </div>
                                </form>
                        {/if}
                </section>

                <section class="card" aria-labelledby="acc-title">
                        <h2 id="acc-title">Access address</h2>
                        {#if access === null}
                                <p class="muted">loading…</p>
                        {:else}
                                <p class="muted">
                                        {#if access.mode === 'https'}
                                                Public HTTPS is active{access.https?.host ? ` for ${access.https.host}` : ''} with a publicly trusted
                                                certificate. The daemon itself stays loopback-only.
                                        {:else}
                                                Public HTTPS is not enabled. Access is private over an SSH tunnel — see
                                                <code>sudo drevanta access status</code> on the server.
                                        {/if}
                                </p>

                                {#if currentDomain}
                                        <p class="ok-line">
                                                <span class="pill pill-ok">custom domain</span>
                                                <strong>https://{currentDomain}</strong>
                                                {#if access.https?.domain?.cert_valid}
                                                        · certificate valid
                                                {:else if access.https?.domain?.last_error}
                                                        · last issue attempt failed: {access.https.domain.last_error}
                                                {/if}
                                        </p>
                                        <p class="muted">
                                                Removing it returns the console to the server-IP address only; the IP certificate keeps
                                                serving either way.
                                        </p>
                                        <button class="btn-secondary" type="button" onclick={doRemoveDomain} disabled={removeBusy}>
                                                {removeBusy ? 'Removing…' : 'Remove custom domain'}
                                        </button>
                                {:else}
                                        <p class="muted">
                                                Optionally serve the console under your own domain. DNS must already point at this
                                                server; the certificate is issued and the full route is verified before anything
                                                changes, and the server-IP address keeps working on any failure.
                                        </p>
                                        <div class="field">
                                                <label for="set-domain">Your domain</label>
                                                <input
                                                        id="set-domain"
                                                        class="mono"
                                                        type="text"
                                                        placeholder="vpn.example.com"
                                                        autocomplete="off"
                                                        spellcheck="false"
                                                        aria-invalid={domainTouched && domainErr !== ''}
                                                        bind:value={domainInput}
                                                        onblur={() => (domainTouched = true)}
                                                />
                                                {#if domainTouched && domainErr !== ''}
                                                        <p class="error" role="alert">{domainErr}</p>
                                                {/if}
                                        </div>
                                        <div class="row">
                                                <button class="btn-primary" type="button" disabled={domainBusy || domainErr !== ''} onclick={submitDomainApply}>
                                                        {domainStage === 'applying' ? 'Verifying DNS, issuing certificate…' : 'Verify DNS and enable HTTPS for this domain'}
                                                </button>
                                                <button class="btn-secondary" type="button" disabled={domainBusy || domainErr !== ''} onclick={submitDomainCheck}>
                                                        Check DNS only
                                                </button>
                                        </div>
                                        {#if domainMsg}<p class="hint">{domainMsg}</p>{/if}
                                        {#if domainError}<p class="error" role="alert">{domainError}</p>{/if}
                                {/if}
                        {/if}
                </section>
        </div>
{/if}

<style>
        .settings {
                max-width: 640px;
                margin: 0 auto;
                display: flex;
                flex-direction: column;
                gap: 16px;
        }

        .page-head {
                display: flex;
                align-items: baseline;
                justify-content: space-between;
                gap: 12px;
        }

        .page-head h1 {
                margin: 0;
                font-size: 20px;
                font-weight: 700;
        }

        .back {
                color: var(--text-dim);
                font-size: 13px;
                text-decoration: none;
        }

        .back:hover {
                color: var(--accent);
        }

        .card {
                background: var(--bg-card);
                border: 1px solid var(--border);
                border-radius: 12px;
                padding: 16px 18px;
                display: flex;
                flex-direction: column;
                gap: 12px;
        }

        .card h2 {
                margin: 0;
                font-size: 15px;
                font-weight: 700;
        }

        .muted {
                margin: 0;
                color: var(--text-dim);
                font-size: 13.5px;
                line-height: 1.6;
        }

        .stack,
        .field {
                display: flex;
                flex-direction: column;
                gap: 8px;
        }

        .field label {
                font-size: 11px;
                font-weight: 700;
                letter-spacing: 0.1em;
                text-transform: uppercase;
                color: var(--text-dim);
        }

        .field input {
                min-height: 38px;
                padding: 8px 11px;
                border-radius: 8px;
                border: 1px solid var(--border);
                background: var(--bg);
                color: var(--text);
                font-size: 14px;
        }

        .mono {
                font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
                font-size: 13px;
        }

        .row {
                display: flex;
                gap: 8px;
                flex-wrap: wrap;
        }

        .btn-primary {
                min-height: 38px;
                padding: 0 16px;
                border: 0;
                border-radius: 9px;
                font: inherit;
                font-size: 13.5px;
                font-weight: 700;
                cursor: pointer;
                color: #04120c;
                background: var(--accent);
        }

        .btn-primary:disabled {
                opacity: 0.55;
                cursor: progress;
        }

        .btn-secondary {
                min-height: 38px;
                padding: 0 14px;
                border: 1px solid var(--border);
                border-radius: 9px;
                font: inherit;
                font-size: 13.5px;
                font-weight: 600;
                cursor: pointer;
                color: var(--text-dim);
                background: transparent;
        }

        .btn-secondary:hover:not(:disabled) {
                color: var(--text);
                border-color: var(--text-dim);
        }

        .qr {
                background: #ffffff;
                border-radius: 10px;
                padding: 10px;
                width: fit-content;
        }

        .qr :global(svg) {
                display: block;
                width: 184px;
                height: 184px;
        }

        .codes-grid {
                display: grid;
                grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
                gap: 8px;
        }

        .code-chip {
                font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
                font-size: 14px;
                background: var(--bg);
                border: 1px solid var(--border);
                border-radius: 8px;
                padding: 7px 9px;
                text-align: center;
                letter-spacing: 0.08em;
        }

        .check-row {
                display: flex;
                align-items: flex-start;
                gap: 9px;
                font-size: 13px;
        }

        .check-row input {
                margin-top: 2px;
                accent-color: var(--accent);
        }

        .pill {
                display: inline-flex;
                padding: 2px 9px;
                border-radius: 999px;
                font-size: 11px;
                font-weight: 700;
                letter-spacing: 0.06em;
                text-transform: uppercase;
        }

        .pill-ok {
                color: var(--accent);
                border: 1px solid var(--accent);
                margin-right: 6px;
        }

        .pill-warn {
                color: var(--accent-warn);
                border: 1px solid var(--accent-warn);
                margin-right: 6px;
        }

        .ok-line,
        .warn-line {
                margin: 0;
                font-size: 13.5px;
                line-height: 1.6;
        }

        .error {
                margin: 0;
                color: var(--accent-crit);
                font-size: 13px;
                line-height: 1.5;
        }

        .hint {
                margin: 0;
                color: var(--text-dim);
                font-size: 12.5px;
                line-height: 1.55;
        }
</style>
