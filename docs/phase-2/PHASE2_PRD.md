# Phase 2 — Complete Linux Administration, Personas, Workspaces & Resource Intelligence

**Product:** Lightweight All-in-One Server Management Platform  
**Document type:** Product Requirements Document (PRD)  
**Version:** 1.1 — P2.0 identity model finalized (switching matrix, agency management mode, hosting_org business types)  
**Date:** 2026-11-13  
**Status:** Design specification; not an implementation or security certification  
**Depends on:** Phase 1 Installer, Secure Onboarding & First Dashboard v1.2  
**Target environment:** Linux VPS, 512 MB RAM to 32 GB+; initial development VPS 1 vCPU / 1 GB RAM / 10 GB disk  
**Primary delivery:** One self-hosted Go management plane, SQLite, embedded static SvelteKit UI, optional future static Next.js modules

---

## 0. Document control and decision status

This PRD consolidates the agreed direction from planning discussions: **Phase 2 is the Linux server administration phase**, and it includes both the focused foundation and the broader Linux control-panel scope. It also incorporates the four product personas and their workspace switching. It is a product specification; detailed implementation designs, threat models, and tested performance data remain separate deliverables.

**Decision labels:**
- **Agreed direction:** Four personas; comprehensive Linux administration in Phase 2; security-first architecture; resource-aware lightweight core; workspace switching; no mandatory heavy workload services.
- **Confirmed (v1.1):** The switching matrix and identity model of §5 — four personas, four workspace types, multiple memberships per user, strictly scoped permissions; a reseller manages authorized customer websites from the Hosting workspace (agency management mode); Website Owner context requires separate membership or an audited support grant; `hosting_org` uses a `business_type` attribute instead of three persona systems. Specified in `docs/phase-2/P2.0-identity-workspaces.md`.
- **Proposed default (still needs testing/review):** Role names, permission names, tenancy schema details, support-access model, milestone sequence, and numerical SLOs below.
- **Deferred:** Billing, actual customer hosting provisioning, website runtime controls, Git deployment, Docker, mail hosting, multi-server control plane, and complex plugin ecosystem.

**Decided (v1.1, supersedes the v1.0 conservative interpretation):** Persona switching and customer management are not the same thing. **Switching never grants privileges**: a user switches only between workspaces they are independently authorized to access (membership or active support grant), and no persona automatically grants another persona's permissions. **A reseller can open and manage authorized customer websites from within the Hosting workspace** — agency management mode covers explicitly delegated hosting-management tasks (an agency managing 50 websites needs no 50 website-owner accounts) — while a **Website Owner context requires separate membership or an audited support grant**. Recorded in §27 and specified in `docs/phase-2/P2.0-identity-workspaces.md`.

**Relationship to existing roadmap:** The original project blueprint described foundational metrics, systemd services, logs, restricted files, and audit, followed by hosting and deployment. This PRD deliberately expands Phase 2 to include firewall, SSH, users, packages, scheduling, backups, and resource intelligence. Consequently, original dates are superseded as estimates; there is no promised calendar completion date.

---

## 1. Executive summary

Phase 1 makes the platform installable and securely claimable. **Phase 2 makes it an operational Linux server administration product**: a person can inspect, troubleshoot, configure, secure, maintain, and recover an authorized Linux server through the dashboard without routinely needing SSH.

The platform will support four personas with one identity system: **Server Administrator**, **Reseller / Hosting Provider / Agency**, **Website Owner**, and **Developer**. Users may hold multiple memberships. A workspace selector changes the active context, available navigation, and resource scope; it must never elevate permissions by itself.

Phase 2 must implement real server administration features and the identity/tenant foundations needed for later hosting and deployment phases. It must **not pretend that customer websites, applications, hosting plans, billing, or Git deployment are functional before those later modules ship**.

The technical foundation remains an unprivileged Go daemon, SQLite, static frontend assets, a narrowly scoped privileged helper, native Linux interfaces, durable jobs, audit records, and measured resource budgets. No Docker, Redis, external database, Nginx, PHP, or Node.js server is required merely to administer the VPS.

### 1.1 Product promise

> One secure dashboard for the Linux server and the people who use it, with only the processes and permissions each task actually needs.

### 1.2 Phase 2 outcome

An authorized server administrator can:
1. Sign in and switch among their authorized workspaces.
2. Understand CPU, RAM, swap, disk, network, processes, service health, and resource pressure.
3. Manage approved systemd services and inspect logs.
4. Work with explicitly permitted files and configurations.
5. Safely manage firewall, SSH settings, keys, Linux users and groups.
6. Inspect and apply approved package updates, schedule jobs, and manage supported backups.
7. See clear confirmations, audit trails, recovery guidance, and resource-aware warnings.
8. Continue using the core on a constrained VPS without mandatory heavyweight dependencies.

---

## 2. Problem statement

Today, VPS administration often requires switching between SSH, systemctl, journalctl, firewall tools, package managers, editors, cron, backup scripts, and separate hosting/deployment dashboards. This creates repetitive work, inconsistent access control, poor visibility for customers and collaborators, and dangerous ad-hoc privilege escalation.

Existing server panels can also introduce a large privileged attack surface or a management footprint that is unsuitable for small servers. Our approach is not to assert that other products are insecure or to promise a particular memory figure without evidence; it is to **engineer and independently test** explicit privilege boundaries, tenant isolation, safe recovery, and low overhead.

### 2.1 User pains

- Server owners cannot easily see whether a workload is safe to start on a 512 MB–1 GB VPS.
- Operators need basic diagnosis without broad root access.
- Agencies need a clear organizational boundary between their customers and the Linux host.
- Website owners and developers should not see unrelated server controls or customer data.
- Firewall, SSH, and package changes can interrupt access or service availability.
- Lightweight servers cannot afford permanently running agents for every feature.
- Existing-service conflicts and failed operations need explainable, recoverable outcomes.

---

## 3. Goals, non-goals, and product principles

### 3.1 Goals

**G1 — Functional administration:** Cover monitoring, services, processes, logs, restricted files, firewall, SSH, users/groups, scheduling, packages, backups, and audit.

**G2 — Persona-aware UX:** Deliver one login, multiple scoped workspaces, role-based navigation, and clear active-context indicators.

**G3 — Strong authorization:** Deny unauthorized actions at the API and at privileged execution boundaries, including cross-tenant access.

**G4 — Safe mutations:** Validate, confirm, journal, and recover high-risk operations; protect remote access.

**G5 — Resource awareness:** Preflight expensive operations, bound concurrency and storage, and present actionable recommendations.

**G6 — Small core:** Preserve the Phase 1 Go/SQLite/static-assets architecture; avoid mandatory extra daemons.

**G7 — Extension readiness:** Expose reusable identity, workspace, job, permission, and resource models for later hosting/deployment modules.

### 3.2 Non-goals in Phase 2

- Creating live customer websites, Nginx vhosts, PHP pools, databases, DNS zones, or SSL certificates for hosted domains (Phase 3).
- Git-based deployments, build pipelines, application environment/secrets management, optional Docker control (Phase 4).
- Mail server provisioning, mailboxes, webmail, AI mail features (later phase).
- Payment processing, subscriptions, invoicing, reseller commerce, automatic plan enforcement (later phase).
- Full hypervisor management, VM/container orchestration, Kubernetes, or multi-server fleet orchestration.
- Arbitrary browser-based root shell or unrestricted file editing.
- Universal guaranteed recovery from kernel, network, storage, or provider failures.

### 3.3 Non-negotiable principles

1. **Identity is not authority:** Persona, role, membership, resource ownership, and permission are distinct.
2. **Deny by default:** No access without a valid membership and explicit permission.
3. **Host privilege is exceptional:** The panel process remains unprivileged.
4. **A switch is not an escalation:** Switching workspace never grants permissions.
5. **No unbounded operations:** Limit CPU, memory, disk, time, logs, and concurrency.
6. **Recoverability is part of UX:** Especially for firewall, SSH, packages, and restore.
7. **Real functionality, honest placeholders:** Later-phase workspaces may exist but must be clearly marked not yet enabled.
8. **No implicit customer-data access:** Server ownership does not imply silent access to private tenant content.

---

## 4. Personas and primary jobs-to-be-done

### 4.1 Server Administrator

**Who:** VPS owner, infrastructure operator, root-equivalent platform administrator, or trusted server maintainer.

**Jobs:** Monitor and troubleshoot the host; manage approved system services; configure network/security; administer OS users; update packages; schedule maintenance; back up/recover; manage memberships and workspace provisioning.

**Default landing:** Server Overview.

**Special controls:** High-risk changes, recovery, host policy, support-access initiation, security settings.

**Important boundary:** Even an owner should use explicit, auditable support mode to inspect another tenant's private website files or secrets; the system should not automatically disclose them in the normal workspace.

### 4.2 Reseller / Hosting Provider / Agency

**Who:** Organization managing customers and future hosting allocations.

**Jobs in Phase 2:** Create/manage their organization workspace, invite team members, manage delegated customer records and future resource relationships, review allowed high-level resource/health summaries, and prepare future hosting accounts.

**Default landing:** Hosting Organization Overview.

**Not yet available:** Provisioning websites, domains, plans, subscriptions, or billing until Phase 3/later.

**Important boundary:** No direct host firewall, SSH, Linux user, package, or privileged service access merely by being a reseller.

### 4.3 Website Owner

**Who:** Person responsible for one or more hosted sites.

**Jobs in Phase 2:** Accept membership, view site workspace identity and assigned-resource status, manage their own membership/profile where authorized, see future feature availability.

**Default landing:** Website Overview (foundation/placeholder until hosting is implemented).

**Not yet available:** Real site file management, domains, SSL, website backups, or web logs until hosting modules supply them. The Phase 2 **server file manager** is not automatically a website file manager.

### 4.4 Developer

**Who:** Person assigned to an application or development project.

**Jobs in Phase 2:** Access a scoped project workspace, view membership and future application metadata, prepare for deployment workflows.

**Default landing:** Developer Project Overview (foundation/placeholder until deployment is implemented).

**Not yet available:** Git, builds, application logs, deployments, app secrets, containers until Phase 4.

### 4.5 One person, multiple contexts

A single user may simultaneously be the server owner, an agency team member, a website owner, and a developer. Authentication occurs once. Each workspace membership is evaluated independently, and the UI clearly identifies the active workspace, role, and resource scope.

---

## 5. Persona hierarchy, switching, and delegation

### 5.1 Core model

**Personas are UX categories, not inherited security roles.** The platform uses four workspace types:

- `server`: a Linux host administration context.
- `hosting_org`: an agency/provider/reseller organization.
- `website`: an individual website/customer context.
- `developer_project`: an application/project context.

A `user` can hold one or more `membership` records, each with a workspace-scoped role. Parent-child workspace relationships define organizational ownership, not automatic access to private data.

**One hosting workspace type, three business types (decided v1.1).** A reseller, hosting provider and agency share the same `hosting_org` workspace interface; a `business_type` attribute (`reseller` | `hosting_provider` | `agency`) records the business model. This leaves room for billing, reseller quotas, customer management and white-labeling later without rebuilding authentication or authorization — no three-way fork of persona systems.

### 5.2 Switching matrix (decided v1.1)

| Current membership / persona | Server workspace | Hosting workspace | Website workspace | Developer workspace |
|---|---|---|---|---|
| Server Administrator | Own authorized servers | Own organization or granted hosting context | Explicit membership or audited support mode | Explicit membership or audited support mode |
| Reseller / Provider / Agency | No, unless separately assigned | Own authorized hosting organization(s) | No persona switch by default; manage customer relationship within hosting UI; explicit grant can add website membership | Only if separately assigned |
| Website Owner | No, unless separately assigned | No, unless separately assigned | Own assigned website(s) | Only if separately assigned |
| Developer | No, unless separately assigned | No, unless separately assigned | Only if separately assigned | Own assigned project(s) |

**Example:** A server administrator with hosting-org membership can switch from `Server A` to `Agency B`. They may see that Agency B has Customer C, but cannot silently browse Customer C's private files. A reseller can manage Customer C's organizational relationship without receiving Server A's root-level capabilities.

**Management relationships, not permission inheritance (decided v1.1).** Server Administrator → Hosting → Website/Developer are *management* relationships: organizational ownership and explicitly delegated tasks, never unrestricted permission inheritance. In the matrix, every cell is an owned/authorized context, an explicit membership, or an audited support grant — never an automatic persona-to-persona grant. The "*" cells resolve only when the same user independently receives membership in that workspace.

### 5.3 Workspace selector rules

- Display only workspaces for which the user has a valid membership or active support grant.
- Group by persona/type; show workspace name, server/organization context, role, and status.
- Preserve last used workspace only when still authorized; otherwise choose a safe default.
- Show an unmistakable active-workspace banner on every mutation screen.
- On switch: clear workspace-specific cached data, terminate scoped streams, re-fetch permissions, update breadcrumbs/navigation, and prevent stale-tab cross-workspace writes.
- Never trust the selected workspace ID from the browser as proof of access.
- Support direct deep links only after fresh authorization checks.
- Use one authentication system and one origin; no separate login for each persona.

### 5.4 Delegation and impersonation

**Normal delegation:** Invite a person to a workspace with a defined role and optional expiry. Accept invitation, verify identity, and audit changes.

**Support access:** Proposed time-limited, reason-coded, explicitly initiated access to a customer context. Prefer customer approval when feasible; emergency access requires a separate documented policy, heightened logging, and notice. Support mode must be visible in the UI and never bypass helper-side host restrictions.

**No silent impersonation:** A user must not obtain another user's credentials or session tokens. Support access is a scoped authorization grant, not impersonation.

### 5.5 Tenant boundaries

- Hosting organization A cannot enumerate organization B, its customers, or resources.
- Website owner A cannot access website owner B's records or files.
- Developer A cannot access project B or its future secrets.
- Workspace parents do not implicitly inherit private child data.
- Resource ownership is checked against authoritative database relations on every request.
- Audit events include actor, effective workspace, support grant (if any), target, and decision.

### 5.6 P2.0 ground rules (decided v1.1)

1. A Server Administrator can create and manage hosting organizations on their server, but accessing customer-private data requires an appropriate grant or audited support access.
2. A Reseller can manage their own hosting customers and assigned websites, but cannot manage the underlying Linux server.
3. A Website Owner controls only their assigned websites.
4. A Developer controls only their assigned applications or projects.
5. A user can hold multiple roles across different workspaces without needing separate accounts — one login, many memberships (e.g. one person who owns a VPS, operates an agency and develops one application holds three memberships on a single account).
6. The platform must never equate workspace switching with privilege escalation.

**Two ways to enter a customer website (decided v1.1).** Take an agency, Acme Hosting, managing example.com:

- **Agency management mode** — Acme opens example.com from its customer directory and performs explicitly delegated hosting-management tasks (view hosting allocation; manage the site's service settings once Phase 3 ships). Acme stays in its Hosting workspace; no persona switch occurs, and customer-private data stays behind its grants.
- **Website Owner mode** — the website's owner logs in and sees only their assigned websites, domains, SSL, files and backups as those features become available. They see no agency-wide customer list and no server administration.

A reseller reaches a Website Owner context only via separate membership or an audited support grant. This gives agencies operational flexibility without making every reseller a website owner or granting unnecessary access to customer-private information.

---

## 6. Roles, permissions, and authorization

### 6.1 Default role templates

| Workspace type | Default roles | Meaning |
|---|---|---|
| Server | `owner`, `administrator`, `operator`, `viewer` | Owner controls membership/policy; administrator manages allowed host tasks; operator handles operational tasks; viewer read-only |
| Hosting org | `owner`, `manager`, `staff`, `viewer` | Manage org/team/customer relationships only |
| Website | `owner`, `manager`, `viewer` | Site-scoped access; functionality expands in Phase 3 |
| Developer project | `owner`, `maintainer`, `viewer` | Project-scoped access; functionality expands in Phase 4 |

Role templates are starting defaults, not proof of universal access. Only a server owner can assign server-admin rights unless explicitly delegated under a tightly controlled policy.

### 6.2 Permission namespace

```text
workspace.read
workspace.members.invite
workspace.members.manage
workspace.support.request
workspace.support.approve

system.overview.read
system.metrics.read
system.processes.read
system.logs.read
system.services.read
system.services.restart
system.services.start
system.services.stop
system.services.enable
system.files.read
system.files.write
system.firewall.read
system.firewall.modify
system.ssh.read
system.ssh.modify
system.users.read
system.users.manage
system.schedule.read
system.schedule.manage
system.packages.read
system.packages.upgrade
system.backups.read
system.backups.create
system.backups.restore
system.audit.read
system.policy.manage

hosting.organization.read
hosting.customers.manage
website.workspace.read
developer.project.read
```

The `hosting.*`, `website.*`, and `developer.*` namespaces are foundation-only in Phase 2. They must not accidentally unlock later-phase features before their resource models and isolation tests exist.

### 6.3 Authorization algorithm

For every API request:

1. Authenticate the user and validate session, CSRF protections, and session freshness.
2. Resolve the requested workspace from the URL or route.
3. Verify active membership or valid support grant.
4. Resolve the resource and its authoritative workspace/host relationship.
5. Check the requested permission against role, scope, policy, and resource state.
6. For high-risk actions, require recent reauthentication and explicit intent confirmation.
7. Record the authorization decision and create a durable job for mutations.
8. At the helper boundary, independently enforce the permitted operation and host-level resource policy.

**Security caveat:** A compromised `platformd` process may invoke all operations its Unix identity can request from the helper. Therefore API RBAC and UI confirmation are not independent defenses against complete daemon compromise; helper-side operation allowlists, OS isolation, and smaller trust domains are essential.

### 6.4 Authorization failure behavior

Return `403` for known resources without permission, or `404` where revealing existence would expose another tenant. Never disclose private resource names, paths, or tenant membership in error bodies. Record denials with redacted context and rate-limit enumeration attempts.

---

## 7. Information architecture and navigation

### 7.1 Shared shell

Global header: product name, active workspace selector, current role, alerts, jobs, profile/security, and help. Persistent active-context indicator. Accessible navigation and responsive layout.

### 7.2 Server Administrator navigation (fully functional in Phase 2)

```text
Server Overview
Monitoring & Health
Processes
Services
Logs
Files (restricted)
Network & Firewall
SSH Security
Linux Users & Groups
Scheduled Tasks
Packages & Updates
Backups & Recovery
Resource Intelligence
Audit & Security
Workspace & Access Management
Settings
```

### 7.3 Hosting Organization navigation (foundation in Phase 2)

```text
Organization Overview
Team & Roles
Customer Directory (metadata only)
Assigned Resources (authorized summary only)
Invitations & Access
Future: Hosting Accounts, Plans, Websites, Domains, Billing
```

### 7.4 Website Owner navigation (foundation in Phase 2)

```text
Website Overview (workspace metadata / status)
Members & Access
Future: Files, Domains, SSL, Website Backups, Access Logs
```

### 7.5 Developer navigation (foundation in Phase 2)

```text
Project Overview (workspace metadata / status)
Members & Access
Future: Applications, Git, Deployments, Logs, Secrets
```

Later-phase entries must be hidden or clearly labeled `Coming in Phase 3/4`; never display buttons that pretend to perform unavailable actions.

---

## 8. Functional requirements — monitoring and health

**Module ID:** P2-MON  
**Primary persona:** Server Administrator; scoped summary for authorized organization users.

### 8.1 Required capabilities

- MON-001: Show OS, kernel, hostname, architecture, uptime, virtualization indicators where detectable, and panel version.
- MON-002: Show CPU usage, load average, core count, and sampled CPU trends.
- MON-003: Show RAM total, available, used, swap total/used, and memory pressure signals where supported.
- MON-004: Show filesystem total/used/available, inode utilization, and mount-level health for relevant mounts.
- MON-005: Show network interface status and throughput; avoid exposing sensitive interface metadata to non-admin contexts.
- MON-006: Show recent process CPU/memory consumption with bounded sampling.
- MON-007: Show service health and relevant failure indicators.
- MON-008: Show timestamp and freshness of each metric; stale metrics must not look live.
- MON-009: Retain a small configurable metric history with bounded SQLite growth; downsample older samples.
- MON-010: Provide low-memory, disk-pressure, swap-thrashing, high-load, and failed-service alerts with actionable explanation.
- MON-011: Handle missing `/proc`/`/sys` features or restricted kernels gracefully.
- MON-012: Make monitoring available without installing Prometheus, Grafana, Redis, or a separate agent.

### 8.2 UX

- Overview cards: CPU, memory, disk, network, uptime, health, alerts.
- Drill-down charts for selected windows, with explicit sampling/retention labels.
- Server status cannot be marked healthy solely because the API is reachable.
- Never confuse Linux page cache with unavailable memory; explain available memory and swap usage.

### 8.3 Acceptance criteria

- Dashboard refreshes within configured intervals without unbounded CPU polling.
- Metrics remain readable on 512 MB and 1 GB test environments.
- A failed collector produces a partial-data warning, not a blank dashboard.
- History retention stays within configured disk budgets.

---

## 9. Functional requirements — process and service management

**Module ID:** P2-SVC

### 9.1 Process visibility

- SVC-001: List processes with PID, user, CPU, memory, start time, and state where permitted.
- SVC-002: Filter/search processes; cap page sizes and sampling work.
- SVC-003: Show cgroup or systemd-unit association where available.
- SVC-004: Do not expose arbitrary kill/signals in initial release; any later process-control operation requires a separate threat model.

### 9.2 Systemd services

- SVC-005: List units and show active/load/substate, description, and last failure.
- SVC-006: Inspect a unit's status, relevant properties, and recent journal entries.
- SVC-007: Start, stop, restart, enable, and disable **explicitly approved** units only.
- SVC-008: Protect critical units (SSH access, panel daemon, helper, core init/system services) under default policy.
- SVC-009: Prevent unit-name injection, arbitrary unit creation, and dangerous D-Bus method selection.
- SVC-010: Provide confirmation for disruptive operations and show dependent-service warnings where feasible.
- SVC-011: Execute mutations as durable jobs with bounded timeouts and outcome reconciliation.
- SVC-012: Report actual resulting state rather than claiming success on command acceptance alone.

### 9.3 Acceptance criteria

- An operator with `system.services.restart` can restart a designated test unit.
- A viewer cannot mutate any unit.
- An allowlisted restart cannot be transformed into a restart of `sshd` or `platformd`.
- A disconnected client cannot cause a false success state or unsafe blind retry.

---

## 10. Functional requirements — logs and diagnostics

**Module ID:** P2-LOG

- LOG-001: View permitted systemd journal entries by unit, severity, and time window.
- LOG-002: Support search and pagination with strict output limits.
- LOG-003: Offer bounded near-real-time tailing with disconnect cleanup and backpressure.
- LOG-004: Mask known secrets and redact sensitive metadata where feasible; document that arbitrary application logs may still contain secrets.
- LOG-005: Enforce per-unit and per-workspace log permissions; no broad journal access for unrelated tenants.
- LOG-006: Provide a read-only diagnostics summary suitable for support, excluding credentials, private keys, and tokens.
- LOG-007: Record log access to particularly sensitive scopes when policy requires it.

**Acceptance:** Long logs cannot exhaust RAM or SQLite; cross-tenant log access is denied; live streams terminate when a membership is revoked or workspace changes.

---

## 11. Functional requirements — restricted file manager

**Module ID:** P2-FILE

### 11.1 Scope

The Phase 2 file manager manages **approved server-administration directories and explicitly delegated paths**. It is not an unrestricted root filesystem browser and does not automatically provide Phase 3 website file management.

### 11.2 Requirements

- FILE-001: Browse only configured roots; show breadcrumbs and explicit path scope.
- FILE-002: Read/download files with size and bandwidth limits.
- FILE-003: Create directories and upload files within assigned roots and quotas.
- FILE-004: Edit approved text/config files with backup, validation hook, atomic replacement, and version/ETag conflict detection.
- FILE-005: Rename, move, and delete within policy; require confirmation for destructive changes.
- FILE-006: Show safe metadata (type, size, timestamps, permitted ownership information).
- FILE-007: Reject path traversal, symlink/mount escapes, special-device access, and unsupported file types.
- FILE-008: Use directory-relative operations (`openat2` resolution constraints where supported) and avoid validate-then-open races.
- FILE-009: Treat hard links, bind mounts, archives, and ownership changes as distinct attack surfaces.
- FILE-010: Stream large files; never buffer uploads/downloads wholly in memory.
- FILE-011: Execute site-owned file operations under the site's own Unix identity when hosting integration arrives.
- FILE-012: No arbitrary editing of `/etc/shadow`, private keys, kernel interfaces, or panel secrets.

**Acceptance:** Automated traversal/symlink/race tests cannot escape a configured root; file writes cannot corrupt protected platform policy or keys; oversized files fail safely.

---

## 12. Functional requirements — network and firewall

**Module ID:** P2-FW

### 12.1 Requirements

- FW-001: Detect supported firewall backend and current state; initially target nftables on supported distributions.
- FW-002: Show normalized inbound rules, ports, protocols, sources, and management-access dependencies.
- FW-003: Allow only schema-driven rule changes, not arbitrary pasted shell commands.
- FW-004: Validate candidate ruleset before activation.
- FW-005: Preserve known-good ruleset and arm an **independent** rollback mechanism before applying remote-access-affecting changes.
- FW-006: Require confirmation over the newly effective management path; revert automatically on timeout where possible.
- FW-007: Warn about SSH port changes, default-drop policies, VPN/tunnel dependencies, IPv6 differences, and provider-level firewalls outside the panel's control.
- FW-008: Keep panel access, loopback bootstrap, and recovery pathways protected by explicit policy.
- FW-009: Provide a recovery CLI and document out-of-band provider-console access.
- FW-010: Audit before/after summaries, actor, confirmation, and rollback outcome.

### 12.2 Safety caveats

A rollback timer cannot recover every failure (e.g., kernel panic, provider firewall, complete disk failure). Never promise guaranteed prevention of lockout. Network changes require disposable-VM and remote-session fault tests.

---

## 13. Functional requirements — SSH security

**Module ID:** P2-SSH

- SSH-001: Show SSH service status, listening ports, and selected effective security settings.
- SSH-002: Manage authorized keys for approved accounts using safe file ownership and permissions.
- SSH-003: Support typed configuration changes to a small allowlist of SSH settings.
- SSH-004: Validate candidate configuration using the installed OpenSSH validation mechanism before reload.
- SSH-005: Detect distribution-specific config/include layouts and preserve unrelated administrator configuration.
- SSH-006: Use independent rollback for changes that may interrupt remote access.
- SSH-007: Prevent removal of the last known working administrator access path without explicit recovery readiness.
- SSH-008: Do not store or display private SSH keys unless a future, separately designed secret-management feature requires it.
- SSH-009: Surface failed-login indicators from permitted logs with bounded processing.

**Acceptance:** Invalid config never replaces working config; a failed post-change confirmation triggers recovery; no unauthorized account key manipulation.

---

## 14. Functional requirements — Linux users and groups

**Module ID:** P2-USR

- USR-001: List human/non-system accounts with safe metadata; hide password hashes and sensitive shadow data.
- USR-002: Create approved non-system users with validated names, UID/GID policy, home roots, and shell restrictions.
- USR-003: Manage approved groups and memberships while protecting root, panel service identities, and system accounts.
- USR-004: Lock/unlock approved accounts and rotate authorized keys through safe workflows.
- USR-005: Delete or disable accounts with explicit warnings about files, processes, jobs, and recovery implications.
- USR-006: Prohibit arbitrary sudoers edits or adding users to root-equivalent groups via generic endpoints.
- USR-007: Record account changes with actor, target, and outcome, never passwords or private keys.
- USR-008: Reconcile external/manual changes to Linux accounts rather than assuming panel database is authoritative for all OS state.

**Acceptance:** An agency manager cannot create Linux accounts by virtue of agency membership; protected users/groups cannot be modified; failed creation is recoverable.

---

## 15. Functional requirements — scheduled tasks and maintenance

**Module ID:** P2-SCH

- SCH-001: View panel-managed scheduled tasks and selected existing systemd timers/cron entries read-only.
- SCH-002: Create scheduled tasks from **approved task types** (backup, diagnostics, cleanup, approved script templates), not arbitrary root shell.
- SCH-003: Support schedule validation, timezone display, next-run preview, and missed-run policy.
- SCH-004: Execute tasks under least-privileged identities and explicit resource/time limits.
- SCH-005: Prevent overlapping runs when unsafe; cap queue depth and retries.
- SCH-006: Show run history, bounded logs, success/failure, and cancellation where safe.
- SCH-007: Disable tasks automatically when membership, resource, or credential grants are revoked where applicable.
- SCH-008: Prefer systemd timers for host-level scheduling; avoid a new always-on scheduler process when possible.

**Acceptance:** No arbitrary root command execution through scheduling; invalid expressions rejected; task output and retention bounded.

---

## 16. Functional requirements — packages, updates, and reboot

**Module ID:** P2-PKG

- PKG-001: Detect supported distribution and package manager; initial support aligns with Phase 1's approved Ubuntu/Debian versions.
- PKG-002: Show pending updates, package source, and security-update classification when reliable metadata exists.
- PKG-003: Run read-only update simulation/preflight where supported.
- PKG-004: Require explicit approval for installs/upgrades/removals; disallow arbitrary package-manager flags.
- PKG-005: Check disk space, RAM headroom, package-manager locks, network availability, and expected service impact.
- PKG-006: Preserve package-manager output and job state with bounded logs; handle interrupted dpkg/apt operations with guided recovery.
- PKG-007: Identify when reboot may be required; never silently reboot the host.
- PKG-008: Treat kernel, SSH, firewall, and panel-related upgrades as high-risk with stronger warnings.
- PKG-009: Provide documented recovery steps; do not claim arbitrary package upgrades can be rolled back transactionally.
- PKG-010: Avoid exposing unrestricted `apt install <user input>` through the helper in the initial release.

**Acceptance:** An update cannot be started by a viewer; concurrent apt jobs are serialized; insufficient disk space blocks risky upgrades; reboot requires separate confirmation.

---

## 17. Functional requirements — backups and recovery

**Module ID:** P2-BKP

### 17.1 Phase 2 backup scope

- Panel SQLite database and configuration (with consistency safeguards).
- Workspace memberships and audit metadata subject to retention/privacy rules.
- Selected system configuration snapshots for supported modules (e.g., managed firewall/SSH configuration).
- Approved user-selected directories where policy and capacity permit.

**Not included:** Full hosted-site/application/database backups until the respective modules exist; full machine snapshots depend on provider/storage capabilities.

### 17.2 Requirements

- BKP-001: Create consistent SQLite backups using supported backup mechanisms, not blind copying of active WAL files.
- BKP-002: Include versioned manifests, checksums, scope, timestamp, and restore compatibility metadata.
- BKP-003: Support local destinations first, with remote destinations only when securely designed and configured.
- BKP-004: Preflight disk space, temporary space, retention, and peak resource requirements.
- BKP-005: Support encryption of backup archives when sensitive content is included; keep keys separate and document key-loss consequences.
- BKP-006: Verify backup integrity and periodically test restoration on disposable environments.
- BKP-007: Require explicit confirmation and a pre-restore safety snapshot for destructive restores where feasible.
- BKP-008: Distinguish panel-state restore from Linux host/system configuration restore.
- BKP-009: Track retention and pruning without silently deleting the last verified recovery point.
- BKP-010: Provide CLI recovery when the dashboard cannot start.

**Acceptance:** A tested restore recovers supported state; corrupt/incompatible backups are rejected before destructive application; backup jobs cannot exhaust the 10 GB development disk without warning.

---

## 18. Resource intelligence and workload safety

**Module ID:** P2-RES

### 18.1 Objective

Resource intelligence is a **decision and execution control layer**, not just a dashboard. It measures current capacity, predicts risks from known operations, bounds concurrency, and explains tradeoffs.

### 18.2 Signals

- Available memory, swap availability and activity, memory PSI where available.
- CPU load/pressure, core count, recent utilization.
- Filesystem free space, inodes, temporary-directory space, expected archive/build size.
- Current active jobs, memory/CPU cgroup limits, recent peaks.
- Existing services and workload estimates where reliable.
- Confidence/unknown-state indicators for incomplete data.

### 18.3 Decision classes

| Decision | Meaning | Example |
|---|---|---|
| `allow` | No material constraint detected | Small read-only metrics query |
| `warn` | Operation may cause pressure | Archive creation on 1 GB VPS |
| `confirm` | Elevated risk; informed user decision | Large backup consuming most free disk |
| `defer` | Temporarily unsafe; retry later | Package manager busy or high active-job load |
| `deny` | Safety invariant or hard capacity limit | Not enough space to create rollback snapshot |

These are **operation policies**, not rigid RAM-tier feature locks. Expert overrides may be available for warnings, but never for security boundaries or hard safety invariants.

### 18.4 Requirements

- RES-001: Run preflight before expensive mutation jobs.
- RES-002: Reserve estimated temporary disk/headroom when feasible.
- RES-003: Enforce per-job timeouts and cgroup resource limits for subprocesses.
- RES-004: Limit concurrent heavy jobs; Nano profile defaults to one heavy job at a time, subject to testing.
- RES-005: Prefer streaming and incremental processing to large in-memory buffers.
- RES-006: Surface decision reasons and actionable mitigations.
- RES-007: Record actual peak memory/CPU/disk against estimates to improve models.
- RES-008: Bound metric history, job logs, caches, and temporary files.
- RES-009: Degrade gracefully under memory/disk pressure and keep the management UI responsive where possible.
- RES-010: Make resource assessment reusable by Phase 3 hosting and Phase 4 deployment.

### 18.5 Illustrative example

A 1 GB server has 400 MB currently available, but a package upgrade may require temporary disk space and can restart services. The engine should show estimated risk and actual preflight findings. It must not claim the operation is safe solely because total RAM is 1 GB.

---

## 19. Privileged operations framework

**Architecture decisions:** ADR-012 through ADR-016, proposed.

### 19.1 Process model

```text
Browser (SvelteKit static UI)
   |
   v
platformd (unprivileged Go API)
   |-- session / CSRF / RBAC / workspace scope
   |-- SQLite jobs + audit + resource preflight
   |
   v
/run/platform/helper.sock (Unix socket)
   |
   v
platform-helper (root, short-lived where feasible)
   |-- peer credential validation
   |-- protocol and schema validation
   |-- root-owned operation policy
   |-- per-operation safety controls
   |
   v
systemd D-Bus / constrained Linux APIs
```

**Critical boundary:** The helper must not expose arbitrary shell execution, arbitrary D-Bus methods, arbitrary filesystem writes, or caller-supplied executable paths.

### 19.2 Protocol v1

Use strict, size-bounded JSON messages over a Unix-domain socket for the first iteration. A newline-delimited frame is acceptable with a maximum request size of **16 KiB initially**; reconsider for operations needing bulk data (bulk data must use a separately secured streaming mechanism, not enlarge the root-control envelope without review).

**Request:**

```json
{
  "protocol_version": 1,
  "request_id": "018f2e8c-7a42-7d12-8f16-5c8c4f6d9a21",
  "operation": "service.restart",
  "parameters": { "unit": "platform-test.service" }
}
```

**Success:**

```json
{
  "protocol_version": 1,
  "request_id": "018f2e8c-7a42-7d12-8f16-5c8c4f6d9a21",
  "status": "succeeded",
  "result": { "unit": "platform-test.service", "active_state": "active" }
}
```

**Failure:**

```json
{
  "protocol_version": 1,
  "request_id": "018f2e8c-7a42-7d12-8f16-5c8c4f6d9a21",
  "status": "failed",
  "error": { "code": "UNIT_NOT_ALLOWED", "message": "Service not manageable under host policy" }
}
```

### 19.3 Helper validation order

1. Validate expected systemd-activation socket and peer Unix credentials.
2. Bound frame size and request duration.
3. Reject unsupported protocol versions, malformed JSON, and unknown fields.
4. Resolve operation in static registry.
5. Decode operation-specific typed parameters.
6. Enforce root-owned policy and protected resources.
7. Apply concurrency/time/resource limits.
8. Execute via constrained OS interface.
9. Verify resulting state when possible.
10. Return stable error codes and minimal safe output.

### 19.4 Initial operation registry

| Operation | Phase 2 milestone | Host-side guard |
|---|---|---|
| `service.list` / `service.inspect` | P2.2 | Scoped unit visibility |
| `service.restart` | P2.2 | Exact unit allowlist |
| `service.start/stop/enable/disable` | P2.2 expanded | Exact unit allowlist, protected units |
| `file.read/write` | P2.2 expanded | Directory-relative roots, file-type policy |
| `firewall.apply` | P2.3 | Schema, syntax check, rollback |
| `ssh.config.apply` | P2.3 | Key allowlist, validation, rollback |
| `user.create/disable` | P2.3 | UID/GID and system-account protections |
| `schedule.manage` | P2.4 | Approved templates and identity |
| `package.upgrade` | P2.4 | Approved backend, preflight, locks |
| `backup.create/restore` | P2.4 | Scoped destinations, integrity, confirmation |

### 19.5 Helper deployment and hardening

- Use systemd socket activation where compatible with the chosen Go listener implementation.
- Root owns executable, policy, and service unit; `platformd` cannot write them.
- Socket permissions restrict callers to the dedicated platform identity; validate `SO_PEERCRED` too.
- Use systemd sandboxing (`ProtectSystem`, `ProtectHome`, `NoNewPrivileges`, capability bounding, address-family and filesystem restrictions) **only after testing each operation**.
- The helper must exit after a bounded idle interval; `RuntimeMaxSec` alone is not an idle timeout.
- Use separate smaller helper services/policies if high-risk modules cannot safely share one root process.
- No debug endpoint or plugin may bypass the operation registry.

### 19.6 Job state and reconciliation

`queued → validated → running → succeeded`, with terminal or recovery states `failed`, `cancelled`, `timed_out`, `outcome_unknown`, `recovery_required`.

The daemon persists the job before mutation, tracks attempts, and reconciles uncertain outcomes. A dropped socket connection must not trigger an unsafe blind retry. Operations require documented idempotency behavior.

---

## 20. API requirements and example endpoints

**Base:** `/api/v1` (same origin as frontend; session authentication; CSRF protection for cookie-based mutations).

### 20.1 Workspace and identity

| Method | Endpoint | Purpose |
|---|---|---|
| GET | `/me` | Identity and session information |
| GET | `/workspaces` | Authorized workspace selector entries |
| GET | `/workspaces/{id}` | Authorized workspace details |
| GET | `/workspaces/{id}/permissions` | Effective scoped permissions |
| POST | `/workspaces/{id}/invitations` | Invite scoped member |
| GET | `/workspaces/{id}/members` | List authorized members |
| PATCH | `/workspaces/{id}/members/{userId}` | Change scoped role |
| POST | `/support-grants` | Request audited support access |
| POST | `/support-grants/{id}/approve` | Approve when policy permits |

### 20.2 Server administration

| Method | Endpoint | Purpose |
|---|---|---|
| GET | `/servers/{serverId}/overview` | Host summary |
| GET | `/servers/{serverId}/metrics` | Metrics and history |
| GET | `/servers/{serverId}/processes` | Bounded process list |
| GET | `/servers/{serverId}/services` | Service list |
| GET | `/servers/{serverId}/services/{unit}` | Service details |
| POST | `/servers/{serverId}/services/{unit}/actions` | Typed service action/job |
| GET | `/servers/{serverId}/logs` | Filtered logs |
| GET | `/servers/{serverId}/files` | Scoped file listing |
| POST | `/servers/{serverId}/files/actions` | Scoped file operation |
| GET | `/servers/{serverId}/firewall` | Current firewall summary |
| POST | `/servers/{serverId}/firewall/changes` | Prepare/apply controlled change |
| GET | `/servers/{serverId}/ssh` | SSH security summary |
| POST | `/servers/{serverId}/ssh/changes` | Controlled SSH change |
| GET | `/servers/{serverId}/users` | Linux user summary |
| POST | `/servers/{serverId}/users` | Approved user creation |
| GET | `/servers/{serverId}/schedules` | Scheduled tasks |
| POST | `/servers/{serverId}/schedules` | Approved scheduled task |
| GET | `/servers/{serverId}/packages/updates` | Pending updates |
| POST | `/servers/{serverId}/packages/upgrade-jobs` | Approved upgrade job |
| GET | `/servers/{serverId}/backups` | Supported backups |
| POST | `/servers/{serverId}/backups` | Create backup job |
| POST | `/servers/{serverId}/backups/{id}/restore-jobs` | Restore job |
| POST | `/servers/{serverId}/resource-preflight` | Explain operation feasibility |
| GET | `/jobs/{id}` | Authorized job state |
| GET | `/servers/{serverId}/audit` | Scoped audit events |

### 20.3 API rules

- Every server endpoint checks server membership and target relationship; never authorize solely by user-supplied `serverId`.
- Use stable resource IDs; encode/validate unit names and paths strictly.
- Mutations use idempotency keys and return job IDs; HTTP acceptance is not completion.
- Use pagination, rate limits, output-size limits, and explicit filtering.
- Avoid exposing helper protocol directly to browsers.
- Never return secrets, private keys, password hashes, or unredacted privileged commands.
- Document `401`, `403`, `404`, `409`, `422`, `429`, `503`, and operation-specific errors.
- Support API schema versioning and backwards-compatible migrations.

---

## 21. Data model and SQLite persistence

### 21.1 Core entities

```text
users
sessions
workspaces
workspace_memberships
roles
permissions
role_permissions
workspace_invitations
support_grants
servers
workspace_resources
jobs
job_attempts
audit_events
metric_samples
health_alerts
managed_service_policies
managed_file_roots
configuration_revisions
scheduled_tasks
backup_manifests
resource_estimates
```

### 21.2 Illustrative normalized schema

```sql
CREATE TABLE workspaces (
  id TEXT PRIMARY KEY,
  type TEXT NOT NULL CHECK (type IN
    ('server','hosting_org','website','developer_project')),
  name TEXT NOT NULL,
  parent_workspace_id TEXT REFERENCES workspaces(id),
  state TEXT NOT NULL DEFAULT 'active',
  created_at TEXT NOT NULL
);

CREATE TABLE roles (
  id TEXT PRIMARY KEY,
  workspace_type TEXT NOT NULL,
  name TEXT NOT NULL,
  UNIQUE (workspace_type, name)
);

CREATE TABLE workspace_memberships (
  user_id TEXT NOT NULL REFERENCES users(id),
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  role_id TEXT NOT NULL REFERENCES roles(id),
  state TEXT NOT NULL DEFAULT 'active',
  expires_at TEXT,
  PRIMARY KEY (user_id, workspace_id)
);

CREATE TABLE permissions (
  key TEXT PRIMARY KEY
);

CREATE TABLE role_permissions (
  role_id TEXT NOT NULL REFERENCES roles(id),
  permission_key TEXT NOT NULL REFERENCES permissions(key),
  PRIMARY KEY (role_id, permission_key)
);

CREATE TABLE servers (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL UNIQUE REFERENCES workspaces(id),
  host_identifier TEXT NOT NULL,
  state TEXT NOT NULL DEFAULT 'active'
);

CREATE TABLE jobs (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  actor_user_id TEXT NOT NULL REFERENCES users(id),
  operation TEXT NOT NULL,
  target_type TEXT NOT NULL,
  target_id TEXT NOT NULL,
  status TEXT NOT NULL,
  idempotency_key TEXT,
  created_at TEXT NOT NULL,
  started_at TEXT,
  finished_at TEXT,
  error_code TEXT,
  UNIQUE (workspace_id, idempotency_key)
);
```

**Schema caveats:** This is illustrative, not migration-ready. Add indexes, foreign-key enforcement, uniqueness for active invitations, permission-role type validation, cycle prevention in workspace hierarchy, lifecycle transitions, encryption/secret handling, audit immutability controls, and retention rules. Existing Phase 1 user/session tables must be migrated rather than duplicated.

### 21.3 Migration and consistency requirements

- SQLite WAL mode with bounded checkpoints and disk monitoring.
- Transactional migrations with backup/rollback plans.
- Atomic first-server-workspace creation for the Phase 1 owner.
- Membership revocation invalidates cached permissions and scoped streams promptly.
- Deleting a workspace must not orphan Linux resources or silently delete customer data.
- Parent workspace relationships cannot create cycles.
- Audit logs use append-only application behavior and protected storage; local root compromise cannot be treated as tamper-proof.

---

## 22. Frontend requirements and user experience

### 22.1 Technology

Use Phase 1's static SvelteKit + TypeScript core UI served by Go. Reserve optional statically exported Next.js modules for later features, without introducing a mandatory Node runtime or splitting auth across origins.

### 22.2 Shared UX requirements

- UX-001: Single login with secure session, MFA support, and recovery flows inherited from Phase 1.
- UX-002: Workspace selector displays only authorized workspaces and active role.
- UX-003: Context-sensitive navigation and action visibility; hidden UI is not authorization.
- UX-004: Accessible keyboard navigation, semantic labels, and responsive layouts.
- UX-005: Clear read-only versus mutating actions.
- UX-006: Preflight screen for risky actions with estimated effects, warnings, and required confirmation.
- UX-007: Job center with progress, logs, retry guidance, and outcome-unknown states.
- UX-008: Audit and history views for privileged actions.
- UX-009: Recovery notices for SSH/firewall changes, including countdown and independent rollback status.
- UX-010: Empty states explain whether a feature is unavailable, unauthorized, or simply has no resources.
- UX-011: UI should not encourage disabling TLS verification or broadening firewall exposure to make setup easier.

### 22.3 Representative journeys

**Journey A — Owner switches context:** Sign in → Server Overview → workspace selector → Agency workspace → customer directory → return to Server Overview. No extra login, no change in privileges beyond each membership.

**Journey B — Reseller:** Sign in → Hosting Organization Overview → invite team member → review customer metadata. Server administration routes remain inaccessible.

**Journey C — Website owner:** Sign in → assigned Website workspace → view site identity and membership → see clearly labeled Phase 3 functionality not yet available.

**Journey D — Developer:** Sign in → assigned Project workspace → view team and project metadata → see Phase 4 deployment features as future capabilities.

**Journey E — Service restart:** Server Operator → service details → restart → preflight/confirmation → job status → resulting service state → audit record.

**Journey F — Firewall change:** Server Admin → propose rule → validate → arm rollback → apply → confirm through new access path → commit; if not confirmed, revert and display outcome.

**Journey G — Low disk backup:** Owner requests backup → resource preflight detects insufficient headroom → blocks or requests alternative destination → explains mitigation; does not partially fill disk silently.

---

## 23. Non-functional requirements and performance budgets

### 23.1 Footprint targets (not guarantees)

| Metric | Proposed target / constraint | Measurement |
|---|---|---|
| Core Go daemon idle RSS | 30–50 MB aspirational; original blueprint ≤40 MB | Idle after warm-up and after 24h |
| Helper idle footprint | Zero resident helper process when socket-activated and idle | `systemctl`, process inventory |
| Management-plane overhead | Report daemon + helper when active + ancillary processes separately | Per-process and cgroup metrics |
| 512 MB VPS | Core dashboard/admin foundation usable without OOM | Real or representative constrained VM |
| 1 GB VPS | Core admin and representative operations with bounded peaks | Initial development VPS |
| Idle CPU | Near zero when no active jobs, measured | Sampling over 24h |
| Metric history | Bounded retention and disk footprint | SQLite size over soak test |
| Background jobs | One heavy job by default on Nano, adjustable by safe policy | Concurrent-job stress test |
| Startup | Preserve Phase 1 goal of fast startup; benchmark, do not promise | Cold start test |

**Measurement honesty:** Report RSS, PSS if available, peak RSS, memory cgroup peak, CPU, I/O, and total VPS usage separately. Exclude hosted workloads from core-daemon claims but include them in whole-server capacity reports. Compare other panels only under reproducible, equivalent conditions.

### 23.2 Reliability

- A failed collector or module must not crash the whole daemon.
- Operations should be idempotent or explicitly non-retryable.
- SQLite corruption/recovery procedures documented and tested.
- Critical mutations require durable state and a recovery plan.
- API remains responsive during bounded heavy jobs where host capacity allows.
- Clear behavior under full disk, low RAM, unavailable D-Bus, and interrupted network.

### 23.3 Compatibility

- Initial supported Linux releases: those formally validated by the Phase 1 installer (Ubuntu/Debian families).
- Systemd is required for service-management features.
- nftables, OpenSSH, and package manager features are enabled only when detected and tested.
- AMD64 and ARM64 builds where Phase 1 supports them.
- Unsupported features show actionable compatibility messages; never silently run alternative unsafe commands.

### 23.4 Accessibility and localization

- Keyboard-operable navigation and dialogs.
- Screen-reader labels for metrics, charts, alerts, and progress.
- Clear non-color-only severity indicators.
- Timezone-aware schedules and consistent date formats.
- Localization-ready strings; initial release language to be decided.

---

## 24. Security, privacy, and threat model requirements

### 24.1 Assets to protect

Owner sessions, membership grants, server control, Linux credentials/keys, private tenant files, service configurations, backup archives, audit records, update artifacts, and platform signing/secret material.

### 24.2 Threats and required controls

| Threat | Required defense | Verification |
|---|---|---|
| Authentication/session bypass | Hardened sessions, CSRF, MFA, rate limits, secure bootstrap | Auth and session tests |
| Cross-tenant IDOR | Resource ownership checks, scoped queries, denial tests | Matrix-based isolation tests |
| Role escalation | Server-side permission checks, role assignment restrictions | Permission mutation tests |
| Root command injection | Typed helper, no shell strings, static registry | Fuzzing and adversarial inputs |
| Compromised daemon | Narrow helper policy, OS sandbox, protected policy files | Compromised-client simulation |
| Path traversal/race | Directory-relative file APIs, mount/symlink controls | Filesystem adversarial tests |
| Firewall/SSH lockout | Validation, independent rollback, provider-console guidance | Disconnect/failure tests |
| Malicious package/update | Verified releases, package provenance, bounded update backend | Update integrity tests |
| Secret leakage | Redaction, file permissions, encryption where needed | Logs/backup inspection |
| Resource exhaustion | Rate limits, streaming, quotas, cgroups, bounded retention | 512 MB stress tests |
| Audit tampering | Protected append-only behavior, optional export | Integrity/permissions tests |
| Unsafe backup restore | Manifest integrity, compatibility, scope validation | Corrupt restore tests |

### 24.3 Release security requirements

- Threat model each privileged operation before enabling it.
- Static analysis, dependency scanning, fuzzing for protocol and path parsing.
- Security review of session handling, workspace scope, helper policy, and update path.
- Reproducible or independently verifiable signed artifacts where feasible.
- Document vulnerability disclosure and security patch process.
- No production claim of “more secure than competitor X” without independent evidence.

### 24.4 Data protection

- Collect only necessary user and server metadata.
- Limit metric, log, and audit retention; allow policy-driven cleanup.
- Do not log passwords, tokens, private keys, or backup encryption keys.
- Clearly disclose that a server owner with OS root access ultimately controls the host; app-level tenant isolation cannot protect against a malicious root administrator.
- Document what support grants reveal and how access is audited.

---

## 25. Milestones, dependencies, and implementation order

Phase 2 includes the entire agreed administration scope, but **must be delivered as independently testable milestones**.

### P2.0 — Identity, personas, workspaces, and tenancy foundation

**Deliver:** Workspace types; memberships; role/permission registry; workspace selector; invitations; scope-aware API middleware; audit; support-grant model; cross-tenant tests.

**Exit gate:** All four personas can authenticate into authorized foundation workspaces; server owner retains Phase 1 access; no unauthorized cross-workspace API access. Reseller switching interpretation explicitly confirmed.

### P2.1 — Observability and resource collection

**Deliver:** Server overview; metrics; process visibility; service health; bounded history; alerts; first resource preflight API.

**Exit gate:** Works on 512 MB and 1 GB test machines without unbounded sampling or metric storage; role-based read access verified.

### P2.2 — Privileged helper, services, logs, restricted files

**Deliver:** Socket-activated helper; strict protocol; root-owned policy; approved systemd operations; journal viewer; restricted file manager; durable jobs.

**Exit gate:** An authorized operator can restart a test service; protected units and forbidden paths are inaccessible; failure outcomes reconcile correctly.

### P2.3 — Security administration

**Deliver:** nftables management; SSH settings and keys; Linux users/groups; independent recovery for remote-access changes; high-risk confirmations.

**Exit gate:** Network/SSH rollback survives browser or daemon disconnection in tested scenarios; protected users/groups cannot be changed.

### P2.4 — Maintenance, scheduling, updates, backups

**Deliver:** Approved scheduled tasks; package update inventory and controlled upgrades; reboot workflow; consistent panel backups; integrity checks; restore tooling.

**Exit gate:** Update/backup workflows have documented recovery, concurrency limits, tested restore, and no arbitrary root task runner.

### P2.5 — Resource intelligence integration, hardening, beta readiness

**Deliver:** Cross-module preflights; pressure-aware job scheduling; security regression suite; memory/disk soak tests; accessibility and documentation; release gates.

**Exit gate:** All Phase 2 acceptance scenarios pass on supported platforms; known limitations and measured resource footprint published.

### 25.1 Critical dependency path

```text
Phase 1 auth + installation
        |
        v
P2.0 identity/workspace/authorization
        |
        +----> P2.1 monitoring and resource signals
        |
        v
P2.2 privileged helper + jobs + audit
        |
        +----> P2.3 firewall/SSH/users
        |
        +----> P2.4 packages/schedules/backups
        |
        v
P2.5 integration/security/performance gates
```

### 25.2 Feature rollout strategy

Ship read-only views first, then controlled low-risk mutations, then high-risk operations behind validated rollback and recovery. High-risk features may remain disabled on unsupported distributions or untested configurations rather than weakening protections to meet a date.

---

## 26. Acceptance criteria, quality gates, and test plan

### 26.1 Persona and tenancy scenarios

| ID | Scenario | Expected |
|---|---|---|
| AT-001 | Server owner logs in after Phase 1 migration | Server workspace available; no loss of owner access |
| AT-002 | Server owner switches to an authorized hosting org | Correct navigation and scoped data |
| AT-003 | Reseller requests `/servers/{id}/firewall` | Denied without separate server membership |
| AT-004 | Reseller attempts to enumerate another agency | Denied without data disclosure |
| AT-005 | Website owner opens another website workspace | Denied |
| AT-006 | Developer opens unassigned project | Denied |
| AT-007 | User has multiple memberships | Workspace switch changes effective permissions correctly |
| AT-008 | Membership revoked while UI is open | Subsequent API calls and scoped streams denied |
| AT-009 | Browser changes workspace ID in a mutation | No cross-scope mutation |
| AT-010 | Support grant expires | Access revoked; event audited |
| AT-011 | Unauthorized direct deep link | No privileged content or action |

### 26.2 Administration scenarios

| ID | Scenario | Expected |
|---|---|---|
| AT-012 | Read CPU/memory/disk on 512 MB VM | Correct, bounded, non-OOM |
| AT-013 | Restart approved test service | Job succeeds and state verified |
| AT-014 | Restart protected SSH/panel unit | Denied |
| AT-015 | Attempt path traversal/symlink escape | Denied |
| AT-016 | Search very large journal | Paginated, bounded memory |
| AT-017 | Apply invalid firewall ruleset | Rejected before activation |
| AT-018 | Lose browser connection after firewall apply | Independent rollback triggers unless confirmed |
| AT-019 | Apply invalid SSH config | Rejected before reload |
| AT-020 | Modify root/system account via user UI | Denied |
| AT-021 | Run unapproved scheduled root command | Denied |
| AT-022 | Run concurrent package upgrades | Serialized/rejected safely |
| AT-023 | Backup with insufficient disk headroom | Safe denial/alternative guidance |
| AT-024 | Restore corrupt backup | Rejected before destructive changes |
| AT-025 | Helper crashes after mutation | Job reconciles uncertain outcome |
| AT-026 | Panel restarts mid-job | Durable state preserved; safe reconciliation |
| AT-027 | Disk fills during operation | Bounded failure and recovery guidance |
| AT-028 | 24-hour idle and stress soak | No sustained memory growth or uncontrolled disk use |

### 26.3 Security gates

- No known critical or high-severity authorization bypass in the supported Phase 2 release.
- Every privileged operation has schema validation, host policy, audit behavior, and integration tests.
- Tenant isolation matrix passes for all four personas and all server APIs.
- Firewall/SSH recovery tested with connection loss and daemon crash.
- Release artifacts verified; migration and backup recovery tested.

### 26.4 Resource gates

- Publish measured idle and peak memory for 512 MB, 1 GB, 2 GB, and 4 GB test profiles where available.
- Show total management-plane footprint, not only one process.
- Test CPU/disk pressure, log volume, backup archive creation, and package update spikes.
- If 30–50 MB idle target is missed, document actual results and optimize before making marketing claims.

### 26.5 Definition of done

A module is done only when API, UI, permissions, audit, failure behavior, tests, documentation, and resource impact are complete. A feature behind a nonfunctional UI button is not done.

---

## 27. Open decisions and product questions

| ID | Question | Proposed default | Owner/status |
|---|---|---|---|
| OD-001 | Does a reseller ever switch into Website Owner persona automatically? | No; remain in hosting UI unless explicitly granted website membership | **Decided (v1.1, 2026-11-13)** — confirmed, plus agency management mode: a reseller manages authorized customer websites from the Hosting workspace without switching persona; see §5.2, §5.6 and `docs/phase-2/P2.0-identity-workspaces.md` |
| OD-002 | Can server owners open customer private workspaces without consent? | Audited, time-limited support grant; no silent access | Product/security review |
| OD-003 | Can one hosting org span multiple servers in Phase 2? | Model supports relation, but only local single-server management is operational | Architecture review |
| OD-004 | Are custom roles available in initial beta? | Built-in role templates first; permission engine supports later customization | Product review |
| OD-005 | How are customer directory records created before Phase 3? | Minimal metadata and invitations only; no fake hosting account provisioning | Product review |
| OD-006 | Which exact Ubuntu/Debian releases are supported? | Follow tested Phase 1 matrix | QA review |
| OD-007 | What metrics retention window is safe on 10 GB disks? | Configurable, bounded, measured | Benchmark review |
| OD-008 | What backup destinations ship first? | Local, with strong disk preflight; remote later | Product/security review |
| OD-009 | What package operations are allowed? | Controlled upgrades of approved packages; no arbitrary install flags | Security review |
| OD-010 | Can high-risk changes be done over public HTTPS without SSH fallback? | Only with independently validated recovery path | Security review |
| OD-011 | Should hosting org managers view aggregate host metrics? | Only authorized, privacy-safe allocation summaries | Product review |
| OD-012 | What is the exact scope of admin file roots? | Explicit roots only; no unrestricted `/` | Security review |
| OD-013 | How should emergency support access work? | Separate policy, short expiry, enhanced audit, notification | Product/legal review |
| OD-014 | What are beta SLOs and supported browsers? | Define after benchmarks and prototype usability tests | QA/product review |

---

## 28. Risks and mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Phase 2 scope is too broad | Delivery delays, unfinished features | Milestone gates; read-only-first; no premature Phase 3 scope |
| Persona hierarchy conflated with privileges | Cross-tenant exposure | Separate persona/workspace/role/permission models; test matrix |
| Privileged helper becomes generic root API | Host takeover risk | Typed registry, static policy, sandbox, adversarial tests |
| Firewall/SSH operation locks out owner | Loss of access | Independent rollback, preflight, provider console guidance |
| Resource monitoring increases idle footprint | Poor Nano experience | Native collectors, bounded sampling, retention, on-demand helper |
| Package updates break host | Downtime | Simulation, locks, confirmation, recovery guidance |
| Backup/restore consumes small disk | OOM/full disk | Preflight, streaming, retention, off-host future option |
| Manual Linux changes conflict with panel state | Incorrect UI or unsafe changes | Reconciliation and drift detection |
| Multi-tenant hosting promises arrive before isolation | Data leakage | Foundation-only workspaces; Phase 3/4 gates |
| Security claims outrun evidence | Trust/reputation risk | Publish test results, caveats, disclosure policy |

---

## 29. Metrics and success measurement

### 29.1 Product metrics

- Successful completion rate of server-owner first admin task after Phase 1.
- Percentage of service actions completed without SSH fallback.
- Workspace-switch success and error rate.
- Unauthorized cross-tenant requests denied (security telemetry; do not expose sensitive identifiers).
- Median time to diagnose a failed service.
- High-risk operation rollback and recovery success rate in controlled tests.
- Backup verification and restore test success rate.

### 29.2 Engineering metrics

- Idle/peak RSS, cgroup memory peak, CPU, disk I/O, SQLite size.
- API latency under representative small-server load.
- Job queue depth, timeout rate, uncertain-outcome rate.
- Helper activation count, idle-exit behavior, crash rate.
- Metrics sample freshness and retention footprint.
- Critical/high vulnerability findings and remediation time.

**No fabricated targets:** Numerical success thresholds beyond the proposed footprint budgets must be established using prototype measurements and user tests.

---

## 30. Developer handoff and implementation artifacts

The Phase 2 PRD should be accompanied by the following engineering documents before major coding begins:

1. **P2.0 Identity and Workspace RFC:** Exact switching policy, schema migrations, role templates, invitation/support grant flows, authorization middleware, and test matrix.
2. **P2.1 Monitoring RFC:** Linux collectors, sampling cadence, metrics schema, retention and pressure signals.
3. **P2.2 Helper Protocol RFC:** Wire protocol, socket activation, peer validation, operation registry, systemd D-Bus integration, fuzz tests.
4. **P2.3 Remote Access Safety RFC:** Firewall/SSH change transactions, independent rollback, provider-console recovery.
5. **P2.4 Maintenance RFC:** Package manager backend, scheduling templates, backup formats, restore compatibility.
6. **P2.5 Performance and Security Test Plan:** VM matrix, reproducible measurements, tenant isolation, adversarial tests, release gates.

### 30.1 Suggested repository additions

```text
cmd/
  platformd/
  platform-helper/
internal/
  identity/
  workspaces/
  authorization/
  supportaccess/
  audit/
  jobs/
  resource/
  privilege/
    protocol/
    policy/
    client/
  modules/
    monitoring/
    processes/
    services/
    logs/
    files/
    firewall/
    ssh/
    users/
    scheduling/
    packages/
    backups/
frontend/
  src/routes/server/
  src/routes/hosting/
  src/routes/website/
  src/routes/developer/
migrations/
packaging/systemd/
tests/
  tenancy/
  integration/
  security/
  resource/
docs/phase-2/
```

Names are illustrative; preserve Phase 1 naming and migrate incrementally rather than introducing duplicate services.

---

## 31. Release readiness checklist

- [ ] Phase 1 ownership/session migration works without account loss.
- [ ] Four personas and workspace memberships implemented.
- [ ] Reseller switching rule explicitly approved.
- [ ] Workspace switching never bypasses authorization.
- [ ] Server administration UI and read-only metrics operational.
- [ ] Approved systemd actions and log viewer operational.
- [ ] Restricted file manager passes filesystem security tests.
- [ ] Firewall and SSH changes validate and recover in tested failure scenarios.
- [ ] Linux users/groups controls protect system accounts.
- [ ] Scheduled tasks cannot become arbitrary root execution.
- [ ] Package updates are bounded, auditable, and recoverable where possible.
- [ ] Panel backup and restore have passed real restore tests.
- [ ] Resource intelligence is integrated into heavy jobs.
- [ ] Tenant isolation tests pass across all four personas.
- [ ] Signed update/release process and vulnerability response documented.
- [ ] 512 MB / 1 GB benchmarks and measured limitations published.
- [ ] Unsupported and future-phase features are clearly identified in UI.
- [ ] Operator runbook, recovery CLI documentation, and audit behavior reviewed.

---

## 32. Phase boundaries and next steps

**Phase 2 delivers:** Persona/workspace foundation, complete Linux administration, resource intelligence, scoped privileged operations, safety, audit, maintenance, and recovery.

**Phase 3 will deliver:** Hosting engine — websites, domains, Nginx, TLS, PHP, databases, hosting accounts, and website-owner functionality. It reuses Phase 2 memberships, file scopes, jobs, audit, service operations, and resource preflight.

**Phase 4 will deliver:** Developer application deployment — Git, native services, builds, environment/secrets handling, release history, optional containers. It reuses Phase 2 identity, project scopes, jobs, isolation, and resource controls.

**Later phases:** Mail hosting, advanced provider commerce/billing, multi-server management, automation, and optional integrations.

### Immediate next action

**Begin with P2.0**: confirm the reseller-switching rule and the administrator support-access policy, then implement workspace schema migrations, membership authorization, and the workspace selector. Only after those permissions are testable should we connect Linux mutations to the privileged helper.

---

## Appendix A — Architecture decision register

| ADR | Decision | Status |
|---|---|---|
| ADR-001 | Management plane separate from optional workload plane | Inherited from Phase 1 |
| ADR-009 | Multiple installer/onboarding paths reuse one Go core | Inherited from Phase 1 |
| ADR-010 | Security-first management architecture | Inherited from Phase 1 |
| ADR-011 | Measurable resource efficiency | Inherited from Phase 1 |
| ADR-012 | Typed privileged operations with independent helper policy | Proposed |
| ADR-013 | Workspace-scoped capability permissions and protected resources | Proposed |
| ADR-014 | Transactional high-risk config changes with independent rollback | Proposed |
| ADR-015 | Durable, auditable jobs for privileged mutations | Proposed |
| ADR-016 | Resource-aware limits for administration tasks | Proposed |
| ADR-017 | Personas are UI contexts; memberships and permissions determine authority | Proposed |
| ADR-018 | No implicit private customer access through parent workspace ownership | Proposed |
| ADR-019 | Phase 2 creates future hosting/developer workspace foundations, not fake runtime features | Proposed |

## Appendix B — Glossary

- **Persona:** Product-facing user category (Server Administrator, Hosting Provider/Agency, Website Owner, Developer).
- **Workspace:** A named server, organization, website, or developer-project context.
- **Membership:** A user's authorized relationship to a workspace.
- **Role:** A set of scoped permissions assigned through a membership.
- **Permission:** A specific allowed operation, such as `system.services.restart`.
- **Resource scope:** The exact host, organization, site, project, or path to which an authorization applies.
- **Support grant:** Time-limited, auditable exceptional access to a resource context.
- **Management plane:** Core API, UI, SQLite, jobs, and administration services.
- **Workload plane:** Hosted websites, apps, databases, mail, and optional runtimes.
- **Privileged helper:** Narrow root-level execution boundary for typed, allowlisted host operations.
- **Resource preflight:** Capacity/safety evaluation before starting an operation.
- **Nano profile:** Resource-constrained VPS, typically 512 MB–1 GB RAM.
- **Outcome unknown:** Job state where a mutation may have happened but confirmation was lost; requires reconciliation.

---

**End of Phase 2 PRD v1.0.**
