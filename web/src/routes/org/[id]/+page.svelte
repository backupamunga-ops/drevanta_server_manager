<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import {
		fetchSession,
		fetchWorkspaceDetail,
		fetchWorkspaces,
		inviteMember,
		revokeInvitation,
		removeMember,
		enterWorkspace,
		personaLabel,
		ROLES_BY_TYPE,
		ApiError,
		type WorkspaceDetail
	} from '$lib/api';

	// The workspace context view for hosting organizations, websites and
	// developer projects. In 0.5.0 this page is REAL about what works:
	// memberships and invitations are fully functional; the persona's
	// management surfaces arrive with their Phase 2/3 milestones — the
	// page says exactly that instead of showing dead buttons.
	let detail = $state<WorkspaceDetail | null>(null);
	let loading = $state(true);
	let notFound = $state(false);
	let username = $state('');

	// Invitation form state.
	let inviteEmail = $state('');
	let inviteRole = $state('');
	let inviteBusy = $state(false);
	let inviteError = $state('');
	let createdInviteUrl = $state('');

	// Clipboard-friendly display of the one-time invite link.
	let copied = $state(false);

	const workspaceId = $derived(Number(page.params.id));

	const canManage = $derived(
		(detail?.permissions ?? []).includes('workspace.members.manage')
	);
	const canInvite = $derived(
		(detail?.permissions ?? []).includes('workspace.members.invite')
	);
	const roleOptions = $derived(
		detail ? ROLES_BY_TYPE[detail.workspace.type].filter((r) => r !== 'owner' || detail?.my_role === 'owner') : []
	);

	$effect(() => {
		let cancelled = false;
		fetchSession()
			.then((s) => {
				if (cancelled) return;
				username = s.username;
				if (!s.authenticated) {
					goto('/login', { replaceState: true });
					return;
				}
				if (s.scope !== 'full') {
					goto('/onboarding', { replaceState: true });
					return;
				}
				load(cancelled);
			})
			.catch(() => {
				if (!cancelled) goto('/login', { replaceState: true });
			});
		return () => {
			cancelled = true;
		};
	});

	async function load(cancelled: boolean) {
		loading = true;
		notFound = false;
		try {
			const d = await fetchWorkspaceDetail(workspaceId);
			if (!cancelled) {
				detail = d;
				inviteRole = roleOptions[0] ?? '';
			}
		} catch (e) {
			if (e instanceof ApiError && e.status === 404) {
				notFound = true;
			} else if (!cancelled) {
				// A vanished membership (revoked elsewhere) sends the user
				// back to a context they still hold.
				await goto('/', { replaceState: true });
				return;
			}
		} finally {
			if (!cancelled) loading = false;
		}
	}

	async function submitInvite(e: SubmitEvent) {
		e.preventDefault();
		if (inviteBusy || !detail) return;
		inviteBusy = true;
		inviteError = '';
		createdInviteUrl = '';
		try {
			const res = await inviteMember(detail.workspace.id, inviteEmail.trim(), inviteRole);
			createdInviteUrl = res.invitation.url;
			inviteEmail = '';
			detail = await fetchWorkspaceDetail(detail.workspace.id);
		} catch (err) {
			inviteError = err instanceof Error ? err.message : String(err);
		} finally {
			inviteBusy = false;
		}
	}

	async function copyInvite() {
		if (!createdInviteUrl) return;
		try {
			await navigator.clipboard.writeText(new URL(createdInviteUrl, location.origin).href);
			copied = true;
			setTimeout(() => (copied = false), 2000);
		} catch {
			// Clipboard unavailable (insecure context): the link stays visible.
		}
	}

	async function revoke(invId: number) {
		if (!detail) return;
		await revokeInvitation(detail.workspace.id, invId).catch(() => undefined);
		detail = await fetchWorkspaceDetail(detail.workspace.id);
	}

	async function eject(userId: number) {
		if (!detail) return;
		await removeMember(detail.workspace.id, userId).catch(() => undefined);
		detail = await fetchWorkspaceDetail(detail.workspace.id);
	}

	async function backToServer() {
		const list = await fetchWorkspaces();
		const server = list?.workspaces.find((w) => w.type === 'server');
		if (server) {
			await enterWorkspace(server.id);
			await goto('/', { replaceState: true });
		}
	}

	const businessLabels: Record<string, string> = {
		reseller: 'Reseller',
		hosting_provider: 'Hosting provider',
		agency: 'Agency'
	};

	const foundationNotes: Record<string, string> = {
		hosting_org:
			'Customer directory and delegated site management arrive with the next Phase 2 milestones. Today this workspace is real where it counts: your team, roles and invitations.',
		website:
			'Website controls (domains, SSL, files, backups) ship in Phase 3. This workspace already carries your role and membership — more is coming, nothing here is pretending.',
		developer_project:
			'Application controls (deployments, environments) ship in Phase 4. This workspace already carries your role and membership.'
	};
</script>

<svelte:head>
	<title>{detail?.workspace.name ?? 'Workspace'} — Drevanta</title>
</svelte:head>

{#if loading}
	<div class="loading">loading workspace…</div>
{:else if notFound}
	<div class="card">
		<div class="card-title">Workspace not available</div>
		<p class="ws-note">
			This workspace does not exist or your account has no membership in it. Switch contexts from
			the workspace menu in the header.
		</p>
		<button class="btn" type="button" onclick={backToServer}>Back to the server console</button>
	</div>
{:else if detail}
	<div class="org">
		<div class="page-head">
			<div>
				<h1>{detail.workspace.name}</h1>
				<div class="org-sub">
					{personaLabel(detail.workspace.type)}
					{#if detail.workspace.business_type}
						· {businessLabels[detail.workspace.business_type] ?? detail.workspace.business_type}
					{/if}
					· you are {detail.my_role}
				</div>
			</div>
			<button class="btn" type="button" onclick={backToServer}>Server console</button>
		</div>

		<div class="card ws-foundation">
			<p class="ws-note">{foundationNotes[detail.workspace.type] ?? foundationNotes.hosting_org}</p>
		</div>

		<div class="org-body">
			<div class="card">
				<div class="card-title">Members</div>
				<table class="ws-table">
					<thead>
						<tr><th>User</th><th>Role</th><th>Joined</th><th></th></tr>
					</thead>
					<tbody>
						{#each detail.members as m (m.user_id)}
							<tr>
								<td>{m.username}</td>
								<td>{m.role}</td>
								<td>{new Date(m.joined_at).toLocaleDateString()}</td>
								<td class="ws-actions">
									{#if canManage && !(m.role === 'owner' && detail.members.filter((x) => x.role === 'owner').length === 1)}
										<button class="btn btn-ghost btn-danger-text" type="button" onclick={() => eject(m.user_id)}>
											Remove
										</button>
									{/if}
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>

			<div class="org-side">
				{#if canInvite}
					<div class="card">
						<div class="card-title">Invite a member</div>
						<form class="ws-invite" onsubmit={submitInvite}>
							<div class="field">
								<label for="invite-email">Email (shown on the invitation)</label>
								<input id="invite-email" type="email" bind:value={inviteEmail} placeholder="person@example.com" />
							</div>
							<div class="field">
								<label for="invite-role">Role</label>
								<select id="invite-role" bind:value={inviteRole}>
									{#each roleOptions as r (r)}
										<option value={r}>{r}</option>
									{/each}
								</select>
							</div>
							<button class="btn btn-primary" type="submit" disabled={inviteBusy || inviteEmail.trim().length === 0}>
								{inviteBusy ? 'Inviting…' : 'Create invitation'}
							</button>
							{#if inviteError}
								<div class="field-error">{inviteError}</div>
							{/if}
						</form>
						{#if createdInviteUrl}
							<div class="ok-line ws-invite-ok">
								<span>One-time link (shown once):</span>
								<code class="ws-invite-url">{createdInviteUrl}</code>
								<button class="btn btn-ghost" type="button" onclick={copyInvite}>
									{copied ? 'Copied' : 'Copy link'}
								</button>
							</div>
						{/if}
					</div>
				{/if}

				{#if canManage && detail.invitations && detail.invitations.length > 0}
					<div class="card">
						<div class="card-title">Pending invitations</div>
						<ul class="ws-invitations">
							{#each detail.invitations as inv (inv.id)}
								<li>
									<span class="ws-invitation-line">
										{inv.email || '(no email)'} · {inv.role} ·
										expires {new Date(inv.expires_at).toLocaleDateString()}
									</span>
									<button class="btn btn-ghost btn-danger-text" type="button" onclick={() => revoke(inv.id)}>
										Revoke
									</button>
								</li>
							{/each}
						</ul>
					</div>
				{/if}
			</div>
		</div>
	</div>
{/if}

<style>
	.org {
		display: flex;
		flex-direction: column;
		gap: 16px;
	}

	.page-head {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 12px;
	}

	.page-head h1 {
		margin: 0;
		font-size: 22px;
	}

	.org-sub {
		color: var(--text-dim);
		font-size: 13px;
		margin-top: 4px;
	}

	.org-body {
		display: grid;
		grid-template-columns: minmax(0, 1.4fr) minmax(0, 1fr);
		gap: 16px;
		align-items: start;
	}

	.org-side {
		display: flex;
		flex-direction: column;
		gap: 16px;
		min-width: 0;
	}

	.ws-note {
		margin: 0;
		font-size: 13.5px;
		line-height: 1.55;
		color: var(--text-dim);
	}

	.ws-foundation {
		border-left: 3px solid var(--accent);
	}

	.ws-table {
		width: 100%;
		border-collapse: collapse;
		font-size: 13.5px;
	}

	.ws-table th {
		text-align: left;
		font-size: 11px;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		color: var(--text-dim);
		padding: 6px 8px;
		border-bottom: 1px solid var(--border);
	}

	.ws-table td {
		padding: 8px;
		border-bottom: 1px solid var(--border);
	}

	.ws-actions {
		text-align: right;
	}

	.btn-danger-text {
		color: var(--accent-warn);
	}

	.ws-invite {
		display: flex;
		flex-direction: column;
		gap: 10px;
	}

	.ws-invite-ok {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 8px;
		font-size: 12.5px;
	}

	.ws-invite-url {
		word-break: break-all;
		font-size: 12px;
	}

	.ws-invitations {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 8px;
		font-size: 13px;
	}

	.ws-invitations li {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 8px;
	}

	.ws-invitation-line {
		color: var(--text-dim);
	}

	@media (max-width: 719px) {
		.org-body {
			grid-template-columns: minmax(0, 1fr);
		}

		.page-head {
			flex-direction: column;
		}
	}
</style>
