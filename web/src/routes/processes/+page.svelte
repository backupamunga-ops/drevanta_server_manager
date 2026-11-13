<script lang="ts">
	import { goto } from '$app/navigation';
	import { fetchSession, fetchWorkspaces, fetchProcesses, ApiError } from '$lib/api';
	import type { ProcessesResponse } from '$lib/api';
	import { formatBytes } from '$lib/format';
	import ConsoleTabs from '$lib/components/ConsoleTabs.svelte';

	// The process list (RFC D-05): computed live over a 500 ms window,
	// never stored. Manual refresh plus an opt-out 10 s auto-refresh
	// while the page is open.
	let data = $state<ProcessesResponse | null>(null);
	let authed = $state(false);
	let lastError = $state('');
	let loading = $state(false);
	let auto = $state(true);

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
				return startPolling();
			})
			.catch(() => {
				if (!cancelled) goto('/login', { replaceState: true });
			});
		// P2.0: a session parked in a non-server context has no business
		// here; send it where it can actually work.
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

		function startPolling() {
			const id = setInterval(() => {
				if (auto) void load();
			}, 10000);
			return () => clearInterval(id);
		}
	});

	async function load() {
		if (loading) return;
		loading = true;
		try {
			data = await fetchProcesses();
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
	<title>Processes — Drevanta</title>
</svelte:head>

{#if authed}
	<div class="toolbar">
		<h1>Processes</h1>
		<div class="userbox">
			<label class="auto">
				<input type="checkbox" bind:checked={auto} /> auto-refresh 10 s
			</label>
			<button class="btn" onclick={load} disabled={loading}>
				{loading ? 'Measuring…' : 'Refresh'}
			</button>
		</div>
	</div>

	<ConsoleTabs />

	{#if lastError}
		<div class="error">process list unavailable — {lastError}</div>
	{/if}

	{#if data}
		<div class="card">
			<div class="card-title">Top by CPU — measured over {data.measured_over_ms} ms</div>
			<table class="proc-table">
				<thead>
					<tr>
						<th>User</th><th>PID</th><th>State</th><th>CPU</th><th>Memory</th><th>Command</th>
					</tr>
				</thead>
				<tbody>
					{#each data.processes as p (p.pid)}
						<tr>
							<td>{p.user}</td>
							<td>{p.pid}</td>
							<td>{p.state}</td>
							<td class:hot={p.cpu_percent >= 50}>{p.cpu_percent.toFixed(1)}%</td>
							<td>{formatBytes(p.rss_bytes)}</td>
							<td class="cmd">{p.cmdline}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
		<div class="updated">point-in-time view · percentages use the top(1) convention (100% = one full core)</div>
	{:else if !lastError}
		<div class="loading">measuring…</div>
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

	.auto {
		font-size: 12.5px;
		color: var(--text-dim);
		display: inline-flex;
		align-items: center;
		gap: 6px;
	}

	.proc-table {
		width: 100%;
		border-collapse: collapse;
		font-size: 13px;
	}

	.proc-table th {
		text-align: left;
		font-size: 11px;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		color: var(--text-dim);
		padding: 6px 8px;
		border-bottom: 1px solid var(--border);
	}

	.proc-table td {
		padding: 6px 8px;
		border-bottom: 1px solid var(--border);
		white-space: nowrap;
	}

	.proc-table td.cmd {
		white-space: normal;
		word-break: break-all;
		color: var(--text-dim);
	}

	.proc-table td.hot {
		color: var(--accent-warn);
		font-weight: 700;
	}
</style>
