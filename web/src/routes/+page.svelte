<script lang="ts">
        import { goto } from '$app/navigation';
        import { fetchMetrics, fetchSession, logout, ApiError, isUp } from '$lib/api';
        import type { MetricsSnapshot, SessionInfo } from '$lib/api';
        import { formatBytes, formatUptime } from '$lib/format';
        import Sparkline from '$lib/components/Sparkline.svelte';
        import Bar from '$lib/components/Bar.svelte';
        import StatusPill from '$lib/components/StatusPill.svelte';

        let snapshot = $state<MetricsSnapshot | null>(null);
        let session = $state<SessionInfo | null>(null);
        let authed = $state(false);
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
                        // Session expired mid-flight: send the operator to sign in again.
                        if (e instanceof ApiError && (e.status === 401 || e.status === 403)) {
                                await goto('/login', { replaceState: true });
                                return;
                        }
                        lastError = e instanceof Error ? e.message : String(e);
                        liveness = 'down';
                        readiness = 'not-ready';
                }
        }

        $effect(() => {
                // Gate the console: anonymous → /login; unfinished onboarding → wizard.
                let cancelled = false;
                fetchSession()
                        .then((s) => {
                                if (cancelled) return;
                                session = s;
                                if (!s.authenticated) {
                                        // A fresh install sends the operator straight to the
                                        // first-owner wizard; afterwards, unauthenticated
                                        // visitors go to sign-in.
                                        if (s.instance_status === 'unclaimed') {
                                                goto('/onboarding', { replaceState: true });
                                        } else {
                                                goto('/login', { replaceState: true });
                                        }
                                } else if (s.scope !== 'full') {
                                        goto('/onboarding', { replaceState: true });
                                } else {
                                        authed = true;
                                        refresh();
                                        const id = setInterval(refresh, 5000);
                                        return () => clearInterval(id);
                                }
                        })
                        .catch(() => {
                                if (!cancelled) goto('/login', { replaceState: true });
                        });
                return () => {
                        cancelled = true;
                };
        });

        async function signOut() {
                await logout();
                await goto('/login', { replaceState: true });
        }
</script>

<svelte:head>
        <title>Drevanta — Server Console</title>
        <meta name="description" content="Private VPS management console" />
</svelte:head>

{#if authed}
        <div class="toolbar">
                <div class="pills">
                        <StatusPill label="healthz" ok={liveness === 'up'} />
                        <StatusPill label="readyz" ok={readiness === 'ready'} />
                        {#if session?.mfa_enabled}
                                <span class="mfa-pill" title="multi-factor authentication is enabled">mfa on</span>
                        {:else}
                                <a
                                        class="mfa-pill warn"
                                        href="/settings"
                                        title="MFA is not enabled — set it up under Account Security"
                                >mfa off</a>
                        {/if}
                </div>
                <div class="userbox">
                        <span>{session?.username}</span>
                        <a class="btn btn--link" href="/settings">Settings</a>
                        <button class="btn" onclick={signOut}>Sign out</button>
                </div>
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
{/if}

<style>
        .mfa-pill {
                display: inline-flex;
                align-items: center;
                padding: 4px 10px;
                border: 1px solid var(--border);
                border-radius: 999px;
                font-size: 11px;
                color: var(--accent);
                text-transform: uppercase;
                letter-spacing: 0.06em;
        }

        .mfa-pill.warn {
                color: var(--accent-warn);
                border-color: rgba(251, 191, 36, 0.5);
                text-decoration: none;
        }

        .btn--link {
                text-decoration: none;
                display: inline-flex;
                align-items: center;
        }
</style>
