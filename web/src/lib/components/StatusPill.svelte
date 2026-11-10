<script lang="ts">
	let { label, ok }: { label: string; ok: boolean } = $props();
</script>

<span class="pill" class:ok class:bad={!ok}>
	<span class="dot"></span>{label}
</span>

<style>
	.pill {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		padding: 4px 10px;
		border: 1px solid var(--border);
		border-radius: 999px;
		font-size: 11px;
		color: var(--text-dim);
		text-transform: uppercase;
		letter-spacing: 0.06em;
	}

	.dot {
		width: 8px;
		height: 8px;
		border-radius: 50%;
		background: var(--accent-crit);
	}

	.pill.ok .dot {
		background: var(--accent);
		box-shadow: 0 0 6px var(--accent);
	}
</style>
