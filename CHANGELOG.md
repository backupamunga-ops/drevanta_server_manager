# Changelog

All notable changes to Drevanta are documented here.
Format follows Keep a Changelog; versioning is CalVer-ish per sprint
(`0.<sprint>.<stage>`).

## [0.3.2-onboard-ux] — 2026-11-10

Locked first-time installation UX: the claim token reaches the operator
as a ready-to-open setup link in the installer output — no hidden files,
no agent hand-off. Onboarding flow unchanged (design LOCKED).

### Added
- **Installer success screen** — after an interactive install the
  installer prints the first-time-setup instructions: the SSH tunnel
  command (real server address/username when the session provides them
  via `SSH_CONNECTION`/`SUDO_USER`, clearly labelled `<placeholders>`
  otherwise), the one-time setup link
  `http://127.0.0.1:18080/onboarding#token=…`, and the raw token as a
  fallback. Default claim-token TTL is now **30 minutes** (installer
  `--ttl` / `drevantad claim init --ttl`).
- **Setup-link prefill** — the onboarding page reads the token from the
  URL fragment, fills the claim form, and immediately erases the fragment
  from the address bar and the current history entry
  (`history.replaceState`). Fragments never reach the server; the token
  is read back via the Performance Navigation entry when the client
  router replaces the URL before hydration. No analytics or external
  scripts exist in the dashboard (`app.html` is dependency-free).
- **`claim init` setup screen** — the CLI prints the same ready-to-open
  setup link (raw token still line 1 of stdout, machine-parseable; screen
  on stderr), so regeneration on the VPS produces the full UX.
- **Installer token-delivery policy** — interactive sessions get the
  screen; noninteractive runs (no TTY) never print the token and deliver
  it via a root-owned 0600 file (`--token-file`, `$DREVANTA_TOKEN_FILE`,
  or `/root/.drevanta-claim-token`). A re-run never rotates a live token
  (`claim_token_active` is honoured); `DREVANTA_INTERACTIVE=1|0` is a
  documented test hook.

### Security
- `store.CreateClaimTokenIfUnclaimed` — authoritative first-owner guard:
  claim tokens can never be issued or regenerated once the instance is
  claimed (`ErrAlreadyClaimed`).
- Plaintext tokens appear only in the operator's own terminal; CI output,
  systemd logs, and noninteractive captures never see them (harness
  asserts both directions).

### Tests
- Installer harness: 49 → 80 assertions — interactive success screen,
  real-vs-placeholder connection info, noninteractive file delivery
  (0600, no token in output), no-reissue with a live token, `claim init`
  screen/rotation via the real binary.
- Go: `TestClaimTokenRegeneration` (rotate → old rejected → new claims →
  replay 409), `TestCreateClaimTokenIfUnclaimed` (guard before/after
  claim).
- Web: `node --test` unit tests for fragment parsing, navigation-entry
  fallback, and clean-URL building (8 tests); wired into CI.
- Dashboard handler: `/onboarding` and `/login` serve their prerendered
  pages directly (previously the SPA fallback answered for them —
  functionally resolved by the client router, but the raw HTML was the
  wrong page). Go tests pin the mapping and the unknown-route fallback.

## [0.2.2-sprint2] — 2026-11-10

Secure installer and first-owner onboarding. The product is now
installable with one verified command and becomes a single-administrator
system through a browser wizard.

### Added
- **`deploy/install.sh`** — one-command, verified installer: preflight
  checks (root, systemd, OS, x86_64, RAM, disk, tools, port conflicts,
  service conflicts, existing-install detection), idempotent release
  layout (`/opt/drevanta/releases/<version>/` + atomic `current`
  symlink), hardened systemd unit generation, `/readyz` health gate with
  automatic symlink rollback on failed readiness, and single-use expiring
  first-owner claim token issuance. `preflight` subcommand runs all
  checks without changing anything; `--uninstall [--purge]` reverses.
- **First-owner claim flow** — `drevantad claim init|status|revoke`
  (runs as the service user): 256-bit CSPRNG token (`drv1_…`), stored
  only as SHA-256, single-use, expiring (default 24 h, rotation revokes
  the previous token), printed exactly once.
- **Identity & sessions** — argon2id password hashing (OWASP minimum
  parameters), server-side sessions (HttpOnly cookie, SameSite=Lax,
  SHA-256 at rest, 24 h sliding / 7 d absolute), per-IP fixed-window rate
  limiting on unauthenticated endpoints, same-origin guard on POSTs,
  bounded argon2id concurrency (2 slots) as a memory-safety backstop.
- **TOTP MFA (RFC 6238)** — standard SHA-1/6-digit/30 s profile, ±1 step
  drift, QR + manual provisioning, and 10 single-use recovery codes
  (shown once, hashed at rest) usable at sign-in when the authenticator
  is unavailable. MFA may be skipped only with an explicit risk
  acknowledgement; the choice is audited and permanent for the sprint.
- **Onboarding state machine** — `unclaimed → mfa_pending → active`
  (singleton `instance_state` row); first-owner semantics enforced
  server-side; every transition and rejection audited.
- **Dashboard wizard** — `/onboarding` (claim → account → MFA setup with
  QR → recovery codes → done) and `/login` (password + optional
  TOTP/recovery second step); console gates on session scope and shows
  MFA state with sign-out. New `qrcode` dependency (client-side SVG).
- **New API surface** (loopback-only, `/api/v1/…`): `onboarding/status`,
  `onboarding/claim`, `onboarding/mfa/{setup,verify,skip}`,
  `auth/{login,logout,session}`. `/api/v1/server/metrics` now REQUIRES a
  full-scope session (401/403 otherwise); `/healthz` + `/readyz` stay
  open by design.
- **Rollback machinery** — `deploy/rollback.sh` (symlink flip + restart +
  readiness verify) and `deploy/healthgate.sh` (delayed decision runner
  for `systemd-run --on-active` transient timers, per the locked design).
- **Installer test harness** (`deploy/tests/test_install.sh`, 49
  assertions): preflight pass/fail paths, fresh install, token issuance
  (masked stdout, 0600 file, real hashed token in SQLite), idempotent
  re-run, upgrade, auto-rollback on broken release, rollback failure
  path, health-gate decisions, uninstall.
- **Sprint 2 deployment evidence** — installer run on the development
  VPS (fresh install + upgrade + idempotent no-op), scripted e2e
  onboarding journey over an SSH tunnel (29 checks), full browser
  onboarding journey verified locally, and the **rollback drill passed**
  (broken release auto-rolled-back by the transient-timer gate in ≤45 s).
- cgroup-vs-RSS accounting investigation in `docs/BENCHMARKS.md` (the
  Sprint 1 discrepancy is explained and measured).

### Changed
- `drevantad` gains subcommands (`serve` default, `claim`, `version`);
  Sprint 1 bare-flag invocation still works.
- Session/auth state survives upgrades (SQLite `instance_state`,
  `users`, `sessions`); release layout is now `releases/<version>/` with
  a `current` symlink; legacy flat installs are preserved as a
  `0.1.0-legacy` release automatically.
- TOTP codes may be re-presented within their 30 s window (mainstream
  behaviour) — a stricter single-use-per-step guard was tried and
  rejected because it locks operators out of quick re-logins.

### Security
- Metrics endpoint is no longer anonymous.
- Claim tokens, session cookies, and recovery codes are stored hashed
  only; the claim token is shown once and never logged; rejected claims
  return precise reasons (invalid/expired/used) only while the instance
  is still unclaimed (no post-claim oracle).
- Password hashes are argon2id (`m=19 MiB, t=2, p=1`); concurrent
  verifications are bounded to cap transient memory.

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
