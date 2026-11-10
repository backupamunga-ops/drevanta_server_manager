<script lang="ts">
	import { goto } from '$app/navigation';
	import QRCode from 'qrcode';
	import {
		ApiError,
		fetchOnboardingStatus,
		postJSON,
		type MfaSetup,
		type MfaVerified
	} from '$lib/api';

	type Step = 'loading' | 'claim' | 'mfa-choice' | 'mfa-setup' | 'recovery' | 'done';

	let step = $state<Step>('loading');
	let busy = $state(false);
	let error = $state('');
	let statusChecked = $state(false);

	// Claim form
	let token = $state('');
	let username = $state('');
	let password = $state('');
	let password2 = $state('');

	// MFA enrolment
	let totpSecret = $state('');
	let otpauthUri = $state('');
	let qrSvg = $state('');
	let totpCode = $state('');

	// Recovery codes
	let recoveryCodes = $state<string[]>([]);
	let codesSaved = $state(false);

	// Skip path
	let riskAcknowledged = $state(false);

	$effect(() => {
		if (statusChecked) return;
		statusChecked = true;
		route();
	});

	async function route() {
		error = '';
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
			error = err instanceof Error ? err.message : String(err);
			step = 'claim';
		}
	}

	async function submitClaim(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		if (password !== password2) {
			error = 'Passwords do not match.';
			return;
		}
		busy = true;
		try {
			await postJSON('/api/v1/onboarding/claim', { token: token.trim(), username, password });
			await beginMfaSetup();
		} catch (err) {
			if (err instanceof ApiError) {
				if (err.code === 'token_expired') {
					error = 'The claim token has expired. Ask the installer to issue a new one (drevantad claim init).';
				} else if (err.code === 'token_used' || err.code === 'invalid_token') {
					error = 'The claim token is not valid. Copy it exactly from the installer output.';
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

	async function beginMfaSetup() {
		error = '';
		busy = true;
		try {
			const res = await postJSON<MfaSetup>('/api/v1/onboarding/mfa/setup', {});
			totpSecret = res.secret;
			otpauthUri = res.otpauth_uri;
			qrSvg = await QRCode.toString(res.otpauth_uri, {
				type: 'svg',
				margin: 1,
				color: { dark: '#0b1220', light: '#ffffff' }
			});
			totpCode = '';
			step = 'mfa-setup';
		} catch (err) {
			error = err instanceof ApiError ? err.message : String(err);
			step = 'mfa-choice';
		} finally {
			busy = false;
		}
	}

	async function submitTotp(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		busy = true;
		try {
			const res = await postJSON<MfaVerified>('/api/v1/onboarding/mfa/verify', { code: totpCode.trim() });
			recoveryCodes = res.recovery_codes;
			step = 'recovery';
		} catch (err) {
			if (err instanceof ApiError && err.code === 'totp_invalid') {
				error = 'That code did not match. Check the clock on this device and try the current code.';
			} else {
				error = err instanceof ApiError ? err.message : String(err);
			}
		} finally {
			busy = false;
		}
	}

	async function submitSkip() {
		error = '';
		if (!riskAcknowledged) return;
		busy = true;
		try {
			await postJSON('/api/v1/onboarding/mfa/skip', { acknowledge_risk: true });
			step = 'done';
			setTimeout(() => goto('/', { replaceState: true }), 1200);
		} catch (err) {
			error = err instanceof ApiError ? err.message : String(err);
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
</script>

<svelte:head>
	<title>Onboarding — Drevanta</title>
</svelte:head>

<div class="auth-wrap wide">
	<div class="steps" aria-hidden="true">
		<span class="step-chip" class:current={step === 'claim'} class:done={['mfa-choice', 'mfa-setup', 'recovery', 'done'].includes(step)}>
			1 · claim
		</span>
		<span class="step-chip" class:current={['mfa-choice', 'mfa-setup'].includes(step)} class:done={['recovery', 'done'].includes(step)}>
			2 · mfa
		</span>
		<span class="step-chip" class:current={step === 'recovery'} class:done={step === 'done'}>
			3 · recovery
		</span>
	</div>

	<div class="auth-card">
		{#if error}
			<div class="error" role="alert">{error}</div>
		{/if}

		{#if step === 'loading'}
			<div class="loading">loading onboarding state…</div>
		{:else if step === 'claim'}
			<h1 class="auth-title">Claim this instance</h1>
			<p class="auth-sub">
				Paste the single-use claim token printed by the installer, then create the
				administrator account. The token expires and works exactly once.
			</p>
			<form onsubmit={submitClaim}>
				<div class="field">
					<label for="token">Claim token</label>
					<input id="token" name="token" placeholder="drv1_…" autocomplete="off" spellcheck="false" required bind:value={token} />
					{#if !token.trim()}
						<span class="hint">Shown once by <code>install.sh</code> after a fresh install.</span>
					{/if}
				</div>
				<div class="field">
					<label for="username">Administrator username</label>
					<input id="username" name="username" autocomplete="username" minlength={3} maxlength={32}
						pattern="[a-zA-Z0-9][a-zA-Z0-9._-]*" required bind:value={username} />
				</div>
				<div class="field">
					<label for="password">Password</label>
					<input id="password" name="new-password" type="password" autocomplete="new-password"
						minlength={12} required bind:value={password} />
					<span class="hint">At least 12 characters. Stored only as an argon2id hash.</span>
				</div>
				<div class="field">
					<label for="password2">Repeat password</label>
					<input id="password2" name="new-password-confirm" type="password" autocomplete="new-password"
						minlength={12} required bind:value={password2} />
				</div>
				<button class="btn-primary" type="submit" disabled={busy}>
					{busy ? 'Claiming…' : 'Create administrator account'}
				</button>
			</form>
		{:else if step === 'mfa-choice'}
			<h1 class="auth-title">Protect the account</h1>
			<p class="auth-sub">
				Add a second factor (recommended) or skip it for now. You can decide only
				during this wizard.
			</p>

			<button class="btn-primary" type="button" onclick={beginMfaSetup} disabled={busy}>
				Set up authenticator app
			</button>

			<div class="divider"><span>or</span></div>

			<div class="warn-box">
				<strong>Skipping MFA lowers security.</strong> Without a second factor, knowing the
				password alone is enough to sign in. You can only reach the console through an SSH
				tunnel, which limits exposure, but the account will not be 2FA-protected.
			</div>
			<label class="check-row">
				<input type="checkbox" bind:checked={riskAcknowledged} />
				<span>I understand the risk of running without multi-factor authentication.</span>
			</label>
			<button class="btn-danger" type="button" onclick={submitSkip} disabled={!riskAcknowledged || busy}>
				Skip MFA and finish
			</button>
		{:else if step === 'mfa-setup'}
			<h1 class="auth-title">Scan and confirm</h1>
			<p class="auth-sub">
				Add the entry to your authenticator app by scanning the QR code, or enter the
				secret manually. Then type the current 6-digit code.
			</p>
			<div class="qr-box">{@html qrSvg}</div>
			<div class="field">
				<label for="secret">Secret (manual entry)</label>
				<input id="secret" readonly value={totpSecret} spellcheck="false" onclick={(e) => (e.currentTarget as HTMLInputElement).select()} />
			</div>
			<form onsubmit={submitTotp}>
				<div class="field">
					<label for="totp-code">6-digit code</label>
					<input id="totp-code" inputmode="numeric" pattern="[0-9]*" maxlength={6}
						placeholder="123456" autocomplete="one-time-code" required bind:value={totpCode} />
				</div>
				<button class="btn-primary" type="submit" disabled={busy || totpCode.trim().length !== 6}>
					{busy ? 'Verifying…' : 'Verify and enable MFA'}
				</button>
			</form>
		{:else if step === 'recovery'}
			<h1 class="auth-title">Save your recovery codes</h1>
			<p class="auth-sub">
				Each code works exactly once at sign-in if your authenticator is unavailable.
				They are shown <strong>only this once</strong>; the server stores just their hashes.
			</p>
			<div class="codes-grid">
				{#each recoveryCodes as c (c)}
					<span class="code-chip">{c}</span>
				{/each}
			</div>
			<button class="btn-ghost" type="button" onclick={copyCodes}>Copy all codes</button>
			<label class="check-row">
				<input type="checkbox" bind:checked={codesSaved} />
				<span>I have stored these codes somewhere safe (password manager or printed copy).</span>
			</label>
			<button class="btn-primary" type="button" onclick={finishRecovery} disabled={!codesSaved}>
				Finish onboarding
			</button>
		{:else if step === 'done'}
			<h1 class="auth-title">You're all set 🎉</h1>
			<p class="auth-sub">The dashboard will open in a moment…</p>
		{/if}
	</div>
</div>

<style>
	.wide {
		max-width: 560px;
	}

	form {
		display: flex;
		flex-direction: column;
		gap: 14px;
	}

	.auth-card {
		gap: 16px;
	}

	.divider {
		display: flex;
		align-items: center;
		gap: 10px;
		color: var(--text-dim);
		font-size: 12px;
		text-transform: uppercase;
		letter-spacing: 0.08em;
	}

	.divider::before,
	.divider::after {
		content: '';
		flex: 1;
		height: 1px;
		background: var(--border);
	}

	code {
		background: var(--bg-inset);
		border-radius: 4px;
		padding: 1px 5px;
		font-size: 12px;
	}
</style>
