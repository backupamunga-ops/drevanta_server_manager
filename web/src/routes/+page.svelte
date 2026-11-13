<script lang="ts">
        import { goto } from '$app/navigation';
        import { fetchMetrics, fetchOverview, fetchSession, fetchWorkspaces, logout, ApiError, isUp } from '$lib/api';
        import type { MetricsSnapshot, OverviewResponse, SessionInfo } from '$lib/api';
        import { formatBytes, formatUptime } from '$lib/format';
        import Sparkline from '$lib/components/Sparkline.svelte';
        import Bar from '$lib/components/Bar.svelte';
        import StatusPill from '$lib/components/StatusPill.svelte';
        import ConsoleTabs from '$lib/components/ConsoleTabs.svelte';

        let snapshot = $state<MetricsSnapshot | null>(null);
        let session = $state<SessionInfo | null>(null);
        let authed = $state(false);
        let liveness = $state<'up' | 'down'>('down');
        let readiness = $state<'ready' | 'not-ready'>('not-ready');
        let history = $state<number[]>([]);
        let paused = $state(false);
        let lastError = $state('');
        let updatedAt = $state('');

        // P2.1 overview data: alerts, 24 h history, systemd rollup. The
        // cards above poll the live snapshot every 5 s; this refetches
        // every 30 s — history is 15 s-resolution data, not a live feed.
        let overview = $state<OverviewResponse | null>(null);

        // The 24 h history can carry up to ~5.8k raw points; stride it
        // down to ~200 for the sparkline (first + last always kept).
        const history24 = $derived.by(() => {
                const pts = overview?.history ?? [];
                if (pts.length === 0) return [] as number[];
                const target = 200;
                const stride = Math.max(1, Math.ceil(pts.length / target));
                const out: number[] = [];
                for (let i = 0; i < pts.length; i += stride) out.push(pts[i].cpu_avg);
                if (out[out.length - 1] !== pts[pts.length - 1].cpu_avg) {
                        out.push(pts[pts.length - 1].cpu_avg);
                }
                return out;
        });

        async function loadOverview() {
                if (!authed) return;
                try {
                        overview = await fetchOverview();
                } catch (e) {
                        if (e instanceof ApiError && e.status === 403 && e.code === 'workspace_mismatch') {
                                await rerouteForContext();
                        }
                        // Other failures degrade honestly: the overview block stays empty.
                }
        }

        async function rerouteForContext() {
                const list = await fetchWorkspaces().catch(() => null);
                const active = list?.workspaces.find((w) => w.id === list?.active_workspace_id);
                await goto(active && active.type !== 'server' ? `/org/${active.id}` : '/', { replaceState: true });
        }

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
                        if (e instanceof ApiError && e.status === 401) {
                                await goto('/login', { replaceState: true });
                                return;
                        }
                        // P2.0: a 403 workspace_mismatch means the session switched into a
                        // non-server context — go to that workspace's page, NOT to login.
                        if (e instanceof ApiError && e.status === 403 && e.code === 'workspace_mismatch') {
                                await rerouteForContext();
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
                                        // P2.0: land in the session's active workspace context —
                                        // a session switched to a hosting organization resumes there.
                                        fetchWorkspaces()
                                                .then((list) => {
                                                        if (cancelled || !list) return;
                                                        const active = list.workspaces.find((w) => w.id === list.active_workspace_id);
                                                        if (active && active.type !== 'server') {
                                                                goto(`/org/${active.id}`, { replaceState: true });
                                                        }
                                                })
                                                .catch(() => undefined);
                                        refresh();
                                        const id = setInterval(refresh, 5000);
                                        loadOverview();
                                        const overviewId = setInterval(loadOverview, 30000);
                                        return () => {
                                                clearInterval(id);
                                                clearInterval(overviewId);
                                        };
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
							href="/settings?section=security"
							title="MFA is not enabled — set it up under Settings → Security"
					>mfa off</a>
                        {/if}
                </div>
                <div class="userbox">
                        <span>{session?.username}</span>
                        <a class="btn btn--link" href="/settings">Settings</a>
                        <button class="btn" onclick={signOut}>Sign out</button>
                </div>
        </div>

        <ConsoleTabs />

        {#if lastError}
                <div class="error">API unreachable — {lastError}</div>
        {/if}

        {#if overview && overview.alerts && overview.alerts.length > 0}
                <div class="alerts" role="status">
                        {#each overview.alerts as a (a.id)}
                                <div class="alert" class:crit={a.severity === 'crit'}>
                                        <span class="alert-sev">{a.severity}</span>
                                        <span class="alert-subject">{a.kind} · {a.subject}</span>
                                        <span class="alert-msg">{a.message} — {a.value.toFixed(1)} (limit {a.threshold.toFixed(0)}{a.kind === 'memory' ? '% available min' : ''})</span>
                                </div>
                        {/each}
                </div>
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

                        {#if overview?.systemd}
                                <div class="card">
                                        <div class="card-title">Services</div>
                                        <div class="card-value">{overview.systemd.running}<span class="unit">/{overview.systemd.total}</span></div>
                                        <div class="card-sub">
                                                running · {overview.systemd.failed} failed
                                                <a href="/services">details →</a>
                                        </div>
                                </div>
                        {/if}
                </div>

                {#if overview?.history && overview.history.length > 1}
                        <div class="card history-card">
                                <div class="card-title">CPU — last 24 hours</div>
                                <Sparkline values={history24} />
                                <div class="card-sub">
                                        {overview.history.length} samples
                                        ({overview.history[0].resolution === '5m' ? '5-minute aggregates' : 'raw 15 s samples'}
                                        {#if overview.history[overview.history.length - 1].resolution !== overview.history[0].resolution}
                                        blending into 5-minute aggregates{/if}) · retention: 24 h raw, 14 d aggregates
                                </div>
                        </div>
                {/if}

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

        .alerts {
                display: flex;
                flex-direction: column;
                gap: 8px;
                margin-bottom: 16px;
        }

        .alert {
                display: flex;
                align-items: baseline;
                gap: 10px;
                flex-wrap: wrap;
                padding: 10px 14px;
                border: 1px solid rgba(251, 191, 36, 0.5);
                border-radius: 10px;
                background: rgba(251, 191, 36, 0.07);
                font-size: 13px;
        }

        .alert.crit {
                border-color: rgba(248, 113, 113, 0.6);
                background: rgba(248, 113, 113, 0.08);
        }

        .alert-sev {
                text-transform: uppercase;
                letter-spacing: 0.06em;
                font-size: 10.5px;
                font-weight: 700;
                color: var(--accent-warn);
        }

        .alert.crit .alert-sev {
                color: #f87171;
        }

        .alert-subject {
                font-weight: 600;
        }

        .alert-msg {
                color: var(--text-dim);
        }

        .history-card {
                margin-top: 16px;
        }

        .card-sub a {
                color: var(--accent);
                text-decoration: none;
        }
</style>
