# Drevanta — self-managed VPS platform

Drevanta is a Linux VPS self-management platform: a single hardened Go
daemon (`drevantad`) with an embedded, private web console. It is designed
for small (512 MB-class) VPS instances and follows a **private-first**
security model: nothing is exposed to the public internet — the daemon
binds to loopback only and is reached through an SSH tunnel.

**Status:** Sprint 2 — secure installer + first-owner onboarding. A host
is set up with one verified command; the first person to present the
single-use claim token becomes the administrator through a browser
wizard (credentials, TOTP MFA with recovery codes, or an explicit,
audited MFA skip). Sprint 1's daemon, API, dashboard, SQLite audit store,
hardened systemd service, CI, and deployment are unchanged in spirit and
preserved.

## Security model (Sprint 1)

- **Loopback-only:** `drevantad` listens on `127.0.0.1:8080` by default.
  There is no public port, no TLS terminator, no reverse proxy.
- **Unprivileged:** the service runs as the dedicated `drevanta` system
  user with an empty capability bounding set and a strict systemd
  sandbox (`ProtectSystem=strict`, `NoNewPrivileges`, `PrivateTmp`, …).
- **Private repo, private dashboard:** the console is only reachable by
  operators who already have SSH access to the host.
- **No secrets in the repository:** credentials live outside the work
  tree and are never committed. CI never receives host credentials.
- **No auto-deploy:** pushes never touch the VPS. Deployment is a manual,
  operator-run procedure (see `deploy/DEPLOY.md`) until the rollback
  drill is approved and passes.

## Quick start (build + run locally)

Requirements: Go ≥ 1.23, Node ≥ 20, npm.

```sh
make build      # builds web/build (SvelteKit static) then bin/drevantad
make test       # Go unit tests
make vet        # gofmt + go vet
make run        # dev run: 127.0.0.1:8080, state in /tmp/drevanta-dev
```

Then open `http://127.0.0.1:8080/` — a fresh instance redirects you to
the onboarding wizard. Run `bin/drevantad claim init --data-dir
/tmp/drevanta-dev` once to get a claim token (printed once).

## Install on a server (one command)

```sh
make cross-linux-amd64                 # dist/drevantad-linux-amd64
sudo deploy/install.sh                 # preflight + install + start + verify
sudo deploy/install.sh preflight       # checks only, changes nothing
```

The installer prints a masked, single-use, expiring claim token and can
also store it root-only (`--token-file`); full details and the rollback
drill are in `deploy/DEPLOY.md`. Re-running the installer is always safe
(idempotent; upgrades roll back automatically if `/readyz` fails).

## Accessing the deployed dev instance

The daemon is loopback-only on the VPS. From your workstation:

```sh
ssh -N -L 18080:127.0.0.1:8080 root@95.211.164.33
# then open http://127.0.0.1:18080/
```

## API (frozen for Sprint 1)

| Endpoint                   | Method | Purpose                                             |
| -------------------------- | ------ | --------------------------------------------------- |
| `/healthz`                 | GET    | Liveness: process is up.                            |
| `/readyz`                  | GET    | Readiness: store reachable, migrations applied.     |
| `/api/v1/server/metrics`   | GET    | On-demand CPU / RAM / disk / load / uptime snapshot.|
| `/`                        | GET    | Embedded dashboard (static SvelteKit build).        |

Unknown `/api/*` paths return `404` JSON — the API namespace is strict.

Example metrics response (fields abbreviated):

```json
{
  "timestamp": "2026-11-10T11:48:59Z",
  "cpu":  { "cores": 4, "usage_percent": 12.3, "load_1m": 0.1, "load_5m": 0.05, "load_15m": 0.02 },
  "memory": { "total_bytes": 6239023513, "used_bytes": 604565504, "used_percent": 13.9 },
  "disk": [ { "path": "/", "total_bytes": 99999999999, "used_percent": 4.1 } ],
  "uptime_seconds": 123456.78
}
```

Metrics are collected **on demand** from `/proc` and `statfs(2)`. There is
no background sampling loop; each request performs exactly one sampling
pass (the first request takes two samples 250 ms apart).

## Repository layout

```
cmd/drevantad/       daemon entry point (flags, lifecycle, graceful shutdown)
internal/httpapi/    frozen API surface + middleware (recovery, logging)
internal/metrics/    on-demand /proc + statfs collectors (pure stdlib)
internal/store/      SQLite (modernc.org/sqlite, no CGO): migrations + audit log
web/                 SvelteKit dashboard (adapter-static) + go:embed package
deploy/              systemd unit + deployment runbook
docs/                as-built architecture, benchmarks
```

## Storage

SQLite database at `/var/lib/drevanta/drevanta.db` (WAL mode) with:

- `schema_migrations` — applied schema versions,
- `audit_events` — append-only lifecycle audit log (daemon start/stop).

## Operations

- Service: `systemctl status drevanta`
- Logs: `journalctl -u drevanta -f` (JSON lines to stderr)
- Memory ceiling: `MemoryMax=256M` in the unit file
- Security review: `systemd-analyze security drevanta`

See `docs/ARCHITECTURE.md` for the as-built design and
`docs/BENCHMARKS.md` for measured memory consumption on the target-class
VPS.
