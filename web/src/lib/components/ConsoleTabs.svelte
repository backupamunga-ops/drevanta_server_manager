<script lang="ts">
	import { page } from '$app/state';

	// The console section tabs (RFC D-08): deep-linkable routes under
	// the same shell. Server workspace context only — the daemon hides
	// these surfaces elsewhere, and the shell follows suit.
	const tabs = [
		{ href: '/', label: 'Overview' },
		{ href: '/processes', label: 'Processes' },
		{ href: '/services', label: 'Services' }
	];
</script>

<nav class="console-tabs" aria-label="Console sections">
	{#each tabs as t (t.href)}
		<a
			href={t.href}
			class="tab"
			class:active={page.url.pathname === t.href}
			aria-current={page.url.pathname === t.href ? 'page' : undefined}
		>{t.label}</a>
	{/each}
</nav>

<style>
	.console-tabs {
		display: flex;
		gap: 6px;
		margin-bottom: 16px;
	}

	.tab {
		padding: 7px 14px;
		border-radius: 999px;
		border: 1px solid var(--border);
		color: var(--text-dim);
		text-decoration: none;
		font-size: 13px;
		font-weight: 600;
	}

	.tab:hover {
		border-color: var(--accent);
		color: var(--text);
	}

	.tab.active {
		background: rgba(56, 189, 248, 0.1);
		border-color: var(--accent);
		color: var(--text);
	}
</style>
