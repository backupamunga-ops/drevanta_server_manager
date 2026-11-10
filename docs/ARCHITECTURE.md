# Drevanta architecture — as built (Sprint 1)

This document describes what is actually implemented in the Sprint 1
walking skeleton. It is the reference for how the system works today; the
Phase 1 installer/onboarding product is a separate work stream and is
marked as roadmap where relevant.

## 1. Overview

Drevanta is a single Go binary (`drevantad`) that:

1. serves a frozen, loopback-only HTTP API for server self-management,
2. serves the operator dashboard embedded in the binary itself,
3. persists lifecycle events in a local SQLite audit log,
4. runs as an unprivileged, hardened systemd service.

```
operator workstation                 VPS (512 MB-class target)
┌──────────────────────┐             ┌───────────────────────────────────┐
│  browser             │  SSH tunnel │  systemd (drevanta.service)       │
│  http://127.0.0.1    ├────────────►│   └─ drevantad (user "drevanta")  │
│  :18080              │  loopback   │       ├─ 127.0.0.1:8080           │
└──────────────────────┘  only       │       │    ├─ /healthz            │
                                     │       │    ├─ /readyz             │
                                     │       │    ├─ /api/v1/server/     │
                                     │       │    │     metrics          │
                                     │       │    └─ / (embedded UI)     │
                                     │       └─ /var/lib/drevanta/      │
                                     │           drevanta.db (SQLite)   │
                                     └───────────────────────────────────┘
```

Design invariants:

- **Private-first:** the only network surface is `127.0.0.1`. Operators
  reach the console through an SSH tunnel; there is no public port, no
  reverse proxy, no TLS terminator in Sprint 1.
- **Unprivileged:** the daemon runs as the dedicated `drevanta` system
  user with an empty capability set. It never needs root.
- **On-demand metrics:** no background sampling loops. Every metrics
  request performs exactly one `/proc` + `statfs(2)` pass.
- **No external runtime deps:** pure-Go SQLite (`modernc.org/sqlite`,
  no CGO), stdlib HTTP, static binary. No Docker, no Nginx, no agents.

## 2. Components

| Component            | Location                | Notes                                                             |
| -------------------- | ----------------------- | ----------------------------------------------------------------- |
| Daemon entry point   | `cmd/drevantad/`        | Flags (`--addr`, `--data-dir`), signal handling, graceful shutdown.|
| HTTP API             | `internal/httpapi/`     | Frozen surface, panic recovery, error-focused logging.             |
| Metrics collectors   | `internal/metrics/`     | `/proc/stat`, `/proc/meminfo`, `/proc/loadavg`, `/proc/uptime`, `statfs(2)`; pure stdlib; unit-tested against fixtures. |
| Store                | `internal/store/`       | modernc.org/sqlite; migrations; `audit_events`; single connection. |
| Dashboard            | `web/` (Go pkg `web`)   | SvelteKit 2 + Svelte 5, TypeScript, adapter-static; embedded via `go:embed all:build`; no CDN assets. |
| Service definition   | `deploy/drevanta.service` | Hardened systemd unit, `MemoryMax=256M`.                          |

Why `go:embed` from `web/`: the embed directive is package-relative, so
the embed package lives beside `web/build`; `npm run build` output is
embedded directly with no copy step. A committed `.gitkeep` keeps
`go build` working before the first dashboard build.

## 3. API contract (frozen for Sprint 1)

| Endpoint                 | Method | Success                     | Failure                        |
| ------------------------ | ------ | --------------------------- | ------------------------------ |
| `/healthz`               | GET    | 200 `{"status":"ok","version":…}` | —                        |
| `/readyz`                | GET    | 200 `{"status":"ready"}`    | 503 `{"status":"not_ready"}`   |
| `/api/v1/server/metrics` | GET    | 200 snapshot JSON           | 500 `{"error":"…"}`            |
| `/`                      | GET    | 200 embedded dashboard      | SPA fallback to `index.html`   |

- Unknown `/api/*` paths: 404 JSON (strict API namespace).
- Wrong method: 405 (Go 1.22+ method-aware mux).
- Adding endpoints requires a deliberate decision; the frozen surface is
  `/healthz`, `/readyz`, `/api/v1/server/metrics` only.

Metrics snapshot shape:

```json
{
  "timestamp": "RFC3339 UTC",
  "cpu":        { "cores": 4, "usage_percent": 0.0, "load_1m": 0.1, "load_5m": 0.05, "load_15m": 0.02 },
  "memory":     { "total_bytes": 0, "available_bytes": 0, "used_bytes": 0, "used_percent": 0.0,
                  "buffers_bytes": 0, "cached_bytes": 0, "swap_total_bytes": 0, "swap_used_bytes": 0 },
  "disk":       [ { "path": "/", "total_bytes": 0, "used_bytes": 0, "available_bytes": 0, "used_percent": 0.0 } ],
  "uptime_seconds": 0.0
}
```

CPU `usage_percent` is the busy fraction between the current and the
previous request (jiffy delta from `/proc/stat`); the first request takes
two samples 250 ms apart. `used = total − available` for memory, matching
kernel reclaimable-pressure semantics.

## 4. Storage and audit

- File: `/var/lib/drevanta/drevanta.db`, WAL mode, `busy_timeout=10s`,
  `foreign_keys=on`, single connection (no `SQLITE_BUSY` in Sprint 1).
- `schema_migrations(version, name, applied_at)` — append-only list;
  migrations are applied transactionally on every start, idempotent.
- `audit_events(id, ts, actor, action, target, detail)` with indexes on
  `ts` and `action`. Sprint 1 records `daemon_started` / `daemon_stopped`
  lifecycle events. Per-request audit of metrics reads is deliberately
  omitted (polling would grow the log without adding accountability);
  request-scoped audit arrives with authenticated actions in later
  sprints.

## 5. Service hardening (systemd)

`deploy/drevanta.service`:

| Area                | Settings                                                                 |
| ------------------- | ------------------------------------------------------------------------ |
| Identity            | `User/Group=drevanta`, `NoNewPrivileges=yes`, empty capability set       |
| Filesystem          | `ProtectSystem=strict`, `ProtectHome=yes`, `PrivateTmp=yes`, `StateDirectory=drevanta` (only `/var/lib/drevanta` is writable) |
| Kernel surface      | `ProtectKernelTunables/Modules/ControlGroups/Clock/Hostname=yes`, `ProtectProc=invisible`, `PrivateDevices=yes` |
| Execution           | `MemoryDenyWriteExecute=yes`, `LockPersonality=yes`, `SystemCallArchitectures=native`, `RestrictSUIDSGID/Realtime=yes` |
| Address families    | `AF_UNIX AF_INET AF_INET6` only                                          |
| Resources           | `MemoryMax=256M`, `MemoryHigh=192M`, `TasksMax=64`, `LimitNOFILE=4096`   |
| Failure policy      | `Restart=on-failure`, `RestartSec=3s`                                    |

Rationale for the memory ceiling: the 512 MB-class VPS budget leaves
headroom for the OS plus future components; see `docs/BENCHMARKS.md` for
measured consumption (an order of magnitude below the ceiling).

## 6. Deployment topology

- **Manual and gated.** Per the agreed engineering order, automatic
  deployment on push is disabled until the rollback drill passes.
  `ci.yml` builds, vets, and tests only; it never deploys and never
  receives host credentials.
- The operator builds a static `linux/amd64` binary
  (`make cross-linux-amd64`), stages it on the host, and installs it via
  the runbook in `deploy/DEPLOY.md` (system user, `/opt/drevanta`,
  systemd unit, enable + verify).
- **Rollback (planned, Sprint 2):** `releases/<version>/` + `current`
  symlink, `/readyz` gate, and a `systemd-run --on-active` transient
  timer that re-points the symlink back on failure (the host has no
  `at`; systemd transient timers are the verified mechanism).

## 7. Testing

- Unit tests for every `/proc` parser against fixtures (no dependence on
  host state).
- Store tests: migration idempotency, readiness, audit round-trip on a
  temp database.
- Handler tests: status codes and JSON shapes for the full frozen
  surface, 405/404 semantics (real Linux collectors in the sandbox/CI).
- CI (`ci.yml`): gofmt, `go vet`, `go test`, `linux/amd64` cross-build;
  `svelte-check` + static dashboard build.

## 8. Deliberately absent in Sprint 1

- TLS / public exposure (stays loopback + SSH tunnel).
- Authentication/authorization on the API and audit of requests
  (arrives with identity work in later sprints).
- Update/rollback machinery, installer, onboarding (Phase 1).
- Automatic deployment (gated on the rollback drill).
- Docker, Nginx, or any privileged helper (excluded by decision).
