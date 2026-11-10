<script lang="ts">
	let { percent }: { percent: number } = $props();

	let clamped = $derived(Math.max(0, Math.min(100, percent)));
</script>

<div
	class="track"
	role="progressbar"
	aria-valuenow={clamped}
	aria-valuemin={0}
	aria-valuemax={100}
>
	<div
		class="fill"
		class:warn={clamped > 80 && clamped <= 90}
		class:crit={clamped > 90}
		style="width: {clamped}%"
	></div>
</div>

<style>
	.track {
		height: 8px;
		background: var(--bg-inset);
		border: 1px solid var(--border);
		border-radius: 999px;
		overflow: hidden;
	}

	.fill {
		height: 100%;
		background: var(--accent);
		border-radius: 999px;
		transition: width 0.4s ease;
	}

	.fill.warn {
		background: var(--accent-warn);
	}

	.fill.crit {
		background: var(--accent-crit);
	}
</style>
