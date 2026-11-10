#!/usr/bin/env bash
# ============================================================================
# Drevanta health gate — the delayed-decision rollback trigger used by
# unattended upgrades.
#
# Intended invocation (locked design, ADR-005 rollback machinery):
#
#   systemd-run --on-active=90s --unit=drevanta-healthgate \
#       /opt/drevanta/bin/healthgate.sh <new-release> <previous-release>
#
# When the timer fires (90 s after the upgrade), this script checks:
#   1. Is /opt/drevanta/current still pointing at the new release?
#      (If the operator already changed it, do nothing.)
#   2. Is GET /readyz passing?
#      (If yes, the upgrade is healthy; do nothing.)
#   3. Otherwise run rollback.sh <previous-release>, which flips the
#      symlink, restarts, and verifies readiness.
#
# The script is deliberately side-effect-free when everything is healthy.
# ============================================================================
set -euo pipefail

OPT_DIR="${DREVANTA_OPT_DIR:-/opt/drevanta}"
BIND_ADDR="${DREVANTA_BIND_ADDR:-127.0.0.1}"
PORT="${DREVANTA_PORT:-8080}"

if [[ $# -ne 2 ]]; then
	echo "usage: healthgate.sh <new-release> <previous-release>" >&2
	exit 2
fi
new="$1"
prev="$2"

log() { printf 'healthgate: %s\n' "$*"; }

cur="$(basename "$(readlink "$OPT_DIR/current")" 2>/dev/null || echo "")"
if [[ "$cur" != "$new" ]]; then
	log "current release is '$cur', expected '$new' — someone already switched; doing nothing"
	exit 0
fi

if curl -fsS --max-time 3 "http://${BIND_ADDR}:${PORT}/readyz" 2>/dev/null | grep -q '"status":"ready"'; then
	log "release $new is healthy — upgrade accepted"
	exit 0
fi

log "/readyz is failing for release $new — rolling back to $prev"
"$OPT_DIR/bin/rollback.sh" "$prev"
log "rollback finished"
