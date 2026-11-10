#!/bin/sh
# ============================================================================
# drevanta — operator-friendly front-end for the drevantad CLI.
#
# Installed by deploy/install.sh as /usr/local/bin/drevanta. It exists so
# a first-time VPS owner never has to type internal paths like
#   runuser -u drevanta -- /opt/drevanta/current/drevantad claim init
# and can instead run the documented, intuitive form:
#
#       sudo drevanta claim regenerate      fresh one-time setup link
#       sudo drevanta claim status          onboarding state (no secrets)
#       sudo drevanta claim revoke          invalidate the current token
#       sudo drevanta access status         public HTTPS + tunnel access state
#       sudo drevanta access enable-https   opt in to public HTTPS
#       sudo drevanta access disable-https  return to tunnel-only
#       drevanta version                    show the running release
#
# What it guarantees:
#   * The daemon itself is NOT touchable through this front-end: `serve`
#     is managed exclusively by systemd (systemctl restart drevanta).
#   * `claim` commands run as the unprivileged `drevanta` service user
#     (via runuser) — same privilege boundary as the daemon itself.
#   * `access` commands run as root: they configure the separate public
#     HTTPS proxy unit (drevanta-https) and never read the database,
#     never see claim tokens, and never touch the firewall or SSH.
#   * Tokens are printed only by the claim subcommands to the caller's
#     own terminal, exactly once.
#
# Environment overrides (used by the installer test harness):
#   DREVANTA_OPT_DIR, DREVANTA_STATE_DIR, DREVANTA_DAEMON_USER,
#   DREVANTA_RUNUSER (empty = drop nothing; test sandboxes only),
#   DREVANTA_BIN_LINK_DIR (install target of this script).
# ============================================================================
set -u

OPT_DIR="${DREVANTA_OPT_DIR:-/opt/drevanta}"
STATE_DIR="${DREVANTA_STATE_DIR:-/var/lib/drevanta}"
DAEMON_USER="${DREVANTA_DAEMON_USER:-drevanta}"
RUNUSER="${DREVANTA_RUNUSER:-runuser}"
DAEMON="$OPT_DIR/current/drevantad"

usage() {
        cat <<'EOF'
usage: drevanta <command>

  claim regenerate    issue a fresh one-time setup link (invalidates the
                      previous token). Works only before the first
                      administrator exists.
  claim status        show onboarding state (no secrets)
  claim revoke        invalidate the current claim token
  access status       show public HTTPS / SSH-tunnel access state
  access enable-https opt in to publicly trusted HTTPS on this VPS
  access disable-https turn public HTTPS off (backend and data unaffected)
  access configure    adjust HTTPS target host / ACME settings
  version             show the installed release

The daemon itself is managed by systemd:  systemctl status drevanta
Full guide: see docs/INSTALLATION.md in the repository.
EOF
}

# No arguments: help, not the daemon's bare-serve fallback.
if [ $# -eq 0 ]; then
        usage
        exit 0
fi

case "$1" in
        -h|--help|help)
                usage
                exit 0
                ;;
        serve)
                echo "drevanta: the daemon is managed by systemd; use:" >&2
                echo "  systemctl restart drevanta      (or stop/status)" >&2
                exit 2
                ;;
        version)
                exec "$DAEMON" version
                ;;
esac

# `access` manages the separate public HTTPS proxy unit (systemd, /etc
# config) and therefore runs as root. It shares none of the claim data:
# no database access, no tokens.
if [ "$1" = "access" ]; then
        if [ "$(id -u 2>/dev/null || echo 1)" != "0" ]; then
                echo "drevanta: must run as root (try: sudo drevanta access ...)" >&2
                exit 2
        fi
        if [ ! -x "$DAEMON" ]; then
                echo "drevanta: daemon not found at $DAEMON — is Drevanta installed?" >&2
                exit 1
        fi
        exec "$DAEMON" "$@"
fi

# Everything past this point touches the state directory and therefore
# runs as the unprivileged service user — root is required to get there.
if [ "$(id -u 2>/dev/null || echo 1)" != "0" ]; then
        echo "drevanta: must run as root (try: sudo drevanta $*)" >&2
        exit 2
fi
if [ ! -x "$DAEMON" ]; then
        echo "drevanta: daemon not found at $DAEMON — is Drevanta installed?" >&2
        exit 1
fi

# Best-effort, first-party only: recover the current SSH session's server
# address and login user from the operator's own utmp entry (matched by
# the controlling terminal) so the printed tunnel command can use real
# values instead of placeholders. If the session is not a TTY login, the
# claim command falls back to its labelled <placeholders>.
if [ -z "${DREVANTA_SSH_HOST:-}" ] && command -v who >/dev/null 2>&1; then
        wline="$(who -m 2>/dev/null | tail -n 1)"
        wuser="$(printf '%s' "$wline" | awk '{print $1}')"
        whost="$(printf '%s' "$wline" | sed -n 's/.*(\([^)]*\)).*/\1/p')"
        case "$whost" in
                ""|:*|localhost) ;;   # X display / local entry: not an SSH session
                *)
                        DREVANTA_SSH_HOST="$whost"
                        export DREVANTA_SSH_HOST
                        if [ -n "$wuser" ] && [ -z "${DREVANTA_SSH_USER:-}" ]; then
                                DREVANTA_SSH_USER="$wuser"
                                export DREVANTA_SSH_USER
                        fi
                        ;;
        esac
fi

# Pass the sandbox state dir through only when explicitly overridden;
# production uses the drevantad built-in default.
if [ -n "${DREVANTA_STATE_DIR:-}" ]; then
        set -- "$@" --data-dir "$STATE_DIR"
fi

if [ -n "$RUNUSER" ] && command -v "$RUNUSER" >/dev/null 2>&1; then
        exec "$RUNUSER" -u "$DAEMON_USER" -- "$DAEMON" "$@"
fi
exec "$DAEMON" "$@"
