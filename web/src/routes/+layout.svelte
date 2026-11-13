<script lang="ts">
        import '../app.css';
        import { page } from '$app/state';
        import { fetchAccessStatus, type AccessStatus } from '$lib/api';
        import { accessLabel, accessLabelTitle } from '$lib/access.js';
        import WorkspaceContext from '$lib/components/WorkspaceContext.svelte';

        let { children } = $props();

        // The onboarding wizard renders the approved standalone full-screen
        // design (its own centered card, background glow and grid), so it
        // opts out of the dashboard shell. Login and the dashboard keep the
        // shell unchanged.
        const bare = $derived(page.url.pathname === '/onboarding' || page.url.pathname.startsWith('/onboarding/'));

        // The access chip is the TRUSTED backend view (root-written config +
        // proxy status file), never a guess from the browser URL. Re-read on
        // navigation (login ↔ dashboard) but not on every poll.
        let access = $state<AccessStatus | null>(null);
        $effect(() => {
                const pathname = page.url.pathname; // dependency: refetch on route change
                void pathname;
                let cancelled = false;
                fetchAccessStatus()
                        .then((a) => {
                                if (!cancelled) access = a;
                        })
                        .catch(() => {
                                if (!cancelled) access = null;
                        });
                return () => {
                        cancelled = true;
                };
        });
        const label = $derived(accessLabel(access));
        const labelTitle = $derived(accessLabelTitle(access));

        // App version from the daemon's public /healthz — shown once, in
        // the footer, next to the access chip. Empty until it loads.
        let version = $state('');
        $effect(() => {
                fetch('/healthz', { headers: { accept: 'application/json' } })
                        .then((r) => (r.ok ? r.json() : null))
                        .then((h: { version?: string } | null) => {
                                if (h?.version) version = h.version;
                        })
                        .catch(() => undefined);
        });
</script>

{#if bare}
        {@render children()}
{:else}
        <div class="shell">
        <header>
                <div class="brand">
                        <span class="brand-mark" aria-hidden="true"></span>
                        <span class="brand-name">Drevanta</span>
                        <span class="brand-sub">server console</span>
                </div>
                <WorkspaceContext />
        </header>

                <main>
                        {@render children()}
                </main>

                <footer>
                        <span title={labelTitle}>{label}</span>
                        <span class="version">{version ? `v${version}` : ''}</span>
                </footer>
        </div>
{/if}

<style>
        .shell {
                max-width: 1080px;
                margin: 0 auto;
                padding: 20px 24px 40px;
                min-height: 100vh;
                display: flex;
                flex-direction: column;
        }

        header {
                display: flex;
                align-items: baseline;
                justify-content: space-between;
                margin-bottom: 24px;
                border-bottom: 1px solid var(--border);
                padding-bottom: 16px;
        }

        .brand {
                display: flex;
                align-items: baseline;
                gap: 10px;
        }

        .brand-mark {
                width: 10px;
                height: 10px;
                border-radius: 3px;
                background: var(--accent);
                display: inline-block;
                box-shadow: 0 0 8px var(--accent);
        }

        .brand-name {
                font-size: 20px;
                font-weight: 700;
                letter-spacing: 0.02em;
        }

        .brand-sub {
                color: var(--text-dim);
                font-size: 13px;
        }

        footer {
                display: flex;
                justify-content: space-between;
                gap: 12px;
                margin-top: 32px;
                padding-top: 16px;
                border-top: 1px solid var(--border);
                color: var(--text-dim);
                font-size: 12px;
        }

        .version {
                font-variant-numeric: tabular-nums;
        }
        main {
                flex: 1;
        }
</style>
