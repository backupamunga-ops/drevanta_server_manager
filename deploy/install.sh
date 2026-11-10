#!/usr/bin/env bash
# ============================================================================
# Drevanta installer — one command, verified installation.
#
#   install.sh                install (or idempotently re-run) drevantad
#   install.sh preflight      run all preflight checks without changing anything
#   install.sh --uninstall    remove the service (state kept unless --purge)
#
# What it does:
#   1. Preflight: OS, architecture, systemd, resources, permissions, port
#      conflicts, tool availability — with clear diagnostics.
#   2. Installs the release under /opt/drevanta/releases/<version>/ and
#      points the /opt/drevanta/current symlink at it (atomic flip).
#   3. Installs the hardened, unprivileged systemd unit (user "drevanta")
#      and enables + starts it.
#   4. Installs the friendly "drevanta" front-end (sudo drevanta claim
#      regenerate, sudo drevanta access ...) in $BIN_LINK_DIR.
#   5. Waits for GET /readyz. On failure it rolls the symlink back to the
#      previous release automatically and reports the journal excerpt.
#   6. On a FRESH (unclaimed) install it attempts public HTTPS
#      provisioning once (--https auto|yes|no): a publicly trusted
#      certificate for the VPS public IP via the separate drevanta-https
#      proxy. On success the setup link is https://YOUR_VPS_IP/...; on
#      failure it prints the SSH-tunnel fallback and a retry command.
#      Upgrades never change the access configuration on their own.
#   7. For a fresh (unclaimed) instance it issues the single-use, expiring
#      first-owner claim token and prints it ONCE (optionally also to
#      --token-file, mode 0600). --skip-claim defers the token entirely.
#
# Re-running is safe: same version + same checksum → no-op; new version →
# upgrade with automatic rollback on failed readiness.
#
# Exit codes: 0 ok · 1 failed check or operation · 2 usage error
# ============================================================================
set -euo pipefail

# --- Overridable locations (tests point these at a temp sandbox) ------------
OPT_DIR="${DREVANTA_OPT_DIR:-/opt/drevanta}"
STATE_DIR="${DREVANTA_STATE_DIR:-/var/lib/drevanta}"
UNIT_PATH="${DREVANTA_UNIT_PATH:-/etc/systemd/system/drevanta.service}"
SERVICE_NAME="${DREVANTA_SERVICE_NAME:-drevanta}"
RUNUSER="${DREVANTA_RUNUSER:-runuser}"   # empty string disables privilege drop (tests)
DAEMON_USER="${DREVANTA_DAEMON_USER:-drevanta}"
PORT="${DREVANTA_PORT:-8080}"
CLAIM_TTL="${DREVANTA_CLAIM_TTL:-30m}"
BIND_ADDR="${DREVANTA_BIND_ADDR:-127.0.0.1}"
# Local port of the documented SSH tunnel (locked first-time-setup UX).
TUNNEL_LOCAL_PORT="${DREVANTA_TUNNEL_LOCAL_PORT:-18080}"
# Where the friendly "drevanta" front-end is installed.
BIN_LINK_DIR="${DREVANTA_BIN_LINK_DIR:-/usr/local/bin}"
# Public HTTPS on fresh install: auto (attempt once when prerequisites
# allow), yes (always attempt, incl. upgrades), no (never). Upgrades in
# auto mode NEVER touch the access configuration.
HTTPS_MODE="${DREVANTA_HTTPS_MODE:-auto}"
# Max seconds to wait for the first certificate during install.
HTTPS_WAIT="${DREVANTA_HTTPS_WAIT:-90}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEFAULT_BINARY="${SCRIPT_DIR}/../dist/drevantad-linux-amd64"

STEP=""
CURRENT_RELEASE_BEFORE=""
RELEASE_REWRITTEN=0
UNIT_CHANGED=0
SYMLINK_CHANGED=0

log()  { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
info() { printf '    %s\n' "$*"; }
ok()   { info "✓ $*"; }
warn() { printf '\033[1;33mWARN\033[0m %s\n' "$*"; }
error() { printf '\033[1;31mERROR\033[0m %s\n' "$*" >&2; }
die()  { error "$*"; exit 1; }

on_error() {
        local exit_code=$?
        printf '\n\033[1;31mInstallation failed at step: %s (exit %d)\033[0m\n' "${STEP:-unknown}" "$exit_code" >&2
        cat >&2 <<EOF

Recovery hints:
  * Nothing destructive has happened to your system: the previous release
    (if any) is still in place under ${OPT_DIR}/releases/.
  * Inspect the service logs:   journalctl -u ${SERVICE_NAME} -n 50 --no-pager
  * After fixing the cause, simply re-run this installer — it is idempotent.
  * To force a rollback to the previous release:
      ${OPT_DIR}/bin/rollback.sh
EOF
        exit "$exit_code"
}
trap on_error ERR

# ---------------------------------------------------------------------------
# Small command shims (tests override these)
# ---------------------------------------------------------------------------
sys_daemon_reload() { systemctl daemon-reload; }
sys_enable()        { systemctl enable "$1" >/dev/null; }
sys_restart()       { systemctl restart "$1"; }
sys_is_active()     { systemctl is-active "$1" >/dev/null 2>&1; }
http_get()          { curl -fsS --max-time 3 "$1" 2>/dev/null; }

# ---------------------------------------------------------------------------
# Preflight checks. Each check_* function prints diagnostics and returns
# non-zero on a hard failure. Warnings do not block.
# ---------------------------------------------------------------------------
CHECK_FAILURES=0

check_root() {
        if [[ "${DREVANTA_SKIP_ROOT_CHECK:-0}" == "1" ]]; then
                warn "root check skipped (DREVANTA_SKIP_ROOT_CHECK=1, test mode)"
                return 0
        fi
        if [[ $(id -u) -ne 0 ]]; then
                error "must run as root (sudo). Current uid: $(id -u)"
                return 1
        fi
        ok "running as root"
}

check_systemd() {
        local run_dir="${DREVANTA_SYSTEMD_RUN_DIR:-/run/systemd/system}"
        if [[ ! -d "$run_dir" ]]; then
                error "systemd is not running (no $run_dir); Drevanta requires systemd"
                return 1
        fi
        if ! command -v systemctl >/dev/null 2>&1; then
                error "systemctl not found"
                return 1
        fi
        ok "systemd is active"
}

check_os() {
        if [[ ! -r /etc/os-release ]]; then
                error "/etc/os-release not readable — unsupported or exotic system"
                return 1
        fi
        # shellcheck disable=SC1091
        . /etc/os-release
        case "${ID:-unknown}" in
                ubuntu|debian)
                        ok "OS: ${PRETTY_NAME:-unknown}"
                        ;;
                *)
                        warn "untested OS '${ID:-unknown}' (${PRETTY_NAME:-?}) — continuing, systemd + x86_64 still required"
                        ;;
        esac
}

check_arch() {
        local arch
        arch="$(uname -m)"
        if [[ "$arch" != "x86_64" ]]; then
                error "unsupported architecture '$arch': this build is linux/amd64 (x86_64)"
                return 1
        fi
        ok "architecture: x86_64"
}

check_binary() {
        local bin="$1"
        if [[ ! -f "$bin" ]]; then
                error "drevantad binary not found at $bin (build it with: make cross-linux-amd64, or pass --binary)"
                return 1
        fi
        if [[ ! -x "$bin" ]]; then
                error "$bin is not executable"
                return 1
        fi
        local version
        if ! version="$("$bin" version 2>/dev/null)"; then
                error "$bin did not answer 'version' — not a drevantad binary?"
                return 1
        fi
        ok "binary OK: $bin (version $version)"
}

check_ram() {
        local total_kib
        total_kib="$(awk '/MemTotal/ {print $2}' /proc/meminfo 2>/dev/null || echo 0)"
        if [[ "$total_kib" -lt 262144 ]]; then
                error "not enough RAM: $((total_kib / 1024)) MiB total, need at least 256 MiB"
                return 1
        fi
        if [[ "$total_kib" -lt 524288 ]]; then
                warn "RAM $((total_kib / 1024)) MiB is below the 512 MiB-class target — will run, but tight"
        else
                ok "RAM: $((total_kib / 1024)) MiB"
        fi
}

check_disk() {
        local path free_kib
        for path in "$(dirname "$OPT_DIR")" "$(dirname "$STATE_DIR")"; do
                free_kib="$(df -Pk "$path" 2>/dev/null | awk 'NR==2 {print $4}' || echo 0)"
                if [[ -z "$free_kib" || "$free_kib" -lt 262144 ]]; then
                        error "not enough disk space on $path: $((free_kib / 1024)) MiB free, need at least 256 MiB"
                        return 1
                fi
                if [[ "$free_kib" -lt 524288 ]]; then
                        warn "disk $path has $((free_kib / 1024)) MiB free (below the comfortable 512 MiB)"
                else
                        ok "disk $path: $((free_kib / 1024)) MiB free"
                fi
        done
}

check_tools() {
        local t missing=""
        for t in useradd getent install awk df uname ss curl diff sha256sum; do
                command -v "$t" >/dev/null 2>&1 || missing="$missing $t"
        done
        if [[ -n "$missing" ]]; then
                error "missing required tools:$missing"
                return 1
        fi
        if [[ -n "$RUNUSER" ]] && ! command -v "$RUNUSER" >/dev/null 2>&1; then
                error "missing required tool: $RUNUSER (needed to run the daemon CLI as the service user)"
                return 1
        fi
        ok "required tools present"
}

check_port() {
        local listener
        listener="$(ss -tlnH "sport = :$PORT" 2>/dev/null | awk 'NR>0 {print $4, $6}' || true)"
        if [[ -z "$listener" ]]; then
                ok "port $PORT is free"
                return 0
        fi
        # During an upgrade our own service may already hold the port.
        if sys_is_active "$SERVICE_NAME" && echo "$listener" | grep -q "127.0.0.1:$PORT"; then
                ok "port $PORT held by existing $SERVICE_NAME service (upgrade)"
                return 0
        fi
        error "port $PORT is already in use:"$'\n'"        $listener"$'\n'"        Stop that service or choose another port with DREVANTA_PORT."
        return 1
}

check_service_conflicts() {
        # A foreign unit with our name but a different ExecStart would fight us.
        if [[ -f "$UNIT_PATH" ]] && ! grep -q "$OPT_DIR/current/drevantad" "$UNIT_PATH" 2>/dev/null; then
                warn "existing unit $UNIT_PATH does not use $OPT_DIR/current — it will be replaced by this installer"
        fi
        # Known conflicting managers (deliberate architectural exclusions).
        for svc in drevanta-container drevanta-docker; do
                if systemctl list-unit-files 2>/dev/null | grep -q "^$svc"; then
                        warn "found legacy/conflicting unit $svc — consider removing it"
                fi
        done
        ok "no service conflicts"
}

check_user() {
        if getent passwd "$DAEMON_USER" >/dev/null 2>&1; then
                ok "system user '$DAEMON_USER' exists"
        else
                ok "system user '$DAEMON_USER' will be created"
        fi
}

run_preflight() {
        local bin="$1"
        log "Preflight checks"
        CHECK_FAILURES=0
        check_root || CHECK_FAILURES=$((CHECK_FAILURES + 1))
        check_systemd || CHECK_FAILURES=$((CHECK_FAILURES + 1))
        check_os || CHECK_FAILURES=$((CHECK_FAILURES + 1))
        check_arch || CHECK_FAILURES=$((CHECK_FAILURES + 1))
        check_binary "$bin" || CHECK_FAILURES=$((CHECK_FAILURES + 1))
        check_ram || CHECK_FAILURES=$((CHECK_FAILURES + 1))
        check_disk || CHECK_FAILURES=$((CHECK_FAILURES + 1))
        check_tools || CHECK_FAILURES=$((CHECK_FAILURES + 1))
        check_port || CHECK_FAILURES=$((CHECK_FAILURES + 1))
        check_service_conflicts || CHECK_FAILURES=$((CHECK_FAILURES + 1))
        check_user || CHECK_FAILURES=$((CHECK_FAILURES + 1))
        if [[ "$CHECK_FAILURES" -gt 0 ]]; then
                echo >&2
                die "preflight failed with $CHECK_FAILURES blocking problem(s); nothing was changed"
        fi
        echo
}

# ---------------------------------------------------------------------------
# Install steps
# ---------------------------------------------------------------------------
bin_version() {
        "$1" version 2>/dev/null
}

sha_of() {
        sha256sum "$1" 2>/dev/null | awk '{print $1}'
}

current_release() {
        if [[ -L "$OPT_DIR/current" ]]; then
                basename "$(readlink "$OPT_DIR/current")"
        else
                echo ""
        fi
}

create_service_user() {
        STEP="create service user"
        if getent passwd "$DAEMON_USER" >/dev/null 2>&1; then
                info "user $DAEMON_USER already exists"
        else
                useradd --system --no-create-home --shell /usr/sbin/nologin "$DAEMON_USER"
                info "created system user $DAEMON_USER (no shell, no home)"
        fi
}

install_release() {
        STEP="install release"
        local bin="$1" version="$2"
        local release_dir="$OPT_DIR/releases/$version"
        mkdir -p "$OPT_DIR/releases" "$OPT_DIR/bin"

        if [[ -f "$release_dir/drevantad" ]]; then
                local existing new
                existing="$(sha_of "$release_dir/drevantad")"
                new="$(sha_of "$bin")"
                if [[ "$existing" == "$new" ]]; then
                        info "release $version already installed (checksum match)"
                else
                        die "release $version exists with a different checksum — releases are immutable; build with a new version or clean $release_dir"
                fi
        else
                mkdir -p "$release_dir"
                install -m 0755 "$bin" "$release_dir/drevantad"
                RELEASE_REWRITTEN=1
                info "installed release $version (sha256 $(sha_of "$bin" | cut -c1-12)…)"
        fi

        # Operations helpers.
        install -m 0755 "${SCRIPT_DIR}/rollback.sh"   "$OPT_DIR/bin/rollback.sh"   2>/dev/null || true
        install -m 0755 "${SCRIPT_DIR}/healthgate.sh" "$OPT_DIR/bin/healthgate.sh" 2>/dev/null || true
        # Friendly operator front-end: sudo drevanta claim regenerate
        if [[ -f "${SCRIPT_DIR}/drevanta.sh" ]]; then
                mkdir -p "$BIN_LINK_DIR" 2>/dev/null || true
                if install -m 0755 "${SCRIPT_DIR}/drevanta.sh" "$BIN_LINK_DIR/drevanta" 2>/dev/null; then
                        info "operator front-end installed: $BIN_LINK_DIR/drevanta (try: sudo drevanta claim status)"
                else
                        warn "could not install $BIN_LINK_DIR/drevanta — the technical CLI still works:"
                        warn "  sudo -u $DAEMON_USER $OPT_DIR/current/drevantad claim regenerate --data-dir $STATE_DIR"
                fi
        fi
}

migrate_legacy_layout() {
        # Sprint 1 installs used a flat /opt/drevanta/drevantad. Preserve that
        # binary as a rollback-able release.
        STEP="migrate legacy layout"
        local legacy="$OPT_DIR/drevantad"
        if [[ -f "$legacy" ]]; then
                local v
                v="$("$legacy" version 2>/dev/null || true)"
                [[ -z "$v" ]] && v="0.1.0-legacy"
                if [[ ! -f "$OPT_DIR/releases/$v/drevantad" ]]; then
                        mkdir -p "$OPT_DIR/releases/$v"
                        mv "$legacy" "$OPT_DIR/releases/$v/drevantad"
                        info "preserved legacy binary as release $v (rollback baseline)"
                else
                        rm -f "$legacy"
                        info "removed legacy binary (release $v already present)"
                fi
        fi
}

write_unit() {
        STEP="write systemd unit"
        local version="$1"
        local wanted
        wanted="$(unit_content "$version")"
        if [[ -f "$UNIT_PATH" ]] && diff -q "$UNIT_PATH" <(printf '%s\n' "$wanted") >/dev/null 2>&1; then
                info "unit file unchanged"
        else
                mkdir -p "$(dirname "$UNIT_PATH")"
                printf '%s\n' "$wanted" > "$UNIT_PATH"
                chmod 0644 "$UNIT_PATH"
                sys_daemon_reload
                UNIT_CHANGED=1
                info "unit written to $UNIT_PATH"
        fi
}

unit_content() {
        local version="$1"
        cat <<EOF
# drevanta.service — generated by install.sh for release $version.
# Do not edit by hand: re-run the installer instead.
[Unit]
Description=Drevanta server management daemon
Documentation=https://github.com/backupamunga-ops/drevanta_server_manager
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${DAEMON_USER}
Group=${DAEMON_USER}
ExecStart=${OPT_DIR}/current/drevantad serve --addr ${BIND_ADDR}:${PORT} --data-dir ${STATE_DIR}
Restart=on-failure
RestartSec=3s

# --- Resource limits (512 MB-class VPS budget) ---
MemoryMax=256M
MemoryHigh=192M
TasksMax=64
LimitNOFILE=4096

# --- Filesystem isolation ---
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
StateDirectory=drevanta

# --- Privilege / kernel surface ---
NoNewPrivileges=yes
CapabilityBoundingSet=
AmbientCapabilities=
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
ProtectClock=yes
ProtectHostname=yes
ProtectProc=invisible
RestrictSUIDSGID=yes
RestrictRealtime=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
SystemCallArchitectures=native
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
UMask=0027

[Install]
WantedBy=multi-user.target
EOF
}

flip_symlink() {
        STEP="point current symlink"
        local version="$1"
        if [[ "$(current_release)" == "$version" ]]; then
                info "current already -> releases/$version"
                return 0
        fi
        local tmp="$OPT_DIR/.current.tmp"
        ln -sfn "$OPT_DIR/releases/$version" "$tmp"
        mv -T "$tmp" "$OPT_DIR/current"
        SYMLINK_CHANGED=1
        info "current -> releases/$version"
}

start_and_verify() {
        STEP="start service"
        local version="$1"
        local prev="$2"
        sys_enable "$SERVICE_NAME"

        # A pure no-op re-run (same release, same unit, service healthy) must
        # not bounce the daemon.
        if [[ "$SYMLINK_CHANGED" == 0 && "$UNIT_CHANGED" == 0 && "$RELEASE_REWRITTEN" == 0 ]] && sys_is_active "$SERVICE_NAME"; then
                info "no changes detected — service left untouched"
        else
                sys_restart "$SERVICE_NAME"
        fi

        local ready=""
        for _ in $(seq 1 20); do
                if [[ "$(http_get "http://${BIND_ADDR}:${PORT}/readyz" || true)" == *'"status":"ready"'* ]]; then
                        ready=1
                        break
                fi
                sleep 0.5
        done

        if [[ -z "$ready" ]]; then
                echo >&2
                warn "service did not become ready after installing release $version"
                journalctl -u "$SERVICE_NAME" -n 20 --no-pager 2>/dev/null | sed 's/^/    /' >&2 || true
                if [[ -n "$prev" && "$prev" != "$version" && -d "$OPT_DIR/releases/$prev" ]]; then
                        warn "rolling back to previous release $prev"
                        flip_symlink "$prev"
                        sys_restart "$SERVICE_NAME"
                        local ok2=""
                        for _ in $(seq 1 10); do
                                if [[ "$(http_get "http://${BIND_ADDR}:${PORT}/readyz" || true)" == *'"status":"ready"'* ]]; then ok2=1; break; fi
                                sleep 0.5
                        done
                        if [[ -n "$ok2" ]]; then
                                die "rolled back to $prev successfully; the new release never became ready"
                        else
                                die "rolled back to $prev but it is not ready either — inspect journalctl -u $SERVICE_NAME"
                        fi
                fi
                die "service not ready and no previous release to roll back to"
        fi
        info "service active and /readyz reports ready"
}

# ---------------------------------------------------------------------------
# Token delivery policy (locked first-time-setup UX)
# ---------------------------------------------------------------------------
# Interactive session  -> the full one-time setup screen (link + raw token)
#                        is printed to the operator's own terminal.
# Noninteractive (CI, pipes, automation) -> the token is NEVER printed;
#                        it is delivered via a root-owned 0600 file.
# DREVANTA_INTERACTIVE=1|0 overrides the TTY probe (test hook).
is_interactive() {
        case "${DREVANTA_INTERACTIVE:-auto}" in
                1|true|yes) return 0 ;;
                0|false|no) return 1 ;;
                *) [[ -t 1 ]] ;;
        esac
}

# ssh_hint prints "<ip> <user>" - the server address and SSH username of
# the current session when reliably known (SSH_CONNECTION / SUDO_USER),
# otherwise empty fields. A missing value becomes a clearly labelled
# placeholder downstream, never a guess.
ssh_hint() {
        local ip="" user=""
        if [[ -n "${DREVANTA_SSH_HOST:-}" ]]; then
                ip="$DREVANTA_SSH_HOST"
        elif [[ -n "${SSH_CONNECTION:-}" ]]; then
                ip="$(printf '%s' "$SSH_CONNECTION" | awk '{print $3}')"
        fi
        if [[ -n "${SUDO_USER:-}" && "${SUDO_USER}" != "root" ]]; then
                user="$SUDO_USER"
        elif [[ -n "$ip" ]] && [[ "$(id -u 2>/dev/null || echo 1)" == "0" ]]; then
                user="root"   # this session IS root over SSH - known, not guessed
        fi
        printf '%s %s' "$ip" "$user"
}

print_success_screen() {
        local token="$1" target_file="$2" https_reason="$3"
        local ttl_disp="$CLAIM_TTL"
        [[ "$CLAIM_TTL" == "30m" ]] && ttl_disp="30 minutes"

        if [[ -n "${HTTPS_HOST:-}" ]]; then
                print_https_success_screen "$token" "$target_file"
                return 0
        fi

        local hint ip user disp_ip disp_user note=""
        hint="$(ssh_hint)"
        ip="${hint%% *}"
        user="${hint#* }"
        disp_ip="$ip"; [[ -z "$disp_ip" ]] && disp_ip="<your-vps-ip>"
        disp_user="$user"; [[ -z "$disp_user" ]] && disp_user="<ssh-user>"
        if [[ -z "$ip" || -z "$user" ]]; then
                note="  (replace the placeholder with this server's address and your SSH user)"
        fi
        local link="http://127.0.0.1:${TUNNEL_LOCAL_PORT}/onboarding#token=${token}"

        cat <<EOF

✓ DREVANTA INSTALLED SUCCESSFULLY

  Drevanta is running securely.
EOF
        if [[ -n "$https_reason" ]]; then
        cat <<EOF

  Direct HTTPS could not be configured:
  ${https_reason}
EOF
        fi
        cat <<EOF

  PRIVATE ACCESS

  On your computer (NOT inside this VPS shell), open a terminal and run:

  ssh -L ${TUNNEL_LOCAL_PORT}:127.0.0.1:${PORT} ${disp_user}@${disp_ip}

  Keep this SSH connection open. (Already connected with plain
  "ssh user@ip"? Forwarding only applies to NEW connections —
  reconnect with the command above or open a second local terminal.)

  Then open:

  ${link}

  Token expires in ${ttl_disp}.
  This link can be used only once.

  Raw token (fallback): ${token}

  Lost this link or it expired? Run on the VPS:
  sudo drevanta claim regenerate

  To retry public HTTPS later:
  sudo drevanta access enable-https

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
EOF
        if [[ -n "$note" ]]; then
                printf '%s\n' "$note"
        fi
        if [[ -n "$target_file" ]]; then
                info "a copy is also saved root-only: $target_file - delete it after onboarding"
        fi
}

# §6 success screen when trusted HTTPS went live during a fresh install.
print_https_success_screen() {
        local token="$1" target_file="$2"
        local ttl_disp="$CLAIM_TTL"
        [[ "$CLAIM_TTL" == "30m" ]] && ttl_disp="30 minutes"

        cat <<EOF

✓ DREVANTA INSTALLED SUCCESSFULLY

  Your private server console is ready.

  OPEN DREVANTA IN YOUR BROWSER

  https://${HTTPS_HOST}/onboarding#token=${token}

  Your setup link expires in ${ttl_disp}
  and can be used only once.

  Lost or expired your link?
  sudo drevanta claim regenerate

  SECURE ACCESS
  HTTPS enabled · Certificate valid

  No domain required.

  (Private fallback, if direct HTTPS ever stops working:
   ssh -L ${TUNNEL_LOCAL_PORT}:127.0.0.1:${PORT} USER@YOUR_VPS_IP
   then open http://127.0.0.1:${TUNNEL_LOCAL_PORT}/ )

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
EOF
        if [[ -n "$target_file" ]]; then
                info "a copy of the token is also saved root-only: $target_file - delete it after onboarding"
        fi
        info "manage public HTTPS any time: sudo drevanta access status | enable-https | disable-https"
}

# attempt_https_provisioning runs the root-only access CLI once and
# captures the outcome. On success HTTPS_HOST is set; on failure
# HTTPS_REASON carries a short diagnostic for the fallback screen.
# Every failure is non-fatal for the installation itself.
attempt_https_provisioning() {
        HTTPS_HOST=""
        HTTPS_REASON=""
        log "attempting public HTTPS provisioning (this may take up to ${HTTPS_WAIT}s)"
        local out rc
        out="$("$OPT_DIR/current/drevantad" access enable-https --wait "${HTTPS_WAIT}s" 2>&1)" && rc=0 || rc=$?
        if [[ $rc -eq 0 ]] && grep -q "PUBLIC HTTPS IS LIVE" <<<"$out"; then
                HTTPS_HOST="$(sed -n 's|.*https://\([^/ ]*\)/.*|\1|p' <<<"$out" | head -n1)"
                if [[ -n "$HTTPS_HOST" ]]; then
                        ok "public HTTPS is live: https://${HTTPS_HOST}/"
                        return 0
                fi
        fi
        HTTPS_REASON="$(sed -n 's/^[[:space:]]*Last reported reason:[[:space:]]*//p' <<<"$out" | head -n1)"
        if [[ -z "$HTTPS_REASON" ]]; then
                # Prefer a real error line over incidental progress output.
                HTTPS_REASON="$(grep -m1 -E 'failed:|refused|error:|ERROR' <<<"$out" | cut -c1-160)"
        fi
        if [[ -z "$HTTPS_REASON" ]]; then
                HTTPS_REASON="$(head -n1 <<<"$out" | sed 's/^==> //' | cut -c1-160)"
        fi
        [[ -n "$HTTPS_REASON" ]] || HTTPS_REASON="unknown error (see: sudo drevanta access status)"
        warn "direct HTTPS could not be configured: $HTTPS_REASON"
        return 1
}

issue_claim_token() {
        STEP="claim token"
        local fresh_install="$2"
        # Public HTTPS: auto attempts exactly once, on a FRESH install
        # only; upgrades never change the access configuration on their
        # own. --https=yes forces an attempt (explicit operator choice);
        # --https=no never attempts. Failures are non-fatal by design.
        HTTPS_HOST=""
        HTTPS_REASON=""
        local want_https=0
        case "$HTTPS_MODE" in
                no)   want_https=0 ;;
                yes)  want_https=1 ;;
                auto) [[ "$fresh_install" == "1" ]] && want_https=1 ;;
                *)    die "invalid --https mode '$HTTPS_MODE' (auto|yes|no)" ;;
        esac
        if [[ $want_https -eq 1 ]]; then
                attempt_https_provisioning || true
        elif [[ "$fresh_install" != "1" ]]; then
                info "existing installation: access configuration left untouched (see: sudo drevanta access status)"
        fi
        if [[ "${DREVANTA_SKIP_CLAIM:-0}" == "1" || "${SKIP_CLAIM:-0}" == "1" ]]; then
                info "claim-token step skipped (--skip-claim): no token issued or rotated"
                info "when you are ready to onboard, run: sudo drevanta claim regenerate"
                return 0
        fi
        local token_file="$1"
        local status version
        status="$(http_get "http://${BIND_ADDR}:${PORT}/api/v1/onboarding/status" || echo "")"
        if [[ "$status" == *'"instance_status":"active"'* || "$status" == *'"instance_status":"mfa_pending"'* ]]; then
                info "instance already claimed - no claim token needed"
                info "sign in via the tunnel: http://127.0.0.1:${TUNNEL_LOCAL_PORT}/login"
                return 0
        fi
        if [[ "$status" == *'"claim_token_active":true'* ]]; then
                info "an active claim token already exists - it is NOT re-issued"
                info "(re-issuing would rotate and invalidate it). Open"
                info "http://127.0.0.1:${TUNNEL_LOCAL_PORT}/onboarding and paste it, or rotate on purpose with:"
                info "  sudo drevanta claim regenerate"
                return 0
        fi

        version="$(bin_version "$OPT_DIR/current/drevantad")"
        local claim_cmd=( "$OPT_DIR/current/drevantad" claim init --data-dir "$STATE_DIR" --ttl "$CLAIM_TTL" )
        local raw errfile
        errfile="$(mktemp)"
        if [[ -n "$RUNUSER" ]]; then
                # claim init prints the raw token on stdout (captured) and its
                # own setup screen on stderr (captured separately: the installer
                # renders the screen itself, so the two never double-print).
                if ! raw="$($RUNUSER -u "$DAEMON_USER" -- "${claim_cmd[@]}" 2>"$errfile")"; then
                        error "claim init failed:"
                        sed 's/^/    /' "$errfile" >&2
                        rm -f "$errfile"
                        die "could not issue claim token"
                fi
        else
                raw="${claim_cmd[*]}"          # test mode
        fi
        rm -f "$errfile"
        local token
        token="$(printf '%s' "$raw" | head -n1 | tr -d '[:space:]')"
        [[ -n "$token" ]] || die "claim init produced no token"

        # Delivery channel: interactive terminal and/or protected file.
        local target_file="$token_file"
        if ! is_interactive && [[ -z "$target_file" ]]; then
                target_file="${DREVANTA_TOKEN_FILE:-/root/.drevanta-claim-token}"
        fi
        if [[ -n "$target_file" ]]; then
                umask 077
                printf '%s\n' "$token" > "$target_file"
                umask 022
                chmod 600 "$target_file" 2>/dev/null || true
        fi

        if is_interactive; then
                print_success_screen "$token" "$target_file" "${HTTPS_REASON:-}"
        else
                info "noninteractive install: claim token written to $target_file (mode 0600)"
                info "retrieve it with: sudo cat $target_file"
                if [[ -n "${HTTPS_HOST:-}" ]]; then
                        info "then open https://${HTTPS_HOST}/onboarding and paste it"
                else
                        info "then open http://127.0.0.1:${TUNNEL_LOCAL_PORT}/onboarding (via SSH tunnel) and paste it"
                fi
                info "if the token expires before you use it, get a fresh link with: sudo drevanta claim regenerate"
        fi
}

summary() {
        local version="$1"
        log "Drevanta $version is installed and running"
        info "service:    systemctl status $SERVICE_NAME"
        info "release:    $OPT_DIR/releases/$version"
        info "endpoint:   http://${BIND_ADDR}:${PORT}  (loopback only)"
}

# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------
usage() {
        cat <<EOF
usage: install.sh [COMMAND] [OPTIONS]

commands:
  (default)           preflight + install + start + verify + claim token
  preflight           run all checks, change nothing
  --uninstall         stop and remove the service (state kept; add --purge to wipe)

options:
  --binary PATH       drevantad binary to install (default: $DEFAULT_BINARY)
  --port N            loopback port (default: $PORT)
  --ttl DURATION      claim token lifetime (default: $CLAIM_TTL)
  --token-file PATH   also write the claim token here, mode 0600
  --skip-claim        do not issue, rotate, or deliver any claim token
                      (operator will run: sudo drevanta claim regenerate)
  --https MODE        public HTTPS on install: auto (default: attempt once
                      on a FRESH install), yes (force attempt), no (never).
                      Upgrades never touch access config in auto mode.
  --purge             (with --uninstall) also delete /var/lib/drevanta state
  -h, --help          this help
EOF
}

main() {
        local bin="$DEFAULT_BINARY" token_file="" purge=0
        local cmd="install"
        if [[ $# -gt 0 ]]; then
                case "$1" in
                        preflight|install|--uninstall|-h|--help|help)
                                cmd="$1"; shift
                                ;;
                        --*) 
                                # bare-flags invocation: default command with options
                                ;;
                esac
        fi

        case "$cmd" in
                -h|--help|help)
                        usage; return 0
                        ;;
                --uninstall)
                        while [[ $# -gt 0 ]]; do
                                case "$1" in
                                        --purge) purge=1 ;;
                                        *) usage >&2; return 2 ;;
                                esac
                                shift
                        done
                        uninstall "$purge"
                        return $?
                        ;;
        esac

        # Option parsing (works for `install.sh install …` and bare flags).
        while [[ $# -gt 0 ]]; do
                case "$1" in
                        --binary)      bin="${2:?}"; shift 2 ;;
                        --port)        PORT="${2:?}"; shift 2 ;;
                        --ttl)         CLAIM_TTL="${2:?}"; shift 2 ;;
                        --token-file)  token_file="${2:?}"; shift 2 ;;
                        --skip-claim)  SKIP_CLAIM=1; shift ;;
                        --https)       HTTPS_MODE="${2:?}"; shift 2 ;;
                        *) usage >&2; return 2 ;;
                esac
        done

        if [[ "$cmd" == "preflight" ]]; then
                run_preflight "$bin"
                log "Preflight passed — no changes made"
                return 0
        fi

        CURRENT_RELEASE_BEFORE="$(current_release)"
        local fresh_install=0
        [[ -z "$CURRENT_RELEASE_BEFORE" ]] && fresh_install=1
        run_preflight "$bin"
        local version
        version="$(bin_version "$bin")"
        log "Installing Drevanta $version"
        create_service_user
        migrate_legacy_layout
        install_release "$bin" "$version"
        write_unit "$version"
        flip_symlink "$version"
        start_and_verify "$version" "$CURRENT_RELEASE_BEFORE"
        issue_claim_token "$token_file" "$fresh_install"
        summary "$version"
}

uninstall() {
        local purge="${1:-0}"
        STEP="uninstall"
        log "Stopping and removing the $SERVICE_NAME service"
        systemctl disable --now "$SERVICE_NAME" 2>/dev/null || true
        # The public HTTPS proxy shares the release binary: stop it too
        # (config and certificates are kept for a future re-install).
        systemctl disable --now drevanta-https.service 2>/dev/null || true
        rm -f "$UNIT_PATH"
        rm -f "/etc/systemd/system/drevanta-https.service"
        rm -f "$BIN_LINK_DIR/drevanta"
        sys_daemon_reload
        rm -f "$OPT_DIR/current"
        if [[ "$purge" == "1" ]]; then
                warn "deleting state directory $STATE_DIR (audit log, accounts, sessions)"
                rm -rf "$STATE_DIR"
        fi
        info "releases kept under $OPT_DIR/releases; remove manually if desired"
        info "uninstall complete"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
        main "$@"
fi
