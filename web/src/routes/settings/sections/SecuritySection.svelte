<script lang="ts">
	import { goto } from '$app/navigation';
	import {
		ApiError,
		accountMfaSetup,
		accountMfaVerify,
		fetchAccountMfa,
		signOutEverywhere,
		type AccountMfaStatus,
		type MfaSetup
	} from '$lib/api';
	import { qrSvg as renderQrCode } from '$lib/qr.js';

	let mfa = $state<AccountMfaStatus | null>(null);
	let loadError = $state('');

	// Enable-MFA flow (same shape as onboarding).
	let mfaStage = $state<'idle' | 'setup' | 'verify' | 'codes'>('idle');
	let mfaSecret = $state('');
	let qrSvg = $state('');
	let mfaCode = $state('');
	let mfaBusy = $state(false);
	let mfaError = $state('');
	let recoveryCodes = $state<string[]>([]);
	let codesSaved = $state(false);

	// Sign out everywhere ends every session, including this one.
	let soBusy = $state(false);

	$effect(() => {
		let cancelled = false;
		fetchAccountMfa()
			.then((m) => {
				if (!cancelled) mfa = m;
			})
			.catch((err) => {
				if (!cancelled) loadError = err instanceof Error ? err.message : String(err);
			});
		return () => {
			cancelled = true;
		};
	});

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
		mfa = await fetchAccountMfa();
	}

	function copyCodes() {
		void navigator.clipboard.writeText(recoveryCodes.join('\n')).catch(() => undefined);
	}

	async function doSignOutEverywhere() {
		soBusy = true;
		try {
			await signOutEverywhere();
			await goto('/login', { replaceState: true });
		} finally {
			soBusy = false;
		}
	}
</script>

<section class="card" aria-labelledby="mfa-title">
	<h2 id="mfa-title">Multi-factor authentication</h2>
	{#if loadError !== ''}
		<div class="error" role="alert">{loadError}</div>
	{:else if mfa === null}
		<p class="muted">loading…</p>
	{:else if mfa}
		{#if mfa.mfa_enabled || mfaStage === 'codes'}
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
				<div class="row">
					<button class="btn-ghost" type="button" onclick={copyCodes}>Copy all codes</button>
				</div>
				<label class="check-row">
					<input type="checkbox" bind:checked={codesSaved} />
					<span>I have stored these codes somewhere safe.</span>
				</label>
				<div class="row">
					<button class="btn-primary" type="button" onclick={finishMfaEnable} disabled={!codesSaved}>Done</button>
				</div>
			{:else}
				<p class="ok-line">
					<span class="pill pill-ok">MFA enabled</span>
					two-factor sign-in is active · {mfa.recovery_codes_unused} unused recovery code{mfa.recovery_codes_unused === 1
						? ''
						: 's'}
				</p>
			{/if}
		{:else if mfaStage === 'setup'}
			<p class="muted">
				Scan the code with your authenticator app, or enter the secret manually, then confirm
				with the current 6-digit code.
			</p>
			<div class="qr-box">{@html qrSvg}</div>
			<div class="field">
				<label for="acc-secret">Secret (manual entry)</label>
				<input id="acc-secret" class="mono" readonly value={mfaSecret} onclick={(e) => (e.currentTarget as HTMLInputElement).select()} />
			</div>
			<form class="stack" onsubmit={submitMfaVerify}>
				<div class="field">
					<label for="acc-code">6-digit code</label>
					<input id="acc-code" class="mono" inputmode="numeric" maxlength={6} placeholder="123456" autocomplete="one-time-code" bind:value={mfaCode} />
				</div>
				{#if mfaError}<p class="field-error" role="alert">{mfaError}</p>{/if}
				<div class="row">
					<button class="btn-primary" type="submit" disabled={mfaBusy || mfaCode.trim().length !== 6}>
						{mfaBusy ? 'Verifying…' : 'Verify and enable MFA'}
					</button>
					<button class="btn-ghost" type="button" onclick={() => (mfaStage = 'idle')} disabled={mfaBusy}>Cancel</button>
				</div>
			</form>
		{:else}
			<p class="warn-line">
				<span class="pill pill-warn">MFA off</span>
				your account is protected by password only. Recovery codes are issued when you enable it.
			</p>
			{#if mfaError}<p class="field-error" role="alert">{mfaError}</p>{/if}
			<div class="row">
				<button class="btn-primary" type="button" onclick={beginMfaSetup} disabled={mfaBusy}>
					{mfaBusy ? 'Preparing…' : 'Enable MFA'}
				</button>
			</div>
		{/if}
	{/if}
</section>

<section class="card" aria-labelledby="sessions-title">
	<h2 id="sessions-title">Sessions</h2>
	<p class="muted">
		Signing out everywhere ends every signed-in session on every device, including this one.
		Use it if you suspect a device was lost or a session was left behind.
	</p>
	<div class="row">
		<button class="btn-ghost" type="button" onclick={doSignOutEverywhere} disabled={soBusy}>
			{soBusy ? 'Signing out…' : 'Sign out everywhere'}
		</button>
	</div>
</section>
