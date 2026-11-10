<script lang="ts">
	let { values, max = 100 }: { values: number[]; max?: number } = $props();

	const W = 260;
	const H = 56;

	let points = $derived.by(() => {
		if (values.length < 2) return '';
		const step = W / (values.length - 1);
		return values
			.map((v, i) => {
				const x = (i * step).toFixed(1);
				const y = (H - (Math.max(0, Math.min(max, v)) / max) * (H - 4) - 2).toFixed(1);
				return `${x},${y}`;
			})
			.join(' ');
	});
</script>

<svg
	class="spark"
	viewBox="0 0 {W} {H}"
	preserveAspectRatio="none"
	role="img"
	aria-label="CPU usage history"
>
	<polyline
		points={points}
		fill="none"
		stroke="currentColor"
		stroke-width="2"
		stroke-linejoin="round"
		stroke-linecap="round"
	/>
</svg>

<style>
	.spark {
		width: 100%;
		height: 56px;
		color: var(--accent);
		display: block;
	}
</style>
