<script lang="ts">
	import { goto } from '$app/navigation';
	import { fetchSession, fetchWorkspaces, fetchServices, ApiError } from '$lib/api';
	import type { ServicesResponse } from '$lib/api';
	import ConsoleTabs from '$lib/components/ConsoleTabs.svelte';

	// Service health (RFC D-06): read-only. Actions (start/stop/restart)
	// arrive with the P2.2 privileged helper — this page says so instead
	// of showing dead buttons (DESIGN_PRINCIPLES.md).
	let data = $state<ServicesResponse | null>(null);
	let authed = $state(false);
	let lastError = $state('');
	let loading = $state(false);

	$effect(() => {
		let cancelled = false;
		fetchSession()
			.then((s) => {
				if (cancelled) return;
				if (!s.authenticated) {
					goto('/login', { replaceState: true });
					return;
				}
				if (s.scope !== 'full') {
					goto('/onboarding', { replaceState: true });
					return;
				}
				authed = true;
				load();
			})
			.catch(() => {
				if (!cancelled) goto('/login', { replaceState: true });
			});
		fetchWorkspaces()
			.then((list) => {
				if (cancelled || !list) return;
				const active = list.workspaces.find((w) => w.id === list.active_workspace_id);
				if (active && active.type !== 'server') {
					goto(`/org/${active.id}`, { replaceState: true });
				}
			})
			.catch(() => undefined);
		return () => {
			cancelled = true;
		};
	});

	async function load() {
		if (loading) return;
		loading = true;
		try {
			data = await fetchServices();
			lastError = '';
		} catch (e) {
			if (e instanceof ApiError && e.status === 401) {
				await goto('/login', { replaceState: true });
				return;
			}
			if (e instanceof ApiError && e.status === 403) {
				await goto('/', { replaceState: true });
				return;
			}
			lastError = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}
</script>

<svelte:head>
	<title>Services — Drevanta</title>
</svelte:head>

{#if authed}
	<div class="toolbar">
		<h1>Services</h1>
		<div class="userbox">
			<button class="btn" onclick={load} disabled={loading}>
				{loading ? 'Loading…' : 'Refresh'}
			</button>
		</div>
	</div>

	<ConsoleTabs />

	{#if lastError}
		<div class="error">service health unavailable — {lastError}</div>
	{/if}

	{#if data}
		<div class="pills summary">
			<span class="pill">{data.summary.total} units</span>
			<span class="pill pill-ok">{data.summary.running} running</span>
			<span class="pill" class:bad={data.summary.failed > 0}>{data.summary.failed} failed</span>
		</div>

		<div class="card">
			<table class="svc-table">
				<thead>
					<tr>
						<th>Unit</th><th>Load</th><th>Active</th><th>Sub</th><th>Enabled</th><th>Description</th>
					</tr>
				</thead>
				<tbody>
					{#each data.units as u (u.unit)}
						<tr class:failed={u.active === 'failed'}>
							<td>{u.unit}</td>
							<td>{u.load}</td>
							<td class:ok={u.active === 'active'} class:failed-text={u.active === 'failed'}>{u.active}</td>
							<td>{u.sub}</td>
							<td>{u.enabled || '—'}</td>
							<td class="desc">{u.description}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>

		<div class="note">
			Read-only view. Starting, stopping and restarting services arrives
			with the privileged helper in milestone P2.2 — nothing on this page
			mutates the system.
		</div>
	{:else if !lastError}
		<div class="loading">loading units…</div>
	{/if}
{/if}

<style>
	.toolbar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
		margin-bottom: 14px;
	}

	.toolbar h1 {
		margin: 0;
		font-size: 20px;
	}

	.userbox {
		display: flex;
		align-items: center;
		gap: 10px;
	}

	.summary {
		margin-bottom: 14px;
	}

	.pill.bad {
		color: #f87171;
		border-color: rgba(248, 113, 113, 0.5);
	}

	.svc-table {
		width: 100%;
		border-collapse: collapse;
		font-size: 13px;
	}

	.svc-table th {
		text-align: left;
		font-size: 11px;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		color: var(--text-dim);
		padding: 6px 8px;
		border-bottom: 1px solid var(--border);
	}

	.svc-table td {
		padding: 6px 8px;
		border-bottom: 1px solid var(--border);
		white-space: nowrap;
	}

	.svc-table td.desc {
		white-space: normal;
		color: var(--text-dim);
	}

	.svc-table tr.failed td {
		background: rgba(248, 113, 113, 0.06);
	}

	.svc-table td.ok {
		color: #4ade80;
	}

	.svc-table td.failed-text {
		color: #f87171;
		font-weight: 700;
	}

	.note {
		margin-top: 12px;
		font-size: 13px;
		color: var(--text-dim);
	}
</style>
