<script lang="ts">
	import {
		ApiError,
		applyDomain,
		checkDomain,
		fetchAccessStatus,
		removeDomain,
		type AccessStatus,
		type DomainCheckResult
	} from '$lib/api';
	import { domainHint } from '$lib/access.js';

	let access = $state<AccessStatus | null>(null);
	let loadError = $state('');

	// Custom-domain management (IP HTTPS is the default and is never
	// disturbed — the backend rolls the domain back on any failure).
	let domainInput = $state('');
	let domainTouched = $state(false);
	let domainBusy = $state(false);
	let domainStage = $state<'input' | 'checking' | 'applying'>('input');
	let domainMsg = $state('');
	let domainError = $state('');
	let removeBusy = $state(false);

	const domainErr = $derived(domainHint(domainInput));
	const currentDomain = $derived(access?.https?.domain?.host ?? '');

	// Certificate expiry visibility: surface the renewal window instead
	// of letting a silent failure surprise the operator.
	const certDaysLeft = $derived.by(() => {
		const na = access?.https?.not_after;
		if (!na) return null;
		const ms = new Date(na).getTime() - Date.now();
		if (Number.isNaN(ms)) return null;
		return Math.floor(ms / 86_400_000);
	});

	$effect(() => {
		let cancelled = false;
		fetchAccessStatus()
			.then((a) => {
				if (!cancelled) access = a;
			})
			.catch((err) => {
				if (!cancelled) loadError = err instanceof Error ? err.message : String(err);
			});
		return () => {
			cancelled = true;
		};
	});

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
			access = await fetchAccessStatus();
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
			access = await fetchAccessStatus();
		} catch (err) {
			domainError = err instanceof ApiError ? err.message : String(err);
		} finally {
			removeBusy = false;
		}
	}
</script>

<section class="card" aria-labelledby="access-title">
	<h2 id="access-title">Access address</h2>
	{#if loadError !== ''}
		<div class="error" role="alert">{loadError}</div>
	{:else if access === null}
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

		{#if certDaysLeft !== null && access?.https?.cert_valid && certDaysLeft <= 14}
			<p class="warn-line">
				<span class="pill pill-warn">certificate</span>
				expires in {certDaysLeft} day{certDaysLeft === 1 ? '' : 's'} — renewal is automatic; if it
				fails, see <code>sudo drevanta access status</code> on the server.
			</p>
		{/if}
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
			<div class="row">
				<button class="btn-ghost" type="button" onclick={doRemoveDomain} disabled={removeBusy}>
					{removeBusy ? 'Removing…' : 'Remove custom domain'}
				</button>
			</div>
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
					<p class="field-error" role="alert">{domainErr}</p>
				{/if}
			</div>
			<div class="row">
				<button class="btn-primary" type="button" disabled={domainBusy || domainErr !== ''} onclick={submitDomainApply}>
					{domainStage === 'applying' ? 'Verifying DNS, issuing certificate…' : 'Verify DNS and enable HTTPS for this domain'}
				</button>
				<button class="btn-ghost" type="button" disabled={domainBusy || domainErr !== ''} onclick={submitDomainCheck}>
					Check DNS only
				</button>
			</div>
			{#if domainMsg}<p class="hint">{domainMsg}</p>{/if}
			{#if domainError}<p class="field-error" role="alert">{domainError}</p>{/if}
		{/if}
	{/if}
</section>
