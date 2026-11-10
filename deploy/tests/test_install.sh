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
# Mock systemctl: records calls (both $1 $2 shorthand and full args),
# emulates is-active/restart semantics.
echo "$*" >> "${MOCK_SYSTEMCTL_STATE:?}/calls_full"
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
#
# Bootstrap tests delegate fixture downloads to the REAL curl (prefix
# match) so HTTPS/redirect logic is exercised end to end; the prefix
# never matches the daemon's loopback API surface handled below.
if [[ -n "${REAL_CURL_URL_PREFIX:-}" ]]; then
        for a in "$@"; do
                case "$a" in
                        "${REAL_CURL_URL_PREFIX}"*) exec "${REAL_CURL_BIN:-/usr/bin/curl}" "$@" ;;
                esac
        done
fi
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
        # Real curl, captured before the mock PATH exists: the bootstrap
        # fixture downloads must exercise genuine curl behaviour.
        REAL_CURL_BIN="$(command -v curl 2>/dev/null || true)"
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
        export DREVANTA_BIN_LINK_DIR="$sandbox/bin-link"
        export DREVANTA_SKIP_ROOT_CHECK=1
        export DREVANTA_RUNUSER="$mockdir/runuser"
        # Public HTTPS access layer (sandboxed):
        export DREVANTA_HTTPS_MODE="no"          # default: tests opt in explicitly
        export DREVANTA_HTTPS_WAIT="3"
        export DREVANTA_ACCESS_CONFIG="$sandbox/https.json"
        export DREVANTA_HTTPS_STATE_DIR="$sandbox/https-state"
        export DREVANTA_SYSTEMD_UNIT_DIR="$sandbox/systemd"
        export DREVANTA_SYSTEMCTL="$mockdir/systemctl"
        export DREVANTA_ACCESS_FAKE_ROOT=1
        export DREVANTA_SKIP_USERADD=1
        export DREVANTA_HTTP_PORT="18081"
        export DREVANTA_HTTPS_PORT="18443"
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
        # Noninteractive run: the token must appear NOWHERE in the output.
        if grep -q "drv1_" "$sandbox/install.log"; then fail "token leaked to noninteractive stdout"; else pass "no token in noninteractive stdout"; fi
        grep -q "noninteractive install: claim token written to" "$sandbox/install.log" \
                && pass "noninteractive file delivery reported" || fail "no file-delivery message"
}

test_interactive_success_screen() {
        section "interactive install: locked first-time-setup screen"
        rm -f "$MOCK_STATUS_FILE"   # back to unclaimed, no active token
        DREVANTA_INTERACTIVE=1 "$DEPLOY/install.sh" install \
                --binary "$sandbox/staged-drevantad" >"$sandbox/install_tty.log" 2>&1
        local rc=$?
        [[ $rc -eq 0 ]] && pass "installer exited 0" || { fail "exit $rc"; sed -n '1,40p' "$sandbox/install_tty.log"; return; }
        grep -q "DREVANTA INSTALLED SUCCESSFULLY" "$sandbox/install_tty.log" && pass "success headline" || fail "no headline"
        grep -q "Drevanta is running securely." "$sandbox/install_tty.log" && pass "running-securely copy" || fail "no running-securely copy"
        grep -qE "ssh -L 18080:127\.0\.0\.1:8080 <ssh-user>@<your-vps-ip>" "$sandbox/install_tty.log" \
                && pass "tunnel command with labelled placeholders (unknown session)" || fail "tunnel line wrong"
        grep -q "On your computer (NOT inside this VPS shell)" "$sandbox/install_tty.log" \
                && pass "tunnel command runs on the user's computer, stated" || fail "no on-your-computer wording"
        grep -q "sudo drevanta access enable-https" "$sandbox/install_tty.log" && pass "HTTPS retry command shown" || fail "no retry command"
        grep -q "reconnect with the command above or open a second local terminal" "$sandbox/install_tty.log" \
                && pass "already-connected guidance shown" || fail "no already-connected guidance"
        local link_token
        link_token="$(grep -oE 'http://127\.0\.0\.1:18080/onboarding#token=[A-Za-z0-9_-]+' "$sandbox/install_tty.log" | head -n1 | sed 's/.*#token=//')"
        if [[ "$link_token" =~ ^drv1_[A-Za-z0-9_-]{43}$ ]]; then pass "prefilled onboarding link with full token"; else fail "link token malformed: '$link_token'"; fi
        grep -q "Raw token (fallback): $link_token" "$sandbox/install_tty.log" && pass "raw token fallback matches link" || fail "fallback token missing/mismatched"
        grep -q "Token expires in 30 minutes." "$sandbox/install_tty.log" && pass "30-minute default TTL stated" || fail "TTL copy wrong"
        grep -q "This link can be used only once." "$sandbox/install_tty.log" && pass "single-use note" || fail "no single-use note"
        grep -q "replace the placeholder" "$sandbox/install_tty.log" && pass "placeholder explained" || fail "no placeholder note"
        # The screen IS the delivery channel here: no silent extra file.
        grep -q "a copy is also saved root-only" "$sandbox/install_tty.log" && fail "unexpected token file" || pass "no token file without --token-file"
        # The token in the link must be the one stored hashed in the DB.
        "$sandbox/opt/current/drevantad" claim status --data-dir "$sandbox/state" 2>/dev/null | grep -q "claim_token_active=true" \
                && pass "screen token is the live DB token" || fail "token not active in DB"
}

test_interactive_uses_real_connection_info() {
        section "interactive install: real SSH session info when known"
        rm -f "$MOCK_STATUS_FILE"
        DREVANTA_INTERACTIVE=1 DREVANTA_SSH_HOST="203.0.113.7" SUDO_USER="" SSH_CONNECTION="198.51.100.9 51000 203.0.113.7 22" \
                "$DEPLOY/install.sh" install --binary "$sandbox/staged-drevantad" >"$sandbox/install_conn.log" 2>&1
        local rc=$?
        [[ $rc -eq 0 ]] && pass "installer exited 0" || { fail "exit $rc"; sed -n '1,40p' "$sandbox/install_conn.log"; return; }
        grep -q "ssh -L 18080:127.0.0.1:8080 root@203.0.113.7" "$sandbox/install_conn.log" \
                && pass "server IP + root taken from the real SSH session" || fail "connection info not used"
        if grep -q "<your-vps-ip>" "$sandbox/install_conn.log"; then fail "placeholder used despite known IP"; else pass "no placeholder when known"; fi
}

test_https_fresh_install_success() {
        section "fresh install, HTTPS auto: public HTTPS provisioned, §6 success screen"
        rm -f "$MOCK_STATUS_FILE"
        rm -rf "$sandbox/https-state"; mkdir -p "$sandbox/https-state"
        # A live certificate (secret-free status file the proxy writes).
        cat > "$sandbox/https-state/status.json" <<'EOF'
{"phase":"active","host":"23.20.15.8","profile":"shortlived","staging":false,
 "not_before":"2026-11-01T00:00:00Z","not_after":"2030-01-01T00:00:00Z",
 "issued_at":"2026-11-01T00:00:00Z","renewals":0,"updated_at":"2026-11-01T00:00:00Z"}
EOF
        DREVANTA_INTERACTIVE=1 DREVANTA_HTTPS_MODE=auto \
                SSH_CONNECTION="198.51.100.9 51000 23.20.15.8 22" \
                "$DEPLOY/install.sh" install --binary "$sandbox/staged-drevantad" >"$sandbox/install_https.log" 2>&1
        local rc=$?
        [[ $rc -eq 0 ]] && pass "installer exited 0" || { fail "exit $rc"; sed -n '1,60p' "$sandbox/install_https.log"; return; }
        grep -q "OPEN DREVANTA IN YOUR BROWSER" "$sandbox/install_https.log" && pass "§6 HTTPS success screen" || fail "no HTTPS success screen"
        grep -qE "https://23\.20\.15\.8/onboarding\#token=[A-Za-z0-9_-]{44}" "$sandbox/install_https.log" \
                && pass "setup link uses the public HTTPS address" || fail "HTTPS setup link wrong"
        grep -q "No domain required." "$sandbox/install_https.log" && pass "no-domain-required copy" || fail "no no-domain copy"
        grep -q "HTTPS enabled · Certificate valid" "$sandbox/install_https.log" && pass "secure access summary" || fail "no secure access summary"
        grep -q '"host": "23.20.15.8"' "$sandbox/https.json" && pass "access config written" || fail "config missing/wrong host"
        grep -q "enable --now drevanta-https.service" "$sandbox/calls_full" && pass "proxy unit enabled via systemd" || fail "unit not enabled"
        [[ -f "$sandbox/systemd/drevanta-https.service" ]] && pass "hardened proxy unit written" || fail "proxy unit missing"
        grep -q "User=drevanta-https" "$sandbox/systemd/drevanta-https.service" && pass "proxy unit runs unprivileged" || fail "proxy unit user wrong"
        # The claim CLI (unprivileged user context) must ALSO pick the https link.
        local claim_link
        claim_link="$("$sandbox/opt/current/drevantad" claim regenerate --data-dir "$sandbox/state" 2>&1 >/dev/null | grep -oE 'https://23\.20\.15\.8/onboarding#token=[A-Za-z0-9_-]+' | head -n1)"
        [[ -n "$claim_link" ]] && pass "claim screen switches to the HTTPS link when live" || fail "claim screen still tunnel-only"
}

test_https_upgrade_leaves_config() {
        section "upgrade with existing config: access configuration untouched"
        local before after
        before="$(cat "$sandbox/https.json")"
        DREVANTA_INTERACTIVE=0 DREVANTA_TOKEN_FILE="$sandbox/state/claim-upgrade.txt" \
                "$DEPLOY/install.sh" install --binary "$sandbox/staged-drevantad" \
                >"$sandbox/install_upgrade_access.log" 2>&1 && pass "upgrade exit 0" || { fail "upgrade exit $?"; sed -n '1,50p' "$sandbox/install_upgrade_access.log"; }
        grep -q "access configuration left untouched" "$sandbox/install_upgrade_access.log" \
                && pass "explicitly reported untouched" || fail "no untouched message"
        after="$(cat "$sandbox/https.json")"
        [[ "$before" == "$after" ]] && pass "config byte-identical after upgrade" || fail "config changed on upgrade"
        if grep -q "PUBLIC HTTPS IS LIVE" "$sandbox/install_upgrade_access.log"; then fail "HTTPS attempted on upgrade"; else pass "no HTTPS attempt on upgrade"; fi
}

test_https_force_failure_fallback() {
        section "HTTPS attempt fails: honest fallback screen, no public HTTP"
        cat > "$mockdir/systemctl-fail" <<'EOF'
#!/usr/bin/env bash
if [[ "$1 $2 $3" == "enable --now drevanta-https.service" ]]; then
        echo "mock: enable failed" >&2; exit 1
fi
exit 0
EOF
        chmod +x "$mockdir/systemctl-fail"
        # Reset any state from earlier HTTPS tests so this attempt genuinely
        # goes through the failure path.
        rm -rf "$sandbox/https-state"; mkdir -p "$sandbox/https-state"
        DREVANTA_INTERACTIVE=1 DREVANTA_HTTPS_MODE=yes DREVANTA_SYSTEMCTL="$mockdir/systemctl-fail" \
                "$DEPLOY/install.sh" install --binary "$sandbox/staged-drevantad" >"$sandbox/install_httpsfail.log" 2>&1 \
                && pass "installer still exits 0 (non-fatal)" || fail "installer exit $?"
        grep -q "Direct HTTPS could not be configured:" "$sandbox/install_httpsfail.log" && pass "failure reason shown" || fail "no failure reason"
        grep -q "starting drevanta-https.service failed" "$sandbox/install_httpsfail.log" && pass "precise reason propagated" || fail "reason not propagated"
        grep -q "sudo drevanta access enable-https" "$sandbox/install_httpsfail.log" && pass "retry command shown" || fail "no retry command"
        if grep -q "OPEN DREVANTA IN YOUR BROWSER" "$sandbox/install_httpsfail.log"; then fail "success screen shown on failure"; else pass "no success screen on failure"; fi
        if grep -qE "https://[0-9a-zA-Z.]+/onboarding" "$sandbox/install_httpsfail.log"; then fail "public HTTPS link shown despite failure"; else pass "no public HTTPS link offered"; fi
        if grep "http://" "$sandbox/install_httpsfail.log" | grep -qv "127.0.0.1"; then fail "public HTTP URL offered"; else pass "no public HTTP fallback (loopback only)"; fi
        rm -f "$mockdir/systemctl-fail"
}

test_access_cli_status_and_disable() {
        section "access CLI: status report and disable semantics"
        local bin="$sandbox/opt/current/drevantad" out
        out="$(SSH_CONNECTION="198.51.100.9 51000 23.20.15.8 22" "$bin" access status 2>/dev/null)"
        grep -q "ACCESS MODE" <<<"$out" && pass "status renders" || fail "status broken"
        grep -q "SSH-TUNNEL FALLBACK" <<<"$out" && pass "status shows tunnel fallback" || fail "no tunnel fallback"
        grep -q "this session:  server address 23.20.15.8" <<<"$out" \
                && pass "status reports the session's server address" || fail "session address not reported"
        "$bin" access disable-https >/dev/null 2>&1 && pass "disable-https exits 0" || fail "disable failed"
        grep -q "disable --now drevanta-https.service" "$sandbox/calls_full" && pass "unit disabled via systemd" || fail "unit not disabled"
        grep -q '"enabled": false' "$sandbox/https.json" && pass "config marked disabled (kept for re-enable)" || fail "config not disabled"
        out="$($bin access status 2>/dev/null)"
        grep -q "public HTTPS:  disabled" <<<"$out" && pass "status reflects disabled" || fail "status stale"
        # Data-preservation promise: disabling must not touch the daemon or state.
        [[ -d "$sandbox/state" ]] && pass "backend state untouched by disable" || fail "state wiped"
        # enable-https after disable re-uses the config and re-enables.
        # (Pre-write a live status as the running proxy would provide.)
        cat > "$sandbox/https-state/status.json" <<'EOF'
{"phase":"active","host":"23.20.15.8","profile":"shortlived","staging":false,
 "not_before":"2026-11-01T00:00:00Z","not_after":"2030-01-01T00:00:00Z",
 "issued_at":"2026-11-01T00:00:00Z","renewals":1,"updated_at":"2026-11-01T00:00:00Z"}
EOF
        DREVANTA_HTTPS_MODE=no SSH_CONNECTION="198.51.100.9 51000 23.20.15.8 22" \
                "$bin" access enable-https >/dev/null 2>&1 && pass "re-enable exits 0" || fail "re-enable failed"
        grep -q '"host": "23.20.15.8"' "$sandbox/https.json" && pass "re-enable kept target host" || fail "host lost"
}

test_wrapper_access() {
        section "drevanta front-end: access passthrough"
        local wrapper="$sandbox/bin-link/drevanta" out
        [[ -x "$wrapper" ]] || { fail "front-end not installed"; return; }
        out="$("$wrapper" access status 2>/dev/null)"
        grep -q "ACCESS MODE" <<<"$out" && pass "drevanta access status works" || fail "wrapper access broken"
        "$wrapper" help 2>/dev/null | grep -q "access enable-https" && pass "wrapper help lists access" || fail "help missing access"
}

test_noninteractive_default_token_file() {
        section "noninteractive install: protected default token file"
        rm -f "$MOCK_STATUS_FILE"
        rm -f "$sandbox/state/claim-default.txt"
        DREVANTA_INTERACTIVE=0 DREVANTA_TOKEN_FILE="$sandbox/state/claim-default.txt" \
                "$DEPLOY/install.sh" install --binary "$sandbox/staged-drevantad" >"$sandbox/install_ni.log" 2>&1
        local rc=$?
        [[ $rc -eq 0 ]] && pass "installer exited 0" || { fail "exit $rc"; sed -n '1,40p' "$sandbox/install_ni.log"; return; }
        [[ -s "$sandbox/state/claim-default.txt" ]] && pass "default token file written" || fail "no default token file"
        [[ "$(stat -c '%a' "$sandbox/state/claim-default.txt")" == "600" ]] && pass "default token file 0600" || fail "wrong mode"
        if grep -q "drv1_" "$sandbox/install_ni.log"; then fail "token leaked in noninteractive mode"; else pass "no token in noninteractive output"; fi
        grep -q "sudo cat $sandbox/state/claim-default.txt" "$sandbox/install_ni.log" && pass "retrieval instructions shown" || fail "no retrieval hint"
        if grep -q "DREVANTA INSTALLED SUCCESSFULLY" "$sandbox/install_ni.log"; then fail "interactive screen shown noninteractively"; else pass "no success screen in noninteractive mode"; fi
}

test_no_reissue_when_token_active() {
        section "re-run with a live claim token does not rotate it"
        echo '{"instance_status":"unclaimed","mfa_mode":"","claim_token_active":true,"authenticated":false}' > "$MOCK_STATUS_FILE"
        local before after
        before="$("$sandbox/opt/current/drevantad" claim status --data-dir "$sandbox/state" 2>/dev/null)"
        DREVANTA_INTERACTIVE=0 "$DEPLOY/install.sh" install --binary "$sandbox/staged-drevantad" >"$sandbox/install_keep.log" 2>&1 \
                && pass "exit 0" || fail "non-zero exit"
        grep -q "it is NOT re-issued" "$sandbox/install_keep.log" && pass "rotation explicitly refused" || fail "no keep-token message"
        if grep -q "claim token written to" "$sandbox/install_keep.log"; then fail "a new token was issued anyway"; else pass "no new token issued"; fi
        after="$("$sandbox/opt/current/drevantad" claim status --data-dir "$sandbox/state" 2>/dev/null)"
        [[ "$before" == "$after" ]] && pass "token state untouched" || fail "token state changed: $before -> $after"
}

test_claim_init_cli_screen() {
        section "claim init CLI: raw token on stdout, setup screen on stderr"
        local out err tok
        out="$("$sandbox/opt/current/drevantad" claim init --data-dir "$sandbox/state" 2>"$sandbox/claim_err.log")"
        err="$(cat "$sandbox/claim_err.log")"
        tok="$(printf '%s' "$out" | head -n1)"
        [[ "$tok" =~ ^drv1_[A-Za-z0-9_-]{43}$ ]] && pass "stdout line 1 is the raw token" || fail "stdout token malformed: '$tok'"
        printf '%s' "$err" | grep -q "CLAIM TOKEN ISSUED" && pass "screen headline on stderr" || fail "no screen headline"
        printf '%s' "$err" | grep -q "http://127.0.0.1:18080/onboarding#token=$tok" && pass "setup link matches stdout token" || fail "link mismatch"
        printf '%s' "$err" | grep -q "Raw token (fallback): $tok" && pass "raw fallback present" || fail "no fallback"
        printf '%s' "$err" | grep -q "Token expires in 30 minutes." && pass "30-minute default TTL" || fail "TTL copy wrong"
        printf '%s' "$err" | grep -q "<your-vps-ip>" && pass "placeholder when session unknown" || fail "placeholder missing"
        # Rotation on purpose: a second init succeeds while unclaimed and the
        # newest token is the only active one (store semantics covered in Go).
        local tok2
        tok2="$("$sandbox/opt/current/drevantad" claim init --data-dir "$sandbox/state" 2>/dev/null | head -n1)"
        [[ "$tok2" =~ ^drv1_ && "$tok2" != "$tok" ]] && pass "authorized regeneration issues a fresh token" || fail "regeneration broken"
}

test_wrapper_frontend() {
        section "friendly drevanta front-end (sudo drevanta claim regenerate)"
        [[ -x "$sandbox/bin-link/drevanta" ]] && pass "wrapper installed to bin-link dir" || { fail "wrapper missing"; return; }

        local st_out
        st_out="$("$sandbox/bin-link/drevanta" claim status 2>/dev/null)"
        [[ "$st_out" == *"instance_status="* ]] && pass "claim status works through the wrapper" || fail "status via wrapper broken: $st_out"

        local ver
        ver="$("$sandbox/bin-link/drevanta" version 2>/dev/null)"
        [[ "$ver" == "$(get_version "$sandbox/staged-drevantad")" ]] && pass "version passthrough" || fail "version mismatch: $ver"

        local o e tok
        o="$("$sandbox/bin-link/drevanta" claim regenerate 2>"$sandbox/wrapper_err.log")"
        e="$(cat "$sandbox/wrapper_err.log")"
        tok="$(printf '%s' "$o" | head -n1)"
        [[ "$tok" =~ ^drv1_[A-Za-z0-9_-]{43}$ ]] && pass "regenerate via wrapper prints raw token" || fail "wrapper token malformed: '$tok'"
        printf '%s' "$e" | grep -q "NEW CLAIM TOKEN ISSUED" && pass "regenerate screen headline" || fail "no regenerate headline"
        printf '%s' "$e" | grep -q "http://127.0.0.1:18080/onboarding#token=$tok" && pass "wrapper link matches token" || fail "wrapper link mismatch"
        printf '%s' "$e" | grep -q "The previous claim token (if any) is now invalid." && pass "rotation explained to the operator" || fail "no rotation note"
        printf '%s' "$e" | grep -q "Token expires in 30 minutes." && pass "wrapper screen TTL copy" || fail "wrapper TTL copy wrong"

        "$sandbox/bin-link/drevanta" serve >/dev/null 2>&1
        [[ $? -eq 2 ]] && pass "wrapper refuses to start the daemon manually" || fail "serve not blocked"
        "$sandbox/bin-link/drevanta" --help >/dev/null 2>&1 && pass "wrapper help works" || fail "wrapper help broken"
        "$sandbox/bin-link/drevanta" 2>/dev/null | grep -q "claim regenerate" && pass "bare wrapper advertises regenerate" || fail "no usage hint"
}

test_skip_claim() {
        section "--skip-claim: upgrade path that never touches tokens"
        # The state dir already holds a live claim token (earlier tests). A
        # --skip-claim install must leave it exactly as it is: no rotation,
        # no delivery, no token file. Same state dir also keeps the unit
        # byte-identical for the idempotency assertions that follow.
        local before_status
        before_status="$("$sandbox/opt/current/drevantad" claim status --data-dir "$sandbox/state" 2>/dev/null)"
        grep -q "claim_token_active=true" <<<"$before_status" \
                && : || fail "precondition: no live token in shared state"
        rm -f "$sandbox/state/claim-default.txt"
        "$DEPLOY/install.sh" install --binary "$sandbox/staged-drevantad" \
                --skip-claim >"$sandbox/install_skip.log" 2>&1
        local rc=$?
        [[ $rc -eq 0 ]] && pass "installer exited 0" || { fail "exit $rc"; sed -n '1,40p' "$sandbox/install_skip.log"; return; }
        grep -q "claim-token step skipped" "$sandbox/install_skip.log" && pass "skip reported" || fail "no skip message"
        grep -q "sudo drevanta claim regenerate" "$sandbox/install_skip.log" && pass "documented next step shown" || fail "no next step"
        if grep -q "drv1_" "$sandbox/install_skip.log"; then fail "token leaked despite --skip-claim"; else pass "no token anywhere in output"; fi
        if grep -q "claim token written to" "$sandbox/install_skip.log"; then fail "token file written despite --skip-claim"; else pass "no token file delivered"; fi
        local after_status
        after_status="$("$sandbox/opt/current/drevantad" claim status --data-dir "$sandbox/state" 2>/dev/null)"
        [[ "$after_status" == "$before_status" ]] && pass "live token untouched (no rotation)" \
                || fail "token state changed: $before_status -> $after_status"
}

# ---------------------------------------------------------------------------
# Bootstrap (the prepared public one-command installer)
# ---------------------------------------------------------------------------

# A minimal GitHub-Releases-shaped fixture server:
#   GET /releases/latest                     -> 302 to /releases/download/<tag>/
#   GET /releases/download/<tag>/<artifact>  -> file from $1 (missing = 404)
# Writes its bound port to $2. Mimics exactly the URL layout
# deploy/bootstrap.sh expects from the official release source.
write_fixture_server() {
        cat > "$sandbox/fixture_server.py" <<'PYEOF'
import http.server
import os
import sys
import urllib.parse

FIXTURE = sys.argv[1]
TAG = sys.argv[2]
PORT_FILE = sys.argv[3]


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):
        path = urllib.parse.urlparse(self.path).path
        if path == '/releases/latest':
            # GitHub shape: redirect to the tag PAGE (no trailing slash).
            self.send_response(302)
            self.send_header('Location', '/releases/tag/%s' % TAG)
            self.end_headers()
            return
        if path.startswith('/releases/tag/'):
            self.send_response(200)
            self.send_header('Content-Type', 'text/html')
            self.end_headers()
            self.wfile.write(b'<html><body>tag page</body></html>')
            return
        if path.startswith('/releases/download/'):
            rest = path[len('/releases/download/'):]
            parts = rest.split('/', 1)
            if len(parts) == 2 and parts[0] == TAG and parts[1]:
                candidate = os.path.join(FIXTURE, parts[1])
                if os.path.isfile(candidate):
                    self.send_response(200)
                    self.send_header('Content-Type', 'application/octet-stream')
                    self.end_headers()
                    with open(candidate, 'rb') as fh:
                        self.wfile.write(fh.read())
                    return
        self.send_response(404)
        self.end_headers()


server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
with open(PORT_FILE, 'w') as fh:
    fh.write(str(server.server_address[1]))
server.serve_forever()
PYEOF
}

test_bootstrap_installer() {
        section "bootstrap: verified download -> install, tampering refused"

        local fixture="$sandbox/bootstrap-fixture"
        mkdir -p "$fixture"
        cp "$sandbox/staged-drevantad" "$fixture/drevantad-linux-amd64"
        cp "$DEPLOY/install.sh" "$DEPLOY/drevanta.sh" "$DEPLOY/rollback.sh" \
                "$DEPLOY/healthgate.sh" "$fixture/"
        ( cd "$fixture" && sha256sum drevantad-linux-amd64 install.sh drevanta.sh \
                rollback.sh healthgate.sh > checksums.txt )
        write_fixture_server
        python3 "$sandbox/fixture_server.py" "$fixture" v0.0.0-test \
                "$sandbox/fixture.port" >/dev/null 2>&1 &
        local srv_pid=$!
        local tries=0
        while [[ ! -s "$sandbox/fixture.port" && $tries -lt 50 ]]; do
                sleep 0.1
                tries=$((tries + 1))
        done
        if [[ ! -s "$sandbox/fixture.port" ]]; then
                kill "$srv_pid" 2>/dev/null || true
                fail "fixture server did not start"
                return
        fi
        local port base
        port="$(cat "$sandbox/fixture.port")"
        base="http://127.0.0.1:$port/releases"
        export REAL_CURL_URL_PREFIX="$base/"
        export REAL_CURL_BIN

        # Isolated opt/state dirs so this install is unambiguous; the unit
        # path stays shared (the systemctl mock watches that exact file).
        local boot_opt="$sandbox/opt-boot"
        mkdir -p "$boot_opt/releases"
        export MOCK_OPT_DIR="$boot_opt"
        echo '{"instance_status":"unclaimed","mfa_mode":"","claim_token_active":false,"authenticated":false}' \
                > "$MOCK_STATUS_FILE"
        local env_pairs=(
                DREVANTA_SKIP_ROOT_CHECK=1
                DREVANTA_BOOTSTRAP_ALLOW_HTTP=1
                DREVANTA_INTERACTIVE=0
                DREVANTA_TOKEN_FILE="$sandbox/state-boot/claim.txt"
                DREVANTA_OPT_DIR="$boot_opt"
                DREVANTA_STATE_DIR="$sandbox/state-boot"
                DREVANTA_BIN_LINK_DIR="$sandbox/bin-link-boot"
        )

        # 1. A plain-HTTP release source is refused without the test escape.
        if env -u DREVANTA_BOOTSTRAP_ALLOW_HTTP "${env_pairs[0]}" \
                DREVANTA_OPT_DIR="$boot_opt" DREVANTA_STATE_DIR="$sandbox/state-boot" \
                DREVANTA_BIN_LINK_DIR="$sandbox/bin-link-boot" \
                "$DEPLOY/bootstrap.sh" --base-url "$base" \
                >"$sandbox/boot-http.log" 2>&1; then
                fail "plain-HTTP release source accepted"
        elif grep -q "non-HTTPS" "$sandbox/boot-http.log"; then
                pass "plain-HTTP release source refused"
        else
                fail "unexpected refusal: $(head -n 1 "$sandbox/boot-http.log")"
        fi

        # 2. A tampered artifact fails closed and installs nothing.
        printf 'tampered' >> "$fixture/drevantad-linux-amd64"
        if env "${env_pairs[@]}" "$DEPLOY/bootstrap.sh" \
                --release v0.0.0-test --base-url "$base" \
                >"$sandbox/boot-tamper.log" 2>&1; then
                fail "tampered artifact passed verification"
        elif grep -q "integrity check FAILED" "$sandbox/boot-tamper.log"; then
                pass "tampered artifact refused (fail closed)"
        else
                fail "unexpected bootstrap failure: $(head -n 1 "$sandbox/boot-tamper.log")"
        fi
        [[ ! -e "$boot_opt/current" ]] && pass "nothing installed after refusal" \
                || fail "release installed despite failed verification"

        # 3. A missing checksum entry refuses even a bit-identical download.
        cp "$sandbox/staged-drevantad" "$fixture/drevantad-linux-amd64"
        grep -v ' healthgate.sh$' "$fixture/checksums.txt" > "$fixture/checksums.partial"
        mv "$fixture/checksums.partial" "$fixture/checksums.txt"
        if env "${env_pairs[@]}" "$DEPLOY/bootstrap.sh" \
                --release v0.0.0-test --base-url "$base" \
                >"$sandbox/boot-nosum.log" 2>&1; then
                fail "missing checksum entry tolerated"
        elif grep -q "no sha256 entry for 'healthgate.sh'" "$sandbox/boot-nosum.log"; then
                pass "missing checksum entry refused"
        else
                fail "unexpected bootstrap failure: $(head -n 1 "$sandbox/boot-nosum.log")"
        fi

        # 4. A missing artifact (404) aborts before anything is executed.
        ( cd "$fixture" && sha256sum drevantad-linux-amd64 install.sh drevanta.sh \
                rollback.sh healthgate.sh > checksums.txt )
        rm -f "$fixture/rollback.sh"
        if env "${env_pairs[@]}" "$DEPLOY/bootstrap.sh" \
                --release v0.0.0-test --base-url "$base" \
                >"$sandbox/boot-404.log" 2>&1; then
                fail "missing artifact tolerated"
        elif grep -q "downloading rollback.sh failed" "$sandbox/boot-404.log"; then
                pass "missing artifact aborts before execution"
        else
                fail "unexpected bootstrap failure: $(head -n 1 "$sandbox/boot-404.log")"
        fi

        # 5. Happy path: 'latest' resolved via the redirect, every artifact
        #    verified, then the REAL install.sh runs from the staged payload.
        cp "$DEPLOY/rollback.sh" "$fixture/rollback.sh"
        if env "${env_pairs[@]}" "$DEPLOY/bootstrap.sh" --base-url "$base" \
                >"$sandbox/boot-ok.log" 2>&1; then
                pass "bootstrap install exit 0"
        else
                fail "bootstrap install failed"
                sed -n '1,50p' "$sandbox/boot-ok.log"
        fi
        grep -q "resolving the latest published release" "$sandbox/boot-ok.log" \
                && pass "latest release resolved via redirect" || fail "latest not resolved"
        grep -q "integrity verified" "$sandbox/boot-ok.log" \
                && pass "payload verified before execution" || fail "verification not logged"
        grep -q "is installed and running" "$sandbox/boot-ok.log" \
                && pass "install.sh completed through the bootstrap" || fail "install did not finish"
        [[ -x "$boot_opt/current/drevantad" ]] \
                && pass "release installed from verified download" || fail "no release installed"
        [[ "$(get_version "$boot_opt/current/drevantad")" == "$(get_version "$sandbox/staged-drevantad")" ]] \
                && pass "installed binary is the verified one" || fail "wrong binary installed"
        # The Sprint 2.5 token-delivery policy survives the bootstrap path:
        # noninteractive install delivers via the 0600 file, never stdout.
        [[ -s "$sandbox/state-boot/claim.txt" ]] && pass "claim token delivered via file" \
                || fail "no claim token file"
        [[ "$(stat -c '%a' "$sandbox/state-boot/claim.txt")" == "600" ]] \
                && pass "claim token file is 0600" || fail "token file mode wrong"
        if grep -q "drv1_" "$sandbox/boot-ok.log"; then
                fail "token leaked through bootstrap output"
        else
                pass "no token in bootstrap output"
        fi
        rm -f "$sandbox/state-boot/claim.txt"

        kill "$srv_pid" 2>/dev/null || true
        unset REAL_CURL_URL_PREFIX REAL_CURL_BIN
        export MOCK_OPT_DIR="$sandbox/opt"
}

test_docs_match_cli() {
        section "documentation commands match the actual CLI"
        local repo
        repo="$(dirname "$DEPLOY")"
        local doc="$repo/docs/INSTALLATION.md"
        [[ -f "$doc" ]] && pass "docs/INSTALLATION.md exists" || { fail "installation guide missing"; return; }
        local bin="$sandbox/staged-drevantad"

        # Commands documented in the guide must be real: cross-check against
        # the binary's own usage output.
        local cli_help claim_help access_help
        cli_help="$("$bin" help 2>&1 || true)"
        claim_help="$("$bin" claim 2>&1 || true)"
        access_help="$("$bin" access --help 2>&1 || true)"
        local frag
        for frag in "claim regenerate" "claim status" "claim revoke"; do
                if grep -qF "$frag" "$doc" && ! printf '%s\n%s' "$cli_help" "$claim_help" | grep -qF "$frag"; then
                        fail "docs reference '$frag' but the CLI does not list it"
                else
                        pass "doc command '$frag' exists in the CLI"
                fi
        done
        for frag in "access status" "access enable-https" "access disable-https" "access configure"; do
                if grep -qF "$frag" "$doc" && ! printf '%s' "$access_help" | grep -qF "${frag#access }"; then
                        fail "docs reference '$frag' but the access CLI does not list it"
                else
                        pass "doc command '$frag' exists in the CLI"
                fi
        done
        # The guide must teach the real, locked tunnel + link format.
        for frag in "ssh -L 18080:127.0.0.1:8080" "/onboarding#token=" "Token expires" "sudo drevanta claim regenerate"; do
                grep -qF "$frag" "$doc" && pass "guide states: $frag" || fail "guide missing: $frag"
        done
        # README must link the guide prominently.
        grep -qF "docs/INSTALLATION.md" "$repo/README.md" \
                && pass "README links the installation guide" || fail "README does not link the guide"
        # The wrapper's advertised commands must exist in drevantad.
        for frag in "regenerate" "status" "revoke"; do
                grep -qF "claim $frag" "$DEPLOY/drevanta.sh" \
                        && pass "wrapper advertises claim $frag" || fail "wrapper missing claim $frag"
        done
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
        section "uninstall removes unit, front-end, keeps releases and state"
        "$DEPLOY/install.sh" --uninstall >"$sandbox/uninstall.log" 2>&1 && pass "exit 0" || fail "exit $?"
        [[ ! -f "$sandbox/systemd/drevanta.service" ]] && pass "unit removed" || fail "unit still present"
        [[ ! -e "$sandbox/bin-link/drevanta" ]] && pass "front-end removed" || fail "front-end still present"
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
        test_https_fresh_install_success
        test_install_fresh_and_claim
        test_https_upgrade_leaves_config
        test_https_force_failure_fallback
        test_interactive_success_screen
        test_interactive_uses_real_connection_info
        test_noninteractive_default_token_file
        test_no_reissue_when_token_active
        test_claim_init_cli_screen
        test_wrapper_frontend
        test_wrapper_access
        test_skip_claim
        test_docs_match_cli
        test_access_cli_status_and_disable
        test_install_idempotent
        test_install_upgrade_and_autorollback
        test_rollback_script
        test_rollback_no_target
        test_healthgate
        test_bootstrap_installer
        test_uninstall

        echo
        echo "== results: $PASS passed, $FAIL failed =="
        [[ $FAIL -eq 0 ]]
}

main "$@"
