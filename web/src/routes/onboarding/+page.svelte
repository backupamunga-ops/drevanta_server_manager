<script lang="ts">
        import { tick } from 'svelte';
        import { goto } from '$app/navigation';
        import QRCode from 'qrcode';
        import {
                ApiError,
                fetchOnboardingStatus,
                postJSON,
                type MfaSetup,
                type MfaVerified
        } from '$lib/api';
        import {
                hashFromUrl,
                isValidTokenShape,
                maskToken,
                rawTokenParamFromHash,
                tokenFromPastedInput,
                urlWithoutHash
        } from '$lib/claimToken.js';
        import {
                PASSWORD_MAX,
                PASSWORD_MIN,
                STRENGTH_LABELS,
                confirmError,
                passwordError,
                scorePassword,
                tokenError,
                usernameError
        } from '$lib/onboardingValidate.js';

        type Step = 'loading' | 'claim' | 'mfa-choice' | 'mfa-setup' | 'recovery' | 'done';
        type TokenMode = 'chip' | 'manual';

        const DEFAULT_TOKEN_HINT = 'Paste the whole token or the setup link.';
        const INCOMPLETE_TOKEN_HINT = 'That token looks incomplete — check you copied the whole setup link.';

        // Meter / strength colours indexed by scorePassword() level, exactly
        // as in the approved prototype (1 danger, 2 warn, 3 mint, 4 accent).
        const LEVEL_COLORS = ['', 'var(--danger)', 'var(--warn)', '#8fe6c0', 'var(--accent)'] as const;

        let step = $state<Step>('loading');
        let busy = $state(false);
        let stepError = $state('');
        let showLoginLink = $state(false);
        let statusChecked = $state(false);
        let liveMsg = $state('');

        // --- claim form ----------------------------------------------------
        // The token lives in component memory only: never in localStorage,
        // sessionStorage, cookies, logs or analytics. It is consumed by the
        // backend exclusively on a successful claim.
        let tokenMode = $state<TokenMode>('manual');
        let token = $state('');
        let tokenOrigin = $state('');
        let tokenTouched = $state(false);
        let tokenHint = $state(DEFAULT_TOKEN_HINT);
        let tokenRejectMsg = $state('');

        let username = $state('');
        let password = $state('');
        let password2 = $state('');
        let userTouched = $state(false);
        let pwTouched = $state(false);
        let confirmTouched = $state(false);
        let showPw = $state(false);
        let showPw2 = $state(false);
        let capsOn = $state(false);

        // --- MFA enrolment ---------------------------------------------------
        let totpSecret = $state('');
        let qrSvg = $state('');
        let totpCode = $state('');

        // --- recovery codes --------------------------------------------------
        let recoveryCodes = $state<string[]>([]);
        let codesSaved = $state(false);

        // --- skip path -------------------------------------------------------
        let riskAcknowledged = $state(false);

        // Element refs for focus management.
        let tokenEl = $state<HTMLInputElement | null>(null);
        let usernameEl = $state<HTMLInputElement | null>(null);
        let passwordEl = $state<HTMLInputElement | null>(null);
        let password2El = $state<HTMLInputElement | null>(null);

        const userErr = $derived(usernameError(username));
        const pwErr = $derived(passwordError(password, username));
        const confirmErr = $derived(confirmError(password2, password));
        const tokenErr = $derived(tokenMode === 'manual' ? tokenError(token) : '');
        const showTokenErr = $derived(tokenMode === 'manual' && tokenRejectMsg === '' && tokenTouched && tokenErr !== '');
        const showTokenReject = $derived(tokenMode === 'manual' && tokenRejectMsg !== '');
        const tokenValidManual = $derived(tokenMode === 'manual' && token.trim() !== '' && tokenErr === '');
        const chipMasked = $derived(maskToken(token));
        const pwLevel = $derived(scorePassword(password));
        const strengthLabel = $derived(STRENGTH_LABELS[pwLevel]);
        const strengthColor = $derived(LEVEL_COLORS[pwLevel]);

        $effect(() => {
                if (statusChecked) return;
                statusChecked = true;
                void route();
        });

        function announce(msg: string) {
                liveMsg = '';
                setTimeout(() => (liveMsg = msg), 40);
        }

        // The setup-link fragment may be erased by the client router before
        // this component's effects run. The Performance Navigation entry
        // keeps the URL the document was actually loaded with (replaceState
        // never touches it), so it is the reliable fallback source.
        function initialHash(): string {
                if (typeof location === 'undefined') return '';
                if (location.hash) return location.hash;
                try {
                        const nav = performance.getEntriesByType('navigation')[0] as PerformanceNavigationTiming | undefined;
                        if (nav && nav.name) return hashFromUrl(nav.name);
                } catch {
                        // performance/URL unavailable — fall through to ''
                }
                return '';
        }

        // One-time setup link (…/onboarding#token=drv1_…): retain the token
        // in memory, then erase the fragment from the address bar and the
        // current history entry. history.replaceState never adds a history
        // entry and never sends anything over the network; fragments are
        // stripped by the browser before any request, so the token stays on
        // this machine end to end. No token-verification call happens here:
        // the backend validates and burns the single-use token only when the
        // administrator account is created. A malformed fragment value falls
        // back to manual entry with the raw value kept for correction.
        function consumeSetupLinkToken() {
                if (typeof location === 'undefined') return;
                const raw = rawTokenParamFromHash(initialHash());
                if (raw === '') return; // no token parameter: pristine manual entry
                history.replaceState(history.state, '', urlWithoutHash(location));
                const cleaned = tokenFromPastedInput(raw);
                if (isValidTokenShape(cleaned)) {
                        adoptToken(cleaned, 'from your setup link');
                } else {
                        token = cleaned;
                        tokenMode = 'manual';
                        tokenTouched = true;
                        tokenHint = INCOMPLETE_TOKEN_HINT;
                }
        }

        async function route() {
                stepError = '';
                showLoginLink = false;
                consumeSetupLinkToken();
                try {
                        const s = await fetchOnboardingStatus();
                        if (s.instance_status === 'active') {
                                if (s.authenticated && s.scope === 'full') {
                                        await goto('/', { replaceState: true });
                                        return;
                                }
                                await goto('/login', { replaceState: true });
                                return;
                        }
                        if (s.instance_status === 'mfa_pending') {
                                // Resume is only possible with the onboarding session created
                                // by the claim step; otherwise the operator must sign in.
                                if (s.authenticated) {
                                        step = 'mfa-choice';
                                } else {
                                        await goto('/login', { replaceState: true });
                                }
                                return;
                        }
                        step = 'claim';
                } catch (err) {
                        stepError = err instanceof Error ? err.message : String(err);
                        step = 'claim';
                }
                if (step !== 'claim') return;
                await tick();
                if (tokenMode === 'chip') {
                        usernameEl?.focus({ preventScroll: true });
                        announce('Setup token loaded. Now create the administrator account.');
                } else if (token === '') {
                        tokenEl?.focus({ preventScroll: true });
                }
        }

        // --- token state transitions -----------------------------------------

        /** Show the approved green "Setup token loaded" chip. */
        function adoptToken(value: string, origin: string) {
                token = value;
                tokenOrigin = origin;
                tokenMode = 'chip';
                tokenTouched = false;
                tokenRejectMsg = '';
                tokenHint = DEFAULT_TOKEN_HINT;
        }

        /** Fall back to the manual token input. */
        function useManualToken(prefill: string, focus: boolean) {
                token = prefill;
                tokenMode = 'manual';
                if (focus) void tick().then(() => tokenEl?.focus({ preventScroll: true }));
        }

        /** "Change" on the chip: clear and reveal manual entry, focused. */
        function changeToken() {
                tokenTouched = false;
                tokenHint = DEFAULT_TOKEN_HINT;
                useManualToken('', true);
        }

        function onTokenInput(e: Event) {
                const el = e.currentTarget as HTMLInputElement;
                let v = el.value;
                // Accept a complete setup link pasted or typed into the field.
                if (v.includes('token=')) v = tokenFromPastedInput(v);
                token = v;
                if (el.value !== v) el.value = v;
                tokenRejectMsg = '';
        }

        function onTokenPaste(e: ClipboardEvent) {
                const text = e.clipboardData?.getData('text') ?? '';
                if (text === '') return; // nothing readable: let the browser paste normally
                e.preventDefault();
                const v = tokenFromPastedInput(text);
                token = v;
                tokenRejectMsg = '';
                if (isValidTokenShape(v)) {
                        adoptToken(v, 'pasted just now');
                        announce('Setup token loaded. Now create the administrator account.');
                        void tick().then(() => usernameEl?.focus({ preventScroll: true }));
                } else {
                        tokenTouched = true;
                }
        }

        // --- claim submission --------------------------------------------------

        async function submitClaim(e: SubmitEvent) {
                e.preventDefault();
                tokenTouched = true;
                userTouched = true;
                pwTouched = true;
                confirmTouched = true;
                stepError = '';
                showLoginLink = false;

                if (tokenMode === 'manual' && tokenErr !== '') {
                        void tick().then(() => tokenEl?.focus({ preventScroll: true }));
                        return;
                }
                if (userErr !== '') {
                        void tick().then(() => usernameEl?.focus({ preventScroll: true }));
                        return;
                }
                if (pwErr !== '') {
                        void tick().then(() => passwordEl?.focus({ preventScroll: true }));
                        return;
                }
                if (confirmErr !== '') {
                        void tick().then(() => password2El?.focus({ preventScroll: true }));
                        return;
                }

                busy = true;
                try {
                        await postJSON('/api/v1/onboarding/claim', {
                                token: token.trim(),
                                username: username.trim(),
                                password
                        });
                        await beginMfaSetup();
                } catch (err) {
                        handleClaimError(err);
                } finally {
                        busy = false;
                }
        }

        function handleClaimError(err: unknown) {
                if (!(err instanceof ApiError)) {
                        stepError = String(err);
                        return;
                }
                if (err.code === 'token_expired' || err.code === 'token_used' || err.code === 'invalid_token') {
                        // Keep the tried value and all other form state so only the
                        // token needs correcting; point at the documented fallback.
                        tokenRejectMsg = tokenRejectMessage(err.code);
                        tokenMode = 'manual';
                        tokenTouched = true;
                        announce(tokenRejectMsg);
                        void tick().then(() => tokenEl?.focus({ preventScroll: true }));
                        return;
                }
                if (err.code === 'wrong_state') {
                        stepError = err.message;
                        showLoginLink = true;
                        return;
                }
                stepError = err.message;
        }

        function tokenRejectMessage(code: string): string {
                const again =
                        'Enter a fresh one below, or run "sudo drevanta claim regenerate" on the server and open the new link.';
                if (code === 'token_expired') return 'This setup token has expired. ' + again;
                if (code === 'token_used') return 'This setup token was already used. ' + again;
                return 'This setup token is not valid. Check it against the installer output, or run "sudo drevanta claim regenerate" on the server.';
        }

        // --- MFA enrolment -----------------------------------------------------

        async function beginMfaSetup() {
                stepError = '';
                busy = true;
                try {
                        const res = await postJSON<MfaSetup>('/api/v1/onboarding/mfa/setup', {});
                        totpSecret = res.secret;
                        qrSvg = await QRCode.toString(res.otpauth_uri, {
                                type: 'svg',
                                margin: 1,
                                color: { dark: '#0b1220', light: '#ffffff' }
                        });
                        totpCode = '';
                        step = 'mfa-setup';
                } catch (err) {
                        stepError = err instanceof ApiError ? err.message : String(err);
                        step = 'mfa-choice';
                } finally {
                        busy = false;
                }
        }

        async function submitTotp(e: SubmitEvent) {
                e.preventDefault();
                stepError = '';
                busy = true;
                try {
                        const res = await postJSON<MfaVerified>('/api/v1/onboarding/mfa/verify', { code: totpCode.trim() });
                        recoveryCodes = res.recovery_codes;
                        step = 'recovery';
                } catch (err) {
                        if (err instanceof ApiError && err.code === 'totp_invalid') {
                                stepError = 'That code did not match. Check the clock on this device and try the current code.';
                        } else {
                                stepError = err instanceof ApiError ? err.message : String(err);
                        }
                } finally {
                        busy = false;
                }
        }

        async function submitSkip() {
                stepError = '';
                if (!riskAcknowledged) return;
                busy = true;
                try {
                        await postJSON('/api/v1/onboarding/mfa/skip', { acknowledge_risk: true });
                        step = 'done';
                        setTimeout(() => goto('/', { replaceState: true }), 1200);
                } catch (err) {
                        stepError = err instanceof ApiError ? err.message : String(err);
                } finally {
                        busy = false;
                }
        }

        function finishRecovery() {
                if (!codesSaved) return;
                step = 'done';
                setTimeout(() => goto('/', { replaceState: true }), 1200);
        }

        async function copyCodes() {
                try {
                        await navigator.clipboard.writeText(recoveryCodes.join('\n'));
                } catch {
                        // Clipboard can be unavailable over plain http; the codes remain selectable.
                }
        }

        // --- password helpers ---------------------------------------------------

        function generatePassword() {
                const alphabet = 'abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789!@#$%^&*-_=+';
                const length = 20;
                const bytes = new Uint32Array(length);
                crypto.getRandomValues(bytes);
                let out = '';
                for (let i = 0; i < length; i++) out += alphabet[bytes[i] % alphabet.length];
                password = out;
                password2 = out;
                showPw = true;
                showPw2 = true;
                pwTouched = true;
                confirmTouched = true;
                void tick().then(() => password2El?.focus({ preventScroll: true }));
        }

        function capsHandler(e: KeyboardEvent) {
                if (typeof e.getModifierState !== 'function') return;
                capsOn = e.getModifierState('CapsLock');
        }
</script>

<svelte:head>
        <title>Onboarding — Drevanta</title>
</svelte:head>

<div class="ob">
        <p class="sr-only" role="status" aria-live="polite">{liveMsg}</p>

        <main class="obcard">
                <header class="ohead">
                        <div class="obrand">
                                <span class="obrand__mark" aria-hidden="true">
                                        <svg
                                                width="19"
                                                height="19"
                                                viewBox="0 0 24 24"
                                                fill="none"
                                                stroke="currentColor"
                                                stroke-width="1.7"
                                                stroke-linecap="round"
                                                stroke-linejoin="round"
                                        >
                                                <path d="M12 3l7.5 3.2v5.6c0 4.5-3.1 8.4-7.5 10.2-4.4-1.8-7.5-5.7-7.5-10.2V6.2L12 3z" />
                                                <path d="M9.2 12.2l2 2 3.6-3.9" />
                                        </svg>
                                </span>
                                <div>
                                        <h1>Drevanta</h1>
                                        <p>Private server console</p>
                                </div>
                        </div>

                </header>

                <div class="odivider"></div>

                {#if stepError}
                        <div class="oerror" role="alert">
                                <span>{stepError}</span>
                                {#if showLoginLink}
                                        <a href="/login">Sign in</a>
                                {/if}
                        </div>
                {/if}

                {#if step === 'loading'}
                        <p class="oloading">loading onboarding state…</p>
                {:else if step === 'claim'}
                        <form class="oform oform--claim" novalidate onsubmit={submitClaim}>
                                {#if tokenMode === 'chip'}
                                                                                <!-- locked compact token row: loaded ≠ verified; the
                                                                                     backend checks the single-use token only on claim -->
                                                                                <div class="otoken-row" role="group" aria-label="Setup token loaded">
                                                                                        <span class="otoken-row__badge" aria-hidden="true">
                                                                                                <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.4" stroke-linecap="round" stroke-linejoin="round">
                                                                                                        <path d="M20 6 9 17l-5-5" />
                                                                                                </svg>
                                                                                        </span>
                                                                                        <span class="otoken-row__label">Token loaded</span>
                                                                                        <span class="otoken-row__sep" aria-hidden="true">·</span>
                                                                                        <code class="otoken-row__mask">{chipMasked}</code>
                                                                                        <button
                                                                                                class="otoken-row__action"
                                                                                                type="button"
                                                                                                onclick={changeToken}
                                                                                                aria-label="Enter a different claim token"
                                                                                        >
                                                                                                Change
                                                                                        </button>
                                                                                </div>
                                                                        {:else}
                                        <div
                                                class="ofield"
                                                class:has-error={showTokenErr || showTokenReject}
                                                class:is-valid={tokenValidManual}
                                        >
                                                <div class="olabel-row">
                                                        <label class="olabel" for="token">Claim token</label>
                                                        <span class="olabel-note">single-use</span>
                                                </div>
                                                <div class="ocontrol">
                                                        <input
                                                                class="oinput oinput--mono"
                                                                id="token"
                                                                name="token"
                                                                type="text"
                                                                placeholder="drv1_••••••••••••••••••••••••••"
                                                                autocomplete="off"
                                                                autocapitalize="off"
                                                                autocorrect="off"
                                                                spellcheck="false"
                                                                aria-describedby="token-hint token-err"
                                                                aria-invalid={showTokenErr || showTokenReject}
                                                                value={token}
                                                                bind:this={tokenEl}
                                                                oninput={onTokenInput}
                                                                onpaste={onTokenPaste}
                                                                onblur={() => (tokenTouched = true)}
                                                        />
                                                        <span class="ostatus" aria-hidden="true">
                                                                <svg
                                                                        width="16"
                                                                        height="16"
                                                                        viewBox="0 0 24 24"
                                                                        fill="none"
                                                                        stroke="currentColor"
                                                                        stroke-width="3"
                                                                        stroke-linecap="round"
                                                                        stroke-linejoin="round"
                                                                >
                                                                        <path d="M20 6 9 17l-5-5" />
                                                                </svg>
                                                        </span>
                                                </div>
                                                <p class="ohint" id="token-hint">{tokenHint}</p>
                                                <p class="oerr" id="token-err" hidden={!showTokenErr && !showTokenReject}>
                                                        {showTokenReject ? tokenRejectMsg : tokenErr}
                                                </p>
                                        </div>
                                {/if}

                                <div class="ofield" class:has-error={userTouched && userErr !== ''}>
                                        <label class="olabel" for="username">Admin username</label>
                                        <input
                                                class="oinput"
                                                id="username"
                                                name="username"
                                                type="text"
                                                placeholder="e.g. rootadmin"
                                                autocomplete="username"
                                                autocapitalize="off"
                                                spellcheck="false"
                                                aria-describedby="user-err"
                                                aria-invalid={userTouched && userErr !== ''}
                                                bind:value={username}
                                                bind:this={usernameEl}
                                                onblur={() => (userTouched = true)}
                                        />
                                        <p class="oerr" id="user-err" hidden={!(userTouched && userErr !== '')}>{userErr}</p>
                                </div>

                                <div class="ofield" class:has-error={pwTouched && pwErr !== ''}>
                                        <div class="olabel-row">
                                                <label class="olabel" for="password">Password</label>
                                                <button class="olinkbtn" type="button" onclick={generatePassword}>Generate</button>
                                        </div>
                                        <div class="ocontrol">
                                                <input
                                                        class="oinput"
                                                        id="password"
                                                        name="password"
                                                        type={showPw ? 'text' : 'password'}
                                                        placeholder="At least 12 characters"
                                                        autocomplete="new-password"
                                                        maxlength={PASSWORD_MAX}
                                                        aria-describedby="pw-err"
                                                        aria-invalid={pwTouched && pwErr !== ''}
                                                        bind:value={password}
                                                        bind:this={passwordEl}
                                                        onkeydown={capsHandler}
                                                        onkeyup={capsHandler}
                                                        onblur={() => {
                                                                pwTouched = true;
                                                                capsOn = false;
                                                        }}
                                                />
                                                <button
                                                        class="ocontrol__btn oeye"
                                                        class:is-on={showPw}
                                                        type="button"
                                                        aria-label={showPw ? 'Hide password' : 'Show password'}
                                                        aria-pressed={showPw}
                                                        onclick={() => (showPw = !showPw)}
                                                >
                                                        <svg
                                                                class="icon-show"
                                                                width="17"
                                                                height="17"
                                                                viewBox="0 0 24 24"
                                                                fill="none"
                                                                stroke="currentColor"
                                                                stroke-width="1.8"
                                                                stroke-linecap="round"
                                                                stroke-linejoin="round"
                                                        >
                                                                <path d="M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7-10-7-10-7z" />
                                                                <circle cx="12" cy="12" r="3" />
                                                        </svg>
                                                        <svg
                                                                class="icon-hide"
                                                                width="17"
                                                                height="17"
                                                                viewBox="0 0 24 24"
                                                                fill="none"
                                                                stroke="currentColor"
                                                                stroke-width="1.8"
                                                                stroke-linecap="round"
                                                                stroke-linejoin="round"
                                                        >
                                                                <path d="M3 3l18 18" />
                                                                <path d="M10.6 10.6a3 3 0 0 0 4.2 4.2" />
                                                                <path d="M9.9 5.2A10.6 10.6 0 0 1 12 5c6.4 0 10 7 10 7a17.6 17.6 0 0 1-3.5 4.3" />
                                                                <path d="M6.6 6.6A17.2 17.2 0 0 0 2 12s3.6 7 10 7a10.9 10.9 0 0 0 3.4-.5" />
                                                        </svg>
                                                </button>
                                        </div>

                                        <div class="ometer-row">
                                                <div class="ometer" aria-hidden="true">
                                                        <span class="ometer__bar" class:on={pwLevel >= 1} style:background={pwLevel >= 1 ? strengthColor : ''}></span>
                                                        <span class="ometer__bar" class:on={pwLevel >= 2} style:background={pwLevel >= 2 ? strengthColor : ''}></span>
                                                        <span class="ometer__bar" class:on={pwLevel >= 3} style:background={pwLevel >= 3 ? strengthColor : ''}></span>
                                                        <span class="ometer__bar" class:on={pwLevel >= 4} style:background={pwLevel >= 4 ? strengthColor : ''}></span>
                                                </div>
                                                <span class="ostrength" aria-live="polite" style:color={pwLevel > 0 ? strengthColor : ''}>{strengthLabel}</span>
                                        </div>

                                        <p class="ohint ohint--warn" hidden={!capsOn}>Caps Lock is on.</p>
                                        <p class="oerr" id="pw-err" hidden={!(pwTouched && pwErr !== '')}>{pwErr}</p>
                                </div>

                                <div class="ofield" class:has-error={confirmTouched && confirmErr !== ''}>
                                        <label class="olabel" for="password2">Confirm password</label>
                                        <div class="ocontrol">
                                                <input
                                                        class="oinput"
                                                        id="password2"
                                                        name="password2"
                                                        type={showPw2 ? 'text' : 'password'}
                                                        placeholder="Repeat your password"
                                                        autocomplete="new-password"
                                                        maxlength={PASSWORD_MAX}
                                                        aria-describedby="confirm-err"
                                                        aria-invalid={confirmTouched && confirmErr !== ''}
                                                        bind:value={password2}
                                                        bind:this={password2El}
                                                        onkeydown={capsHandler}
                                                        onkeyup={capsHandler}
                                                        onblur={() => {
                                                                confirmTouched = true;
                                                                capsOn = false;
                                                        }}
                                                />
                                                <button
                                                        class="ocontrol__btn oeye"
                                                        class:is-on={showPw2}
                                                        type="button"
                                                        aria-label={showPw2 ? 'Hide password' : 'Show password'}
                                                        aria-pressed={showPw2}
                                                        onclick={() => (showPw2 = !showPw2)}
                                                >
                                                        <svg class="icon-show" width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
                                                                <path d="M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7-10-7-10-7z" />
                                                                <circle cx="12" cy="12" r="3" />
                                                        </svg>
                                                        <svg class="icon-hide" width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
                                                                <path d="M3 3l18 18" />
                                                                <path d="M10.6 10.6a3 3 0 0 0 4.2 4.2" />
                                                                <path d="M9.9 5.2A10.6 10.6 0 0 1 12 5c6.4 0 10 7 10 7a17.6 17.6 0 0 1-3.5 4.3" />
                                                                <path d="M6.6 6.6A17.2 17.2 0 0 0 2 12s3.6 7 10 7a10.9 10.9 0 0 0 3.4-.5" />
                                                        </svg>
                                                </button>
                                        </div>
                                        <p class="oerr" id="confirm-err" hidden={!(confirmTouched && confirmErr !== '')}>{confirmErr}</p>
                                </div>

                                <button class="obtn" type="submit" disabled={busy} class:is-loading={busy} aria-busy={busy}>
                                        <span class="obtn__spinner" aria-hidden="true"></span>
                                        <span>{busy ? 'Creating administrator…' : 'Create administrator account'}</span>
                                </button>
                        </form>

                        <div class="ometa">
                                <span>argon2id</span><span class="dot">·</span>
                                <span>single-use setup token</span>
                        </div>

                        <details class="odisclosure">
                                <summary>
                                        Where do I find the claim token?
                                        <svg class="chev" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                                                <path d="M6 9l6 6 6-6" />
                                        </svg>
                                </summary>
                                <p>
                                        The installer prints a one-time link. With public HTTPS it is
                                        <code>https://YOUR_VPS_IP/onboarding#token=drv1_…</code> — open it directly in your
                                        browser. Without HTTPS, run <code>ssh -L 18080:127.0.0.1:8080 user@server</code> on
                                        <em>your computer</em> and open <code>http://127.0.0.1:18080/onboarding#token=…</code>.
                                        Paste the whole link (or just the token) above. The token is consumed on success and
                                        cannot be reused. If it expired, run <code>sudo drevanta claim regenerate</code> on
                                        the server for a fresh one.
                                </p>
                        </details>
                {:else if step === 'mfa-choice'}
                        <section class="ointro">
                                <h2>Protect the account</h2>
                                <p>Add a second factor (recommended) or skip it for now. You can decide only during this wizard.</p>
                        </section>

                        <div class="oactions">
                                <button class="obtn" type="button" onclick={beginMfaSetup} disabled={busy} class:is-loading={busy} aria-busy={busy}>
                                        <span class="obtn__spinner" aria-hidden="true"></span>
                                        <span>{busy ? 'Preparing…' : 'Set up authenticator app'}</span>
                                </button>

                                <div class="odivider"><span>or</span></div>

                                <div class="owarn-box">
                                        <strong>Skipping MFA lowers security.</strong> Without a second factor, knowing the
                                                                                password alone is enough to sign in from anywhere the console is reachable,
                                                                                and the account will not be 2FA-protected.
                                </div>
                                <label class="ocheck-row">
                                        <input type="checkbox" bind:checked={riskAcknowledged} />
                                        <span>I understand the risk of running without multi-factor authentication.</span>
                                </label>
                                <button class="obtn obtn--danger" type="button" onclick={submitSkip} disabled={!riskAcknowledged || busy}>
                                        Skip MFA and finish
                                </button>
                        </div>
                {:else if step === 'mfa-setup'}
                        <section class="ointro">
                                <h2>Scan and confirm</h2>
                                <p>
                                        Add the entry to your authenticator app by scanning the QR code, or enter the secret
                                        manually. Then type the current 6-digit code.
                                </p>
                        </section>
                        <div class="oqr">{@html qrSvg}</div>
                        <div class="ofield">
                                <label class="olabel" for="secret">Secret (manual entry)</label>
                                <input
                                        class="oinput oinput--mono"
                                        id="secret"
                                        readonly
                                        value={totpSecret}
                                        spellcheck="false"
                                        onclick={(e) => (e.currentTarget as HTMLInputElement).select()}
                                />
                        </div>
                        <form class="oform" novalidate onsubmit={submitTotp}>
                                <div class="ofield">
                                        <label class="olabel" for="totp-code">6-digit code</label>
                                        <input
                                                class="oinput oinput--mono"
                                                id="totp-code"
                                                inputmode="numeric"
                                                maxlength={6}
                                                placeholder="123456"
                                                autocomplete="one-time-code"
                                                bind:value={totpCode}
                                        />
                                </div>
                                <button class="obtn" type="submit" disabled={busy || totpCode.trim().length !== 6} class:is-loading={busy} aria-busy={busy}>
                                        <span class="obtn__spinner" aria-hidden="true"></span>
                                        <span>{busy ? 'Verifying…' : 'Verify and enable MFA'}</span>
                                </button>
                        </form>
                {:else if step === 'recovery'}
                        <section class="ointro">
                                <h2>Save your recovery codes</h2>
                                <p>
                                        Each code works exactly once at sign-in if your authenticator is unavailable. They are
                                        shown <strong>only this once</strong>; the server stores just their hashes.
                                </p>
                        </section>
                        <div class="ocodes-grid">
                                {#each recoveryCodes as c (c)}
                                        <span class="ocode-chip">{c}</span>
                                {/each}
                        </div>
                        <button class="obtn obtn--secondary" type="button" onclick={copyCodes}>Copy all codes</button>
                        <label class="ocheck-row">
                                <input type="checkbox" bind:checked={codesSaved} />
                                <span>I have stored these codes somewhere safe (password manager or printed copy).</span>
                        </label>
                        <button class="obtn" type="button" onclick={finishRecovery} disabled={!codesSaved}>
                                Finish onboarding
                        </button>
                {:else if step === 'done'}
                        <section class="odone">
                                <div class="odone__icon" aria-hidden="true">
                                        <svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5" /></svg>
                                </div>
                                <h2>You're all set</h2>
                                <p>Onboarding is complete. Opening the dashboard…</p>
                        </section>
                {/if}
        </main>
</div>

<style>
        /* Design tokens from the approved prototype, scoped to this page so
           the dashboard's global palette is untouched. */
        .ob {
                --line: #1c2530;
                --line-hi: #28323f;
                --line-soft: #161e27;
                --text: #e7eef5;
                --muted: #93a1b0;
                --dim: #61707f;
                --accent: #3ddc97;
                --accent-glow: rgba(61, 220, 151, 0.14);
                --danger: #ff6b6b;
                --warn: #ffb454;
                --mono: ui-monospace, SFMono-Regular, 'SF Mono', Menlo, Consolas, 'Liberation Mono', monospace;

                min-height: 100vh;
                display: grid;
                place-items: center;
                padding: 22px 16px;
                background: #06080b;
                color: var(--text);
                font-size: 15px;
                line-height: 1.5;
        }

        /* Ambient green glow + subtle grid, as fixed overlays like the
           prototype's body::before/::after. Pointer events pass through. */
        .ob::before {
                content: '';
                position: fixed;
                inset: 0;
                pointer-events: none;
                background:
                        radial-gradient(900px 520px at 50% -12%, rgba(61, 220, 151, 0.11), transparent 62%),
                        radial-gradient(700px 420px at 92% 108%, rgba(84, 132, 255, 0.07), transparent 60%);
        }

        .ob::after {
                content: '';
                position: fixed;
                inset: 0;
                pointer-events: none;
                background-image:
                        linear-gradient(to right, rgba(255, 255, 255, 0.028) 1px, transparent 1px),
                        linear-gradient(to bottom, rgba(255, 255, 255, 0.028) 1px, transparent 1px);
                background-size: 56px 56px;
                -webkit-mask-image: radial-gradient(circle at 50% 32%, #000 0%, transparent 72%);
                mask-image: radial-gradient(circle at 50% 32%, #000 0%, transparent 72%);
        }

        .sr-only {
                position: absolute;
                width: 1px;
                height: 1px;
                padding: 0;
                margin: -1px;
                overflow: hidden;
                clip: rect(0 0 0 0);
                white-space: nowrap;
                border: 0;
        }

        /* As in the prototype: the hidden attribute must always win over
           component display values (.oerr uses display:flex, the hint rows
           are plain blocks) so untouched error paragraphs truly vanish. */
        .ob [hidden] {
                display: none !important;
        }

        /* ---------- card ---------- */
        .obcard {
                position: relative;
                width: 100%;
                max-width: 520px;
                padding: 20px 22px 18px;
                border-radius: 16px;
                border: 1px solid var(--line);
                background:
                        radial-gradient(120% 100% at 50% 0%, rgba(61, 220, 151, 0.055), transparent 56%),
                        linear-gradient(180deg, #0d1319 0%, #0a0e13 100%);
                box-shadow:
                        0 1px 0 rgba(255, 255, 255, 0.045) inset,
                        0 40px 90px -32px rgba(0, 0, 0, 0.95),
                        0 0 0 1px rgba(0, 0, 0, 0.4);
                animation: rise 0.55s cubic-bezier(0.2, 0.7, 0.3, 1) both;
        }

        .obcard::before {
                content: '';
                position: absolute;
                left: 14px;
                right: 14px;
                top: 0;
                height: 1px;
                background: linear-gradient(90deg, transparent, rgba(61, 220, 151, 0.55), transparent);
        }

        @keyframes rise {
                from {
                        opacity: 0;
                        transform: translateY(12px);
                }
                to {
                        opacity: 1;
                        transform: none;
                }
        }

        /* ---------- header ---------- */
        .ohead {
                display: flex;
                align-items: center;
                justify-content: space-between;
                gap: 14px;
                flex-wrap: wrap;
        }

        .obrand {
                display: flex;
                align-items: center;
                gap: 12px;
                min-width: 0;
        }

        .obrand__mark {
                width: 38px;
                height: 38px;
                flex: 0 0 auto;
                border-radius: 11px;
                display: grid;
                place-items: center;
                color: var(--accent);
                background: linear-gradient(160deg, rgba(61, 220, 151, 0.24), rgba(61, 220, 151, 0.05));
                border: 1px solid rgba(61, 220, 151, 0.28);
                box-shadow: 0 0 26px -8px rgba(61, 220, 151, 0.8);
        }

        .obrand h1 {
                margin: 0;
                font-size: 14px;
                font-weight: 700;
                letter-spacing: 0.16em;
                text-transform: uppercase;
                color: #eef5fb;
        }

        .obrand p {
                margin: 1px 0 0;
                font-size: 12px;
                color: var(--dim);
                letter-spacing: 0.02em;
        }

        .odivider {
                height: 1px;
                margin: 12px 0 12px;
                background: linear-gradient(90deg, transparent, var(--line) 10%, var(--line) 90%, transparent);
        }

        .oactions .odivider {
                display: flex;
                align-items: center;
                gap: 10px;
                height: auto;
                background: none;
                margin: 4px 0;
                color: var(--dim);
                font-size: 12px;
                text-transform: uppercase;
                letter-spacing: 0.08em;
        }

        .oactions .odivider::before,
        .oactions .odivider::after {
                content: '';
                flex: 1;
                height: 1px;
                background: var(--line);
        }

        /* ---------- intro / loading / error ---------- */
        .ointro h2 {
                margin: 0;
                font-size: 17px;
                font-weight: 600;
                letter-spacing: -0.01em;
        }

        .ointro p {
                margin: 5px 0 0;
                font-size: 13.5px;
                line-height: 1.62;
                color: var(--muted);
                max-width: 52ch;
        }

        .oloading {
                color: var(--dim);
                padding: 40px 0;
                text-align: center;
                margin: 0;
        }

        .oerror {
                background: rgba(255, 107, 107, 0.08);
                border: 1px solid rgba(255, 107, 107, 0.4);
                color: var(--danger);
                border-radius: 10px;
                padding: 10px 14px;
                font-size: 13px;
                line-height: 1.55;
                margin-bottom: 16px;
                display: flex;
                align-items: baseline;
                gap: 8px;
                flex-wrap: wrap;
        }

        .oerror a {
                color: var(--danger);
                font-weight: 600;
        }

        .oerror a:focus-visible {
                outline: 2px solid rgba(255, 107, 107, 0.6);
                outline-offset: 2px;
                border-radius: 4px;
        }

        /* ---------- form ---------- */
        .oform {
                display: flex;
                flex-direction: column;
                gap: 12px;
                margin-top: 0;
        }

        .oform--claim {
                margin-top: 2px;
        }

        .ofield {
                display: flex;
                flex-direction: column;
                gap: 6px;
        }

        .olabel-row {
                display: flex;
                align-items: center;
                justify-content: space-between;
                gap: 10px;
        }

        .olabel {
                font-size: 11px;
                font-weight: 700;
                letter-spacing: 0.11em;
                text-transform: uppercase;
                color: #8494a3;
        }

        .olabel-note {
                font-family: var(--mono);
                font-size: 10.5px;
                letter-spacing: 0.02em;
                color: var(--accent);
                opacity: 0.85;
                padding: 2px 7px;
                border-radius: 5px;
                background: rgba(61, 220, 151, 0.1);
                border: 1px solid rgba(61, 220, 151, 0.22);
        }

        .olinkbtn {
                border: 0;
                background: none;
                padding: 2px 0;
                cursor: pointer;
                font-family: var(--mono);
                font-size: 11px;
                letter-spacing: 0.03em;
                color: var(--dim);
                transition: color 0.15s;
        }

        .olinkbtn:hover {
                color: var(--accent);
        }

        .olinkbtn:focus-visible {
                outline: 2px solid rgba(61, 220, 151, 0.6);
                outline-offset: 3px;
                border-radius: 4px;
        }

        .ocontrol {
                position: relative;
        }

        .ocontrol > .oinput {
                padding-right: 42px;
        }

        .oinput {
                width: 100%;
                min-height: 40px;
                padding: 9px 11px;
                border-radius: 9px;
                border: 1px solid var(--line);
                background: #080c11;
                color: var(--text);
                font: inherit;
                font-size: 14.5px;
                line-height: 1.3;
                transition:
                        border-color 0.16s ease,
                        box-shadow 0.16s ease,
                        background 0.16s ease;
        }

        .oinput::placeholder {
                color: #3f4b58;
        }

        .oinput:hover {
                border-color: var(--line-hi);
        }

        .oinput:focus {
                outline: none;
                border-color: rgba(61, 220, 151, 0.5);
                box-shadow: 0 0 0 3px var(--accent-glow);
                background: #090e13;
        }

        .oinput--mono {
                font-family: var(--mono);
                font-size: 13px;
                letter-spacing: 0.02em;
        }

        .ocontrol__btn {
                position: absolute;
                right: 6px;
                top: 50%;
                transform: translateY(-50%);
                width: 34px;
                height: 34px;
                display: grid;
                place-items: center;
                border: 0;
                border-radius: 8px;
                background: transparent;
                color: var(--dim);
                cursor: pointer;
                transition:
                        color 0.15s,
                        background 0.15s;
        }

        .ocontrol__btn:hover {
                color: var(--text);
                background: rgba(255, 255, 255, 0.06);
        }

        .ocontrol__btn:focus-visible {
                outline: 2px solid rgba(61, 220, 151, 0.6);
                outline-offset: 1px;
        }

        .oeye .icon-hide {
                display: none;
        }

        .oeye.is-on .icon-show {
                display: none;
        }

        .oeye.is-on .icon-hide {
                display: block;
        }

        .ostatus {
                position: absolute;
                right: 14px;
                top: 50%;
                transform: translateY(-50%);
                color: var(--accent);
                opacity: 0;
                pointer-events: none;
                transition:
                        opacity 0.2s ease,
                        transform 0.2s ease;
        }

        .ostatus svg {
                display: block;
        }

        .ofield.is-valid .ostatus {
                opacity: 1;
        }

        .ohint {
                margin: 0;
                font-size: 12.25px;
                color: var(--dim);
                line-height: 1.5;
        }

        .ohint--warn {
                color: var(--warn);
        }

        .oerr {
                margin: 0;
                font-size: 12.25px;
                color: var(--danger);
                display: flex;
                align-items: center;
                gap: 6px;
        }

        .ofield.has-error .oinput {
                border-color: rgba(255, 107, 107, 0.55);
                box-shadow: 0 0 0 3px rgba(255, 107, 107, 0.1);
        }

        /* ---------- compact token row (locked wording) ---------- */
        .otoken-row {
                display: flex;
                align-items: center;
                gap: 8px;
                flex-wrap: wrap;
                padding: 8px 11px;
                border-radius: 10px;
                border: 1px solid rgba(61, 220, 151, 0.3);
                background: linear-gradient(180deg, rgba(61, 220, 151, 0.1), rgba(61, 220, 151, 0.035));
                box-shadow:
                        0 0 0 1px rgba(0, 0, 0, 0.3) inset,
                        0 10px 28px -20px rgba(61, 220, 151, 0.95);
                animation: rise 0.35s cubic-bezier(0.2, 0.7, 0.3, 1) both;
        }

        .otoken-row__badge {
                width: 18px;
                height: 18px;
                flex: 0 0 auto;
                border-radius: 50%;
                display: grid;
                place-items: center;
                color: var(--accent);
                background: rgba(61, 220, 151, 0.16);
                border: 1px solid rgba(61, 220, 151, 0.42);
                animation: pop 0.34s cubic-bezier(0.2, 0.9, 0.3, 1.35) both;
        }

        @keyframes pop {
                from {
                        transform: scale(0.72);
                        opacity: 0.35;
                }
                to {
                        transform: none;
                        opacity: 1;
                }
        }

        .otoken-row__label {
                font-size: 12.5px;
                font-weight: 600;
                color: #c8f4de;
                white-space: nowrap;
        }

        .otoken-row__sep {
                opacity: 0.4;
                color: var(--dim);
        }

        .otoken-row__mask {
                font-family: var(--mono);
                font-size: 11.5px;
                color: #8fb3a5;
                white-space: nowrap;
                overflow: hidden;
                text-overflow: ellipsis;
                min-width: 0;
                flex: 1 1 auto;
        }

        .otoken-row__action {
                flex: 0 0 auto;
                border: 1px solid var(--line);
                background: rgba(255, 255, 255, 0.03);
                color: var(--muted);
                border-radius: 7px;
                padding: 5px 10px;
                cursor: pointer;
                font: inherit;
                font-family: var(--mono);
                font-size: 11px;
                letter-spacing: 0.02em;
                transition:
                        color 0.15s,
                        border-color 0.15s,
                        background 0.15s;
        }

        .otoken-row__action:hover {
                color: var(--text);
                border-color: var(--line-hi);
                background: rgba(255, 255, 255, 0.06);
        }

        .otoken-row__action:focus-visible {
                outline: 2px solid rgba(61, 220, 151, 0.6);
                outline-offset: 2px;
        }

                /* ---------- password meter ---------- */
        .ometer {
                display: flex;
                gap: 5px;
                margin-top: 2px;
        }

        .ometer__bar {
                flex: 1;
                height: 3px;
                border-radius: 2px;
                background: #18212b;
                transition: background 0.25s ease;
        }

        .ometer-row {
                display: flex;
                align-items: center;
                gap: 10px;
                margin-top: 2px;
        }

        .ostrength {
                font-size: 11px;
                font-weight: 700;
                letter-spacing: 0.09em;
                text-transform: uppercase;
                white-space: nowrap;
                color: var(--dim);
                transition: color 0.2s;
        }

        /* ---------- button ---------- */
        .obtn {
                position: relative;
                width: 100%;
                min-height: 42px;
                margin-top: 2px;
                display: inline-flex;
                align-items: center;
                justify-content: center;
                gap: 10px;
                padding: 0 18px;
                border: 1px solid transparent;
                border-radius: 11px;
                font: inherit;
                font-size: 14.5px;
                font-weight: 700;
                letter-spacing: 0.01em;
                color: #04120c;
                cursor: pointer;
                background: linear-gradient(180deg, #4ae5a4, #2fc98a);
                box-shadow:
                        0 1px 0 rgba(255, 255, 255, 0.35) inset,
                        0 12px 32px -14px rgba(61, 220, 151, 0.9);
                transition:
                        filter 0.16s ease,
                        transform 0.1s ease,
                        box-shadow 0.16s ease;
        }

        .obtn:hover {
                filter: brightness(1.06);
        }

        .obtn:active {
                transform: translateY(1px);
        }

        .obtn:focus-visible {
                outline: 2px solid rgba(61, 220, 151, 0.9);
                outline-offset: 3px;
        }

        .obtn[disabled] {
                cursor: progress;
                filter: saturate(0.75) brightness(0.92);
        }

        .obtn--danger {
                background: transparent;
                color: var(--danger);
                border: 1px solid rgba(255, 107, 107, 0.5);
                box-shadow: none;
                font-weight: 600;
        }

        .obtn--danger:hover {
                filter: none;
                background: rgba(255, 107, 107, 0.06);
        }

        .obtn--secondary {
                background: rgba(255, 255, 255, 0.03);
                color: var(--muted);
                border: 1px solid var(--line);
                box-shadow: none;
                font-weight: 600;
        }

        .obtn--secondary:hover {
                filter: none;
                color: var(--text);
                border-color: var(--line-hi);
                background: rgba(255, 255, 255, 0.06);
        }

        .obtn__spinner {
                display: none;
                width: 16px;
                height: 16px;
                border-radius: 50%;
                border: 2px solid rgba(4, 18, 12, 0.3);
                border-top-color: #04120c;
                animation: spin 0.7s linear infinite;
        }

        .obtn.is-loading .obtn__spinner {
                display: block;
        }

        @keyframes spin {
                to {
                        transform: rotate(360deg);
                }
        }

        /* ---------- meta / disclosure ---------- */
        .ometa {
                display: flex;
                align-items: center;
                justify-content: center;
                gap: 8px;
                flex-wrap: wrap;
                margin-top: 10px;
                font-family: var(--mono);
                font-size: 10.5px;
                letter-spacing: 0.05em;
                color: #4e5c6a;
                text-transform: lowercase;
        }

        .ometa .dot {
                opacity: 0.45;
        }

        .odisclosure {
                margin-top: 12px;
                padding-top: 10px;
                border-top: 1px solid var(--line-soft);
        }

        .odisclosure summary {
                display: inline-flex;
                align-items: center;
                gap: 6px;
                cursor: pointer;
                list-style: none;
                font-size: 12.5px;
                color: var(--dim);
                transition: color 0.15s;
        }

        .odisclosure summary::-webkit-details-marker {
                display: none;
        }

        .odisclosure summary:hover {
                color: var(--muted);
        }

        .odisclosure summary:focus-visible {
                outline: 2px solid rgba(61, 220, 151, 0.55);
                outline-offset: 3px;
                border-radius: 4px;
        }

        .odisclosure summary .chev {
                transition: transform 0.2s ease;
        }

        .odisclosure[open] summary .chev {
                transform: rotate(180deg);
        }

        .odisclosure p {
                margin: 9px 0 0;
                font-size: 12.5px;
                line-height: 1.65;
                color: var(--dim);
        }

        .odisclosure code {
                font-family: var(--mono);
                font-size: 11.5px;
                padding: 1px 5px;
                border-radius: 4px;
                background: rgba(255, 255, 255, 0.05);
                color: #a9b8c6;
        }

        /* ---------- wizard steps (same design language) ---------- */
        .oactions {
                display: flex;
                flex-direction: column;
                gap: 12px;
                margin-top: 14px;
        }

        .owarn-box {
                background: rgba(255, 180, 84, 0.07);
                border: 1px solid rgba(255, 180, 84, 0.45);
                color: var(--warn);
                border-radius: 10px;
                padding: 12px 14px;
                font-size: 13px;
                line-height: 1.55;
        }

        .ocheck-row {
                display: flex;
                align-items: flex-start;
                gap: 10px;
                font-size: 13px;
                color: var(--text);
                line-height: 1.5;
        }

        .ocheck-row input {
                margin-top: 2px;
                accent-color: var(--accent);
        }

        .oqr {
                background: #ffffff;
                border-radius: 10px;
                padding: 12px;
                width: fit-content;
                align-self: center;
                margin: 4px 0 2px;
        }

        .ocodes-grid {
                display: grid;
                grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
                gap: 8px;
                margin-top: 4px;
        }

        .ocode-chip {
                font-family: var(--mono);
                font-size: 15px;
                background: #080c11;
                border: 1px solid var(--line);
                border-radius: 8px;
                padding: 8px 10px;
                text-align: center;
                letter-spacing: 0.08em;
                color: #cfe0ee;
        }

        /* ---------- done ---------- */
        .odone {
                display: flex;
                flex-direction: column;
                align-items: center;
                text-align: center;
                padding: 16px 0 8px;
                animation: rise 0.45s cubic-bezier(0.2, 0.7, 0.3, 1) both;
        }

        .odone__icon {
                width: 58px;
                height: 58px;
                border-radius: 50%;
                display: grid;
                place-items: center;
                color: var(--accent);
                background: radial-gradient(circle, rgba(61, 220, 151, 0.22), rgba(61, 220, 151, 0.05));
                border: 1px solid rgba(61, 220, 151, 0.4);
                box-shadow: 0 0 44px -8px rgba(61, 220, 151, 0.7);
                margin-bottom: 18px;
        }

        .odone h2 {
                margin: 0;
                font-size: 19px;
                font-weight: 600;
        }

        .odone p {
                margin: 8px 0 0;
                font-size: 13.5px;
                line-height: 1.65;
                color: var(--muted);
                max-width: 44ch;
        }

        /* ---------- responsive ---------- */
        @media (max-width: 520px) {
                .ob {
                        padding: 12px 10px;
                }

                .obcard {
                        padding: 16px 14px 14px;
                        border-radius: 13px;
                }

                .ohead {
                        gap: 10px;
                }

                .ometer-row {
                        flex-wrap: wrap;
                }

                .ostrength {
                        margin-left: auto;
                }
        }

        @media (prefers-reduced-motion: reduce) {
                .ob,
                .ob::before,
                .ob::after,
                .obcard,
                .obcard::before,
                .otoken-row,
                .otoken-row__badge,
                .odone,
                .odone__icon {
                        animation-duration: 0.001ms !important;
                        transition-duration: 0.001ms !important;
                }
        }
</style>
