#!/usr/bin/env bash
# ============================================================================
# Drevanta rollback — point /opt/drevanta/current at the previous (or named)
# release and restart, then verify /readyz.
#
# usage: rollback.sh [release-name]
#   release-name     rollback target; default: the most recent release older
#                    than the current one (by directory mtime)
#
# Exit codes: 0 rolled back and healthy · 1 rollback failed · 2 usage
# ============================================================================
set -euo pipefail

OPT_DIR="${DREVANTA_OPT_DIR:-/opt/drevanta}"
SERVICE_NAME="${DREVANTA_SERVICE_NAME:-drevanta}"
BIND_ADDR="${DREVANTA_BIND_ADDR:-127.0.0.1}"
PORT="${DREVANTA_PORT:-8080}"

log()  { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mWARN\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31mERROR\033[0m %s\n' "$*" >&2; exit 1; }

http_ready() {
	curl -fsS --max-time 3 "http://${BIND_ADDR}:${PORT}/readyz" 2>/dev/null | grep -q '"status":"ready"'
}

[[ -L "$OPT_DIR/current" ]] || die "$OPT_DIR/current is not a symlink — nothing to roll back"
cur="$(basename "$(readlink "$OPT_DIR/current")")"

if [[ $# -gt 1 ]]; then
	echo "usage: rollback.sh [release-name]" >&2
	exit 2
fi

target=""
if [[ $# -eq 1 ]]; then
	target="$1"
	[[ -d "$OPT_DIR/releases/$target" ]] || die "release '$target' does not exist"
	[[ "$target" != "$cur" ]] || die "current release is already $cur"
else
	# Most recent release that is not the current one. Release names are
	# version strings produced by this toolchain, so mtime ordering via ls
	# is deliberate here (SC2012).
	# shellcheck disable=SC2012
	while IFS= read -r d; do
		name="$(basename "$d")"
		[[ "$name" != "$cur" ]] && { target="$name"; break; }
	done < <(ls -1t "$OPT_DIR/releases" 2>/dev/null | while read -r n; do
		[[ -x "$OPT_DIR/releases/$n/drevantad" ]] && echo "$OPT_DIR/releases/$n"
	done)
	[[ -n "$target" ]] || die "no other release found to roll back to (current: $cur)"
fi

log "Rolling back: $cur -> $target"
tmp="$OPT_DIR/.current.tmp"
ln -sfn "$OPT_DIR/releases/$target" "$tmp"
mv -T "$tmp" "$OPT_DIR/current"

systemctl restart "$SERVICE_NAME"

ok=""
for _ in $(seq 1 20); do
	if http_ready; then ok=1; break; fi
	sleep 0.5
done

if [[ -n "$ok" ]]; then
	log "Rollback complete: current -> $target, service ready"
	echo "rollback target=$target from=$cur result=ready"
	exit 0
fi

warn "service not ready after rollback to $target"
journalctl -u "$SERVICE_NAME" -n 20 --no-pager 2>/dev/null | tail -10 || true
echo "rollback target=$target from=$cur result=not_ready"
exit 1
