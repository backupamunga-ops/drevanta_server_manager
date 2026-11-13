<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import {
		fetchWorkspaces,
		enterWorkspace,
		createHostingOrg,
		personaLabel,
		type WorkspacesList,
		type WorkspaceView
	} from '$lib/api';

	// The workspace context switcher (PRD §5.3) plus the unmistakable
	// context banner (§7.1). One shared fetch drives both. The selector
	// lists ONLY workspaces the caller has a live membership in; the
	// daemon re-authorizes every switch, so the browser can never switch
	// itself into more access than it has.
	let list = $state<WorkspacesList | null>(null);
	let open = $state(false);
	let switching = $state(false);
	let creating = $state(false);
	let createError = $state('');
	let newName = $state('');
	let newBusinessType = $state<'agency' | 'reseller' | 'hosting_provider'>('agency');
	let switchError = $state('');

	const active: WorkspaceView | null = $derived(
		list ? (list.workspaces.find((w) => w.id === list?.active_workspace_id) ?? null) : null
	);
	const showBanner: boolean = $derived(active !== null && active.type !== 'server');
	const canCreateOrg: boolean = $derived(
		(list?.workspaces ?? []).some(
			(w) => w.type === 'server' && w.permissions.includes('hosting.org.create')
		)
	);

	$effect(() => {
		// Re-read on navigation (login ↔ dashboard) like the access chip;
		// never on dashboard polling.
		const pathname = page.url.pathname;
		void pathname;
		let cancelled = false;
		fetchWorkspaces()
			.then((l) => {
				if (!cancelled) list = l;
			})
			.catch(() => {
				if (!cancelled) list = null;
			});
		return () => {
			cancelled = true;
		};
	});

	async function refresh() {
		list = await fetchWorkspaces().catch(() => null);
	}

	function destinationFor(w: WorkspaceView): string {
		// Server context = the console; every other persona has its own
		// workspace page (honest foundation views included).
		return w.type === 'server' ? '/' : `/org/${w.id}`;
	}

	async function switchTo(w: WorkspaceView) {
		if (switching || w.id === list?.active_workspace_id) {
			open = false;
			return;
		}
		switching = true;
		switchError = '';
		try {
			await enterWorkspace(w.id);
			await refresh();
			open = false;
			await goto(destinationFor(w), { replaceState: true });
		} catch (e) {
			switchError = e instanceof Error ? e.message : String(e);
		} finally {
			switching = false;
		}
	}

	async function createOrg(e: SubmitEvent) {
		e.preventDefault();
		if (creating || newName.trim().length < 3) return;
		creating = true;
		createError = '';
		try {
			const res = await createHostingOrg(newName.trim(), newBusinessType);
			newName = '';
			await refresh();
			open = false;
			await goto(`/org/${res.workspace.id}`, { replaceState: true });
		} catch (err) {
			createError = err instanceof Error ? err.message : String(err);
		} finally {
			creating = false;
		}
	}
</script>

{#if list && list.workspaces.length > 0}
	<div class="ws-area">
		{#if showBanner && active}
			<div class="ws-banner" role="status">
				<span class="ws-banner-label">{personaLabel(active.type)}</span>
				<span class="ws-banner-name">{active.name}</span>
				<span class="ws-banner-note">server administration is not available in this context</span>
			</div>
		{/if}

		<details class="ws-switch" bind:open>
			<summary class="ws-chip">
				{#if active}
					<span class="ws-chip-persona">{personaLabel(active.type)}</span>
					<span class="ws-chip-name">{active.name}</span>
				{:else}
					<span class="ws-chip-name">Workspace</span>
				{/if}
				<span class="ws-caret" aria-hidden="true">▾</span>
			</summary>
			<div class="ws-menu">
				{#each list.workspaces as w (w.id)}
					<button
						type="button"
						class="ws-item"
						class:current={w.id === list.active_workspace_id}
						onclick={() => switchTo(w)}
						disabled={switching}
					>
						<span class="ws-item-persona">{personaLabel(w.type)}{w.business_type ? ` · ${w.business_type.replace('_', ' ')}` : ''}</span>
						<span class="ws-item-name">{w.name}</span>
						<span class="ws-item-role">{w.role}</span>
					</button>
				{/each}

				{#if canCreateOrg}
					<form class="ws-create" onsubmit={createOrg}>
						<div class="ws-create-title">New hosting organization</div>
						<input
							class="ws-create-name"
							type="text"
							placeholder="Organization name"
							bind:value={newName}
							minlength={3}
							maxlength={64}
							required
						/>
						<select class="ws-create-type" bind:value={newBusinessType}>
							<option value="agency">Agency</option>
							<option value="reseller">Reseller</option>
							<option value="hosting_provider">Hosting provider</option>
						</select>
						<button class="btn btn-primary ws-create-btn" type="submit" disabled={creating || newName.trim().length < 3}>
							{creating ? 'Creating…' : 'Create'}
						</button>
						{#if createError}
							<div class="ws-create-error">{createError}</div>
						{/if}
					</form>
				{/if}

				{#if switchError}
					<div class="ws-create-error">{switchError}</div>
				{/if}
			</div>
		</details>
	</div>
{/if}

<style>
	.ws-area {
		display: flex;
		flex-direction: column;
		align-items: flex-end;
		gap: 8px;
	}

	.ws-banner {
		display: flex;
		align-items: baseline;
		gap: 8px;
		flex-wrap: wrap;
		justify-content: flex-end;
		font-size: 12px;
		color: var(--text-dim);
	}

	.ws-banner-label {
		color: var(--accent);
		text-transform: uppercase;
		letter-spacing: 0.06em;
		font-size: 10.5px;
		font-weight: 700;
	}

	.ws-banner-name {
		color: var(--text);
		font-weight: 600;
	}

	.ws-chip {
		display: inline-flex;
		align-items: center;
		gap: 8px;
		padding: 6px 12px;
		border: 1px solid var(--border);
		border-radius: 999px;
		background: var(--bg-card);
		cursor: pointer;
		list-style: none;
		font: inherit;
		white-space: nowrap;
	}

	.ws-chip::-webkit-details-marker {
		display: none;
	}

	.ws-chip:hover {
		border-color: var(--accent);
	}

	.ws-chip-persona {
		font-size: 10.5px;
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--text-dim);
		font-weight: 700;
	}

	.ws-chip-name {
		font-size: 13px;
		font-weight: 600;
		color: var(--text);
		max-width: 220px;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.ws-caret {
		color: var(--text-dim);
		font-size: 10px;
	}

	.ws-menu {
		margin-top: 6px;
		min-width: 280px;
		background: var(--bg-card);
		border: 1px solid var(--border);
		border-radius: 12px;
		padding: 8px;
		display: flex;
		flex-direction: column;
		gap: 4px;
		box-shadow: 0 12px 32px rgba(0, 0, 0, 0.35);
		z-index: 30;
	}

	.ws-item {
		display: flex;
		flex-direction: column;
		gap: 1px;
		text-align: left;
		padding: 8px 10px;
		border-radius: 8px;
		border: 1px solid transparent;
		background: transparent;
		cursor: pointer;
		font: inherit;
	}

	.ws-item:hover {
		border-color: var(--border);
	}

	.ws-item.current {
		background: rgba(56, 189, 248, 0.08);
		border-color: var(--accent);
		cursor: default;
	}

	.ws-item-persona {
		font-size: 10.5px;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		color: var(--text-dim);
	}

	.ws-item-name {
		font-size: 13px;
		font-weight: 600;
		color: var(--text);
	}

	.ws-item-role {
		font-size: 11.5px;
		color: var(--text-dim);
	}

	.ws-create {
		border-top: 1px solid var(--border);
		margin-top: 4px;
		padding-top: 10px;
		display: flex;
		flex-direction: column;
		gap: 6px;
	}

	.ws-create-title {
		font-size: 11px;
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--text-dim);
		font-weight: 700;
	}

	.ws-create-name,
	.ws-create-type {
		font: inherit;
		font-size: 13px;
		padding: 7px 10px;
		border-radius: 8px;
		border: 1px solid var(--border);
		background: var(--bg);
		color: var(--text);
	}

	.ws-create-btn {
		justify-content: center;
	}

	.ws-create-error {
		font-size: 12px;
		color: var(--accent-warn);
	}

	@media (max-width: 719px) {
		.ws-area {
			align-items: stretch;
			width: 100%;
		}

		.ws-banner {
			justify-content: flex-start;
		}

		.ws-menu {
			min-width: 0;
		}
	}
</style>
