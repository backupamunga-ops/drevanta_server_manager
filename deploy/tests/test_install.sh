#!/usr/bin/env bash
# ============================================================================
# Installer + rollback test harness.
#
# Runs install.sh / rollback.sh / healthgate.sh against a mocked system:
# a temp sandbox replaces /opt and /etc/systemd, systemctl and curl are
# faked, and the release binary is a REAL drevantad build (so `claim init`
# genuinely writes a hashed token into a real SQLite database).
#
# usage: test_install.sh <path-to-drevantad-binary>
# ============================================================================
# shellcheck disable=SC2015
set -uo pipefail

BIN_INPUT="${1:?usage: test_install.sh <drevantad-binary>}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY="$(dirname "$HERE")"

PASS=0
FAIL=0
sandbox=""

fail() { echo "  ✗ FAIL: $*"; FAIL=$((FAIL + 1)); }
pass() { echo "  ✓ $*"; PASS=$((PASS + 1)); }
section() { echo; echo "== $* =="; }

# ---------------------------------------------------------------------------
# Mock environment
# ---------------------------------------------------------------------------
setup_mocks() {
        sandbox="$(mktemp -d)"
        mockdir="$sandbox/mock"
        mkdir -p "$mockdir" "$sandbox/opt" "$sandbox/state" "$sandbox/systemd" "$sandbox/systemd-run"
        mkdir -p "$sandbox/opt/releases"

        cp "$BIN_INPUT" "$sandbox/staged-drevantad"
        chmod +x "$sandbox/staged-drevantad"

        cat > "$mockdir/systemctl" <<'EOF'
#!/usr/bin/env bash
# Mock systemctl: records calls, emulates is-active/restart semantics.
case "$1 $2" in
  "daemon-reload "*) echo "daemon-reload" >> "$MOCK_SYSTEMCTL_STATE/calls"; exit 0 ;;
  "enable "*)        echo "enable $2" >> "$MOCK_SYSTEMCTL_STATE/calls"; exit 0 ;;
  "restart "*)
     echo "restart $2" >> "$MOCK_SYSTEMCTL_STATE/calls"
     if [[ -f "$MOCK_UNIT_PATH" ]]; then rm -f "$MOCK_SYSTEMCTL_STATE/failed"; else touch "$MOCK_SYSTEMCTL_STATE/failed"; fi
     exit 0 ;;
  "is-active "*)
     if [[ -f "$MOCK_UNIT_PATH" && ! -f "$MOCK_SYSTEMCTL_STATE/failed" ]]; then exit 0; else exit 3; fi ;;
  "disable --now "*) echo "disable $2" >> "$MOCK_SYSTEMCTL_STATE/calls"; exit 0 ;;
  *) exit 0 ;;
esac
EOF

        cat > "$mockdir/curl" <<'EOF'
#!/usr/bin/env bash
# Mock curl: emulates the loopback HTTP surface of the CURRENT release.
# /readyz answers ready unless the current release dir carries a .broken
# marker (the harness's way to simulate a bad release).
url="${@: -1}"
cur_dir="$(readlink -f "$MOCK_OPT_DIR/current" 2>/dev/null || true)"
case "$url" in
  *readyz*)
     if [[ -n "$cur_dir" && -x "$cur_dir/drevantad" && ! -f "$cur_dir/.broken" ]]; then
       echo '{"status":"ready"}'; exit 0
     fi
     exit 22 ;;
  *onboarding/status*)
     if [[ -f "$MOCK_STATUS_FILE" ]]; then cat "$MOCK_STATUS_FILE"; exit 0; fi
     echo '{"instance_status":"unclaimed","mfa_mode":"","claim_token_active":false,"authenticated":false}'; exit 0 ;;
  *) exit 22 ;;
esac
EOF

        cat > "$mockdir/runuser" <<'EOF'
#!/usr/bin/env bash
# Mock runuser: drop the "-u USER --" prefix and execute directly.
shift 2
exec "$@"
EOF

        # ss: no listeners by default (port free). Created BEFORE the link loop
        # so the loop cannot clobber it with a symlink to the real binary.
        printf '#!/usr/bin/env bash\nexit 0\n' > "$mockdir/ss"
        chmod +x "$mockdir/ss"
        # getent/useradd: the harness runs unprivileged, so the service user
        # "exists" per mock and useradd is a recorded no-op.
        cat > "$mockdir/getent" <<'EOF'
#!/usr/bin/env bash
if [[ "$1 $2" == "passwd drevanta" ]]; then echo "drevanta:x:990:990:Drevanta service user:/nonexistent:/usr/sbin/nologin"; exit 0; fi
exec /usr/bin/getent "$@"
EOF
        cat > "$mockdir/useradd" <<'EOF'
#!/usr/bin/env bash
echo "useradd $*" >> "${MOCK_SYSTEMCTL_STATE:?}/calls"
exit 0
EOF
        chmod +x "$mockdir/getent" "$mockdir/useradd"
        for t in uname awk df sha256sum install diff journalctl head sleep seq mkdir ln mv chmod chown grep basename dirname readlink cat sed tr cut stat ls touch rm printf echo; do
                p="$(command -v "$t" 2>/dev/null)" && ln -sf "$p" "$mockdir/$t"
        done
        cat > "$mockdir/id" <<'EOF'
#!/usr/bin/env bash
[[ "$1" == "-u" ]] && { echo 0; exit 0; }
exec /usr/bin/id "$@"
EOF
        chmod +x "$mockdir/id" "$mockdir/systemctl" "$mockdir/curl" "$mockdir/runuser"
}

export_mock_env() {
        export MOCK_OPT_DIR="$sandbox/opt"
        export MOCK_UNIT_PATH="$sandbox/systemd/drevanta.service"
        export MOCK_SYSTEMCTL_STATE="$sandbox"
        export MOCK_STATUS_FILE="$sandbox/status.json"
        export DREVANTA_OPT_DIR="$sandbox/opt"
        export DREVANTA_STATE_DIR="$sandbox/state"
        export DREVANTA_UNIT_PATH="$sandbox/systemd/drevanta.service"
        export DREVANTA_SYSTEMD_RUN_DIR="$sandbox/systemd-run"
        export DREVANTA_SKIP_ROOT_CHECK=1
        export DREVANTA_RUNUSER="$mockdir/runuser"
        export PATH="$mockdir:/usr/local/bin:/usr/bin:/bin"
}

get_version() { "$1" version 2>/dev/null; }

make_version_wrapper() { # <name> -> wrapper binary that reports <name> as version
        local name="$1"
        local out="$sandbox/staged-$name"
        cat > "$out" <<EOF
#!/usr/bin/env bash
if [[ "\$1" == "version" ]]; then echo "$name"; exit 0; fi
exec "$sandbox/staged-drevantad" "\$@"
EOF
        chmod +x "$out"
        echo "$out"
}

link_of_current() { basename "$(readlink "$sandbox/opt/current" 2>/dev/null)" ; }

# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------
test_preflight_pass() {
        section "preflight passes on a clean host"
        DREVANTA_SKIP_ROOT_CHECK=1 "$DEPLOY/install.sh" preflight \
                --binary "$sandbox/staged-drevantad" >"$sandbox/preflight.log" 2>&1 && pass "exit 0" \
                || { fail "non-zero exit"; sed -n '1,30p' "$sandbox/preflight.log"; return; }
        grep -q "Preflight passed" "$sandbox/preflight.log" && pass "success reported" || fail "no success line"
}

test_preflight_fail_arch() {
        section "preflight blocks wrong architecture"
        mv "$mockdir/uname" "$mockdir/uname.bak"
        cat > "$mockdir/uname" <<'EOF'
#!/usr/bin/env bash
[[ "$1" == "-m" ]] && { echo aarch64; exit 0; }
exec /bin/uname "$@"
EOF
        chmod +x "$mockdir/uname"
        DREVANTA_SKIP_ROOT_CHECK=1 "$DEPLOY/install.sh" preflight \
                --binary "$sandbox/staged-drevantad" >"$sandbox/arch.log" 2>&1
        local rc=$?
        [[ $rc -ne 0 ]] && pass "exit=$rc (blocked)" || fail "exit=0"
        grep -q "unsupported architecture" "$sandbox/arch.log" && pass "clear diagnostic" || fail "no arch message"
        mv "$mockdir/uname.bak" "$mockdir/uname"
}

test_preflight_fail_port() {
        section "preflight blocks an occupied port"
        mv "$mockdir/ss" "$mockdir/ss.bak"
        printf '#!/usr/bin/env bash\nprintf "127.0.0.1:8080  users:(("intruder"))\\n"\n' > "$mockdir/ss"
        chmod +x "$mockdir/ss"
        DREVANTA_SKIP_ROOT_CHECK=1 "$DEPLOY/install.sh" preflight \
                --binary "$sandbox/staged-drevantad" >"$sandbox/port.log" 2>&1
        local rc=$?
        [[ $rc -ne 0 ]] && pass "exit=$rc (blocked)" || fail "exit=0"
        grep -q "already in use" "$sandbox/port.log" && pass "conflict diagnostic" || fail "no port message"
        mv "$mockdir/ss.bak" "$mockdir/ss"
}

test_install_fresh_and_claim() {
        section "fresh install: release, symlink, unit, claim token"
        "$DEPLOY/install.sh" install --binary "$sandbox/staged-drevantad" \
                --token-file "$sandbox/claim.txt" >"$sandbox/install.log" 2>&1
        local rc=$?
        [[ $rc -eq 0 ]] && pass "installer exited 0" || { fail "exit $rc"; sed -n '1,40p' "$sandbox/install.log"; return; }

        local ver; ver="$(get_version "$sandbox/staged-drevantad")"
        [[ -x "$sandbox/opt/releases/$ver/drevantad" ]] && pass "release installed" || fail "release missing"
        [[ "$(link_of_current)" == "$ver" ]] && pass "current symlink set" || fail "symlink wrong: $(link_of_current)"
        [[ -f "$sandbox/systemd/drevanta.service" ]] && pass "unit written" || fail "unit missing"
        grep -q "MemoryMax=256M" "$sandbox/systemd/drevanta.service" && pass "unit hardened" || fail "unit not hardened"
        grep -q "$sandbox/opt/current/drevantad serve" "$sandbox/systemd/drevanta.service" && pass "unit uses releases layout" || fail "unit ExecStart wrong"
        grep -q "User=drevanta" "$sandbox/systemd/drevanta.service" && pass "unit unprivileged user" || fail "unit user wrong"
        [[ -s "$sandbox/claim.txt" ]] && pass "claim token issued to file" || fail "no claim token"
        grep -q "^drv1_" "$sandbox/claim.txt" && pass "token has drv1_ prefix" || fail "token malformed"
        [[ "$(stat -c '%a' "$sandbox/claim.txt")" == "600" ]] && pass "token file mode 0600" || fail "token mode: $(stat -c '%a' "$sandbox/claim.txt")"
        "$sandbox/opt/current/drevantad" claim status --data-dir "$sandbox/state" 2>/dev/null | grep -q "claim_token_active=true" \
                && pass "token stored hashed in daemon DB" || fail "token not in DB"
        grep -qE "drv1_[A-Za-z0-9_-]{5}…[A-Za-z0-9_-]{4}" "$sandbox/install.log" && pass "stdout shows masked token only" || fail "token not masked in stdout"
        if grep -qE "drv1_[A-Za-z0-9_-]{39,}" "$sandbox/install.log"; then fail "FULL token leaked to stdout"; else pass "full token not printed to stdout"; fi
}

test_install_idempotent() {
        section "idempotent re-run changes nothing"
        echo '{"instance_status":"active","mfa_mode":"totp","claim_token_active":false,"authenticated":false}' > "$MOCK_STATUS_FILE"
        local before_link before_sum
        before_link="$(readlink "$sandbox/opt/current")"
        before_sum="$(sha256sum "$sandbox/systemd/drevanta.service" | cut -d' ' -f1)"
        "$DEPLOY/install.sh" install --binary "$sandbox/staged-drevantad" >"$sandbox/install2.log" 2>&1 && pass "exit 0" \
                || { fail "non-zero"; sed -n '1,40p' "$sandbox/install2.log"; return; }
        [[ "$(readlink "$sandbox/opt/current")" == "$before_link" ]] && pass "symlink unchanged" || fail "symlink changed"
        [[ "$(sha256sum "$sandbox/systemd/drevanta.service" | cut -d' ' -f1)" == "$before_sum" ]] && pass "unit unchanged" || fail "unit rewritten"
        grep -q "already claimed" "$sandbox/install2.log" && pass "no new claim token" || fail "token re-issued"
        grep -q "already installed" "$sandbox/install2.log" && pass "release not duplicated" || fail "release rewritten"
}

test_install_upgrade_and_autorollback() {
        section "upgrade then auto-rollback on a broken new release"
        local staged_new; staged_new="$(make_version_wrapper "99.0.0-test")"
        "$DEPLOY/install.sh" install --binary "$staged_new" >"$sandbox/install3.log" 2>&1 && pass "upgrade exited 0" \
                || { fail "upgrade failed"; sed -n '1,40p' "$sandbox/install3.log"; return; }
        [[ "$(link_of_current)" == "99.0.0-test" ]] && pass "symlink flipped to new release" || fail "symlink not flipped"
        local base; base="$(get_version "$sandbox/staged-drevantad")"
        [[ -x "$sandbox/opt/releases/$base/drevantad" ]] && pass "old release preserved" || fail "old release gone"

        # Now a NEW release that will fail readiness: pre-plant the .broken
        # marker; the installer fills in the binary and the mock refuses /readyz.
        local staged_broken; staged_broken="$(make_version_wrapper "98.0.0-test")"
        mkdir -p "$sandbox/opt/releases/98.0.0-test"
        touch "$sandbox/opt/releases/98.0.0-test/.broken"
        DREVANTA_SKIP_ROOT_CHECK=1 "$DEPLOY/install.sh" install --binary "$staged_broken" >"$sandbox/install4.log" 2>&1
        local rc=$?
        [[ $rc -ne 0 ]] && pass "broken release rejected (exit=$rc)" || fail "broken release accepted"
        [[ "$(link_of_current)" == "99.0.0-test" ]] && pass "auto-rollback restored 99.0.0-test" || fail "symlink not restored: $(link_of_current)"
        grep -q "rolling back" "$sandbox/install4.log" && pass "rollback reported" || fail "no rollback message"
        grep -q "rolled back to 99.0.0-test successfully" "$sandbox/install4.log" && pass "rollback verified healthy" || fail "no success rollback line"
        # Remove the experiment so later tests see a deterministic release set.
        rm -rf "$sandbox/opt/releases/98.0.0-test"
}

test_rollback_script() {
        section "rollback.sh explicit target and failure path"
        # Healthy path: current 99 -> base.
        local base; base="$(get_version "$sandbox/staged-drevantad")"
        ln -sfn "$sandbox/opt/releases/99.0.0-test" "$sandbox/opt/.t" && mv -T "$sandbox/opt/.t" "$sandbox/opt/current"
        "$DEPLOY/rollback.sh" "$base" >"$sandbox/rollback.log" 2>&1 && pass "rollback exited 0" \
                || { fail "exit $?"; cat "$sandbox/rollback.log"; }
        [[ "$(link_of_current)" == "$base" ]] && pass "symlink flipped back" || fail "symlink wrong"
        grep -q "result=ready" "$sandbox/rollback.log" && pass "readiness verified" || fail "no ready result"

        # Failure path: rolling back TO a broken release must fail loudly.
        touch "$sandbox/opt/releases/99.0.0-test/.broken"
        DREVANTA_SKIP_ROOT_CHECK=1 "$DEPLOY/rollback.sh" "99.0.0-test" >"$sandbox/rollback2.log" 2>&1
        local rc=$?
        [[ $rc -eq 1 ]] && pass "broken rollback exits 1" || fail "exit=$rc"
        grep -q "result=not_ready" "$sandbox/rollback2.log" && pass "not_ready reported" || fail "no not_ready result"
        [[ "$(link_of_current)" == "99.0.0-test" ]] && pass "symlink left at attempted target" || fail "symlink moved"
        rm -f "$sandbox/opt/releases/99.0.0-test/.broken"
        # restore healthy baseline
        DREVANTA_SKIP_ROOT_CHECK=1 "$DEPLOY/rollback.sh" "$base" >/dev/null 2>&1 || true
}

test_rollback_no_target() {
        section "rollback.sh without argument picks the previous release"
        local base; base="$(get_version "$sandbox/staged-drevantad")"
        # current -> 99 (newest); the only other release is base.
        ln -sfn "$sandbox/opt/releases/99.0.0-test" "$sandbox/opt/.t" && mv -T "$sandbox/opt/.t" "$sandbox/opt/current"
        touch "$sandbox/opt/releases/$base/drevantad"  # refresh mtime ordering determinism
        sleep 0.1
        ln -sfn "$sandbox/opt/releases/99.0.0-test" "$sandbox/opt/.t" && mv -T "$sandbox/opt/.t" "$sandbox/opt/current"
        "$DEPLOY/rollback.sh" >"$sandbox/rollback3.log" 2>&1 && pass "auto-target rollback exited 0" \
                || { fail "exit $?"; cat "$sandbox/rollback3.log"; }
        [[ "$(link_of_current)" == "$base" ]] && pass "rolled back to $base" || fail "unexpected target: $(link_of_current)"
}

test_healthgate() {
        section "healthgate.sh decisions"
        local base; base="$(get_version "$sandbox/staged-drevantad")"
        # Healthy new release: accept, no action.
        ln -sfn "$sandbox/opt/releases/99.0.0-test" "$sandbox/opt/.t" && mv -T "$sandbox/opt/.t" "$sandbox/opt/current"
        "$DEPLOY/healthgate.sh" 99.0.0-test "$base" >"$sandbox/gate1.log" 2>&1 && pass "healthy gate exits 0" || fail "healthy gate failed"
        grep -q "upgrade accepted" "$sandbox/gate1.log" && pass "acceptance reported" || fail "no accept message"
        # Broken new release still current: gate must roll back.
        touch "$sandbox/opt/releases/99.0.0-test/.broken"
        "$DEPLOY/healthgate.sh" 99.0.0-test "$base" >"$sandbox/gate2.log" 2>&1 && pass "gate rolled back (exit 0)" || fail "gate exit $?"
        [[ "$(link_of_current)" == "$base" ]] && pass "gate restored previous release" || fail "gate symlink wrong"
        rm -f "$sandbox/opt/releases/99.0.0-test/.broken"
        # Operator switched manually: gate must be inert.
        "$DEPLOY/healthgate.sh" 99.0.0-test "$base" >"$sandbox/gate3.log" 2>&1 && pass "inert gate exits 0" || fail "gate exit $?"
        grep -q "doing nothing" "$sandbox/gate3.log" && pass "gate did not touch manual switch" || fail "gate acted"
}

test_uninstall() {
        section "uninstall removes unit, keeps releases and state"
        "$DEPLOY/install.sh" --uninstall >"$sandbox/uninstall.log" 2>&1 && pass "exit 0" || fail "exit $?"
        [[ ! -f "$sandbox/systemd/drevanta.service" ]] && pass "unit removed" || fail "unit still present"
        [[ -d "$sandbox/opt/releases" ]] && pass "releases preserved" || fail "releases removed"
        [[ -d "$sandbox/state" ]] && pass "state kept without --purge" || fail "state wiped"
}

# ---------------------------------------------------------------------------
main() {
        [[ -x "$BIN_INPUT" ]] || { echo "binary not executable: $BIN_INPUT" >&2; exit 2; }
        echo "Drevanta installer test harness"
        echo "   binary: $BIN_INPUT (version $(get_version "$BIN_INPUT"))"

        setup_mocks
        trap '[[ -n "$sandbox" ]] && rm -rf "$sandbox"' EXIT
        export_mock_env

        test_preflight_pass
        test_preflight_fail_arch
        test_preflight_fail_port
        test_install_fresh_and_claim
        test_install_idempotent
        test_install_upgrade_and_autorollback
        test_rollback_script
        test_rollback_no_target
        test_healthgate
        test_uninstall

        echo
        echo "== results: $PASS passed, $FAIL failed =="
        [[ $FAIL -eq 0 ]]
}

main "$@"
