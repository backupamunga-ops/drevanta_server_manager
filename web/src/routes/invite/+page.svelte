<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import {
		fetchSession,
		fetchInvitationPreview,
		acceptInvitation,
		personaLabel,
		ApiError,
		type InvitationPreview
	} from '$lib/api';

	// Invitation acceptance (PRD §5.4). The 256-bit token IS the
	// credential: it previews the workspace without a session, and
	// acceptance either attaches the membership to the current session
	// (one login, many memberships) or creates the invitee's account.
	const token = $derived(page.url.searchParams.get('token') ?? '');

	let preview = $state<InvitationPreview | null>(null);
	let loading = $state(true);
	let invalid = $state(false);

	let sessionUsername = $state('');
	let authenticated = $state(false);
	let checkedSession = $state(false);

	let newUsername = $state('');
	let newPassword = $state('');
	let busy = $state(false);
	let error = $state('');
	let created = $state(false);

	$effect(() => {
		let cancelled = false;
		const t = token;
		if (!t) {
			invalid = true;
			loading = false;
			return;
		}
		fetchInvitationPreview(t)
			.then((p) => {
				if (!cancelled) preview = p;
			})
			.catch(() => {
				if (!cancelled) invalid = true;
			})
			.finally(() => {
				if (!cancelled) loading = false;
			});
		fetchSession()
			.then((s) => {
				if (!cancelled) {
					authenticated = s.authenticated;
					sessionUsername = s.username;
					checkedSession = true;
				}
			})
			.catch(() => {
				if (!cancelled) checkedSession = true;
			});
		return () => {
			cancelled = true;
		};
	});

	async function accept(asNewAccount: boolean) {
		if (busy) return;
		busy = true;
		error = '';
		try {
			const res = asNewAccount
				? await acceptInvitation(token, newUsername.trim(), newPassword)
				: await acceptInvitation(token);
			if (res.account_created) {
				created = true;
				await goto('/login', { replaceState: true });
			} else {
				await goto(res.next, { replaceState: true });
			}
		} catch (e) {
			if (e instanceof ApiError && e.status === 404) {
				invalid = true;
			} else {
				error = e instanceof Error ? e.message : String(e);
			}
		} finally {
			busy = false;
		}
	}
</script>

<svelte:head>
	<title>Workspace invitation — Drevanta</title>
</svelte:head>

{#if loading}
	<div class="loading">checking invitation…</div>
{:else if invalid}
	<div class="invite-card card">
		<div class="card-title">Invitation not valid</div>
		<p class="invite-note">
			This invitation link is unknown, expired, already used or revoked. Ask the workspace owner
			for a fresh link.
		</p>
	</div>
{:else if preview}
	<div class="invite-card card">
		<div class="card-title">You are invited</div>
		<p class="invite-line">
			<strong>{preview.workspace_name}</strong>
			<span class="invite-meta">
				{personaLabel(preview.workspace_type)}{preview.business_type ? ` · ${preview.business_type.replace('_', ' ')}` : ''}
				· role <strong>{preview.role}</strong>
			</span>
			{#if preview.email}
				<span class="invite-meta">invited for {preview.email}</span>
			{/if}
		</p>

		{#if !checkedSession}
			<div class="loading">checking session…</div>
		{:else if authenticated}
			<p class="invite-note">
				Signed in as <strong>{sessionUsername}</strong>. Accepting joins this workspace with your
				existing account — one login, many memberships.
			</p>
			<button class="btn btn-primary" type="button" onclick={() => accept(false)} disabled={busy}>
				{busy ? 'Accepting…' : `Accept as ${sessionUsername}`}
			</button>
		{:else}
			<p class="invite-note">
				You do not have an account yet. Choose a username and password — accepting creates your
				account and joins this workspace in one step.
			</p>
			<form class="invite-form" onsubmit={(e) => { e.preventDefault(); void accept(true); }}>
				<div class="field">
					<label for="inv-username">Username</label>
					<input id="inv-username" type="text" bind:value={newUsername} minlength={3} maxlength={32} required autocomplete="username" />
					<span class="hint">3–32 characters; letters, digits, dots, dashes</span>
				</div>
				<div class="field">
					<label for="inv-password">Password</label>
					<input id="inv-password" type="password" bind:value={newPassword} minlength={12} maxlength={128} required autocomplete="new-password" />
					<span class="hint">at least 12 characters</span>
				</div>
				<button class="btn btn-primary" type="submit" disabled={busy || newUsername.length < 3 || newPassword.length < 12}>
					{busy ? 'Creating account…' : 'Create account and join'}
				</button>
			</form>
		{/if}
		{#if error && !invalid}
			<div class="field-error">{error}</div>
		{/if}
	</div>
{/if}

<style>
	.invite-card {
		max-width: 460px;
		margin: 8vh auto 0;
		display: flex;
		flex-direction: column;
		gap: 14px;
	}

	.invite-line {
		margin: 0;
		display: flex;
		flex-direction: column;
		gap: 4px;
		font-size: 15px;
	}

	.invite-meta {
		color: var(--text-dim);
		font-size: 13px;
	}

	.invite-note {
		margin: 0;
		font-size: 13.5px;
		line-height: 1.55;
		color: var(--text-dim);
	}

	.invite-form {
		display: flex;
		flex-direction: column;
		gap: 12px;
	}
</style>
