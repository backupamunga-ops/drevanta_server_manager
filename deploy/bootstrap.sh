#!/usr/bin/env bash
# ============================================================================
# Drevanta bootstrap — the public one-command installation entry point.
#
#   INTENDED SERVING MODEL (not live yet):
#
#     curl -fsSL https://github.com/backupamunga-ops/drevanta_server_manager \
#       /releases/latest/download/bootstrap.sh | sh
#
#   This script becomes official only when the first GitHub release that
#   carries the artifacts below is published and announced. Until then it
#   is exercised exclusively by the installer test harness.
#
# What it does:
#   1. Resolves the requested release (default: latest) over HTTPS.
#   2. Downloads the release payload into a private temp directory:
#        drevantad-linux-amd64, install.sh, drevanta.sh,
#        rollback.sh, healthgate.sh, checksums.txt
#   3. VERIFIES authenticity and integrity, all-or-nothing, strictly
#      before executing anything:
#        - checksums.txt must carry a valid Ed25519 signature made with
#          the official Drevanta release key (embedded below); a missing
#          signature, an unusable openssl, or a failed verification
#          aborts with nothing changed;
#        - every artifact must match its sha256 in the signed
#          checksums.txt.
#   4. Hands over to the existing, tested deploy/install.sh (no
#      installation logic is duplicated here) with the staged binary.
#
# Security posture:
#   - HTTPS only: downloads refuse non-HTTPS base URLs and refuse to
#     follow a redirect that downgrades the protocol (--proto-redir).
#   - Signed checksums: swapping an artifact AND its checksums is not
#     enough — the checksums file must be signed by the release key.
#   - No unverified execution: the only piped script is this bootstrap
#     itself (TLS-authenticated from the official source); everything
#     else is signature- AND checksum-verified before it is run.
#   - Verification errors always fail closed.
#
# Usage:
#   bootstrap.sh [--release vX.Y.Z] [--base-url URL] [installer args...]
#
#   --release TAG     pin a release tag (default: latest published release)
#   --base-url URL    release base URL; files live at <base>/download/<tag>/
#                     and "latest" resolves via <base>/latest
#                     (default: the official GitHub Releases of this project)
#   [installer args]  everything else is forwarded to install.sh, e.g.
#                     --skip-claim, --ttl 15m
#
# Environment (test/mirror overrides):
#   DREVANTA_BOOTSTRAP_BASE_URL     same as --base-url
#   DREVANTA_BOOTSTRAP_ALLOW_HTTP   set to 1 ONLY by the test harness to
#                                   serve fixtures over plain-loopback HTTP;
#                                   prints a loud warning when used
#   DREVANTA_BOOTSTRAP_PUBKEY       Ed25519 public key (PEM) replacing the
#                                   embedded one — TEST HARNESS ONLY, prints
#                                   a loud warning; production never sets it
#
# Exit codes: 0 installed · 1 verification/download failure (nothing changed)
#             · 2 usage error
# ============================================================================
set -euo pipefail

DEFAULT_BASE_URL="https://github.com/backupamunga-ops/drevanta_server_manager/releases"

# The release payload. install.sh stages its companions from its own
# directory, so all scripts must sit beside the binary — which is exactly
# the artifact set the release workflow publishes and checksums.
ARTIFACTS=(
        drevantad-linux-amd64
        install.sh
        drevanta.sh
        rollback.sh
        healthgate.sh
)

# Guard: this script uses bash arrays. Piping into `sh` (dash on
# Debian/Ubuntu) would die with a cryptic syntax error — fail with the
# correct command instead, before anything happens.
if [[ -z "${BASH_VERSION:-}" ]]; then
        echo "ERROR: this installer must run under bash. Use:" >&2
        echo "  curl -fsSL <bootstrap-url> | bash" >&2
        exit 2
fi

log()  { printf '==> %s\n' "$*"; }
info() { printf '    %s\n' "$*"; }
error() { printf 'ERROR: %s\n' "$*" >&2; }
die()  { error "$*"; exit 1; }

usage() {
        sed -n '2,50p' "$0" | sed 's/^# \{0,1\}//'
        exit "${1:-0}"
}

need_tool() { command -v "$1" >/dev/null 2>&1 || die "required tool '$1' not found"; }

# The official Drevanta release signing key (Ed25519, PEM). The matching
# private key exists ONLY in the release CI (GitHub Actions secret
# DREVANTA_RELEASE_SIGN_KEY); see deploy/release-signing/.
EMBEDDED_PUBKEY='-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEA+4Tm22+bmx8vVNSi50g2gcujlUyAxO/b37AywvCLXrA=
-----END PUBLIC KEY-----'
PUBKEY="$EMBEDDED_PUBKEY"
if [[ -n "${DREVANTA_BOOTSTRAP_PUBKEY:-}" ]]; then
        echo "WARNING: DREVANTA_BOOTSTRAP_PUBKEY is set — verifying against an OVERRIDE key (test harness only)" >&2
        PUBKEY="$DREVANTA_BOOTSTRAP_PUBKEY"
fi

# ---------------------------------------------------------------------------
# Arguments
# ---------------------------------------------------------------------------
BASE_URL="${DREVANTA_BOOTSTRAP_BASE_URL:-$DEFAULT_BASE_URL}"
RELEASE_TAG=""
FORWARD_ARGS=()

while [[ $# -gt 0 ]]; do
        case "$1" in
                --release)
                        [[ $# -ge 2 ]] || usage 2
                        RELEASE_TAG="$2"
                        shift 2
                        ;;
                --base-url)
                        [[ $# -ge 2 ]] || usage 2
                        BASE_URL="$2"
                        shift 2
                        ;;
                -h | --help)
                        usage 0
                        ;;
                *)
                        # Anything else belongs to install.sh.
                        FORWARD_ARGS+=("$1")
                        shift
                        ;;
        esac
done

# ---------------------------------------------------------------------------
# Preflight (the installer repeats its own, deeper checks later)
# ---------------------------------------------------------------------------
[[ "$(uname -m 2>/dev/null || true)" == "x86_64" ]] ||
        die "this installer ships a linux/amd64 build; $(uname -m 2>&1 || echo 'unknown arch') is not supported yet"
need_tool curl
need_tool sha256sum
need_tool mktemp
need_tool uname
need_tool grep
need_tool awk

case "$BASE_URL" in
        https://*) ;;
        *)
                if [[ "${DREVANTA_BOOTSTRAP_ALLOW_HTTP:-0}" == "1" ]]; then
                        log "WARNING: DREVANTA_BOOTSTRAP_ALLOW_HTTP=1 — downloads over PLAIN HTTP (test harness only)"
                else
                        die "refusing non-HTTPS release source '$BASE_URL' (use the official HTTPS URL)"
                fi
                ;;
esac

# Protocol lock for every transfer (including redirects). The test-harness
# HTTP escape above also unlocks the protocol — it has already warned.
PROTO_ARGS=(--proto '=https' --proto-redir '=https')
if [[ "${DREVANTA_BOOTSTRAP_ALLOW_HTTP:-0}" == "1" ]]; then
        PROTO_ARGS=()
fi

# ---------------------------------------------------------------------------
# Resolve the release tag
# ---------------------------------------------------------------------------
fetch() { # fetch <url> <outfile> — protocol-enforced, retrying
        curl -fsSL --retry 2 "${PROTO_ARGS[@]+"${PROTO_ARGS[@]}"}" -o "$2" "$1"
}

if [[ -z "$RELEASE_TAG" ]]; then
        log "resolving the latest published release"
        effective="$(curl -fsSL -o /dev/null -w '%{url_effective}' \
                "${PROTO_ARGS[@]+"${PROTO_ARGS[@]}"}" "$BASE_URL/latest")" ||
                die "could not resolve the latest release at $BASE_URL/latest — is any release published yet?"
        RELEASE_TAG="${effective##*/}"
        [[ "$RELEASE_TAG" != "latest" && -n "$RELEASE_TAG" ]] ||
                die "could not determine the latest release tag from '$effective'"
fi
DOWNLOAD_URL="$BASE_URL/download/$RELEASE_TAG"
log "release: $RELEASE_TAG"
log "source:  $DOWNLOAD_URL"

# ---------------------------------------------------------------------------
# Download everything first, verify everything next, execute last
# ---------------------------------------------------------------------------
WORKDIR="$(mktemp -d)"
# shellcheck disable=SC2317  # reached via the EXIT trap
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT

for name in "${ARTIFACTS[@]}"; do
        fetch "$DOWNLOAD_URL/$name" "$WORKDIR/$name" ||
                die "downloading $name failed — aborting, nothing was changed on this host"
done
fetch "$DOWNLOAD_URL/checksums.txt" "$WORKDIR/checksums.txt" ||
        die "downloading checksums.txt failed — refusing to install unverified artifacts"

# --- Authenticity: the checksums file must be signed by the release key ---
need_tool openssl
fetch "$DOWNLOAD_URL/checksums.txt.sig" "$WORKDIR/checksums.txt.sig" ||
        die "downloading checksums.txt.sig failed — refusing UNSIGNED release artifacts (fail closed)"
printf '%s\n' "$PUBKEY" > "$WORKDIR/release.pub.pem"
if ! openssl pkeyutl -verify -pubin -inkey "$WORKDIR/release.pub.pem" \
                -rawin -in "$WORKDIR/checksums.txt" -sigfile "$WORKDIR/checksums.txt.sig" \
                > /dev/null 2>&1; then
        error "SIGNATURE VERIFICATION FAILED for checksums.txt"
        die "the release checksums are not signed with the official Drevanta release key — nothing was changed on this host"
fi
log "checksums.txt signature verified against the official release key"

log "verifying release integrity"
for name in "${ARTIFACTS[@]}"; do
        expected="$(awk -v n="$name" '$2 == n || $2 == "*" n { print $1 }' "$WORKDIR/checksums.txt")"
        [[ "$expected" =~ ^[0-9a-f]{64}$ ]] ||
                die "checksums.txt has no sha256 entry for '$name' — refusing to continue"
        actual="$(sha256sum "$WORKDIR/$name" | awk '{print $1}')"
        if [[ "$actual" != "$expected" ]]; then
                error "integrity check FAILED for $name"
                error "  expected sha256: $expected"
                error "  actual   sha256: $actual"
                die "the downloaded release does not match its published checksums — nothing was changed on this host"
        fi
        info "✓ $name  (${actual:0:16}…)"
done

chmod 0755 "$WORKDIR/drevantad-linux-amd64"
"$WORKDIR/drevantad-linux-amd64" version >/dev/null ||
        die "the verified binary does not answer 'version' — refusing to install it"

log "integrity verified — starting the Drevanta installer"
# Run as a child (not exec) so the temp directory is always cleaned up,
# and propagate the installer's exit code faithfully.
status=0
bash "$WORKDIR/install.sh" install --binary "$WORKDIR/drevantad-linux-amd64" \
        "${FORWARD_ARGS[@]+"${FORWARD_ARGS[@]}"}" || status=$?
exit "$status"
