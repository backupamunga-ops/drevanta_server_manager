# For Developers — manual builds, staged installs, test harness

Everything in this document is a development-only procedure. The
customer journey (one command, HTTPS link, browser onboarding) lives in
[docs/INSTALLATION.md](INSTALLATION.md) and never requires anything
below.

## Prerequisites (build machine)

- Go ≥ 1.25 (the dashboard is embedded, so build web first)
- Node ≥ 20 for the SvelteKit dashboard

## Clone and build

```sh
git clone git@github.com:backupamunga-ops/drevanta_server_manager.git
cd drevanta_server_manager
make build                  # web/build (SvelteKit) then bin/drevantad (dev)
make cross-linux-amd64      # static release binary → dist/drevantad-linux-amd64
```

## Transfer the artifacts to a VPS (manual, dev-only)

Stage `dist/drevantad-linux-amd64` and the `deploy/` scripts on the
host (any root-only dir), then:

```sh
sudo install.sh preflight              # optional: checks only, changes nothing
sudo install.sh install --binary /root/staging/drevantad
sudo install.sh install --binary /root/staging/drevantad --https no   # tunnel-only
sudo install.sh --uninstall            # remove; add --purge to wipe state
```

`install.sh` flags (dev-oriented):

- `--binary PATH` — install a staged binary instead of the release
  payload.
- `--https auto|yes|no` — public HTTPS attempt policy. `auto` (default)
  attempts once on a FRESH install only; `yes` forces an attempt;
  `no` never attempts. Upgrades in `auto` mode never touch the access
  configuration — this is how existing development VPSes are upgraded
  safely.
- `--skip-claim` — issue, rotate and deliver no claim token (the
  operator runs `sudo drevanta claim regenerate` afterwards).
- `--token-file PATH` — additionally deliver the claim token to a
  0600 file (noninteractive installs do this by default).

## Local development and testing

```sh
go test ./...                                   # unit tests
go build -o /tmp/drevantad ./cmd/drevantad
bash deploy/tests/test_install.sh /tmp/drevantad   # installer harness (~170 assertions)

# ACME integration suite against pebble (Let's Encrypt test CA):
# download pebble, then:
DREVANTA_PEBBLE_BIN=/path/to/pebble go test ./internal/httpsd/ -run TestPebble -v

cd web && npm ci && npm run check && npm test && npm run build
```

### Run a local daemon (tunnel-free development)

```sh
rm -rf /tmp/drv-state && mkdir -p /tmp/drv-state
./bin/drevantad serve --addr 127.0.0.1:8080 --data-dir /tmp/drv-state
./bin/drevantad claim init --data-dir /tmp/drv-state   # prints the local setup link once
```

### Public HTTPS locally (test rigs only)

The httpsd package is developed against [pebble](https://github.com/letsencrypt/pebble)
(see the CI `acme` job). Point a config at pebble's directory with
`acme_ca_root` set to its minica PEM and `http01_port`/`tlsalpn_port`
set to unprivileged ports. Never test against the Let's Encrypt
production endpoint from a dev box, and never enable public HTTPS on a
machine you do not own.

## Release process

1. Ensure CI is green on `main`.
2. `git tag v0.X.Y && git push origin v0.X.Y` — the release workflow
   builds, checksums and **signs** the payload (secret
   `DREVANTA_RELEASE_SIGN_KEY`; the release fails without it) and
   publishes the GitHub Release.
3. Public key: `deploy/release-signing/` (also embedded in
   `deploy/bootstrap.sh`). Rotation and manual verification are
   documented there.

## Troubleshooting developer installs

- **`systemd is not running`** in sandboxes — the installer needs a
  real systemd; the test harness fakes the environment, look at
  `deploy/tests/test_install.sh` for the override variables
  (`DREVANTA_*`).
- **HTTPS provisioning fails in a container** — binding ports 80/443
  needs `CAP_NET_BIND_SERVICE`; the harness overrides the ports via
  `DREVANTA_HTTP_PORT`/`DREVANTA_HTTPS_PORT`.
- **Embedded dashboard stale** — re-run `make build` (or
  `npm run build` in `web/`) before `go build`; the dashboard is
  embedded at compile time.
