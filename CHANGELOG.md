# Changelog

All notable changes to Drevanta are documented here.
Format follows Keep a Changelog; versioning is CalVer-ish per sprint
(`0.<sprint>.<stage>`).

## [0.4.11] — interactive completion checklist + domain apply connection fix + fresh UI bundles

### Added
- **The completion screen is now an interactive checklist**: MFA not set
  shows "(x) MFA authentication — Not set [Secure your account]" and the
  link re-opens MFA enrolment in place (new `requireMFAEnrollable` guard:
  enrolment is allowed post-completion while mfa_mode is "skipped"/"" and
  closes once "totp" — audited, recovery codes re-issued, instance stays
  active via the new `SetMFAMode` store method). Using the server IP shows
  "[Configure domain]" which jumps back to the domain configuration
  screen; finishing (or skipping) returns to the checklist with refreshed
  stats and timestamp. Wizard screens in re-entry mode show "← Back to
  checklist".

### Fixed
- **"Enable and verify HTTPS" could fail with "TypeError: Failed to
  fetch"**: the certificate wait legitimately holds the response open for
  up to 90s, but the loopback server (20s) and the TLS edge (60s) write
  deadlines cut the connection first — while provisioning silently
  continued. The apply handler and the proxy now extend the write
  deadline per-request past the wait window, and the UI polls the trusted
  status endpoint for the domain slot outcome instead of showing a hard
  error when the connection is lost.
- **Stale UI after an upgrade**: embedded HTML shells were served without
  cache headers, so browsers heuristically kept serving the previous
  release's bundle (symptom: "not implemented yet" until a hard refresh).
  HTML now ships `Cache-Control: no-store, must-revalidate`, immutable
  hashed assets keep their year-long cache, everything else revalidates.

## [0.4.10] — onboarding: manual-entry secret is a real text field

### Changed
- **Secret (manual entry) is now a real (readonly) text input field** and
  Reveal/Hide swaps the WHOLE field content: hidden shows the grouped
  bullet mask (`•••• •••• …`), revealed shows the entire base32 secret —
  previously the revealed value was ellipsis-clipped inside a static
  `<code>` block. The revealed size is deterministic: the daemon issues a
  fixed 160-bit secret (32 base32 chars, 39 chars grouped), which fits the
  field at 11px, so nothing is ever cut off. Copy still copies the raw
  ungrouped secret in both states. UI-only; no functional logic changed.

## [0.4.9] — onboarding fix round: button color, paste-link autofill, domain apply on fresh installs

### Fixed
- **Domain apply failed on fresh installs** ("could not store the
  domain request"): the daemon unit's `ProtectSystem=strict` made
  `/var/lib/drevanta-access` read-only and the installer never created
  it, so the secret-free domain overlay could not be written and the
  DNS-verified domain could not be applied. The unit now declares
  `StateDirectory=drevanta drevanta-access` (created, owned by the
  service user, writable) and the installer pre-creates the directory.
- **Active onboarding buttons rendered white text instead of the
  prototype's near-black**: a `.ob button { color: inherit }` reset
  out-specified every button's own color rule; the reset now sets font
  only.
- **Pasting the setup link into an already-open onboarding page did
  nothing until a reload**: the token is now adopted on `hashchange`
  (claim step only), so pasting or clicking `…onboarding#token=…`
  autofills immediately.

### Changed
- **MFA manual entry**: the setup secret is masked by default with a
  Reveal/Hide toggle; Copy sits inline inside the field and always
  copies the real secret.
- **MFA instructions below the QR**: "In your authenticator app, tap
  Add account…" and the app-store links moved under the QR row,
  toggled by "How does this work?".
- **DNS instructions panel**: Copy buttons on the Host / Name and
  Points to rows.
- **Completion screen is a checklist**: admin credentials, MFA state
  (secured or a "secure your account in Settings" hint) and the
  domain/IP configuration, with live cpu/memory/disk stats from the
  dashboard metrics endpoint, an "Updated at" timestamp and an explicit
  "Proceed to dashboard" button (auto-redirect removed).
- **Installer success screens**: the onboarding URL is emitted as an
  OSC 8 terminal hyperlink (clickable in Windows Terminal and other
  supporting terminals; plain-text fallback elsewhere).

## [0.4.8] — approved onboarding UI v6

### Changed
- **Onboarding rebuilt to the approved HTML prototype v6, token for
  token**: one shared visual system across all six wizard steps —
  the exact prototype palette (`--bg:#0b1417`, `--card:#142024`,
  `--raised:#1a2b2e`, `--input:#0e191d`, `--line:#304348`,
  `--accent:#83d9bf`, `--accent-deep:#0b2822`, `--danger:#f1aaa1`,
  `--warn:#e2bb7e`), 436px card, prototype typography (650/750/850
  font weights, 13px base size, 9px meta), and prototype spacing
  (gap 11/7, 13px action margins, 10px small spaces).
- **Connect-your-domain screen restructured to the prototype**: the
  13px breathing space between the input field and the Verify DNS
  button, a warning feedback bar plus a neutral raised instructions
  panel with the 88px-label dnsrow table on DNS failure, and raised
  panels with the dns-passed icon on DNS success and HTTPS state.
- **Flow copy matches the approved prototype** ("Create your admin
  account", "Scan and confirm", "Connect your domain", "You're all
  set", …) with the same option cards, action rows and dividers.

### Unchanged
- **Functional logic byte-identical**: every handler, state machine
  (`verifiedDomain`, monotonic `dnsSeq` stale guards, domain stage
  flow), API contract and guard is untouched — svelte-check 0/0,
  37/37 unit tests, full Go suite, production build green; the whole
  flow re-driven end-to-end on a throwaway local daemon (real claim,
  real TOTP, real DNS checks, real apply-failure fixture).

## [0.4.7] — onboarding polish: compact scale, prototype MFA screen, installer spacing

### Changed
- **Onboarding UI compact scale, tuned after review**: the brand topbar
  (with a live access-mode pill fed by the same `/api/v1/access/status`
  the dashboard and sign-in page use) now sits above the card on every
  step, and the card, typography, inputs, buttons, QR and every wizard
  step share one reduced frame — the explicit compact values carry the
  review-time "-20%" pass and a uniform +10% zoom on the shared shell
  lands the final surface at ~0.88x of the 0.4.6 frame (mobile keeps
  its own unzoomed spacing), so the flow no longer looks zoomed in.
- **MFA setup screen implements the approved prototype faithfully**:
  QR code beside the copy text, the setup key in a grouped
  copy-inside-the-field input (the copy button copies the ungrouped
  secret), six single-digit code boxes with auto-advance, paste
  support, arrow/backspace navigation, a red invalid state with the
  shake animation and an inline "That code didn't work…" message, and
  a centered "MFA enabled" success view before the recovery codes.
  Same endpoints and flow: no change to enrolment, skip, or the
  audited risk acknowledgement.
- **Installer success screen spacing**: the "OPEN DREVANTA IN YOUR
  BROWSER" link block is framed by light horizontal rules with
  breathing room so the single action stands out in terminal output
  (pinned by the installer harness).

### Fixed
- `QR_SIZE` lowered to 120 to match the rendered size, keeping the
  embedded SVG and the `.oqr svg` CSS rule in sync.

## [0.4.6] — access-status readability fix

### Fixed
- **`/api/v1/access/status` reported `cert_valid:false` (and the claim
  screen fell back to the tunnel link) on real installs**: the
  `drevanta-https` unit's `UMask=0027` masked the secret-free
  `status.json` down to `0640`, so the unprivileged daemon user could
  not read it. `WriteStatus` now enforces `0644` explicitly (pinned by
  a umask-restrictive regression test). No other file or permission
  changed; certificate private keys remain `0600`.

## [0.4.5] — post-install UX & maintenance: optional MFA, custom domain, installer cleanup

### Added
- **Optional MFA ("Skip for now")** — the initial administrator can
  complete onboarding without MFA via an explicit two-step warning and
  a machine-readable risk acknowledgement (`acknowledge_risk: true`);
  the choice is audited (`mfa_skipped`). A skipped administrator enters
  the dashboard immediately and can enable MFA at any time under the
  new **Settings → Account Security** page; recovery codes are always
  issued when MFA is enabled. Enrolled administrators keep every
  protection (login still requires TOTP or a recovery code).
- **Optional custom domain** — the onboarding wizard offers a compact
  "server IP (default) / I have a domain" choice, and Settings can
  configure or change the domain later. The backend validates the
  hostname, verifies DNS A/AAAA against the VPS's own addresses, and
  the httpsd proxy gains an independent SECOND certificate slot: the
  IP certificate is always the primary slot and can never be displaced
  by a domain operation. `apply` succeeds only after the certificate is
  active AND the full TLS+proxy+backend route is verified; any failure
  rolls the overlay back, so working IP HTTPS is never disturbed
  (pinned by pebble and unit tests). SNI routes domain handshakes to
  the domain certificate and IP/SNI-less handshakes to the primary one;
  both slots renew independently. No Drevanta cloud account involved.
- **Trusted dashboard access status** — `GET /api/v1/access/status`
  (public, secret-free) reports the real access mode from the
  root-written access configuration and the proxy status file. The
  dashboard header/footer and the sign-in subtitle now show
  "HTTPS secured" or "Private · SSH tunnel" from that source instead of
  hardcoded labels; nothing is guessed from the browser URL.

### Fixed
- **Installer false "port 80 already in use" on upgrades** — the
  enable-https precheck now recognizes its own running
  `drevanta-https` unit (systemctl exit status) and proceeds; only a
  foreign listener is a conflict. Harness scenarios added for fresh
  installs, upgrades with an active HTTPS service, and genuine
  conflicts; certificates, renewal state, config, administrator data
  and rollback releases are preserved.
- **Installer completion output cleanup** — the HTTPS success screen
  shows exactly one action: the verified HTTPS onboarding link with the
  single-use token in its URL fragment. No localhost URLs, tunnel
  commands or backend addresses are printed on success; failures show
  the actual reason plus the SSH-tunnel fallback, and the tunnel is
  documented under troubleshooting/advanced access. The success screen
  is unreachable until issuance AND readiness are verified.
- CI `push` trigger repaired (`branches: [main]`; a corrupted literal
  had disabled push builds).

### Changed
- MFA onboarding step is compact (matches the approved administrator
  form density) while keeping the scannable QR, manual secret and
  6-digit verification; the dashboard "mfa off" pill links to Account
  Security; customer docs updated for both installer outcomes.

## [0.4.4] — production HTTPS hotfix: ACME profile + MFA fixes

### Fixed
- **Public HTTPS provisioning failed on Let's Encrypt production** with
  "Default profile does not permit IP address identifiers." The
  production directory advertises ACME profiles under `meta.profiles`,
  not at the top level the way test CAs do; the profile-selection code
  only inspected the top level, silently dropped the requested
  `shortlived` profile, and the default-profile order was rejected for
  IP identifiers. Profiles are now read from both directory shapes, and
  the new-order request carries `profile: shortlived` for IP
  certificates.
- **Fail closed on profile selection for IP identifiers**: if the CA
  does not advertise the required profile (or the directory cannot be
  read), issuance aborts with a clear error instead of silently
  requesting a default-profile order. DNS-name orders keep the default
  fallback.
- **MFA QR code rendered as a blank white pill** (reported as "shows
  the literal text svg"): the `qrcode` SVG carried only a `viewBox` and
  no intrinsic size, collapsing to 0x0 inside the fit-content
  container. The QR is now generated with explicit width/height and
  sized via CSS. Regression-tested.

### Changed
- **MFA is now mandatory for the initial administrator** (no skip
  path): the onboarding wizard goes straight from administrator
  creation to authenticator enrolment, the `mfa/skip` API endpoint was
  removed, and the resume flow re-enters enrolment directly. Instances
  that skipped before this change keep working (password-only login).
- Pebble CI rig now advertises the `shortlived` profile so the ACME
  integration exercises the exact production path (profile included in
  the new-order request).

## [0.4.3] — release integrity

### Fixed
- **Binary version now matches the release tag.** Tag `v0.4.2` shipped a
  binary self-reporting `0.4.1-access` (cosmetic, but it confused release
  identification and the installer's release-directory naming). The release
  workflow now stamps `main.version` from the pushed tag and fails closed if
  the built binary does not report it exactly.

### Added
- **Proprietary license notice** (`LICENSE`, README "License" section,
  `license: "UNLICENSED"` in the dashboard's `package.json`): Drevanta is
  proprietary software; repository visibility grants no redistribution or
  relicensing rights, and release artifacts may be installed and evaluated
  on servers you own or control under the evaluation grant.

## [0.4.0-access] — one-command install, direct HTTPS to the VPS IP, compact onboarding

### Added
- **Public HTTPS access layer (`internal/httpsd`, `drevantad https-serve`)** —
  a separate minimal reverse proxy in front of the loopback-only daemon:
  publicly trusted certificate for the VPS **public IP address** (RFC 8738)
  via the Let's Encrypt `shortlived` profile through the maintained lego
  ACME library; tls-alpn-01 and http-01 answered by the proxy's own
  listeners; automatic renewal (hourly check, renew at 2/3 lifetime or
  <48 h remaining); HSTS; port 80 serves only the ACME challenge and
  redirects; fail closed without a certificate; strict trusted-proxy
  boundary (inbound `Forwarded`/`X-Forwarded-*` stripped,
  `X-Forwarded-Proto/For/Host` set exactly once, browser `Host` passed
  through so the backend's CSRF checks keep working).
- **`drevanta access status | enable-https | disable-https | configure`** —
  root-only CLI managing the separate hardened `drevanta-https.service`
  unit (dedicated unprivileged user + `CAP_NET_BIND_SERVICE` only) and the
  secret-free `/etc/drevanta/https.json`. Retrying HTTPS never requires a
  reinstall; disabling never stops the backend or touches data.
- **Installer `--https auto|yes|no`** — a fresh install attempts public
  HTTPS once automatically and prints the locked §6 success screen
  (`https://YOUR_VPS_IP/onboarding#token=…`, "No domain required."); on
  failure it prints the reason plus the SSH-tunnel fallback (explicitly
  "on your computer", with the already-connected-user guidance and the
  `enable-https` retry hint). Upgrades never change the access
  configuration on their own. `--uninstall` also stops the proxy unit.
- **Release authenticity**: `checksums.txt` is signed with Ed25519
  (`checksums.txt.sig`); `bootstrap.sh` verifies the signature against the
  embedded official public key **before executing anything** and fails
  closed on a missing signature, wrong key or verification failure. The
  private key exists only as the `DREVANTA_RELEASE_SIGN_KEY` Actions
  secret; the release workflow refuses to publish unsigned artifacts.
- **CI `acme` job** — the httpsd integration suite runs against pebble
  (Let's Encrypt's test ACME server): IP-identifier order, challenge
  solving through the real proxy listeners, renewal, fail-closed states.
- **`docs/FOR-DEVELOPERS.md`** — manual builds, staged installs, test
  harness, local pebble rigs and the release process.

### Changed
- **Onboarding UI compacted** per the approved spec: the "Claim this
  instance" heading, intro paragraph and token explanation are gone; the
  token chip is now one horizontal row — green check, "Token loaded",
  masked `drv1_•••XXXX`, functional **Change**; 520 px card, 40 px
  inputs, reduced gaps; no permanent character checklist or argon2id
  hint; quiet validation (errors on blur/submit only, cleared as soon as
  the value is valid, first invalid field focused, token and values
  preserved); help disclosure teaches the HTTPS-first journey with the
  SSH fallback.
- **docs/INSTALLATION.md rewritten** around the real customer journey
  (buy VPS → SSH → one command → open setup link → administrator → MFA →
  recovery codes → dashboard); development procedures moved to
  `docs/FOR-DEVELOPERS.md`.
- Secure session cookies whenever the request arrives over TLS or
  through the trusted proxy; per-client rate limiting behind the proxy
  uses the proxy-set `X-Forwarded-For` (one hop of truth) instead of
  collapsing all clients into the loopback bucket.
- The claim setup screen prints the HTTPS link automatically whenever a
  valid certificate is live, otherwise the SSH-tunnel link.
- Go toolchain 1.25 (required by lego v4.35).

### Security
- Port 80 never exposes the backend (challenge + redirect only); no
  public HTTP fallback exists in any screen or code path.
- ACME account key and certificate private keys live 0600 in the proxy
  state directory; the status file is secret-free and world-readable.
- The development VPS remains unclaimed with public HTTPS **off**;
  enabling it there requires explicit operator authorization.

## [Unreleased] — installation UX & documentation correction

Makes the SSH-first installation journey the spine of the user guide
and prepares (but deliberately does **not** activate) the public
one-command installer: the repository is private and no release
artifacts exist, so no installation URL is functional yet and none is
presented as such.

### Added
- **`deploy/bootstrap.sh`** — the public one-command entry point,
  implemented and harness-tested but **not published**. Resolves the
  requested release over HTTPS (tag or `latest` via the GitHub
  redirect), downloads the release payload (daemon, installer, wrapper,
  rollback/healthgate scripts), **verifies every artifact against the
  release's `checksums.txt` strictly before executing anything**, and
  then runs the existing tested `deploy/install.sh` — no installation
  logic is duplicated. HTTPS-only: non-HTTPS sources are refused and
  redirects may not downgrade the protocol; any verification failure
  aborts with nothing changed on the host.
- **`.github/workflows/release.yml`** — tag-triggered release
  publishing (prepares the delivery endpoint; creates nothing until a
  `v*` tag is pushed): builds the release payload, emits `checksums.txt`,
  verifies the checksum round-trip, and publishes the GitHub Release
  with all six artifacts including `bootstrap.sh`.
- Installer harness: `test_bootstrap_installer` — end-to-end against a
  GitHub-shaped local fixture server (real curl): latest-release
  redirect resolution, verified download → real `install.sh` → install
  with the Sprint 2.5 token-delivery policy intact (0600 file, nothing
  in stdout), plus fail-closed refusals for a tampered artifact, a
  missing checksum entry, a missing artifact and a plain-HTTP source.
  109 → 123 assertions.

### Changed
- **`docs/INSTALLATION.md` restructured around the user journey** —
  Step 1 Connect (`ssh root@YOUR_VPS_IP`) through Step 7 dashboard;
  Step 2 shows the verified one-command installer clearly marked
  **planned** (with its release source and verification mechanism) and
  names today's temporary development procedure without making
  developer steps a user prerequisite. Manual artifact handling moved
  to **For Developers — Manual Installation & Building from Source**
  (clone, build, cross-compile, transfer/SCP, running
  `deploy/install.sh` directly, local testing, troubleshooting).
- **README** links the user guide and the developer instructions
  separately, and no longer describes installation as a one-command
  operation until it actually is one.

### Security
- No runtime code changed: daemon, CLI, installer, token handling and
  the deployed VPS are untouched. The bootstrap adds no privileges and
  changes no defaults; it only stages and verifies artifacts before
  invoking the tested installer.

## [0.3.5-onboard-ui] — 2026-11-10

Fix: untouched field errors were rendered on the claim form. The port
kept the `hidden` attribute for error paragraphs and the caps-lock hint
but dropped the prototype's `[hidden]{display:none !important}` guard,
and `.oerr`'s `display:flex` outranked the UA default — so "Choose an
administrator username." and friends showed before any interaction.
The guard is restored (scoped to the onboarding page); validation
logic, wording and all other behaviour are unchanged.

## [0.3.4-onboard-ui] — 2026-11-10

Sprint 2.7: the approved onboarding UI/UX prototype is now the real
onboarding. The claim wizard reproduces the prototype's dark design
(grid + glow, centered card, green accents) with the locked "Setup
token loaded" status, while the backend API, token semantics and the
real MFA → recovery-code journey are unchanged.

### Added
- **Approved claim design** — the onboarding page now renders the
  approved standalone full-screen design: dark background with ambient
  green glow and subtle grid, centered card with brand header and
  loopback-only pill, green token-status chip, password strength meter
  with live requirement checks, password visibility toggles, secure
  client-side password generation, caps-lock hint, field-level errors
  with focus management, aria-live announcements, disclosure help and
  responsive/reduced-motion behaviour. The dashboard shell is unchanged
  for /login and /; only /onboarding renders bare.
- **Locked token status** — a token loaded from the setup link replaces
  the token input with the approved green chip: **Setup token loaded**,
  "Automatically added from your setup link. It will be checked when you
  create your account." The wording deliberately does not claim backend
  verification — the single-use token is validated and consumed only by
  a successful claim, exactly as before. No pre-flight verification API
  was added. A Change button reveals manual token entry at any time.
- **Manual fallback parity** — the manual input accepts a raw token or
  the complete setup link (whitespace a mail client wrapped into the
  value is healed, delimiters cut); a valid paste is adopted into the
  chip ("Pasted just now"); a malformed link value keeps the raw text
  with an explanatory hint.
- **Backend-rejection handling** — expired/used/invalid tokens keep the
  tried value and the rest of the form, explain the reason, and point at
  the documented `sudo drevanta claim regenerate` fallback. Username and
  password survive every validation or claim failure.
- **Client-side validators mirror the daemon** — username 3–32 chars
  `[a-zA-Z0-9._-]`, password 12–128 chars (the 128 max is new, matching
  the backend), plus the prototype's "different from username" check.
  The backend remains authoritative.

### Changed
- The illustrative "Administrator created" screen of the prototype is
  intentionally not used as an end state: after a successful claim the
  wizard continues into the real MFA enrolment, recovery-code display
  and dashboard hand-off, restyled with the same design language. The
  old wizard step chips are dropped (the prototype has no step
  indicator).

### Security
- The token is held in component memory only — never in localStorage,
  sessionStorage, cookies, logs or analytics — and the fragment is still
  scrubbed from the address bar and history immediately on load
  (Performance Navigation fallback retained). Opening the onboarding
  page still never consumes the token; no new API surface exists.
- All existing protections are untouched: single-use/expiring hashed
  tokens, argon2id, origin guards, rate limits, audit logging.

### Tests
- Web unit tests 12 → 28: token-shape checks, setup-link paste healing
  (wrapped lines, query/fragment links, delimiters), raw fragment
  param extraction, token masking, username/password/confirm/token
  validators incl. daemon boundaries, exact prototype strength scoring.
- Browser E2E against a throwaway local instance: setup link → chip +
  scrubbed address bar (exactly one `history.replaceState`), Change →
  manual, full-link paste → chip, field-level validation with preserved
  token/form state, real claim → TOTP verify → 10 recovery codes →
  acknowledgement → dashboard; rejected (rotated) token shows the
  regenerate fallback with the form intact; malformed fragment falls
  back to manual; no token in storage; mobile layout and keyboard tab
  order verified.

## [0.3.3-claim-regen] — 2026-11-10

Sprint 2.6: onboarding completion & documentation. Extends the locked
Sprint 2.5 first-time UX with a friendly manual fallback — one simple,
documented command re-issues the setup link — and a complete,
first-time-owner installation guide. No working 2.5 behaviour changed.

### Added
- **`sudo drevanta claim regenerate`** — the operator-facing fallback
  for a lost or expired setup link. `deploy/drevanta.sh` is installed
  by the installer as `/usr/local/bin/drevanta`: it runs the claim
  subcommands as the unprivileged `drevanta` service user, refuses to
  start the daemon manually (`serve` is systemd's job), and passes the
  operator's real SSH session (server address + login user, recovered
  first-party from the current utmp entry) to the printed tunnel hint.
  Bare `drevanta` prints a short usage; `drevanta --help` explains the
  subcommands and points at the installation guide.
- **`drevantad claim regenerate`** — the underlying subcommand. Shares
  the single token-issuance path with `claim init` (only while the
  instance is unclaimed, rotates out the previous token, stores only
  the SHA-256 hash, prints the plaintext exactly once, writes an
  audit event `claim_token_regenerated` without the token). Its setup
  screen differs from `init` only in the headline ("NEW CLAIM TOKEN
  ISSUED") and an explicit "the previous claim token (if any) is now
  invalid" note. Refused permanently once an owner exists.
- **`install.sh --skip-claim`** — operator-driven upgrades where token
  delivery is deliberately deferred: the installer issues, rotates and
  delivers nothing, and prints the documented next step
  (`sudo drevanta claim regenerate`). Also accepted as
  `DREVANTA_SKIP_CLAIM=1`.
- **`docs/INSTALLATION.md`** — complete first-time installation guide
  for a first-time VPS owner: prerequisites/OS support, one-command
  install, example success screen (redacted token), automatic token
  delivery, manual regeneration, Windows PowerShell and Linux/macOS
  tunnel instructions, opening the token-prefilled URL, creating the
  first administrator, MFA + recovery codes, sign-in, troubleshooting
  (expired tokens, closed terminals, tunnel/port problems, wrong
  URLs), why Drevanta is loopback-only by default, and the future
  optional public-HTTPS model. Ends with a command reference table.
- Installer harness: docs↔CLI parity test — every command documented
  in the guide must exist in `drevantad`'s own usage output, the guide
  must teach the real tunnel/link formats, and README must link it.

### Changed
- The installer's rotation hints and the noninteractive delivery hint
  now advertise `sudo drevanta claim regenerate` instead of the
  technical `runuser`/`sudo -u` form (the long form still works).
- The interactive success screen adds a "Lost this link or it expired?"
  line pointing at the regeneration command.
- Uninstall removes the front-end (`$BIN_LINK_DIR/drevanta`).
- README links the installation guide prominently and documents the
  `drevanta` front-end; DEPLOY.md uses the friendly command.

### Security
- The front-end adds no network surface and no privilege: commands run
  as the unprivileged `drevanta` user exactly as before, tokens are
  still printed only once to the invoking terminal, and the wrapper
  cannot touch the daemon process itself (systemd-only `serve`).
- `--skip-claim` gives deployment flows a way to guarantee "no token
  was minted or rotated" — asserted by a new harness test.

### Tests
- Go: 10 new tests in `cmd/drevantad` — regenerate happy path (token +
  full screen, stdout carries only the token line), rotation
  invalidation (previous token revoked, only the new one active),
  rejection after claiming (no token printed, non-zero exit, nothing
  rotated), init/regenerate output consistency, real-session info via
  `DREVANTA_SSH_HOST`/`DREVANTA_SSH_USER`, usage lists `regenerate`,
  TTL flag respected, fresh data-dir creation, sshHint precedence.
- Installer harness: 80 → 112 assertions — the `drevanta` front-end
  (status/version/regenerate through the wrapper, `serve` refusal,
  usage advertises regenerate), `--skip-claim` (no token in output,
  none minted), docs↔CLI parity, uninstall removes the front-end.

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
