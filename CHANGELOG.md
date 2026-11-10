# Changelog

All notable changes to Drevanta are documented here.
Format follows Keep a Changelog; versioning is CalVer-ish per sprint
(`0.<sprint>.<stage>`).

## [0.1.0-sprint1] — 2026-11-10

First walking skeleton: the daemon exists, is deployed, and is observable.

### Added
- `drevantad` Go daemon: frozen API surface (`GET /healthz`,
  `GET /readyz`, `GET /api/v1/server/metrics`), loopback-only binding,
  graceful shutdown, JSON logs to stderr.
- On-demand metrics collectors (`/proc/stat`, `/proc/meminfo`,
  `/proc/loadavg`, `/proc/uptime`, `statfs(2)`) — no background loops.
- SQLite store (`modernc.org/sqlite`, no CGO): `schema_migrations` +
  append-only `audit_events` lifecycle log, transactional idempotent
  migrations, WAL mode.
- Embedded SvelteKit 2 / Svelte 5 dashboard (static export, TypeScript,
  no CDN assets): live CPU sparkline, memory/disk bars, uptime,
  healthz/readyz pills, 5 s polling with pause.
- Hardened systemd unit (`deploy/drevanta.service`): unprivileged
  `drevanta` user, `ProtectSystem=strict`, empty capability set,
  `MemoryMax=256M`, restart-on-failure.
- GitHub Actions CI: gofmt/vet/test + linux/amd64 cross-build;
  svelte-check + static dashboard build. No deploy job by design.
- Manual deployment runbook (`deploy/DEPLOY.md`) and as-built
  architecture notes (`docs/ARCHITECTURE.md`).
- Memory benchmark of the deployed daemon on the development VPS
  (`docs/BENCHMARKS.md`).

### Security
- No public ports: daemon binds `127.0.0.1:8080` only; SSH-tunnel access.
- No secrets in the repository; CI receives no host credentials.
- Strict `/api/*` namespace (unknown API paths → 404 JSON, no SPA leak).
