<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { fetchSession } from '$lib/api';
	import AccountSection from './sections/AccountSection.svelte';
	import SecuritySection from './sections/SecuritySection.svelte';
	import MailSection from './sections/MailSection.svelte';
	import AccessSection from './sections/AccessSection.svelte';

	const SECTIONS = [
		{ id: 'account', label: 'Account', desc: 'Username, email and password' },
		{ id: 'security', label: 'Security', desc: 'MFA, recovery codes, sessions' },
		{ id: 'mail', label: 'Mail delivery', desc: 'SMTP for password-reset email' },
		{ id: 'access', label: 'Access', desc: 'HTTPS address and custom domain' }
	] as const;
	type SectionId = (typeof SECTIONS)[number]['id'];

	let session = $state<Awaited<ReturnType<typeof fetchSession>> | null>(null);

	// One section is visible at a time; the sidebar (desktop) or chip row
	// (mobile) switches it. The active section is reflected in the URL so
	// any section can be deep-linked: /settings?section=security.
	let section = $state<SectionId>('account');

	$effect(() => {
		const s = page.url.searchParams.get('section');
		if (s && SECTIONS.some((x) => x.id === s)) section = s as SectionId;
	});

	$effect(() => {
		// Gate the settings area: anonymous → /login; unfinished
		// onboarding → wizard. Same contract as the dashboard.
		let cancelled = false;
		fetchSession()
			.then((s) => {
				if (cancelled) return;
				session = s;
				if (!s.authenticated) {
					goto('/login', { replaceState: true });
				} else if (s.scope !== 'full') {
					goto('/onboarding', { replaceState: true });
				}
			})
			.catch(() => {
				if (!cancelled) goto('/login', { replaceState: true });
			});
		return () => {
			cancelled = true;
		};
	});

	function selectSection(id: SectionId) {
		section = id;
		void goto(`/settings?section=${id}`, { replaceState: true, noScroll: true, keepFocus: true });
	}
</script>

<svelte:head>
	<title>Settings — Drevanta</title>
</svelte:head>

{#if session?.authenticated && session.scope === 'full'}
	<div class="settings">
		<div class="page-head">
			<h1>Settings</h1>
			<a class="back" href="/">Back to console</a>
		</div>

		<div class="settings-body">
			<nav class="settings-nav" aria-label="Settings sections">
				{#each SECTIONS as s (s.id)}
					<button
						type="button"
						class="settings-nav-item"
						class:active={section === s.id}
						aria-current={section === s.id ? 'page' : undefined}
						onclick={() => selectSection(s.id)}
					>
						<span class="settings-nav-label">{s.label}</span>
						<span class="settings-nav-desc">{s.desc}</span>
					</button>
				{/each}
			</nav>

			<div class="settings-content">
				{#if section === 'account'}
					<AccountSection />
				{:else if section === 'security'}
					<SecuritySection />
				{:else if section === 'mail'}
					<MailSection />
				{:else}
					<AccessSection />
				{/if}
			</div>
		</div>
	</div>
{/if}

<style>
	.settings {
		max-width: 860px;
		margin: 0 auto;
		display: flex;
		flex-direction: column;
		gap: 16px;
	}

	.settings-body {
		display: grid;
		grid-template-columns: 216px minmax(0, 1fr);
		gap: 20px;
		align-items: start;
	}

	.settings-nav {
		display: flex;
		flex-direction: column;
		gap: 4px;
		position: sticky;
		top: 20px;
	}

	.settings-nav-item {
		display: flex;
		flex-direction: column;
		gap: 2px;
		text-align: left;
		padding: 9px 12px;
		border-radius: 10px;
		border: 1px solid transparent;
		background: transparent;
		cursor: pointer;
		font: inherit;
	}

	.settings-nav-item:hover {
		border-color: var(--border);
	}

	.settings-nav-item.active {
		background: var(--bg-card);
		border-color: var(--border);
	}

	.settings-nav-item:focus-visible {
		outline: none;
		border-color: var(--accent);
	}

	.settings-nav-label {
		font-size: 13.5px;
		font-weight: 600;
		color: var(--text);
	}

	.settings-nav-item.active .settings-nav-label {
		color: var(--accent);
	}

	.settings-nav-desc {
		font-size: 11.5px;
		color: var(--text-dim);
		line-height: 1.45;
	}

	.settings-content {
		display: flex;
		flex-direction: column;
		gap: 16px;
		min-width: 0;
	}

	@media (max-width: 719px) {
		.settings-body {
			grid-template-columns: minmax(0, 1fr);
			gap: 14px;
		}

		.settings-nav {
			position: static;
			flex-direction: row;
			overflow-x: auto;
			gap: 8px;
			padding-bottom: 2px;
		}

		.settings-nav-item {
			flex: 0 0 auto;
			flex-direction: row;
			align-items: baseline;
			gap: 8px;
			padding: 7px 12px;
			border-radius: 999px;
			border: 1px solid var(--border);
		}

		.settings-nav-item.active {
			border-color: var(--accent);
		}

		.settings-nav-desc {
			display: none;
		}
	}
</style>
