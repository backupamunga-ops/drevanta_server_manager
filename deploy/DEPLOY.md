# Deployment runbook — manual, gated (Sprint 1)

> Deployment is **manual only** in Sprint 1. Automatic deployment on push
> is disabled by design until the rollback drill passes. Every step below
> is run by the operator; CI never deploys and never receives host
> credentials.

## Prerequisites

- Build host: Go ≥ 1.23, Node ≥ 20.
- Target host: Linux with systemd, a `linux/amd64` CPU, SSH access with
  sudo/root for the one-time install.
- The dashboard must be built before the binary (it is embedded):

```sh
make build                  # web/build (SvelteKit) then bin/drevantad
make cross-linux-amd64      # static release binary → dist/drevantad-linux-amd64
```

## Install (fresh host)

```sh
# 1. Stage the two files on the host (any root-only staging dir)
scp dist/drevantad-linux-amd64 root@HOST:/root/drevanta-staging/drevantad
scp deploy/drevanta.service    root@HOST:/root/drevanta-staging/

# 2. On the host (as root):
useradd --system --no-create-home --shell /usr/sbin/nologin drevanta
install -d -m 0755 /opt/drevanta
install -m 0755 /root/drevanta-staging/drevantad /opt/drevanta/drevantad
install -m 0644 /root/drevanta-staging/drevanta.service /etc/systemd/system/drevanta.service
rm -rf /root/drevanta-staging

# 3. Start
systemctl daemon-reload
systemctl enable --now drevanta
```

`StateDirectory=drevanta` creates `/var/lib/drevanta` (0700, owned by
`drevanta`) automatically on first start.

## Verify

```sh
systemctl is-active drevanta
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/readyz
curl -fsS http://127.0.0.1:8080/api/v1/server/metrics
ss -tlnp | grep 8080          # must show 127.0.0.1:8080 only
systemd-analyze security drevanta --no-pager | tail -1
journalctl -u drevanta -n 20 --no-pager
```

Operator access (from a workstation with SSH access):

```sh
ssh -N -L 18080:127.0.0.1:8080 root@HOST
# open http://127.0.0.1:18080/
```

## Upgrade

```sh
# stage new binary, then:
systemctl stop drevanta
install -m 0755 /root/drevanta-staging/drevantad /opt/drevanta/drevantad
systemctl start drevanta
# verify as above; keep the previous binary at /opt/drevanta/drevantad.prev
# until the releases/+symlink+timer mechanism lands (planned Sprint 2)
```

## Uninstall

```sh
systemctl disable --now drevanta
rm -f /etc/systemd/system/drevanta.service /opt/drevanta/drevantad
userdel drevanta          # only if no other component uses it
rm -rf /var/lib/drevanta  # destroys state (audit log) — deliberate action
systemctl daemon-reload
```

## Security notes

- The unit binds to `127.0.0.1` only; do not change `--addr` to a public
  interface.
- `systemd-analyze security drevanta` should stay in the low-exposure
  band (measured value recorded in `docs/BENCHMARKS.md`).
- The `drevanta` user has no shell, no home, no sudo membership.
