# Drevanta architecture — as built (Sprint 2)

This document describes what is actually implemented after Sprint 2
(secure installer + first-owner onboarding). Sprint 1's walking skeleton
is preserved unchanged where it applied (daemon core, metrics, audit,
systemd hardening); this document adds identity, onboarding, the
installer, and the release/rollback machinery.
Section 1 records Sprint 1 itself.

## 1. Sprint 1 — the walking skeleton (as built)

Sprint 1 (release `0.1.0-sprint1`, 2026-11-10) laid the foundation
everything above builds on. Its goal in one line: *the daemon exists,
is deployed, and is observable*.

**Delivered**

- **Daemon core** (`cmd/drevantad/`, `internal/httpapi/`) — frozen API
  surface (`GET /healthz`, `GET /readyz`,
  `GET /api/v1/server/metrics`), loopback-only binding
  (`127.0.0.1:8080`), graceful shutdown, JSON logs to stderr.
- **On-demand metrics** (`internal/metrics/`) — every request performs
  one `/proc` + `statfs(2)` pass; no background sampling loops; parsers
  unit-tested against fixtures, never against host state.
- **SQLite store** (`internal/store/`) — `modernc.org/sqlite` (pure Go,
  no CGO), WAL mode, transactional idempotent migrations
  (`schema_migrations`), and the append-only `audit_events` lifecycle
  log (migration v1).
- **Embedded dashboard** (`web/`) — SvelteKit 2 / Svelte 5 static
  export, TypeScript, no CDN assets: live CPU sparkline, memory/disk
  bars, uptime, healthz/readyz pills, 5 s polling with pause; embedded
  into the binary with `go:embed`.
- **Hardened systemd unit** (`deploy/drevanta.service`) — unprivileged
  `drevanta` user, `ProtectSystem=strict`, empty capability set,
  `MemoryMax=256M`, restart-on-failure; the full posture (including the
  later sandbox additions) is tabulated in §6.
- **CI** (`ci.yml`) — gofmt/vet/test + `linux/amd64` cross-build;
  `svelte-check` + static dashboard build. No deploy job by design.
- **Runbook and evidence** — manual deployment runbook
  (`deploy/DEPLOY.md`), as-built architecture notes (this file), and a
  memory benchmark of the deployed daemon on the development VPS
  (`docs/BENCHMARKS.md`).

**Security posture carried from day one**

- No public ports: the daemon binds `127.0.0.1:8080` only; operators
  reach it through an SSH tunnel.
- No secrets in the repository; CI receives no host credentials.
- Strict `/api/*` namespace — unknown API paths answer 404 JSON, no
  SPA leak.

**What later sprints changed on top of the skeleton** — metrics is no
longer anonymous (a full-scope session is required since Sprint 2); the
daemon gained subcommands (`serve`, `claim`, `version` — the Sprint 1
bare-flag invocation still works); the flat binary layout became
`releases/<version>/` with a `current` symlink (legacy flat installs
are preserved automatically as `0.1.0-legacy`); identity, onboarding
and public HTTPS were added around the same frozen core.

---

## 2. Overview

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

## 3. Components

| Component            | Location                | Notes                                                             |
| -------------------- | ----------------------- | ----------------------------------------------------------------- |
| Daemon entry point   | `cmd/drevantad/`        | Subcommands `serve` (default) / `claim init\|status\|revoke` / `version`; signal handling, graceful shutdown. |
| HTTP API             | `internal/httpapi/`     | API surface (§4), session middleware, origin guard, panic recovery, error-focused logging. |
| Auth primitives      | `internal/auth/`        | argon2id, tokens, RFC 6238 TOTP, recovery codes, rate limiter — pure stdlib + `golang.org/x/crypto`. |
| Metrics collectors   | `internal/metrics/`     | `/proc/stat`, `/proc/meminfo`, `/proc/loadavg`, `/proc/uptime`, `statfs(2)`; pure stdlib; unit-tested against fixtures. |
| Store                | `internal/store/`       | modernc.org/sqlite; migrations v1–v2; identity + audit queries; single connection. |
| Dashboard            | `web/` (Go pkg `web`)   | SvelteKit 2 + Svelte 5, TypeScript, adapter-static; embedded via `go:embed all:build`; onboarding wizard + login + console. |
| Installer / rollback | `deploy/`               | `install.sh` (preflight + idempotent install + health gate), `rollback.sh`, `healthgate.sh`, `tests/test_install.sh` (49 assertions). |
| Service definition   | generated by installer  | Same hardening posture as Sprint 1 (`MemoryMax=256M`, unprivileged, sandboxed). |
| Public HTTPS proxy   | `internal/httpsd/`      | Separate unprivileged unit (`drevanta-https`, CAP_NET_BIND_SERVICE only): lego ACME client, IP identifier (RFC 8738) + optional custom domain as an independent SECOND certificate slot (SNI-routed; the IP slot can never be displaced by a domain operation), hourly renewal, strict trusted-proxy boundary, secret-free `status.json`. Optional domain via a daemon-writable, proxy-readable overlay (`/var/lib/drevanta-access/domain.json`). |

Why `go:embed` from `web/`: the embed directive is package-relative, so
the embed package lives beside `web/build`; `npm run build` output is
embedded directly with no copy step. A committed `.gitkeep` keeps
`go build` working before the first dashboard build.

## 4. API contract

### 4.1 Unauthenticated (by design)

| Endpoint   | Method | Success                            | Failure                      |
| ---------- | ------ | ---------------------------------- | ---------------------------- |
| `/healthz` | GET    | 200 `{"status":"ok","version":…}`  | —                            |
| `/readyz`  | GET    | 200 `{"status":"ready"}`           | 503 `{"status":"not_ready"}` |
| `/`        | GET    | 200 embedded dashboard             | SPA fallback to `index.html` |

`/healthz` and `/readyz` stay open for systemd/monitoring and reveal
nothing. The dashboard shell (HTML/JS) is static content.

### 4.2 Identity and onboarding (Sprint 2)

| Endpoint                       | Method | Auth             | Notes                                             |
| ------------------------------ | ------ | ---------------- | ------------------------------------------------- |
| `/api/v1/onboarding/status`    | GET    | public           | instance state, `claim_token_active` flag          |
| `/api/v1/onboarding/claim`     | POST   | claim token      | creates the first administrator; single-use token  |
| `/api/v1/onboarding/mfa/setup` | POST   | session (onb.)   | TOTP secret + `otpauth://` URI                     |
| `/api/v1/onboarding/mfa/verify`| POST   | session (onb.)   | confirms TOTP, returns 10 recovery codes           |
| `/api/v1/onboarding/mfa/skip`  | POST   | session (onb.)   | "Skip for now": completes onboarding without MFA; requires `acknowledge_risk:true`, audited |
| `/api/v1/access/status`        | GET    | public           | trusted access mode from the root-written config + proxy status file (secret-free) |
| `/api/v1/access/domain/check`  | POST   | session (full)   | hostname shape + DNS A/AAAA must point at this VPS |
| `/api/v1/access/domain/apply`  | POST   | session (full)   | DNS verified → overlay → cert issued → route verified; any failure rolls the overlay back, IP HTTPS untouched |
| `/api/v1/access/domain/remove` | POST   | session (full)   | clears the domain slot (`confirm:true` required)   |
| `/api/v1/account/mfa`          | GET    | session (full)   | MFA posture + unused recovery-code count           |
| `/api/v1/account/mfa/setup`    | POST   | session (full)   | begin TOTP enrolment later (Account Security)      |
| `/api/v1/account/mfa/verify`   | POST   | session (full)   | enable MFA later; fresh 10 recovery codes are mandatory |
| `/api/v1/auth/login`           | POST   | credentials      | password + TOTP or recovery code when MFA is on    |
| `/api/v1/auth/logout`          | POST   | any session      | deletes the server-side session                    |
| `/api/v1/auth/session`         | GET    | public           | current session/instance summary                   |
| `/api/v1/server/metrics`       | GET    | session (full)   | 401 anonymous / 403 onboarding-scope               |

- Unknown `/api/*` paths: 404 JSON (strict API namespace).
- Wrong method: 405 (Go 1.22+ method-aware mux).
- State-changing (POST) requests are rejected when a browser `Origin`/
  `Referer` header is present and mismatches the request host (CSRF
  guard, combined with `SameSite=Lax` cookies).
- Unauthenticated endpoints (`login`, `claim`, `mfa/verify`) are rate
  limited to 10 attempts/min per source address (429 + `Retry-After`).

### 4.3 Metrics snapshot shape (unchanged)

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

## 5. Storage, identity, and audit

- File: `/var/lib/drevanta/drevanta.db`, WAL mode, `busy_timeout=10s`,
  `foreign_keys=on`, single connection per process.
- `schema_migrations(version, name, applied_at)` — append-only list;
  migrations are applied transactionally on every start, idempotent.
  - **v1** `initial_audit_schema` — `audit_events`.
  - **v2** `identity_onboarding_schema` — `users`, `instance_state`
    (singleton `unclaimed → mfa_pending → active` row),
    `claim_tokens`, `sessions`, `recovery_codes`.
- `audit_events(id, ts, actor, action, target, detail)` with indexes on
  `ts` and `action`. Lifecycle events (`daemon_started`/`daemon_stopped`)
  plus every identity-relevant event: `claim_token_created/revoked`,
  `claim_succeeded/rejected`, `admin_created`, `mfa_setup_started`,
  `mfa_enabled` (onboarding or Account Security), `mfa_verify_failed`,
  `mfa_skipped` (explicit risk acknowledgement), `onboarding_completed`,
  `login_success/failed` (with reason, never secret values),
  `recovery_code_used`, `logout`, `domain_applied/apply_failed/removed`.
  MFA is optional ("Skip for now" with explicit acknowledgement) since
  0.4.5; enrolled administrators keep every protection. Metrics polling
  is still deliberately
  not audited (5 s polling would flood the log without adding
  accountability).

### 5.1 Identity model

- **First-owner claim** — the installer (or an operator via
  `drevantad claim init`) issues a 256-bit CSPRNG token (`drv1_…`) that
  exists only as a SHA-256 hash in `claim_tokens`. It is single-use,
  expires (default 24 h), and rotation revokes the previous token. While
  the instance is `unclaimed`, a rejected claim returns a precise reason
  (`invalid_token` / `token_expired` / `token_used`); after the claim the
  endpoint answers `409 wrong_state` for everything (no validity oracle).
- **Passwords** — argon2id (`m=19 MiB, t=2, p=1`, OWASP minimum),
  PHC-formatted; verification parameters are read from the stored hash.
  Concurrent verifications are bounded to 2 (each allocates a ~19 MiB
  workspace; the bound caps transient memory for the 512 MB-class box).
- **Sessions** — 32-byte cookie values, SHA-256 at rest, HttpOnly,
  `SameSite=Lax`, 24 h sliding expiry with a 7 d absolute cap, two scopes:
  `onboarding` (created by the claim; may only finish MFA setup) and
  `full` (everything). Expired sessions are pruned on lookup, at login,
  and at start-up.
- **TOTP MFA** — RFC 6238, SHA-1/6-digit/30 s, ±1 step drift. The secret
  is provisioned at `mfa/setup` (QR + manual), stored base32, and only
  marked enabled after a successful `mfa/verify`. Codes may be re-presented
  within their 30 s window (mainstream behaviour; a stricter per-step
  single-use guard was tried and rejected — it locks operators out of
  quick re-logins without practical security gain).
- **Recovery codes** — 10 × 8-char codes from a typo-resistant 30-char
  alphabet (~38 bits each), shown exactly once, stored as SHA-256 hashes,
  single-use at login, consumption audited.
- **MFA skip ("Skip for now")** — allowed only while `mfa_pending` and
  only with `acknowledge_risk: true`; audited (`mfa_skipped`);
  completes onboarding with `mfa_mode:"skipped"`, upgrades the session
  to full scope, and the console shows an `mfa off` pill that links to
  Account Security, where MFA can be enabled later (recovery codes are
  always issued when MFA is enabled). Enrolled accounts are unaffected:
  their login still requires TOTP or a recovery code.

## 6. Service hardening (systemd)

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

## 7. Installer, release layout, rollback

### 7.1 Installer (`deploy/install.sh`)

One command, verified and idempotent:

- **Preflight** (also `install.sh preflight`, changes nothing): root,
  systemd running, Ubuntu/Debian (warns on other Linux), `x86_64`,
  binary runs and reports a version, RAM ≥ 256 MiB (warn < 512), free
  disk ≥ 256 MiB under `/opt` and `/var/lib` (warn < 512), required
  tools, port free (or held by our own service = upgrade), unit
  conflicts, service-user presence.
- **Layout** — `/opt/drevanta/releases/<version>/drevantad` (immutable:
  same version with a different checksum is refused) + atomic
  `/opt/drevanta/current` symlink flip (`ln` to temp name + `mv -T`).
  A pre-Sprint-2 flat `/opt/drevanta/drevantad` is preserved
  automatically as release `0.1.0-legacy`.
- **Unit** — regenerated deterministically (rewritten only when the
  content differs), `ExecStart=/opt/drevanta/current/drevantad serve …`.
- **Health gate** — after (re)start the installer polls `/readyz` for
  10 s; on failure it flips the symlink back to the previous release,
  restarts, verifies, and reports the journal excerpt (safe failure).
  A no-op re-run (same release, same unit, service active) does not
  restart the daemon.
- **Claim token** — for a fresh (unclaimed) instance the installer runs
  `claim init` as the service user and prints the token masked to stdout
  (optionally also to `--token-file`, mode 0600). A claimed instance is
  never re-issued a token.

### 7.2 Rollback machinery

- `rollback.sh [release]` — flips `current` to the named (or previous)
  release, restarts, verifies `/readyz`, prints a machine-readable
  `result=ready|not_ready` line.
- `healthgate.sh <new> <prev>` — the delayed decision runner for
  unattended upgrades: if `current` is still `<new>` and `/readyz` fails,
  it runs `rollback.sh <prev>`; otherwise it does nothing. Scheduled via
  the locked design's `systemd-run --on-active=<delay>` transient timer.
- **Rollback drill (passed 2026-11-10, dev VPS):** a deliberately broken
  release (`9.9.9-drill`) was activated; the transient gate
  (`--on-active=45s`) detected the failing `/readyz`, rolled back to
  `0.2.1-sprint2`, and the service was `ready` again the same second the
  gate fired. Full log in `deploy/DEPLOY.md`.
- **Automatic deployment on push remains disabled** (engineering order);
  CI builds/tests only and never receives host credentials.

## 8. Testing

- Unit tests for every `/proc` parser against fixtures (no dependence on
  host state).
- Store tests: migration idempotency, readiness, audit round-trip on a
  temp database.
- Handler tests: status codes and JSON shapes for the full frozen
  surface, 405/404 semantics (real Linux collectors in the sandbox/CI).
- CI (`ci.yml`): gofmt, `go vet`, `go test`, `linux/amd64` cross-build;
  `svelte-check` + static dashboard build.

## 9. Deliberately absent after Sprint 2

- TLS / public exposure (stays loopback + SSH tunnel).
- Multiple administrators, password change/reset flows, MFA re-enrolment
  or recovery-code regeneration (next identity work).
- Automatic deployment on push (gated on engineering order; the rollback
  machinery and drill are in place).
- Docker, Nginx, or any privileged helper (excluded by decision).
