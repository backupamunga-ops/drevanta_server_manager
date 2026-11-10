# Deployment runbook — installer-driven, gated

> Deployment is **manual only**: automatic deployment on push is disabled
> by engineering order. CI builds and tests; it never deploys and never
> receives host credentials. Every step below is run by the operator.

## Prerequisites

- Build host: Go ≥ 1.23, Node ≥ 20.
- Target host: Linux with systemd, `linux/amd64`, SSH access with
  sudo/root for the one-time install.
- Build the release artifact (the dashboard is embedded, so build web
  first):

```sh
make build                  # web/build (SvelteKit) then bin/drevantad  (dev)
make cross-linux-amd64      # static release binary → dist/drevantad-linux-amd64
```

## Install (one command)

Stage `dist/drevantad-linux-amd64` and the `deploy/` scripts on the host
(any root-only dir), then:

```sh
sudo install.sh preflight              # optional: checks only, changes nothing
sudo install.sh install --binary /root/staging/drevantad
sudo install.sh --uninstall            # remove; add --purge to wipe state
```

### First-time setup screen (locked UX)

After a successful **interactive** installation the installer prints the
one-time setup screen: the SSH tunnel command (real server address when
the session provides it, clearly labelled `<placeholders>` otherwise), a
ready-to-open `http://127.0.0.1:18080/onboarding#token=…` link with the
claim token embedded, and the raw token as fallback. The token expires
after **30 minutes** by default (`--ttl` to change) and is consumed only
by a successful claim — opening the link never consumes it. The
onboarding page prefills the token from the URL fragment and immediately
strips the fragment from the address bar and history.

**Noninteractive** runs (CI, pipes, automation — no TTY) never print the
token: it is delivered via a root-owned 0600 file — `--token-file PATH`,
then `$DREVANTA_TOKEN_FILE`, then `/root/.drevanta-claim-token` — and the
output only names that file. An existing active claim token is never
re-issued by a re-run (re-issuing would rotate and invalidate it).

What the installer does, in order:

1. **Preflight** — root, systemd, OS (Ubuntu/Debian; warns elsewhere),
   `x86_64`, binary sanity (`version` answers), RAM ≥ 256 MiB, ≥ 256 MiB
   free under `/opt` and `/var/lib`, required tools, port 8080 free (or
   held by our own service = upgrade), unit conflicts, service user.
   Any blocking failure exits non-zero **having changed nothing**.
2. **Release layout** — `useradd --system` for `drevanta` (idempotent),
   `/opt/drevanta/releases/<version>/drevantad` (releases are immutable:
   same version + different checksum is refused), atomic
   `current` symlink flip. A pre-existing flat `/opt/drevanta/drevantad`
   (Sprint 1 style) is preserved as release `0.1.0-legacy`.
3. **Unit** — `/etc/systemd/system/drevanta.service` regenerated only
   when its content differs; hardened exactly like Sprint 1
   (unprivileged `drevanta`, `ProtectSystem=strict`, `MemoryMax=256M`,
   …) with `ExecStart=/opt/drevanta/current/drevantad serve --addr
   127.0.0.1:8080 --data-dir /var/lib/drevanta`.
4. **Start + health gate** — enable, restart, poll `GET /readyz` for
   10 s. On failure: journal excerpt is printed and the symlink is
   **automatically rolled back** to the previous release, restarted, and
   re-verified (safe failure / recovery).
5. **Claim token** — only for a fresh (unclaimed) instance with no live
   token: runs `drevantad claim init` as the `drevanta` user (default TTL
   30 minutes) and prints the locked setup screen (interactive) or writes
   a 0600 token file (noninteractive). The token is single-use, expiring,
   and stored hashed server-side.
6. **Summary** — service state, release, loopback endpoint.

Idempotency: re-running with the same release + unit is a verified
no-op (the daemon is **not** restarted); a new release upgrades with
automatic rollback; a claimed instance is not re-issued a token; an
unclaimed instance with a live token keeps that token (no rotation).

## Operator access (SSH tunnel)

```sh
ssh -N -L 18080:127.0.0.1:8080 root@HOST
# open http://127.0.0.1:18080/
```

- Fresh install (interactive) → the installer screen already contains the
  ready-to-open `#token=` link; open it and the token field prefills.
- Token expired or lost? Regenerate on the VPS — one command, prints the
  setup link once and rotates out any previous token:

```sh
runuser -u drevanta -- /opt/drevanta/current/drevantad claim init \
        --data-dir /var/lib/drevanta
```

- Later visits → `/login` (password + authenticator code, or a recovery
  code if the authenticator is lost).

## Verify

```sh
systemctl is-active drevanta
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/readyz
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/api/v1/server/metrics   # 401 = auth enforced
ss -tlnp | grep 8080          # must show 127.0.0.1:8080 only
sudo -u drevanta /opt/drevanta/current/drevantad claim status
systemd-analyze security drevanta --no-pager | tail -1    # 3.7 OK measured
journalctl -u drevanta -n 20 --no-pager
```

## Upgrade

```sh
# stage the new release binary, then:
sudo install.sh install --binary /root/staging/drevantad --token-file /root/.drevanta-claim-token
```

New release dir + symlink flip + restart + `/readyz` gate with automatic
rollback. Onboarding/account/session state survives upgrades (SQLite).
For unattended upgrades, schedule the delayed gate per the locked
design:

```sh
systemd-run --on-active=90s --unit=drevanta-healthgate \
  /opt/drevanta/bin/healthgate.sh <new-release> <previous-release>
```

## Rollback drill — PASSED (2026-11-10, development VPS)

Executed exactly per the locked design (transient timer, no `at`):

| Time (UTC)  | Event                                                                   |
| ----------- | ----------------------------------------------------------------------- |
| 12:36:09    | `systemd-run --on-active=45s --unit=drevanta-healthgate-drill /opt/drevanta/bin/healthgate.sh 9.9.9-drill 0.2.1-sprint2` |
| 12:36:1x    | Broken release `9.9.9-drill` activated (`current` flipped, restart): daemon in restart loop, `readyz` connection refused |
| 12:36:54    | Transient timer fires → gate sees `current == 9.9.9-drill` + `readyz` failing → `rollback.sh 0.2.1-sprint2` |
| 12:36:54    | `current → 0.2.1-sprint2`, service restarted, **`readyz` = ready (same second)** |
| after       | `systemctl is-active` = active; transient timer unit completed and gone |

Journal excerpt:

```
systemd[1]: Started drevanta-healthgate-drill.service - Drevanta upgrade health gate (rollback drill).
healthgate.sh: healthgate: /readyz is failing for release 9.9.9-drill — rolling back to 0.2.1-sprint2
healthgate.sh: ==> Rollback complete: current -> 0.2.1-sprint2, service ready
healthgate.sh: rollback target=0.2.1-sprint2 from=9.9.9-drill result=ready
```

Maximum unavailability window = the gate delay (45 s here, 90 s in the
suggested invocation above). The installer's synchronous gate covers
attended upgrades with a 10 s window.

## Installer test harness

```sh
go build -o /tmp/drevantad ./cmd/drevantad
bash deploy/tests/test_install.sh /tmp/drevantad     # 49 assertions
```

Covers: preflight pass/arch-fail/port-fail, fresh install (release,
symlink, hardened unit, token file 0600, masked stdout, real hashed
token in SQLite), idempotent re-run, upgrade, auto-rollback on a broken
release, rollback failure path, health-gate decisions (healthy → accept;
broken → roll back; manually switched → inert), uninstall.

## Security notes

- The unit binds `127.0.0.1` only; do not change `--addr` to a public
  interface.
- The claim token is shown once and stored only hashed server-side; keep
  `--token-file` root-only (0600) and delete it after onboarding.
- The `drevanta` user has no shell, no home, no sudo membership.
- Uninstall: `sudo install.sh --uninstall` (keeps releases and state;
  `--purge` also deletes `/var/lib/drevanta` — audit log, accounts).
