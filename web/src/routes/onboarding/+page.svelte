<script lang="ts">
        import { tick } from 'svelte';
        import { goto } from '$app/navigation';
        import { qrSvg as renderQrCode } from '$lib/qr.js';
        import {
                ApiError,
                applyDomain,
                checkDomain,
                fetchAccessStatus,
                fetchMetrics,
                fetchOnboardingStatus,
                postJSON,
                skipMfa,
                type DomainCheckResult,
                type MfaSetup,
                type MfaVerified
        } from '$lib/api';
        import { accessLabel, accessLabelTitle, domainHint } from '$lib/access.js';
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

        type Step = 'loading' | 'claim' | 'mfa-setup' | 'recovery' | 'domain' | 'done';
        type TokenMode = 'chip' | 'manual';

        const DEFAULT_TOKEN_HINT = 'Paste the whole token or the setup link.';
        const INCOMPLETE_TOKEN_HINT = 'That token looks incomplete — check you copied the whole setup link.';

        // Meter / strength colours indexed by scorePassword() level, in the
        // approved v6 palette (1 danger, 2 warn, 3 soft mint, 4 mint).
        const LEVEL_COLORS = ['', '#df8b83', '#e2bb7e', '#9ddbb9', '#83d9bf'] as const;

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
        // Six single-digit boxes exactly as in the approved MFA prototype;
        // the joined digits go to the same verify endpoint. Invalid codes
        // get the prototype's red state, shake animation and inline error row.
        const OTP_LEN = 6;
        let totpSecret = $state('');
        let qrSvg = $state('');
        let otpDigits = $state<string[]>(['', '', '', '', '', '']);
        let otpInvalid = $state(false);
        let otpShake = $state(false);
        let otpEls = $state<(HTMLInputElement | null)[]>([]);
        let mfaDone = $state(false);
        // Completion-checklist re-entry: true while the operator follows a
        // link from the done screen ("secure your account" / "configure
        // domain") — navigation returns to the checklist instead of
        // marching on through the wizard.
        let returnToDone = $state(false);
        let mfaSkipped = $state(false);
        let copyDone = $state(false);
        let copyTimer: ReturnType<typeof setTimeout> | undefined;
        let continueEl = $state<HTMLButtonElement | null>(null);
        // Manual-entry secret: masked by default, explicit Reveal toggle.
        // Copy always copies the real secret regardless of the mask.
        let secretRevealed = $state(false);
        let mfaHelpOpen = $state(false);

        // --- recovery codes --------------------------------------------------
        let recoveryCodes = $state<string[]>([]);
        let codesSaved = $state(false);

        // --- optional MFA ("Skip for now") ---------------------------------
        // Two explicit steps: reveal the warning, then confirm it. The
        // backend additionally requires the machine-readable acknowledgement.
        let skipShown = $state(false);
        let skipConfirm = $state(false);

        // --- access address (custom domain, optional) ----------------------
        // Approved v6 presentation: the address choice and the domain setup
        // are two screens. The backend contract is unchanged — DNS
        // verification (check) and HTTPS provisioning (apply) remain the
        // same two calls as before.
        let domainChoice = $state<'ip' | 'domain'>('ip');
        let domainScreen = $state(false);
        let domainInput = $state('');
        let domainTouched = $state(false);
        let domainBusy = $state(false);
        let domainStage = $state<'input' | 'checking' | 'applying' | 'applied'>('input');
        let domainMsg = $state('');
        let domainError = $state('');
        // The domain that passed DNS verification. HTTPS provisioning is
        // permitted for THIS value only; editing the input invalidates it.
        let verifiedDomain = $state('');
        let dnsCheckResult = $state<DomainCheckResult | null>(null);
        // Monotonic DNS request sequence: an answer that arrives after the
        // input changed must never mark, message or un-busy the new domain.
        let dnsSeq = 0;
        let domainApplied = $state('');

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
        const otpCode = $derived(otpDigits.join(''));
        // Grouped 4-4-4 display of the base32 secret; the copy button copies
        // the ungrouped value the authenticator expects. This is the only
        // presentation: the whole field is collapsed until Reveal, and the
        // input always holds the real secret while it is visible.
        const secretDisplay = $derived(totpSecret.replace(/(.{4})/g, '$1 ').trim());
        // The copyable Host / Name value in the DNS instructions panel.
        // (Points to is derived below, after serverIp is declared.)
        const dnsHostValue = $derived(dnsHostFor(domainInput.trim().toLowerCase()));

        const domainErr = $derived(domainHint(domainInput));
        // The verification counts only while the input still IS the verified
        // domain — a global boolean would leak a pass to an edited domain.
        const domainChecked = $derived(
                verifiedDomain !== '' && verifiedDomain === domainInput.trim().toLowerCase()
        );

        // --- trusted access pill + server IP (topbar, all steps) -----------
        // Same source of truth as the dashboard and the sign-in page: the
        // secret-free /api/v1/access/status. Nothing is guessed from the URL.
        let access = $state<{ mode: 'https' | 'private'; https?: { host?: string } | null } | null>(null);
        const accessPill = $derived(access ? accessLabel(access) : 'checking access…');
        const accessTitle = $derived(accessLabelTitle(access));
        // The server IP on the address screen comes exclusively from the
        // trusted access status; it is never hardcoded and never taken from
        // the browser URL.
        const serverIp = $derived.by(() => {
                const host = access?.https?.host ?? '';
                return /^(?:\d{1,3}\.){3}\d{1,3}$/.test(host) ? host : '';
        });
        // The copyable "Points to" value in the DNS instructions panel:
        // the IPs the DNS check resolved expectations from, else the
        // trusted server IP.
        const dnsPointsValue = $derived(
                dnsCheckResult?.expected?.join(', ') || serverIp || 'your server public IP'
        );

        // Approved v6 step meta ("Initial setup · Step 1 of 4", ...).
        const stepMeta = $derived.by(() => {
                switch (step) {
                        case 'claim':
                                return { label: 'Initial setup', right: 'Step 1 of 4' };
                        case 'mfa-setup':
                                return { label: 'Account security', right: 'Step 2 of 4' };
                        case 'recovery':
                                return { label: 'Account recovery', right: 'Step 3 of 4' };
                        case 'domain':
                                return domainScreen
                                        ? { label: 'Connect domain', right: 'Optional setup' }
                                        : { label: 'Server access', right: 'Step 4 of 4' };
                        default:
                                return null; // loading and done carry no meta row
                }
        });

        // Completion screen facts (v6 panel). The address is the trusted
        // applied domain or the trusted server IP — never a guessed value.
        const finalAddress = $derived.by(() => {
                if (domainApplied) return 'https://' + domainApplied;
                return serverIp ? 'https://' + serverIp : '';
        });

        $effect(() => {
                if (statusChecked) return;
                statusChecked = true;
                void route();
        });

        $effect(() => {
                fetchAccessStatus()
                        .then((a) => (access = { mode: a.mode, https: a.https ?? null }))
                        .catch(() => (access = null));
        });

        // Pasted setup link (…#token=drv1_…) while the page is already open:
        // changing only the URL fragment does not reload the document and the
        // client router does not navigate, so the token must be adopted in
        // place on hashchange. Only the claim step consumes it — on any later
        // step the token was already used or is not applicable.
        $effect(() => {
                const onHashChange = () => {
                        if (step === 'claim') consumeSetupLinkToken();
                };
                window.addEventListener('hashchange', onHashChange);
                return () => window.removeEventListener('hashchange', onHashChange);
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
                                // MFA is optional: resuming goes to enrolment, which also offers the audited "Skip for now".
                                if (s.authenticated) {
                                        await beginMfaSetup();
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

        /** Show the approved green "Setup token detected" chip. */
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
                mfaSkipped = false;
                try {
                        const res = await postJSON<MfaSetup>('/api/v1/onboarding/mfa/setup', {});
                        totpSecret = res.secret;
                        // renderQrCode returns an SVG with explicit width/height;
                        // a viewBox-only <svg> collapses to 0x0 inside .oqr
                        // (production bug: blank white pill instead of a QR code).
                        qrSvg = await renderQrCode(res.otpauth_uri);
                        resetOtp();
                        mfaDone = false;
                        step = 'mfa-setup';
                } catch (err) {
                        stepError = err instanceof ApiError ? err.message : String(err);
                        // Stay on the setup step: with no secret yet it shows the
                        // error and a retry button (skip is only offered with a
                        // scannable secret on screen).
                        step = 'mfa-setup';
                } finally {
                        busy = false;
                }
        }

        function resetOtp() {
                otpDigits = ['', '', '', '', '', ''];
                otpInvalid = false;
                otpShake = false;
        }

        function clearOtpDom() {
                for (let k = 0; k < OTP_LEN; k++) {
                        otpDigits[k] = '';
                        const el = otpEls[k];
                        if (el) el.value = '';
                }
        }

        function onOtpInput(e: Event, i: number) {
                const el = e.currentTarget as HTMLInputElement;
                const digits = el.value.replace(/\D/g, '');
                el.value = digits.slice(-1);
                otpDigits[i] = el.value;
                otpInvalid = false;
                if (el.value && i < OTP_LEN - 1) otpEls[i + 1]?.focus();
        }

        function onOtpKey(e: KeyboardEvent, i: number) {
                const el = e.currentTarget as HTMLInputElement;
                if (e.key === 'Backspace') {
                        if (!el.value && i > 0) {
                                const prev = otpEls[i - 1];
                                if (prev) prev.value = '';
                                otpDigits[i - 1] = '';
                                prev?.focus();
                                otpInvalid = false;
                                e.preventDefault();
                        } else {
                                otpInvalid = false;
                        }
                        return;
                }
                if (e.key === 'ArrowLeft' && i > 0) {
                        otpEls[i - 1]?.focus();
                        e.preventDefault();
                } else if (e.key === 'ArrowRight' && i < OTP_LEN - 1) {
                        otpEls[i + 1]?.focus();
                        e.preventDefault();
                } else if (e.key === 'Delete') {
                        el.value = '';
                        otpDigits[i] = '';
                        otpInvalid = false;
                        e.preventDefault();
                }
        }

        function onOtpPaste(e: ClipboardEvent) {
                e.preventDefault();
                const digits = (e.clipboardData?.getData('text') ?? '')
                        .replace(/\D/g, '')
                        .slice(0, OTP_LEN)
                        .split('');
                if (!digits.length) return;
                otpInvalid = false;
                for (let k = 0; k < OTP_LEN; k++) {
                        const d = k < digits.length ? digits[k] : '';
                        otpDigits[k] = d;
                        const el = otpEls[k];
                        if (el) el.value = d;
                }
                otpEls[Math.min(digits.length, OTP_LEN - 1)]?.focus();
        }

        async function copySecret() {
                try {
                        await navigator.clipboard.writeText(totpSecret);
                } catch {
                        // Clipboard API can be unavailable; fall back to a temporary
                        // textarea like the approved prototype does.
                        const ta = document.createElement('textarea');
                        ta.value = totpSecret;
                        ta.style.position = 'fixed';
                        ta.style.opacity = '0';
                        document.body.appendChild(ta);
                        ta.select();
                        document.execCommand('copy');
                        ta.remove();
                }
                copyDone = true;
                clearTimeout(copyTimer);
                copyTimer = setTimeout(() => (copyDone = false), 1800);
        }

        // Copy one of the DNS instructions panel values (Host / Name or
        // Points to). Same transient "Copied" pattern as the setup key.
        let dnsCopied = $state<'host' | 'ip' | ''>('');
        let dnsCopyTimer: ReturnType<typeof setTimeout> | undefined;
        async function copyDnsValue(kind: 'host' | 'ip', value: string) {
                if (!value || value === 'your server public IP') return;
                try {
                        await navigator.clipboard.writeText(value);
                } catch {
                        const ta = document.createElement('textarea');
                        ta.value = value;
                        ta.style.position = 'fixed';
                        ta.style.opacity = '0';
                        document.body.appendChild(ta);
                        ta.select();
                        document.execCommand('copy');
                        ta.remove();
                }
                dnsCopied = kind;
                clearTimeout(dnsCopyTimer);
                dnsCopyTimer = setTimeout(() => (dnsCopied = ''), 1800);
        }

        async function submitTotp(e: SubmitEvent) {
                e.preventDefault();
                const code = otpDigits.join('');
                if (code.length !== OTP_LEN) return;
                stepError = '';
                busy = true;
                try {
                        const res = await postJSON<MfaVerified>('/api/v1/onboarding/mfa/verify', { code });
                        recoveryCodes = res.recovery_codes;
                        mfaDone = true;
                        announce('Multi-factor authentication is enabled. Save your recovery codes next.');
                        void tick().then(() => continueEl?.focus({ preventScroll: true }));
                } catch (err) {
                        if (err instanceof ApiError && err.code === 'totp_invalid') {
                                // Inline error state from the prototype: red boxes, shake,
                                // friendly message — no top-of-card banner for this case.
                                otpInvalid = true;
                                clearOtpDom();
                                otpShake = false;
                                await tick();
                                requestAnimationFrame(() => {
                                        requestAnimationFrame(() => (otpShake = true));
                                });
                                otpEls[0]?.focus();
                        } else {
                                stepError = err instanceof ApiError ? err.message : String(err);
                        }
                } finally {
                        busy = false;
                }
        }

        function finishRecovery() {
                if (!codesSaved) return;
                if (returnToDone) {
                        backToDone();
                        return;
                }
                step = 'domain';
        }

        // --- optional MFA: "Skip for now" ----------------------------------

        /** Explicit, audited skip: completes onboarding without MFA. */
        async function confirmSkipMfa() {
                if (!skipConfirm || busy) return;
                busy = true;
                stepError = '';
                try {
                        await skipMfa();
                        skipShown = false;
                        skipConfirm = false;
                        mfaSkipped = true;
                        step = 'domain';
                        announce('Multi-factor skipped for now. You can enable it later in Account Security.');
                } catch (err) {
                        stepError = err instanceof ApiError ? err.message : String(err);
                } finally {
                        busy = false;
                }
        }

        // --- access address: server IP (default) or custom domain ----------

        /** Default path: keep the server-IP address, no extra setup. */
        function proceedWithIp() {
                returnToDone = false;
                step = 'done';
                void loadDoneStats();
        }

        /** Completion checklist: start MFA enrolment in place. */
        function secureAccountFromDone() {
                returnToDone = true;
                void beginMfaSetup();
        }

        /** Completion checklist: jump to the domain configuration screen. */
        function configureDomainFromDone() {
                returnToDone = true;
                step = 'domain';
                domainScreen = true;
        }

        /** Leave the re-entry flow and show the checklist again. */
        function backToDone() {
                returnToDone = false;
                step = 'done';
                void loadDoneStats();
        }

        /** Reset every DNS/HTTPS state — used when the domain input changes. */
        function resetDomainFlow() {
                dnsSeq++;
                verifiedDomain = '';
                dnsCheckResult = null;
                domainMsg = '';
                domainError = '';
                domainStage = 'input';
                domainApplied = '';
        }

        function onDomainInput(e: Event) {
                domainInput = (e.currentTarget as HTMLInputElement).value;
                // A different domain invalidates the previous verification
                // immediately: success panel, instructions and HTTPS readiness.
                resetDomainFlow();
        }

        /** DNS Host/Name field for the instructions panel (approved v6 rule). */
        function dnsHostFor(domain: string): string {
                const labels = domain.split('.');
                return labels.length > 2 ? labels.slice(0, -2).join('.') : '@';
        }

        /** Verify DNS points at this VPS (no changes made). */
        async function submitDomainCheck() {
                domainTouched = true;
                if (domainErr !== '') return;
                const requested = domainInput.trim().toLowerCase();
                const seq = ++dnsSeq;
                domainError = '';
                domainMsg = '';
                dnsCheckResult = null;
                verifiedDomain = '';
                domainBusy = true;
                domainStage = 'checking';
                try {
                        const res: DomainCheckResult = await checkDomain(requested);
                        // Stale-response guard: only an answer for the domain
                        // still in the input may update any state.
                        if (seq !== dnsSeq || domainInput.trim().toLowerCase() !== requested) return;
                        dnsCheckResult = res;
                        if (res.matches) {
                                verifiedDomain = requested;
                                domainMsg = '';
                        } else {
                                verifiedDomain = '';
                                domainMsg = res.message;
                        }
                        domainStage = 'input';
                } catch (err) {
                        if (seq !== dnsSeq || domainInput.trim().toLowerCase() !== requested) return;
                        verifiedDomain = '';
                        dnsCheckResult = null;
                        domainError = err instanceof ApiError ? err.message : String(err);
                        domainStage = 'input';
                } finally {
                        // A stale request never clears the newer request's busy flag.
                        if (seq === dnsSeq) domainBusy = false;
                }
        }

        /**
         * Apply the VERIFIED domain: the backend re-verifies DNS, the
         * certificate is issued and the full route is verified BEFORE
         * success. Any failure leaves the working server-IP HTTPS untouched.
         */
        async function submitDomainApply() {
                domainTouched = true;
                if (domainErr !== '' || !domainChecked || domainBusy) return;
                const requested = verifiedDomain;
                const seq = ++dnsSeq;
                domainError = '';
                domainMsg = '';
                domainBusy = true;
                domainStage = 'applying';
                try {
                        const res = await applyDomain(requested);
                        if (seq !== dnsSeq) return;
                        domainApplied = res.domain;
                        domainStage = 'applied';
                        announce('Custom domain verified. You can open the console at https://' + res.domain + '/');
                } catch (err) {
                        if (seq !== dnsSeq) return;
                        if (err instanceof ApiError) {
                                // The domain stays DNS-verified: the success panel remains
                                // so provisioning can be retried or skipped.
                                domainError = err.message;
                                domainStage = 'input';
                        } else {
                                // Network-level failure: the connection was cut while the
                                // certificate wait was still running (this is what a
                                // "TypeError: Failed to fetch" means here). Provisioning
                                // continues in the background — poll the trusted status
                                // endpoint for the outcome instead of a hard error.
                                const applied = await pollDomainOutcome(requested, seq);
                                if (seq !== dnsSeq) return;
                                if (!applied) {
                                        domainError =
                                                'Lost contact while the certificate was being issued. Provisioning may still finish — open https://' +
                                                requested + ' in a moment, or retry.';
                                        domainStage = 'input';
                                }
                        }
                } finally {
                        if (seq === dnsSeq) domainBusy = false;
                }
        }

        /**
         * Poll the secret-free access status for the domain slot going
         * active (the apply request's connection died mid-wait). Aborts as
         * soon as the sequence moves — the operator edited the input or
         * navigated — and gives up after ~90s, matching the backend's
         * certificate wait window.
         */
        async function pollDomainOutcome(domain: string, seq: number): Promise<boolean> {
                for (let i = 0; i < 45; i++) {
                        await new Promise((r) => setTimeout(r, 2000));
                        if (seq !== dnsSeq) return false;
                        try {
                                const st = await fetchAccessStatus();
                                const d = st.https?.domain;
                                if (d && d.host === domain && d.cert_valid && d.phase === 'active') {
                                        if (seq !== dnsSeq) return false;
                                        domainApplied = d.host;
                                        domainStage = 'applied';
                                        announce('Custom domain verified. You can open the console at https://' + d.host + '/');
                                        return true;
                                }
                                if (d && d.host === domain && d.phase === 'error' && d.last_error) {
                                        domainError = d.last_error;
                                        domainStage = 'input';
                                        return false;
                                }
                        } catch {
                                // Status polls may fail while TLS is busy; keep polling.
                        }
                }
                return false;
        }

        function finishDomain() {
                returnToDone = false;
                step = 'done';
                void loadDoneStats();
        }

        // --- completion summary (checklist + live server stats) ------------
        // The dashboard's own metrics endpoint, fetched once on entering the
        // done step: the onboarding session has full scope by then. Stats are
        // display-only — failures leave the row as em-dashes.
        let doneStats = $state<{ cpu: string; mem: string; disk: string; at: string } | null>(null);
        function fmtBytes(n: number): string {
                if (n >= 1024 ** 3) return (n / 1024 ** 3).toFixed(1) + ' GiB';
                if (n >= 1024 ** 2) return Math.round(n / 1024 ** 2) + ' MiB';
                return Math.round(n / 1024) + ' KiB';
        }
        async function loadDoneStats() {
                doneStats = null;
                try {
                        const m = await fetchMetrics();
                        const disk = m.disk && m.disk.length > 0 ? m.disk[0] : null;
                        doneStats = {
                                cpu: Math.round(m.cpu.usage_percent) + '%',
                                mem: Math.round(m.memory.used_percent) + '% / ' + fmtBytes(m.memory.total_bytes),
                                disk:
                                        (disk ? fmtBytes(disk.used_bytes) : '—') +
                                        ' / ' +
                                        (disk ? fmtBytes(disk.total_bytes) : '—'),
                                at: new Date().toLocaleTimeString([], {
                                        hour: '2-digit',
                                        minute: '2-digit',
                                        second: '2-digit'
                                })
                        };
                } catch {
                        doneStats = null;
                }
        }
        function gotoDashboard() {
                void goto('/', { replaceState: true });
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

        <div class="obshell">
                <header class="otopbar">
                        <div class="obrand">
                                <span class="obrand__mark" aria-hidden="true">D</span>
                                <h1 class="obrand__word">drevanta<i>.</i></h1>
                        </div>
                        <span
                                class="opill"
                                class:is-https={access?.mode === 'https'}
                                class:is-private={access?.mode === 'private'}
                                title={accessTitle}
                        >
                                <span class="opill__dot" aria-hidden="true"></span>
                                {accessPill}
                        </span>
                </header>

                <main class="obcard">

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
                        <div class="ometa-top"><span>{stepMeta?.label}</span><span>{stepMeta?.right}</span></div>
                        <section class="ointro">
                                <h2>Create your admin account</h2>
                                <p>Set up the credentials you'll use to manage this server.</p>
                        </section>
                        <form class="oform" novalidate onsubmit={submitClaim}>
                                {#if tokenMode === 'chip'}
                                        <!-- locked compact token row: loaded ≠ verified; the
                                             backend checks the single-use token only on claim -->
                                        <div class="otoken-row" role="group" aria-label="Setup token loaded">
                                                <span class="otoken-row__badge" aria-hidden="true">
                                                        <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.4" stroke-linecap="round" stroke-linejoin="round">
                                                                <path d="M20 6 9 17l-5-5" />
                                                        </svg>
                                                </span>
                                                <span class="otoken-row__label">Setup token detected</span>
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
                                        <label class="olabel" for="username">Administrator username</label>
                                        <input
                                                class="oinput"
                                                id="username"
                                                name="username"
                                                type="text"
                                                placeholder="e.g. admin"
                                                autocomplete="username"
                                                autocapitalize="off"
                                                spellcheck="false"
                                                maxlength="32"
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
                                                        placeholder="Create a secure password"
                                                        autocomplete="new-password"
                                                        maxlength={PASSWORD_MAX}
                                                        aria-describedby="pw-err pw-strength"
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
                                                                width="16"
                                                                height="16"
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
                                                                width="16"
                                                                height="16"
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

                                        <div class="ometer" aria-hidden="true">
                                                <span class="ometer__bar" class:on={pwLevel >= 1} style:background={pwLevel >= 1 ? strengthColor : ''}></span>
                                                <span class="ometer__bar" class:on={pwLevel >= 2} style:background={pwLevel >= 2 ? strengthColor : ''}></span>
                                                <span class="ometer__bar" class:on={pwLevel >= 3} style:background={pwLevel >= 3 ? strengthColor : ''}></span>
                                                <span class="ometer__bar" class:on={pwLevel >= 4} style:background={pwLevel >= 4 ? strengthColor : ''}></span>
                                        </div>
                                        <div class="ostrength-row" id="pw-strength">
                                                <span>Password strength</span>
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
                                                        <svg class="icon-show" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
                                                                <path d="M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7-10-7-10-7z" />
                                                                <circle cx="12" cy="12" r="3" />
                                                        </svg>
                                                        <svg class="icon-hide" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
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
                                        <span>{busy ? 'Creating administrator…' : 'Create administrator →'}</span>
                                </button>
                        </form>

                        <div class="odivider"></div>
                        <div class="ometa">
                                <span>argon2id</span><span class="dot">·</span>
                                <span>single-use setup token</span>
                        </div>

                        <details class="odisclosure">
                                <summary>
                                        Where do I find my setup token?
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
                {:else if step === 'mfa-setup'}
                        <div class="ometa-top"><span>{stepMeta?.label}</span><span>{stepMeta?.right}</span></div>
                        {#if mfaDone}
                                <section class="osuccess" role="status">
                                        <div class="osuccess__badge" aria-hidden="true">
                                                <svg width="20" height="20" viewBox="0 0 24 24" fill="none">
                                                        <path d="M4.5 12.5l4.6 4.6L19.5 6.8" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" />
                                                </svg>
                                        </div>
                                        <h2>MFA enabled</h2>
                                        <p>
                                                You'll need your authenticator app the next time you sign in to the Drevanta
                                                console. Next: save your recovery codes.
                                        </p>
                                        <button class="obtn" type="button" bind:this={continueEl} onclick={() => (step = 'recovery')}>
                                                Continue →
                                        </button>
                                </section>
                        {:else if totpSecret}
                                <section class="ointro ointro--tight">
                                        <h2>Scan and confirm</h2>
                                </section>

                                <div class="opanel">
                                        <div class="opanel__head">
                                                <strong>Authenticator setup</strong>
                                                <span class="otag">Recommended</span>
                                        </div>
                                        <div class="oqr-row">
                                                <div class="oqr" aria-hidden="true">{@html qrSvg}</div>
                                                <div class="oqr-copy">
                                                        <strong>Scan with your app</strong>
                                                        <p>Google Authenticator, Microsoft Authenticator, or another TOTP app.</p>
                                                        <button
                                                                type="button"
                                                                class="olinkbtn"
                                                                aria-expanded={mfaHelpOpen}
                                                                aria-controls="mfa-help"
                                                                onclick={() => (mfaHelpOpen = !mfaHelpOpen)}
                                                        >
                                                                {mfaHelpOpen ? 'Hide help' : 'How does this work?'}
                                                        </button>
                                                </div>
                                        </div>
                                        <div class="omfa-help" id="mfa-help" hidden={!mfaHelpOpen}>
                                                <p>In your authenticator app, tap <strong>Add account</strong>, scan the QR code, then enter the six-digit code shown in the app.</p>
                                                <p>Need an authenticator app? Get Google Authenticator from the <a href="https://play.google.com/store/apps/details?id=com.google.android.apps.authenticator2" target="_blank" rel="noopener noreferrer">Google Play Store</a> or the <a href="https://apps.apple.com/app/google-authenticator/id388497605" target="_blank" rel="noopener noreferrer">Apple App Store</a>.</p>
                                        </div>
                                        <div class="odivider"></div>
                                        <div class="olabel-row">
                                                <span class="olabel" id="setup-key-label">Secret (manual entry)</span>
                                                <button
                                                        type="button"
                                                        class="okey-field__copy"
                                                        aria-expanded={secretRevealed}
                                                        aria-controls="setup-key-field"
                                                        onclick={() => (secretRevealed = !secretRevealed)}
                                                >
                                                        {secretRevealed ? 'Hide' : 'Reveal'}
                                                </button>
                                        </div>
                                        <div class="okey-field" id="setup-key-field" hidden={!secretRevealed}>
                                                <!-- Same disclosure pattern as "How does this work?": the
                                                     whole field is collapsed until Reveal, and while visible
                                                     the readonly input always holds the full plaintext
                                                     secret; Copy still copies the raw (ungrouped) secret. -->
                                                <input
                                                        id="setup-key"
                                                        type="text"
                                                        class="okey-field__secret"
                                                        readonly
                                                        autocomplete="off"
                                                        spellcheck="false"
                                                        aria-labelledby="setup-key-label"
                                                        value={secretDisplay}
                                                />
                                                <button
                                                        type="button"
                                                        class="okey-field__copy"
                                                        class:done={copyDone}
                                                        onclick={copySecret}
                                                        aria-label="Copy setup key"
                                                >
                                                        {copyDone ? 'Copied' : 'Copy'}
                                                </button>
                                        </div>
                                </div>

                                <form class="oform" novalidate onsubmit={submitTotp}>
                                        <div class="ofield">
                                                <label class="olabel" for="otp0">6-digit code</label>
                                                <div class="ootp" class:invalid={otpInvalid} class:shake={otpShake}>
                                                        {#each otpDigits as _, i (i)}
                                                                <input
                                                                        id="otp{i}"
                                                                        type="text"
                                                                        inputmode="numeric"
                                                                        autocomplete={i === 0 ? 'one-time-code' : 'off'}
                                                                        maxlength="1"
                                                                        aria-label="Digit {i + 1} of 6"
                                                                        aria-invalid={otpInvalid}
                                                                        bind:value={otpDigits[i]}
                                                                        bind:this={otpEls[i]}
                                                                        oninput={(e) => onOtpInput(e, i)}
                                                                        onkeydown={(e) => onOtpKey(e, i)}
                                                                        onpaste={onOtpPaste}
                                                                        onfocus={(e) => (e.currentTarget as HTMLInputElement).select()}
                                                                />
                                                        {/each}
                                                </div>
                                                <p class="ohint">Enter the current code shown in your authenticator app.</p>
                                                <p class="oerr oerr--code" role="alert" hidden={!otpInvalid}>
                                                        <svg width="12" height="12" viewBox="0 0 16 16" fill="none" aria-hidden="true">
                                                                <circle cx="8" cy="8" r="6.4" stroke="currentColor" stroke-width="1.4" />
                                                                <path d="M8 4.9v3.6" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" />
                                                                <circle cx="8" cy="11.1" r=".85" fill="currentColor" />
                                                        </svg>
                                                        <span>That code didn't work. Enter the newest 6-digit code from your authenticator app and try again.</span>
                                                </p>
                                        </div>
                                        <button class="obtn" type="submit" disabled={busy || otpCode.length !== OTP_LEN} class:is-loading={busy} aria-busy={busy}>
                                                <span class="obtn__spinner" aria-hidden="true"></span>
                                                <span>{busy ? 'Verifying…' : 'Verify and enable MFA'}</span>
                                        </button>
                                </form>

                                {#if returnToDone}
                                        <div class="oskip-row">
                                                <span class="oskip-row__text">MFA is optional — you can also finish this later.</span>
                                                <button class="olinkbtn oskip-row__btn" type="button" onclick={backToDone}>
                                                        ← Back to checklist
                                                </button>
                                        </div>
                                {:else if !skipShown}
                                        <div class="oskip-row">
                                                <span class="oskip-row__text">Recommended, not required.</span>
                                                <button class="olinkbtn oskip-row__btn" type="button" onclick={() => (skipShown = true)}>
                                                        Skip for now
                                                </button>
                                        </div>
                                {:else}
                                        <div class="owarn" role="alertdialog" aria-label="Skip multi-factor authentication?">
                                                <p class="owarn__title">Skip multi-factor authentication?</p>
                                                <ul class="owarn__list">
                                                        <li>Anyone with your password could sign in as you.</li>
                                                        <li>A stolen password could be enough to take over this server.</li>
                                                </ul>
                                                <p class="owarn__note">You can enable MFA later under Account Security. This choice is recorded in the audit log.</p>
                                                <label class="ocheck-row ocheck-row--tight">
                                                        <input type="checkbox" bind:checked={skipConfirm} />
                                                        <span>I understand the risk and want to skip MFA for now.</span>
                                                </label>
                                                <div class="owarn__actions">
                                                        <button class="obtn obtn--danger" type="button" disabled={!skipConfirm || busy} onclick={confirmSkipMfa} aria-busy={busy}>
                                                                <span class="obtn__spinner" aria-hidden="true"></span>
                                                                <span>{busy ? 'Skipping…' : 'Skip MFA for now'}</span>
                                                        </button>
                                                        <button class="obtn obtn--secondary" type="button" onclick={() => { skipShown = false; skipConfirm = false; }}>
                                                                Go back and enable MFA
                                                        </button>
                                                </div>
                                        </div>
                                {/if}
                        {:else}
                                <section class="ointro">
                                        <h2>Multi-factor setup</h2>
                                        <p>{stepError || 'The authenticator setup could not be prepared.'}</p>
                                </section>
                                <div class="oactions">
                                        <button class="obtn" type="button" onclick={beginMfaSetup} disabled={busy} class:is-loading={busy} aria-busy={busy}>
                                                <span class="obtn__spinner" aria-hidden="true"></span>
                                                <span>{busy ? 'Preparing…' : 'Try again'}</span>
                                        </button>
                                </div>
                        {/if}
                {:else if step === 'recovery'}
                        <div class="ometa-top"><span>{stepMeta?.label}</span><span>{stepMeta?.right}</span></div>
                        <section class="ointro">
                                <h2>Save your recovery codes</h2>
                                <p>
                                        Each code works once if your authenticator is unavailable. These are shown
                                        <strong>only once</strong>; Drevanta stores only their hashes.
                                </p>
                        </section>
                        <div class="opanel">
                                <div class="opanel__head">
                                        <strong>Recovery codes</strong>
                                        <span class="otag otag--dim">{recoveryCodes.length} one-time codes</span>
                                </div>
                                <div class="ocodegrid">
                                        {#each recoveryCodes as c, i (c)}
                                                <div class="ocode">
                                                        <small>{String(i + 1).padStart(2, '0')}</small>
                                                        <code>{c}</code>
                                                </div>
                                        {/each}
                                </div>
                                <button class="obtn obtn--secondary" type="button" onclick={copyCodes}>Copy all codes</button>
                        </div>
                        <label class="ocheck-row">
                                <input type="checkbox" bind:checked={codesSaved} />
                                <span>I have stored these codes somewhere safe (password manager or printed copy).</span>
                        </label>
                        <button class="obtn" type="button" onclick={finishRecovery} disabled={!codesSaved}>
                                Continue →
                        </button>
                {:else if step === 'domain'}
                        {#if !domainScreen}
                                <div class="ometa-top"><span>{stepMeta?.label}</span><span>{stepMeta?.right}</span></div>
                                <section class="ointro">
                                        <h2>Choose your server address</h2>
                                        <p>Your console is already secured. Keep your server IP or connect a domain you own.</p>
                                </section>
                                <div class="ochoice" role="radiogroup" aria-label="Server address">
                                        <button
                                                class="ochoice__opt"
                                                class:is-active={domainChoice === 'ip'}
                                                type="button"
                                                role="radio"
                                                aria-checked={domainChoice === 'ip'}
                                                onclick={() => (domainChoice = 'ip')}
                                        >
                                                <span class="oradio" aria-hidden="true"></span>
                                                <span class="ochoice__text">
                                                        <span class="ochoice__title">Continue with the server IP</span>
                                                        <span class="ochoice__sub">{serverIp ? serverIp + ' · ' : ''}Ready now — no extra setup</span>
                                                </span>
                                        </button>
                                        <button
                                                class="ochoice__opt"
                                                class:is-active={domainChoice === 'domain'}
                                                type="button"
                                                role="radio"
                                                aria-checked={domainChoice === 'domain'}
                                                onclick={() => (domainChoice = 'domain')}
                                        >
                                                <span class="oradio" aria-hidden="true"></span>
                                                <span class="ochoice__text">
                                                        <span class="ochoice__title">I have a domain</span>
                                                        <span class="ochoice__sub">Connect your own domain or subdomain</span>
                                                </span>
                                        </button>
                                </div>
                                <button class="obtn" type="button" onclick={() => (domainChoice === 'ip' ? proceedWithIp() : (domainScreen = true))}>
                                        {domainChoice === 'ip' ? 'Proceed with the server IP →' : 'Set up your domain →'}
                                </button>
                                <p class="osubtle-center">You can change your address later in Settings.</p>
                        {:else}
                                <div class="ometa-top"><span>{stepMeta?.label}</span><span>{stepMeta?.right}</span></div>
                                <section class="ointro">
                                        <h2>Connect your domain</h2>
                                        <p>Enter a domain you own. We'll check whether its DNS points to your server before setting up HTTPS.</p>
                                </section>
                                <div class="ofield" class:has-error={domainTouched && domainErr !== ''}>
                                        <label class="olabel" for="domain-input">Domain or subdomain</label>
                                        <input
                                                class="oinput oinput--mono"
                                                id="domain-input"
                                                type="text"
                                                placeholder="panel.example.com"
                                                autocomplete="off"
                                                autocapitalize="off"
                                                spellcheck="false"
                                                aria-describedby="domain-hint domain-err"
                                                aria-invalid={domainTouched && domainErr !== ''}
                                                value={domainInput}
                                                oninput={onDomainInput}
                                                onblur={() => (domainTouched = true)}
                                        />
                                        <p class="oerr" id="domain-hint" hidden={!(domainTouched && domainErr !== '')}>{domainErr}</p>
                                </div>
                                {#if domainStage !== 'applied'}
                                        <button
                                                class="obtn"
                                                type="button"
                                                disabled={domainBusy || domainErr !== '' || domainChecked}
                                                class:is-loading={domainBusy && domainStage === 'checking'}
                                                aria-busy={domainBusy}
                                                onclick={submitDomainCheck}
                                        >
                                                <span class="obtn__spinner" aria-hidden="true"></span>
                                                <span>{domainStage === 'checking' ? 'Checking DNS…' : domainChecked ? 'DNS verified' : 'Verify DNS →'}</span>
                                        </button>
                                {/if}
                                {#if domainChecked && domainStage !== 'applied'}
                                        <div class="odns-success" role="status">
                                                <span class="odns-success__icon" aria-hidden="true">
                                                        <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.4" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5" /></svg>
                                                </span>
                                                <span><strong>DNS check passed.</strong> Your domain points to this server.</span>
                                        </div>
                                        <button
                                                class="obtn"
                                                type="button"
                                                disabled={domainBusy}
                                                class:is-loading={domainBusy && domainStage === 'applying'}
                                                aria-busy={domainBusy}
                                                onclick={submitDomainApply}
                                        >
                                                <span class="obtn__spinner" aria-hidden="true"></span>
                                                <span>{domainStage === 'applying' ? 'Enabling and verifying HTTPS…' : 'Enable and verify HTTPS →'}</span>
                                        </button>
                                {:else if dnsCheckResult && !dnsCheckResult.matches && !domainChecked && domainStage !== 'applied'}
                                        <div class="ofeedback" role="status">
                                                {domainMsg || dnsCheckResult.message || 'This domain does not currently point to your server.'}
                                        </div>
                                        <div class="opanel odns-instructions">
                                                <strong>Domain not connected yet</strong>
                                                <p class="odns-instructions__lead">At your domain's DNS provider, create or update the record below, then refresh verification.</p>
                                                <div class="odns-row"><span>Type</span><strong>A</strong></div>
                                                <div class="odns-row">
                                                        <span>Host / Name</span>
                                                        <strong class="mono">{dnsHostValue}</strong>
                                                        <button type="button" class="orow-copy" aria-label="Copy host name" onclick={() => copyDnsValue('host', dnsHostValue)}>
                                                                {dnsCopied === 'host' ? 'Copied' : 'Copy'}
                                                        </button>
                                                </div>
                                                <div class="odns-row">
                                                        <span>Points to</span>
                                                        <strong class="mono">{dnsPointsValue}</strong>
                                                        <button type="button" class="orow-copy" aria-label="Copy IP address" onclick={() => copyDnsValue('ip', dnsPointsValue)}>
                                                                {dnsCopied === 'ip' ? 'Copied' : 'Copy'}
                                                        </button>
                                                </div>
                                                <p class="odns-instructions__note">The host name depends on your DNS provider and zone. Remove conflicting records if needed. DNS updates may take time.</p>
                                                <div class="oactions">
                                                        <button class="obtn obtn--secondary" type="button" disabled={domainBusy || domainErr !== ''} onclick={submitDomainCheck}>
                                                                ↻ Refresh verification
                                                        </button>
                                                </div>
                                        </div>
                                {/if}
                                {#if domainError}
                                        <p class="oerr" id="domain-err" role="alert">{domainError}</p>
                                {/if}
                                {#if domainStage === 'applied'}
                                        <div class="oapplied" role="status">
                                                <p class="oapplied__title">
                                                        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 6 9 17l-5-5" /></svg>
                                                        Domain secured with HTTPS
                                                </p>
                                                <p><strong>https://{domainApplied}</strong> is verified and serving. Your original IP address remains available.</p>
                                                <button class="obtn" type="button" onclick={finishDomain}>Continue →</button>
                                        </div>
                                {/if}
                                <div class="odivider"></div>
                                <div class="odomain-exit">
                                        <button class="obtn obtn--secondary" type="button" onclick={proceedWithIp}>Skip — keep using the server IP</button>
                                        {#if returnToDone}
                                                <button class="olinkbtn" type="button" onclick={backToDone}>← Back to checklist</button>
                                        {:else}
                                                <button class="olinkbtn" type="button" onclick={() => (domainScreen = false)}>← Back to address choices</button>
                                        {/if}
                                </div>
                        {/if}
                {:else if step === 'done'}
                        <section class="odone">
                                <div class="osuccess__badge" aria-hidden="true">
                                        <svg width="20" height="20" viewBox="0 0 24 24" fill="none">
                                                <path d="M4.5 12.5l4.6 4.6L19.5 6.8" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" />
                                        </svg>
                                </div>
                                <h2>You're all set</h2>
                                <p>Onboarding is complete.</p>
                                <div class="opanel odone__panel">
                                        <div class="odone__row">
                                                <span class="odone__mark is-done" aria-hidden="true">
                                                        <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.4" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5" /></svg>
                                                </span>
                                                <span class="odone__name">Admin credentials</span>
                                                <span class="odone__val">Completed</span>
                                        </div>
                                        <div class="odone__row">
                                                {#if mfaDone}
                                                        <span class="odone__mark is-done" aria-hidden="true">
                                                                <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.4" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5" /></svg>
                                                        </span>
                                                        <span class="odone__name">MFA authentication</span>
                                                        <span class="odone__val">Account secured</span>
                                                {:else}
                                                        <span class="odone__mark is-skip" aria-hidden="true">
                                                                <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.2" stroke-linecap="round"><path d="M18 6 6 18M6 6l12 12" /></svg>
                                                        </span>
                                                        <span class="odone__name">MFA authentication</span>
                                                        <span class="odone__val">Not set</span>
                                                        <button class="olinkbtn odone__action" type="button" onclick={secureAccountFromDone}>
                                                                Secure your account
                                                        </button>
                                                {/if}
                                        </div>
                                        <div class="odone__row">
                                                <span class="odone__mark is-done" aria-hidden="true">
                                                        <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.4" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5" /></svg>
                                                </span>
                                                <span class="odone__name">Domain configuration</span>
                                                <span class="odone__val mono">{finalAddress || serverIp || accessPill}</span>
                                                {#if !domainApplied && serverIp}
                                                        <button class="olinkbtn odone__action" type="button" onclick={configureDomainFromDone}>
                                                                Configure domain
                                                        </button>
                                                {/if}
                                        </div>
                                        <div class="odivider"></div>
                                        <div class="odone__stats mono">
                                                <span>cpu: {doneStats ? doneStats.cpu : '—'}</span>
                                                <span>memory: {doneStats ? doneStats.mem : '—'}</span>
                                                <span>disk: {doneStats ? doneStats.disk : '—'}</span>
                                        </div>
                                        {#if doneStats}
                                                <p class="odone__updated">Updated at: {doneStats.at}</p>
                                        {/if}
                                        <button class="obtn odone__proceed" type="button" onclick={gotoDashboard}>Proceed to dashboard →</button>
                                </div>
                        </section>
                {/if}
                </main>
        </div>
</div>

<style>
        /* Approved v6 design tokens, scoped to this page so the dashboard's
           global palette is untouched. Values come from the approved HTML v6
           prototype (teal-tinted dark theme, mint accent). */
        .ob {
                --bg: #0b1417;
                --card: #142024;
                --raised: #1a2b2e;
                --input: #0e191d;
                --line: #304348;
                --line-hi: #3b5052;
                --line-soft: #243437;
                --text: #ecf4f1;
                --muted: #a0b3b4;
                --dim: #819a9b;
                --accent: #83d9bf;
                --accent-deep: #0b2822;
                --accent-glow: rgba(131, 217, 191, 0.094);
                --danger: #f1aaa1;
                --warn: #e2bb7e;
                --mono: ui-monospace, SFMono-Regular, 'SF Mono', Menlo, Consolas, 'Liberation Mono', monospace;

                min-height: 100vh;
                display: grid;
                place-items: center;
                padding: 22px 12px 18px;
                background: radial-gradient(ellipse at 50% 0%, #19312e 0%, #0b1417 56%);
                color: var(--text);
                font-family: Inter, ui-sans-serif, system-ui, -apple-system, 'Segoe UI', sans-serif;
                font-size: 13px;
                line-height: 1.55;
        }

        .ob ::selection {
                background: rgba(131, 217, 191, 0.28);
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
           component display values (.oerr uses flex, panels are blocks) so
           untouched error paragraphs truly vanish. */
        .ob [hidden] {
                display: none !important;
        }

        /* font only: `color: inherit` here would out-specify every button's
           own color rule (`.ob.svelte-x button` = 0,2,1 beats `.obtn.svelte-x`
           = 0,2,0) and rendered every primary button's text white instead of
           the prototype's near-black #0b2923. Every button/input class sets
           its own color explicitly. */
        .ob button,
        .ob input {
                font: inherit;
        }

        .ob button {
                cursor: pointer;
        }

        .ob button:disabled {
                cursor: not-allowed;
                opacity: 0.48;
                transform: none;
        }

        /* Visible keyboard focus everywhere, as in the approved prototype. */
        .ob button:focus-visible,
        .ob input:focus-visible,
        .ob summary:focus-visible,
        .ob a:focus-visible {
                outline: 2px solid var(--accent);
                outline-offset: 3px;
        }

        .obshell {
                width: 100%;
                max-width: 436px;
        }

        /* ---------- header: centered brand + trusted access pill ---------- */
        .otopbar {
                display: grid;
                grid-template-columns: 1fr auto 1fr;
                align-items: center;
                gap: 10px;
                margin-bottom: 13px;
        }

        .obrand {
                grid-column: 2;
                display: flex;
                align-items: center;
                justify-content: center;
                gap: 7px;
                min-width: 0;
        }

        .obrand__mark {
                width: 25px;
                height: 25px;
                flex: 0 0 auto;
                display: grid;
                place-items: center;
                background: var(--accent);
                border-radius: 7px;
                color: var(--accent-deep);
                font-size: 15px;
                font-weight: 850;
                line-height: 1;
        }

        .obrand__word {
                margin: 0;
                font-size: 15px;
                font-weight: 750;
                letter-spacing: -0.45px;
                color: var(--text);
                white-space: nowrap;
        }

        .obrand__word i {
                color: var(--accent);
                font-style: normal;
        }

        /* Trusted access chip — same data as the dashboard, v6-tinted. */
        .opill {
                grid-column: 3;
                justify-self: end;
                display: inline-flex;
                align-items: center;
                gap: 6px;
                font-family: var(--mono);
                font-size: 10px;
                letter-spacing: 0.02em;
                color: var(--muted);
                border: 1px solid var(--line);
                background: var(--input);
                border-radius: 999px;
                padding: 3px 9px;
                white-space: nowrap;
        }

        .opill__dot {
                width: 6px;
                height: 6px;
                border-radius: 50%;
                background: var(--dim);
        }

        .opill.is-https .opill__dot {
                background: var(--accent);
                box-shadow: 0 0 0 2.5px var(--accent-glow);
        }

        .opill.is-private .opill__dot {
                background: var(--warn);
                box-shadow: 0 0 0 2.5px rgba(226, 187, 126, 0.16);
        }

        /* ---------- card ---------- */
        .obcard {
                position: relative;
                width: 100%;
                max-width: 436px;
                padding: 21px 23px;
                border: 1px solid var(--line);
                background: var(--card);
                border-radius: 14px;
                box-shadow: 0 18px 55px rgba(0, 0, 0, 0.25);
                animation: rise 0.45s cubic-bezier(0.2, 0.7, 0.3, 1) both;
        }

        @keyframes rise {
                from {
                        opacity: 0;
                        transform: translateY(10px);
                }
                to {
                        opacity: 1;
                        transform: none;
                }
        }

        /* ---------- step meta (v6: label · Step N of 4) ---------- */
        .ometa-top {
                display: flex;
                align-items: center;
                justify-content: space-between;
                gap: 10px;
                color: #9cb4b2;
                font-size: 9px;
                font-weight: 750;
                letter-spacing: 1.1px;
                text-transform: uppercase;
                margin-bottom: 12px;
        }

        .ometa-top span:last-child {
                display: inline-flex;
                align-items: center;
                color: #9cb4b2;
                font-size: 11px;
                font-weight: 500;
                letter-spacing: 0;
                text-transform: none;
        }

        .ometa-top span:last-child::before {
                content: '';
                display: inline-block;
                width: 6px;
                height: 6px;
                border-radius: 50%;
                background: var(--accent);
                margin-right: 7px;
        }

        /* ---------- intro / loading / error ---------- */
        .ointro h2 {
                margin: 0 0 5px;
                font-size: 19px;
                font-weight: 700;
                letter-spacing: -0.45px;
                line-height: 1.25;
        }

        .ointro p {
                margin: 0 0 13px;
                font-size: 11.5px;
                line-height: 1.5;
                color: var(--muted);
        }

        .ointro--tight h2 {
                margin-bottom: 5px;
        }

        .oloading {
                color: var(--dim);
                padding: 32px 0;
                text-align: center;
                margin: 0;
        }

        .oerror {
                display: flex;
                flex-wrap: wrap;
                align-items: baseline;
                gap: 4px 10px;
                background: rgba(241, 170, 161, 0.08);
                border: 1px solid rgba(241, 170, 161, 0.4);
                color: #ffd9d2;
                padding: 10px 12px;
                border-radius: 9px;
                font-size: 11.5px;
                line-height: 1.5;
                margin-bottom: 12px;
        }

        .oerror a {
                color: var(--accent);
                font-weight: 600;
        }

        /* ---------- fields ---------- */
        .oform {
                display: grid;
                gap: 11px;
        }

        .ofield {
                display: grid;
                gap: 5px;
                margin-top: 0;
        }

        .olabel-row {
                display: flex;
                align-items: center;
                justify-content: space-between;
                gap: 10px;
        }

        .olabel {
                font-size: 11px;
                font-weight: 650;
                color: #d1dfda;
        }

        .olabel-note {
                font-size: 10px;
                color: var(--dim);
        }

        .ocontrol {
                position: relative;
        }

        .ocontrol .oinput {
                padding-right: 52px;
        }

        .oinput {
                width: 100%;
                min-width: 0;
                min-height: 38px;
                padding: 0 12px;
                background: var(--input);
                border: 1px solid var(--line-hi);
                color: var(--text);
                border-radius: 8px;
                outline: none;
                font-size: 12px;
                transition: border-color 0.15s, box-shadow 0.15s;
        }

        .oinput--mono {
                font-family: var(--mono);
        }

        .oinput:focus {
                border-color: var(--accent);
                box-shadow: 0 0 0 3px var(--accent-glow);
        }

        .oinput::placeholder {
                color: #71868a;
        }

        .ofield.has-error .oinput {
                border-color: rgba(241, 170, 161, 0.65);
        }

        .ofield.is-valid .oinput {
                border-color: #3c6258;
        }

        .ocontrol__btn {
                position: absolute;
                right: 7px;
                top: 50%;
                transform: translateY(-50%);
                display: grid;
                place-items: center;
                width: 28px;
                height: 28px;
                background: none;
                border: 0;
                border-radius: 6px;
                color: var(--dim);
        }

        .ocontrol__btn:hover {
                color: var(--text);
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

        .oeye.is-on {
                color: var(--accent);
        }

        .ostatus {
                position: absolute;
                right: 11px;
                top: 50%;
                transform: translateY(-50%);
                display: grid;
                place-items: center;
                color: var(--accent);
                pointer-events: none;
        }

        .ohint {
                font-size: 11px;
                color: var(--dim);
                margin: 1px 0 0;
        }

        .ohint--warn {
                color: #f1d6aa;
        }

        .oerr {
                display: flex;
                gap: 6px;
                align-items: flex-start;
                font-size: 11px;
                color: var(--danger);
                margin: 0;
        }

        /* Standalone errors (not inside a field grid) sit 5px under the
           preceding block, as in the prototype (.error margin-top). */
        .obcard > .oerr {
                margin-top: 5px;
        }

        .oerr svg {
                flex: 0 0 auto;
                margin-top: 2px;
        }

        .oerr--code {
                color: var(--danger);
        }

        /* ---------- password strength meter (v6) ---------- */
        .ometer {
                display: flex;
                gap: 4px;
                margin-top: 8px;
        }

        .ometer__bar {
                height: 3px;
                flex: 1;
                background: #2d3d3f;
                border-radius: 5px;
                transition: background 0.2s;
        }

        .ostrength-row {
                display: flex;
                align-items: center;
                justify-content: space-between;
                gap: 10px;
                margin-top: 6px;
                font-size: 11px;
                color: var(--muted);
        }

        .ostrength {
                font-weight: 400;
        }

        /* ---------- buttons ---------- */
        .obtn {
                width: 100%;
                min-height: 38px;
                display: inline-flex;
                align-items: center;
                justify-content: center;
                gap: 8px;
                padding: 8px 10px;
                border: 1px solid var(--accent);
                background: var(--accent);
                color: #0b2923;
                font-size: 12px;
                font-weight: 750;
                border-radius: 8px;
                transition: background 0.15s, transform 0.15s, border-color 0.15s;
        }

        .obtn:hover:not(:disabled) {
                background: #a4e7d3;
                transform: translateY(-1px);
        }

        .obtn:active:not(:disabled) {
                transform: translateY(0);
        }

        .obtn:disabled {
                filter: none;
        }

        .obtn--secondary {
                background: transparent;
                border-color: #455c5e;
                color: #e1ece8;
        }

        .obtn--secondary:hover:not(:disabled) {
                background: #26383b;
        }

        .obtn--danger {
                background: #49302d;
                border-color: #99665d;
                color: #ffddd7;
        }

        .obtn--danger:hover:not(:disabled) {
                background: #563832;
        }

        .obtn__spinner {
                display: none;
                width: 12px;
                height: 12px;
                flex: 0 0 auto;
                border: 2px solid currentColor;
                border-right-color: transparent;
                border-radius: 50%;
        }

        .obtn.is-loading .obtn__spinner {
                display: inline-block;
                animation: spin 0.7s linear infinite;
        }

        @keyframes spin {
                to {
                        transform: rotate(360deg);
                }
        }

        .olinkbtn {
                background: none;
                border: 0;
                padding: 3px 0;
                color: var(--accent);
                font-size: 12px;
                font-weight: 650;
        }

        .olinkbtn:hover {
                text-decoration: underline;
        }

        .oactions {
                display: grid;
                gap: 7px;
                margin-top: 13px;
        }

        /* Bare buttons that follow a field/check/choice block get the
           prototype's .actions margin-top (13px) — e.g. Verify DNS. */
        .ofield + .obtn,
        .ocheck-row + .obtn,
        .ochoice + .obtn {
                margin-top: 13px;
        }

        .oform > .obtn {
                margin-top: 2px;
        }

        /* ---------- token chip (v6 panel.token) ---------- */
        .otoken-row {
                display: flex;
                align-items: center;
                gap: 8px;
                padding: 9px 11px;
                background: #1a302e;
                border: 1px solid #3c6258;
                border-radius: 10px;
        }

        .otoken-row__badge {
                display: grid;
                place-items: center;
                color: var(--accent);
                flex: 0 0 auto;
        }

        .otoken-row__label {
                font-size: 11px;
                font-weight: 700;
                color: var(--text);
                white-space: nowrap;
        }

        .otoken-row__mask {
                flex: 1 1 auto;
                min-width: 0;
                overflow: hidden;
                text-overflow: ellipsis;
                white-space: nowrap;
                font-family: var(--mono);
                font-size: 11px;
                color: var(--muted);
        }

        .otoken-row__action {
                margin-left: auto;
                flex: 0 0 auto;
                background: none;
                border: 0;
                padding: 3px 0;
                color: var(--accent);
                font-size: 12px;
                font-weight: 650;
        }

        .otoken-row__action:hover {
                text-decoration: underline;
        }

        /* ---------- panels (v6 .panel) ---------- */
        .opanel {
                background: var(--raised);
                border: 1px solid #315052;
                border-radius: 10px;
                padding: 11px;
                margin-top: 2px;
        }

        .opanel__head {
                display: flex;
                align-items: center;
                justify-content: space-between;
                gap: 10px;
                margin-bottom: 8px;
        }

        .opanel__head strong {
                font-size: 12px;
                color: var(--text);
        }

        .otag {
                font-size: 11px;
                font-weight: 400;
                color: var(--accent);
        }

        .otag--dim {
                color: var(--muted);
        }

        /* ---------- MFA: QR + manual secret ---------- */
        .oqr-row {
                display: flex;
                align-items: center;
                gap: 11px;
                margin-top: 9px;
        }

        .oqr {
                width: 112px;
                height: 112px;
                flex: 0 0 112px;
                background: #ffffff;
                padding: 8px;
                border-radius: 8px;
                display: grid;
                place-items: center;
        }

        /* The QR SVG arrives with explicit dimensions; scale it to the tile
           (v0.4.4 lesson: a viewBox-only svg collapses to 0x0). */
        .oqr :global(svg) {
                width: 100%;
                height: 100%;
                display: block;
        }

        .oqr-copy {
                min-width: 0;
        }

        .oqr-copy strong {
                display: block;
                font-size: 12px;
                color: var(--text);
        }

        .oqr-copy > p {
                margin: 5px 0 0;
                font-size: 11px;
                line-height: 1.5;
                color: var(--muted);
        }

        .omfa-help {
                /* Help block below the QR row (prototype MFA screen): the two
                   instructional paragraphs live under the QR, toggled from
                   the copy block's "How does this work?" button. */
                margin-top: 10px;
                padding: 9px 11px;
                background: var(--raised);
                border: 1px solid var(--line-soft);
                border-radius: 8px;
                font-size: 11px;
                color: var(--muted);
                text-align: left;
        }

        .omfa-help p {
                margin: 0;
                line-height: 1.5;
        }

        .omfa-help p + p {
                margin-top: 6px;
        }

        .omfa-help a {
                color: var(--accent);
                text-underline-offset: 2px;
        }

        .okey-field {
                display: flex;
                align-items: center;
                gap: 8px;
                min-height: 38px;
                padding: 0 6px 0 11px;
                background: var(--input);
                border: 1px solid var(--line-hi);
                border-radius: 8px;
                margin-top: 10px;
        }

        /* A real text field lives here: show the caret affordance and a
           focus ring on the container, like the other inputs. */
        .okey-field:focus-within {
                border-color: var(--accent);
        }

        /* The secret is a real (readonly) text input, visible only while the
           section is revealed. It always shows the grouped plaintext (32
           base32 chars + 7 spaces = 39 chars): at 12px that overflows the
           field by a few px and ellipsis-clips the last group, so 11px is
           the size that always shows the WHOLE block. The `input` part
           matters: `.ob input { font: inherit }` compiles to (0,2,1) and
           would beat a plain class rule (0,2,0), silently forcing 13px
           Inter over this mono/11px styling — with the element qualifier
           this rule is (0,3,1) and wins outright. */
        .okey-field input.okey-field__secret {
                flex: 1 1 auto;
                min-width: 0;
                font-family: var(--mono);
                font-size: 11px;
                color: var(--text);
                background: none;
                border: 0;
                padding: 0;
                outline: none;
                white-space: nowrap;
                overflow: hidden;
                text-overflow: ellipsis;
        }

        .okey-field__copy {
                flex: 0 0 auto;
                background: none;
                border: 0;
                padding: 6px 8px;
                color: var(--accent);
                font-size: 12px;
                font-weight: 650;
                border-radius: 6px;
        }

        .okey-field__copy:hover {
                text-decoration: underline;
        }

        .okey-field__copy.done {
                color: var(--accent);
        }

        /* ---------- six-digit OTP boxes (behaviour preserved) ---------- */
        .ootp {
                display: flex;
                gap: 6px;
                margin-top: 2px;
        }

        .ootp input {
                width: 100%;
                min-width: 0;
                height: 40px;
                text-align: center;
                font-family: var(--mono);
                font-size: 15px;
                font-weight: 600;
                color: var(--text);
                background: var(--input);
                border: 1px solid var(--line-hi);
                border-radius: 8px;
                outline: none;
                transition: border-color 0.15s, box-shadow 0.15s, background 0.15s;
        }

        .ootp input:focus {
                border-color: var(--accent);
                box-shadow: 0 0 0 3px var(--accent-glow);
        }

        .ootp.invalid input {
                border-color: #b96a5e;
                background: #2a1b18;
                color: #ffddd7;
        }

        .ootp.shake {
                animation: shake 0.4s ease-in-out;
        }

        @keyframes shake {
                0%, 100% {
                        transform: translateX(0);
                }
                20% {
                        transform: translateX(-5px);
                }
                40% {
                        transform: translateX(5px);
                }
                60% {
                        transform: translateX(-4px);
                }
                80% {
                        transform: translateX(3px);
                }
        }

        /* ---------- skip row + warning ---------- */
        .oskip-row {
                display: flex;
                align-items: center;
                justify-content: space-between;
                gap: 10px;
                margin-top: 10px;
        }

        .oskip-row__text {
                font-size: 11px;
                color: var(--muted);
        }

        .owarn {
                border: 1px solid #7b5549;
                background: #302522;
                padding: 14px;
                border-radius: 9px;
                margin-top: 12px;
                color: #e5c9bb;
        }

        .owarn__title {
                margin: 0;
                font-size: 13px;
                font-weight: 700;
                color: #ffe0ce;
        }

        .owarn__list {
                margin: 8px 0;
                padding-left: 16px;
                font-size: 13px;
                line-height: 1.55;
        }

        .owarn__list li {
                margin: 3px 0;
        }

        .owarn__note {
                margin: 0;
                font-size: 11px;
                line-height: 1.5;
                color: var(--muted);
        }

        .owarn__actions {
                display: grid;
                gap: 7px;
                margin-top: 13px;
        }

        .ocheck-row {
                display: flex;
                gap: 10px;
                align-items: flex-start;
                margin-top: 11px;
                font-size: 12px;
                line-height: 1.5;
                color: #d8e4df;
                cursor: pointer;
        }

        .ocheck-row input {
                accent-color: var(--accent);
                width: 16px;
                height: 16px;
                flex-shrink: 0;
                margin: 2px 0 0;
        }

        .ocheck-row--tight {
                margin-top: 8px;
        }

        /* ---------- recovery codes (v6 grid) ---------- */
        .ocodegrid {
                display: grid;
                grid-template-columns: 1fr 1fr;
                gap: 6px;
                margin: 10px 0;
        }

        .ocode {
                display: flex;
                gap: 7px;
                align-items: center;
                background: #0c191d;
                border-radius: 7px;
                padding: 7px;
                min-width: 0;
        }

        .ocode small {
                color: #779093;
                font-size: 10px;
                flex: 0 0 auto;
        }

        .ocode code {
                font-family: var(--mono);
                font-size: 10.5px;
                white-space: nowrap;
                overflow: hidden;
                text-overflow: ellipsis;
                color: #e0eee9;
        }

        /* ---------- address choices (v6) ---------- */
        .ochoice {
                display: grid;
                gap: 7px;
                margin: 13px 0;
        }

        .ochoice__opt {
                display: flex;
                align-items: center;
                gap: 11px;
                text-align: left;
                width: 100%;
                padding: 11px;
                background: #111e22;
                border: 1px solid #3b4f52;
                border-radius: 10px;
                color: var(--text);
                transition: border-color 0.15s, background 0.15s;
        }

        .ochoice__opt:hover {
                border-color: #4b6365;
        }

        .ochoice__opt.is-active {
                border-color: var(--accent);
                background: #1b3230;
        }

        .oradio {
                width: 17px;
                height: 17px;
                border-radius: 50%;
                border: 1.5px solid #839c9c;
                display: grid;
                place-items: center;
                flex-shrink: 0;
        }

        .ochoice__opt.is-active .oradio {
                border-color: var(--accent);
        }

        .ochoice__opt.is-active .oradio::after {
                content: '';
                width: 7px;
                height: 7px;
                border-radius: 50%;
                background: var(--accent);
        }

        .ochoice__text {
                min-width: 0;
        }

        .ochoice__title {
                display: block;
                font-size: 12px;
                font-weight: 700;
                color: var(--text);
        }

        .ochoice__sub {
                display: block;
                color: var(--muted);
                font-size: 11px;
                margin-top: 2px;
        }

        .osubtle-center {
                text-align: center;
                color: #789092;
                font-size: 11px;
                margin: 14px 0 0;
        }

        /* ---------- DNS verification (v6: feedback bar + panel, as prototype) ---------- */
        .odns-success {
                display: flex;
                gap: 9px;
                align-items: center;
                background: var(--raised);
                color: #bdebdc;
                border: 1px solid #315052;
                border-radius: 10px;
                padding: 11px;
                font-size: 12px;
                line-height: 1.5;
                margin-top: 10px;
        }

        .odns-success strong {
                color: var(--accent);
        }

        .odns-success__icon {
                flex: 0 0 21px;
                width: 21px;
                height: 21px;
                display: grid;
                place-items: center;
                border: 1.5px solid var(--accent);
                border-radius: 50%;
                color: var(--accent);
        }

        .odns-success + .obtn {
                margin-top: 13px;
        }

        .ofeedback {
                padding: 10px 12px;
                margin-top: 11px;
                background: #352c22;
                border-radius: 8px;
                color: #f1d6aa;
                font-size: 12px;
                line-height: 1.5;
        }

        .odns-instructions {
                margin-top: 10px;
        }

        .odns-instructions strong {
                display: block;
                font-size: 12px;
                color: #f0d7a7;
        }

        .odns-instructions__lead {
                margin: 7px 0 12px;
                font-size: 11px;
                line-height: 1.5;
                color: var(--muted);
        }

        .odns-row {
                display: flex;
                align-items: center;
                gap: 12px;
                padding: 8px 0;
                border-bottom: 1px solid var(--line);
                font-size: 12px;
        }

        .odns-row span {
                color: var(--muted);
                min-width: 88px;
        }

        .odns-row strong {
                flex: 1 1 auto;
                min-width: 0;
                font-weight: 650;
                overflow-wrap: anywhere;
        }

        /* Copy buttons inside the DNS instructions rows and the setup-key
           field: bare accent text, transient "Copied" feedback. */
        .orow-copy {
                flex: 0 0 auto;
                background: none;
                border: 0;
                padding: 2px 4px;
                color: var(--accent);
                font-size: 11px;
                font-weight: 650;
        }

        .orow-copy:hover {
                text-decoration: underline;
        }

        .mono {
                font-family: var(--mono);
        }

        .odns-instructions__note {
                margin: 11px 0 0;
                font-size: 11px;
                line-height: 1.5;
                color: var(--muted);
        }

        .oapplied {
                background: var(--raised);
                border: 1px solid #315052;
                border-radius: 10px;
                padding: 11px;
                margin-top: 10px;
                font-size: 12px;
                color: var(--muted);
        }

        .oapplied__title {
                display: flex;
                align-items: center;
                gap: 7px;
                margin: 0 0 7px;
                font-weight: 700;
                font-size: 12px;
                color: var(--accent);
        }

        .oapplied p {
                margin: 7px 0 11px;
                font-size: 11px;
                line-height: 1.5;
        }

        .oapplied strong {
                color: var(--text);
        }

        .odomain-exit {
                display: grid;
                gap: 7px;
                margin-top: 0;
                text-align: center;
        }

        .odomain-exit .olinkbtn {
                justify-self: center;
        }

        /* ---------- success / completion (v6) ---------- */
        .osuccess {
                text-align: center;
                margin-top: 6px;
        }

        .osuccess__badge {
                width: 46px;
                height: 46px;
                margin: 4px auto 12px;
                background: #1d3d35;
                border: 1px solid #4b927c;
                border-radius: 50%;
                display: grid;
                place-items: center;
                color: var(--accent);
        }

        .osuccess h2 {
                margin: 0 0 5px;
                font-size: 19px;
                font-weight: 700;
                letter-spacing: -0.45px;
                line-height: 1.25;
        }

        .osuccess p {
                margin: 0 auto 13px;
                max-width: 42ch;
                font-size: 11.5px;
                line-height: 1.55;
                color: var(--muted);
        }

        .odone {
                text-align: center;
                margin-top: 6px;
        }

        .odone h2 {
                margin: 0 0 5px;
                font-size: 19px;
                font-weight: 700;
                letter-spacing: -0.45px;
        }

        .odone > p {
                margin: 0 0 10px;
                font-size: 11.5px;
                color: var(--muted);
        }

        .odone__panel {
                margin-top: 10px;
                text-align: left;
        }

        /* Completion checklist: one row per completed (or skipped) piece
           of the setup, then live server stats and a proceed action. */
        .odone__row {
                display: flex;
                align-items: center;
                gap: 8px;
                padding: 7px 0;
                border-bottom: 1px solid var(--line-soft);
                font-size: 11.5px;
                flex-wrap: wrap;
        }

        .odone__row:last-of-type {
                border-bottom: 0;
        }

        .odone__mark {
                flex: 0 0 auto;
                width: 16px;
                height: 16px;
                border-radius: 50%;
                display: inline-flex;
                align-items: center;
                justify-content: center;
        }

        .odone__mark.is-done {
                background: var(--accent-deep);
                border: 1px solid var(--accent);
                color: var(--accent);
        }

        .odone__mark.is-skip {
                background: rgba(226, 187, 126, 0.12);
                border: 1px solid rgba(226, 187, 126, 0.55);
                color: var(--warn);
        }

        .odone__name {
                flex: 1 1 auto;
                min-width: 0;
                font-weight: 650;
        }

        /* Checklist action links ("Secure your account" / "Configure
           domain"): navigate back into the wizard from the done screen.
           With row wrapping on, a tight row drops the link to its own
           right-aligned line instead of breaking the value mid-string. */
        .odone__action {
                flex: 0 0 auto;
                margin-left: auto;
                font-size: 11px;
        }

        .odone__val {
                flex: 0 1 auto;
                min-width: 0;
                color: var(--muted);
                text-align: right;
                overflow-wrap: anywhere;
        }

        /* URLs and addresses never break mid-string: when the row is tight
           the action link wraps to the next line instead. */
        .odone__val.mono {
                white-space: nowrap;
        }

        .odone__stats {
                display: flex;
                flex-wrap: wrap;
                gap: 4px 14px;
                margin-top: 10px;
                font-size: 11px;
                color: var(--dim);
        }

        .odone__updated {
                margin: 4px 0 0;
                font-size: 10px;
                letter-spacing: 0.1px;
                color: var(--dim);
        }

        .odone__proceed {
                margin-top: 10px;
        }

        /* ---------- small print + disclosure ---------- */
        .odivider {
                height: 1px;
                background: var(--line);
                margin: 12px 0 10px;
        }

        .ometa {
                display: flex;
                align-items: center;
                justify-content: center;
                gap: 6px;
                font-size: 10.5px;
                color: #789092;
        }

        .ometa .dot {
                color: var(--dim);
        }

        .odisclosure {
                margin-top: 10px;
                font-size: 11px;
        }

        .odisclosure summary {
                display: flex;
                align-items: center;
                gap: 5px;
                color: #a6bcbb;
                font-weight: 500;
                cursor: pointer;
                list-style: none;
        }

        .odisclosure summary::-webkit-details-marker {
                display: none;
        }

        .odisclosure .chev {
                transition: transform 0.15s;
        }

        .odisclosure[open] .chev {
                transform: rotate(180deg);
        }

        .odisclosure p {
                margin: 7px 0 0;
                color: var(--muted);
                line-height: 1.55;
        }

        .odisclosure code {
                font-family: var(--mono);
                font-size: 10.5px;
                color: #cfe3dd;
                background: #0c191d;
                border-radius: 5px;
                padding: 1px 5px;
        }

        /* ---------- responsive (v4/v6 compact refinements) ---------- */
        @media (max-width: 490px) {
                .ob {
                        padding: 15px 12px 18px;
                }

                .otopbar {
                        margin-bottom: 11px;
                }

                .obcard {
                        padding: 18px;
                }

                .ointro h2 {
                        font-size: 18px;
                }

                .oqr {
                        width: 105px;
                        height: 105px;
                        flex-basis: 105px;
                }

                .ocode {
                        padding: 6px 5px;
                        gap: 5px;
                }

                .ocode code {
                        font-size: 9.5px;
                }
        }

        @media (max-height: 760px) and (min-width: 491px) {
                .ob {
                        padding-top: 12px;
                }

                .otopbar {
                        margin-bottom: 10px;
                }

                .obcard {
                        padding: 18px 22px;
                }

                .oform {
                        gap: 9px;
                }

                .ointro p {
                        margin-bottom: 10px;
                }
        }

        @media (prefers-reduced-motion: reduce) {
                .ob *,
                .ob *::before,
                .ob *::after {
                        transition-duration: 0.001ms !important;
                        animation-duration: 0.001ms !important;
                        animation-iteration-count: 1 !important;
                }
        }
</style>
