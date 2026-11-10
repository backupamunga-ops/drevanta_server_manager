<script lang="ts">
	import { fetchMetrics, isUp } from '$lib/api';
	import type { MetricsSnapshot } from '$lib/api';
	import { formatBytes, formatUptime } from '$lib/format';
	import Sparkline from '$lib/components/Sparkline.svelte';
	import Bar from '$lib/components/Bar.svelte';
	import StatusPill from '$lib/components/StatusPill.svelte';

	let snapshot = $state<MetricsSnapshot | null>(null);
	let liveness = $state<'up' | 'down'>('down');
	let readiness = $state<'ready' | 'not-ready'>('not-ready');
	let history = $state<number[]>([]);
	let paused = $state(false);
	let lastError = $state('');
	let updatedAt = $state('');

	async function refresh() {
		if (paused) return;
		try {
			const s = await fetchMetrics();
			snapshot = s;
			history = [...history.slice(-59), s.cpu.usage_percent];
			updatedAt = new Date().toLocaleTimeString();
			lastError = '';
			const [h, r] = await Promise.all([isUp('/healthz'), isUp('/readyz')]);
			liveness = h ? 'up' : 'down';
			readiness = r ? 'ready' : 'not-ready';
		} catch (e) {
			lastError = e instanceof Error ? e.message : String(e);
			liveness = 'down';
			readiness = 'not-ready';
		}
	}

	$effect(() => {
		refresh();
		const id = setInterval(refresh, 5000);
		return () => clearInterval(id);
	});
</script>

<svelte:head>
	<title>Drevanta — Server Console</title>
	<meta name="description" content="Private VPS management console" />
</svelte:head>

<div class="toolbar">
	<div class="pills">
		<StatusPill label="healthz" ok={liveness === 'up'} />
		<StatusPill label="readyz" ok={readiness === 'ready'} />
	</div>
	<button class="btn" onclick={() => (paused = !paused)}>
		{paused ? 'Resume' : 'Pause'}
	</button>
</div>

{#if lastError}
	<div class="error">API unreachable — {lastError}</div>
{/if}

{#if snapshot}
	<div class="grid">
		<div class="card">
			<div class="card-title">CPU</div>
			<div class="card-value">
				{snapshot.cpu.usage_percent.toFixed(1)}<span class="unit">%</span>
			</div>
			<Sparkline values={history} />
			<div class="card-sub">
				{snapshot.cpu.cores} cores · load {snapshot.cpu.load_1m.toFixed(2)} /
				{snapshot.cpu.load_5m.toFixed(2)} / {snapshot.cpu.load_15m.toFixed(2)}
			</div>
		</div>

		<div class="card">
			<div class="card-title">Memory</div>
			<div class="card-value">
				{snapshot.memory.used_percent.toFixed(1)}<span class="unit">%</span>
			</div>
			<Bar percent={snapshot.memory.used_percent} />
			<div class="card-sub">
				{formatBytes(snapshot.memory.used_bytes)} used of
				{formatBytes(snapshot.memory.total_bytes)} ·
				{formatBytes(snapshot.memory.available_bytes)} available
			</div>
		</div>

		<div class="card">
			<div class="card-title">Uptime</div>
			<div class="card-value">{formatUptime(snapshot.uptime_seconds)}</div>
			<div class="card-sub">since last boot</div>
		</div>

		{#each snapshot.disk as d (d.path)}
			<div class="card">
				<div class="card-title">Disk {d.path}</div>
				<div class="card-value">
					{d.used_percent.toFixed(1)}<span class="unit">%</span>
				</div>
				<Bar percent={d.used_percent} />
				<div class="card-sub">
					{formatBytes(d.used_bytes)} used of {formatBytes(d.total_bytes)} ·
					{formatBytes(d.available_bytes)} free
				</div>
			</div>
		{/each}
	</div>
	<div class="updated">updated {updatedAt} · refresh every 5 s</div>
{:else if !lastError}
	<div class="loading">connecting to daemon…</div>
{/if}
