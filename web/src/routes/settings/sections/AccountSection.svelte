<script lang="ts">
	import {
		ApiError,
		changeAccountPassword,
		fetchAccountProfile,
		setAccountEmail,
		type AccountProfile
	} from '$lib/api';

	let profile = $state<AccountProfile | null>(null);
	let loadError = $state('');

	// Email binding: sign-in works with the username or the email.
	let emailInput = $state('');
	let emailBusy = $state(false);
	let emailMsg = $state('');
	let emailSaveError = $state('');

	// Password change ends every other session.
	let pwCurrent = $state('');
	let pwNew = $state('');
	let pwConfirm = $state('');
	let pwBusy = $state(false);
	let pwMsg = $state('');
	let pwError = $state('');

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
		fetchAccountProfile()
			.then((p) => {
				if (cancelled) return;
				profile = p;
				emailInput = p.email;
			})
			.catch((err) => {
				if (!cancelled) loadError = err instanceof Error ? err.message : String(err);
			});
		return () => {
			cancelled = true;
		};
	});

	async function submitEmail(e: SubmitEvent) {
		e.preventDefault();
		emailSaveError = '';
		emailMsg = '';
		if (emailErr !== '') return;
		emailBusy = true;
		try {
			const res = await setAccountEmail(emailInput.trim());
			emailMsg = res.email === '' ? 'Email removed — sign in with the username.' : `Email saved: ${res.email}`;
			profile = profile ? { ...profile, email: res.email } : profile;
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
</script>

<section class="card" aria-labelledby="account-title">
	<h2 id="account-title">Account</h2>
	{#if loadError !== ''}
		<div class="error" role="alert">{loadError}</div>
	{:else if profile === null}
		<p class="muted">loading…</p>
	{:else if profile}
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
				{#if emailErr !== ''}<p class="field-error" role="alert">{emailErr}</p>{/if}
			</div>
			{#if emailMsg}<p class="ok-line">{emailMsg}</p>{/if}
			{#if emailSaveError}<p class="field-error" role="alert">{emailSaveError}</p>{/if}
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
				{#if pwErr !== ''}<p class="field-error" role="alert">{pwErr}</p>{/if}
			</div>
			<p class="hint">Changing the password signs out every other device.</p>
			{#if pwMsg}<p class="ok-line">{pwMsg}</p>{/if}
			{#if pwError}<p class="field-error" role="alert">{pwError}</p>{/if}
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
