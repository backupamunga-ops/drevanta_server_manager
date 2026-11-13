# Phase 2 plan — Complete Linux Administration

Source of requirements: [phase-2/PHASE2_PRD.md](phase-2/PHASE2_PRD.md)
(Phase 2 PRD, now **v1.1** — the PRD lives in the repo with the
2026-11-13 identity decisions applied; the original upload stays
untouched). This plan maps the PRD onto Drevanta's actual names,
versions and milestones. The PRD's `platformd` / `platform-helper`
names are illustrative — per its §30 we keep Phase 1 naming and grow
incrementally.

## Versioning and what already shipped

Versioning is `0.<sprint>.<stage>`. Phase 1 = sprint 4 (0.4.x). Phase 2
starts at **0.5.0**.

- **0.4.19 — proper Settings shell (shipped before 0.5.0).** The
  temporary single-scroll `/settings` page is replaced by a sectioned
  shell (Account / Security / Mail delivery / Access) with deep links
  (`?section=…`), split into per-section components, all on shared
  primitives. Design rules codified in
  [DESIGN_PRINCIPLES.md](DESIGN_PRINCIPLES.md) — every Phase 2 page
  follows them.
- Phase 1 assets Phase 2 builds on: hardened sessions + MFA + recovery
  paths, audit log (`audit_events`), metrics collectors, SQLite
  migrations, nightly panel backup (`internal/backup`), httpsd
  (domain/cert management), embedded SvelteKit console.

## Milestones

| Milestone | Version | Deliver (PRD §25) | Drevanta-specific shape |
|---|---|---|---|
| P2.0 identity, personas, workspaces | **0.5.0 — shipped** | Workspace types (`server`, `hosting_org`, `website`, `developer_project`); memberships; role/permission registry (§6.2 namespace + documented `hosting.org.create` extension); workspace selector in the header; invitations (with account creation on acceptance); scope-aware API middleware (`403 workspace_mismatch` outside the server context); dormant support-grant schema; cross-tenant tests | Implemented per [phase-2/P2.0-identity-workspaces.md](phase-2/P2.0-identity-workspaces.md) (OD-001 decided 2026-11-13): migration 0005 with registry seed + server-workspace wrap; `internal/workspaces` + store `Can()` re-checked per request; workspace API; header selector + context banner; `/org/{id}` member management with honest foundation notes; `/invite` acceptance. Non-membership answers 404 (no existence leak); last-owner revocation guard |
| P2.1 observability + resource signals | **0.5.1** | Server overview; metric history with bounded retention + downsampling; process list; service health; alerts; first preflight API | Extend `internal/metrics/` (no new daemons); new `metric_samples`/`health_alerts` tables with retention budgets; read-only role access verified |
| P2.2 privileged helper, services, logs, files | **0.5.2** | Socket-activated root helper; strict protocol v1 (§19, 16 KiB frames); root-owned policy; approved systemd actions; journal viewer; restricted file manager; durable jobs | New `cmd/drevanta-helper/` + `internal/privilege/` (protocol/policy/client) and `internal/jobs/`; unit allowlist protects `drevantad`, `drevanta-helper`, `ssh`; file roots configured, never `/` |
| P2.3 security administration | **0.5.3** | nftables management; SSH settings/keys; Linux users & groups; independent rollback for remote-access changes; high-risk confirmations | Typed helper operations only (`firewall.apply`, `ssh.config.apply`, `user.create/disable`); validation before activation; recovery CLI; provider-console guidance in the UI |
| P2.4 maintenance, scheduling, updates, backups | **0.5.4** | Approved task templates (systemd timers); package update inventory + controlled upgrades; reboot workflow; panel/config backups with manifests; restore tooling | Extend `internal/backup/`; serialize package jobs; disk/RAM preflight via P2.1 signals; no arbitrary apt flags, no cron-as-root-runner |
| P2.5 resource intelligence + hardening + beta | **0.5.5 → 0.6.0-beta.1** | Cross-module preflights; pressure-aware job scheduling; security regression suite; 512 MB / 1 GB soak tests; accessibility pass; measured footprint published | Publish idle/peak RSS + disk on the dev VPS; release checklist (PRD §31) walked end to end |

Read-only first, then controlled mutations, then high-risk operations
(PRD §25.2). A milestone ships only when its exit gate passes; dates
are deliberately not promised (PRD §0).

## Engineering docs required before each milestone (PRD §30)

The RFC for each milestone lands in `docs/phase-2/` in the release that
starts it: monitoring RFC (0.5.1), helper protocol RFC (0.5.2),
remote-access safety RFC (0.5.3), maintenance RFC (0.5.4),
performance/security test plan (0.5.5). The identity/workspace RFC has
landed early as [phase-2/P2.0-identity-workspaces.md](phase-2/P2.0-identity-workspaces.md)
(finalized 2026-11-13).

## Open decisions that block or shape work

- **Resolved (OD-001, 2026-11-13):** a reseller does NOT switch into a
  website-owner persona; they manage customer relationships and
  authorized sites inside the hosting workspace, and a website context
  requires an explicit membership or audited support grant. Full model
  and ground rules:
  [phase-2/P2.0-identity-workspaces.md](phase-2/P2.0-identity-workspaces.md).
- **OD-002:** support access = time-limited, reason-coded, audited
  grant; never credential sharing. Policy needed before 0.5.2.
- **OD-012:** admin file roots are explicit configured paths only;
  proposal in the 0.5.2 RFC (default candidates: `/etc/drevanta/`
  managed files, approved service configs).

## Non-negotiables carried from the PRD

Deny by default; a workspace switch never grants permissions; the
panel stays unprivileged and the helper never becomes a shell; no
unbounded operations; later-phase workspaces are honest placeholders;
audit records cover actor, workspace, target and decision.

## Where the UI rules live

All Phase 2 screens follow [DESIGN_PRINCIPLES.md](DESIGN_PRINCIPLES.md).
The 0.4.19 Settings shell is the template for sectioned pages; the
0.5.0 workspace selector extends the shared header, it does not fork
it.
