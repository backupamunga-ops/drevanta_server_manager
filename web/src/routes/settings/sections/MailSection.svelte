<script lang="ts">
	import { ApiError, fetchSmtp, saveSmtp, testSmtp, type SmtpStatus } from '$lib/api';

	let smtp = $state<SmtpStatus | null>(null);
	let loadError = $state('');

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

	$effect(() => {
		let cancelled = false;
		fetchSmtp()
			.then((mail) => {
				if (cancelled) return;
				smtp = mail;
				smtpEnabled = mail.enabled;
				smtpHost = mail.host;
				smtpPort = mail.port || 587;
				smtpUser = mail.username;
				smtpFrom = mail.from_addr;
				smtpPass = '';
			})
			.catch((err) => {
				if (!cancelled) loadError = err instanceof Error ? err.message : String(err);
			});
		return () => {
			cancelled = true;
		};
	});

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
			smtp = await fetchSmtp();
			smtpPass = '';
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
				? `Test message sent to ${smtpFrom.trim() || 'the From address'}. Check the inbox.`
				: `Send failed: ${res.error ?? 'unknown error'}`;
		} catch (err) {
			smtpTestMsg = `Send failed: ${err instanceof ApiError ? err.message : String(err)}`;
		} finally {
			smtpTestBusy = false;
		}
	}
</script>

<section class="card" aria-labelledby="smtp-title">
	<h2 id="smtp-title">Email delivery (SMTP)</h2>
	{#if loadError !== ''}
		<div class="error" role="alert">{loadError}</div>
	{:else if smtp === null}
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
				<label for="smtp-user">SMTP username {#if smtp.password_set}<span class="muted-inline"> · password saved</span>{/if}</label>
				<input id="smtp-user" class="mono" type="text" autocomplete="off" bind:value={smtpUser} />
			</div>
			<div class="field">
				<label for="smtp-pass">SMTP password {#if smtp.password_set}<span class="muted-inline"> · leave blank to keep</span>{/if}</label>
				<input id="smtp-pass" type="password" autocomplete="new-password" placeholder="{smtp.password_set ? '••••••••' : ''}" bind:value={smtpPass} />
			</div>
			<div class="field">
				<label for="smtp-from">From address</label>
				<input id="smtp-from" type="email" placeholder="drevanta@example.com" bind:value={smtpFrom} />
			</div>
			{#if smtpMsg}<p class="ok-line">{smtpMsg}</p>{/if}
			{#if smtpTestMsg}<p class="hint">{smtpTestMsg}</p>{/if}
			{#if smtpError}<p class="field-error" role="alert">{smtpError}</p>{/if}
			<div class="row">
				<button class="btn-primary" type="submit" disabled={smtpBusy}>
					{smtpBusy ? 'Saving…' : 'Save mail settings'}
				</button>
				<button class="btn-ghost" type="button" onclick={sendSmtpTest} disabled={smtpTestBusy}>
					{smtpTestBusy ? 'Sending…' : 'Send test message'}
				</button>
			</div>
		</form>
	{/if}
</section>

<style>
	.muted-inline {
		font-weight: 400;
		text-transform: none;
		letter-spacing: 0;
	}
</style>
