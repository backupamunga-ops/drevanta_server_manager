// drevantad is the Drevanta server management daemon.
//
// Subcommands:
//
//	drevantad serve                 run the loopback HTTP daemon (default when no subcommand)
//	drevantad claim init            create/rotate the first-owner claim token (prints it ONCE)
//	drevantad claim regenerate      friendly alias: re-issue the claim token (prints it ONCE)
//	drevantad claim status          show claim token / onboarding state (no secrets)
//	drevantad claim revoke          revoke the active claim token
//	drevantad access status|enable-https|disable-https|configure
//	                                public HTTPS access (root; manages the separate
//	                                drevanta-https proxy unit and its config)
//	drevantad https-serve           run the public HTTPS proxy worker (systemd only)
//	drevantad version               print the version
//
// Design invariants:
//   - Loopback-only by default: the daemon's only network surface is 127.0.0.1.
//     Public HTTPS is a separate process (drevanta-https) that proxies to it.
//   - Runs unprivileged: no capabilities, no root, no Docker/Nginx helpers.
//   - Metrics are collected on demand from /proc; there is no background
//     sampling loop.
//   - Claim tokens and session cookies exist only as SHA-256 hashes in
//     the database; plaintext tokens are printed exactly once.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/httpsd"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/store"
)

// version is stamped via -ldflags "-X main.version=..." in release builds;
// the release workflow injects the pushed tag (v0.4.3 -> "0.4.3") and fails
// closed if the built binary does not report it. This literal is only the
// fallback for local, non-release builds.
var version = "0.4.16"

// defaultClaimTTL is the locked first-time-setup UX default: a claim
// token lives just long enough to onboard immediately after install.
const defaultClaimTTL = 30 * time.Minute

// setupLinkPort is the local port of the documented SSH tunnel
// (ssh -N -L 18080:127.0.0.1:<port> user@host).
const setupLinkPort = "18080"

// defaultDaemonPort is the daemon's loopback port; only used for display
// of the tunnel command (the real listener port is whatever `serve` binds).
const defaultDaemonPort = "8080"

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "serve":
			os.Exit(cmdServe(os.Args[2:]))
		case "claim":
			os.Exit(cmdClaim(os.Args[2:], os.Stdout, os.Stderr))
		case "access":
			os.Exit(cmdAccess(os.Args[2:], os.Stdout, os.Stderr))
		case "https-serve":
			os.Exit(cmdHTTPServe(os.Args[2:]))
		case "version", "--version":
			fmt.Println(version)
			os.Exit(0)
		case "help", "--help", "-h":
			usage("")
			os.Exit(0)
		}
	}
	// Sprint 1 compatibility: bare invocation (flags only) means serve.
	os.Exit(cmdServe(os.Args[1:]))
}

func usage(topic string) {
	switch topic {
	case "claim":
		fmt.Fprint(os.Stderr, `usage: drevantad claim <init|regenerate|status|revoke> [flags]

  init         create a new first-owner claim token (revokes any previous
               unconsumed token), print it once and show the ready-to-open
               setup link. Only allowed while the instance is unclaimed.
  regenerate   same token semantics as init, worded as the operator-facing
               fallback for a lost or expired setup link — installed as
               "sudo drevanta claim regenerate". Only allowed while the
               instance is unclaimed; permanently refused once an owner
               exists.
  status       print the onboarding state and whether an active, unexpired
               claim token exists (no secrets).
  revoke       revoke the active claim token.

flags:
  --data-dir DIR   state directory holding drevanta.db (default /var/lib/drevanta)
  --ttl DURATION   claim token time-to-live for init (default 30m)
  --port PORT      daemon loopback port shown in the tunnel hint (default 8080)
`)
	default:
		fmt.Fprint(os.Stderr, `usage: drevantad [serve|claim|access|https-serve|version] [flags]

  serve                run the loopback HTTP daemon (default)
  claim init           issue the first-owner claim token and show the setup link
  claim regenerate     re-issue the claim token (lost/expired link fallback)
  claim status         show onboarding state (no secrets)
  claim revoke         revoke the active claim token
  access status|enable-https|disable-https|configure
                       public HTTPS access (root; see: drevanta access --help)
  https-serve          run the public HTTPS proxy worker (used by systemd)
  version              print the version
`)
	}
}

func cmdServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8080", "listen address (loopback only by design)")
	dataDir := fs.String("data-dir", "/var/lib/drevanta", "state directory holding the SQLite database")
	if err := fs.Parse(args); err != nil {
		usage("")
		return 2
	}
	if err := run(*addr, *dataDir); err != nil {
		slogError("drevantad: fatal", err)
		return 1
	}
	return 0
}

func cmdClaim(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage("claim")
		return 2
	}
	fs := flag.NewFlagSet("claim "+args[0], flag.ContinueOnError)
	dataDir := fs.String("data-dir", "/var/lib/drevanta", "state directory holding the SQLite database")
	ttl := fs.Duration("ttl", defaultClaimTTL, "claim token TTL for init")
	port := fs.String("port", defaultDaemonPort, "daemon loopback port shown in the tunnel hint")
	if err := fs.Parse(args[1:]); err != nil {
		usage("claim")
		return 2
	}
	ctx := context.Background()
	st, err := openStore(*dataDir)
	if err != nil {
		slogError("drevantad claim: open store", err)
		return 1
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		slogError("drevantad claim: migrate", err)
		return 1
	}

	switch args[0] {
	case "init", "regenerate":
		return runClaimIssue(ctx, st, args[0], *ttl, *port, stdout, stderr)
	case "status":
		state, err := st.InstanceState(ctx)
		if err != nil {
			slogError("drevantad claim: state", err)
			return 1
		}
		tokenActive := false
		if ct, err := st.ActiveClaimToken(ctx); err == nil && ct.ExpiresAt.After(time.Now()) {
			tokenActive = true
		}
		fmt.Fprintf(stdout, "instance_status=%s mfa_mode=%q claim_token_active=%t\n",
			state.Status, state.MFAMode, tokenActive)
		return 0
	case "revoke":
		ct, err := st.ActiveClaimToken(ctx)
		if err != nil {
			fmt.Fprintln(stderr, "no active claim token")
			return 1
		}
		if err := st.RevokeClaimToken(ctx, ct.ID); err != nil {
			slogError("drevantad claim: revoke", err)
			return 1
		}
		auditEvent(ctx, st, "drevantad", "claim_token_revoked", "claim", nil)
		fmt.Fprintln(stdout, "claim token revoked")
		return 0
	default:
		usage("claim")
		return 2
	}
}

// runClaimIssue is the single token-issuance path shared by `claim init`
// (used by the installer) and `claim regenerate` (the operator-facing
// fallback for a lost or expired link). Both verbosities use the exact
// same security semantics: issue only while unclaimed, rotate out any
// previous unconsumed token, store only the SHA-256 hash, print the
// plaintext exactly once (stdout line 1 + setup screen on stderr), and
// write an audit event that never contains the token.
func runClaimIssue(ctx context.Context, st *store.Store, verb string, ttl time.Duration, port string, stdout, stderr io.Writer) int {
	if state, err := st.InstanceState(ctx); err == nil && state.Status != "unclaimed" {
		fmt.Fprintln(stderr, "instance is already claimed; no claim token is issued")
		return 1
	}
	token, hash, err := newToken()
	if err != nil {
		slogError("drevantad claim: generate", err)
		return 1
	}
	// Authoritative first-owner guard: refuses once claimed, even if the
	// state read above raced with a claim.
	ct, err := st.CreateClaimTokenIfUnclaimed(ctx, hash, ttl)
	if err != nil {
		if errors.Is(err, store.ErrAlreadyClaimed) {
			fmt.Fprintln(stderr, "instance is already claimed; no claim token is issued")
			return 1
		}
		slogError("drevantad claim: store", err)
		return 1
	}
	action := "claim_token_created"
	if verb == "regenerate" {
		action = "claim_token_regenerated"
	}
	auditEvent(ctx, st, "drevantad", action, "claim",
		map[string]any{"ttl_minutes": ttl.Minutes(), "expires_at": ct.ExpiresAt.Format(time.RFC3339)})
	// The plaintext token is printed exactly once: line 1 of stdout
	// (machine-readable) plus the setup screen on stderr. It is never
	// stored or logged anywhere else.
	fmt.Fprintln(stdout, token)
	printClaimSetupScreen(stderr, token, ttl, port, verb)
	return 0
}

// sshHint derives the SSH tunnel target from the current session when it
// is reliably known: SSH_CONNECTION carries the server address the
// operator is connected to, and the invoking identity tells the SSH
// username. The drevanta wrapper (deploy/drevanta.sh) recovers the same
// pair from the operator's own utmp login entry and passes it on as
// DREVANTA_SSH_HOST / DREVANTA_SSH_USER — still first-party session
// data, never a guess. When a value is unknown, a clearly labelled
// placeholder is returned instead.
func sshHint() (ip, user string, complete bool) {
	ip = ""
	if sc := os.Getenv("SSH_CONNECTION"); sc != "" {
		if f := strings.Fields(sc); len(f) >= 3 {
			ip = f[2] // client-ip client-port server-ip server-port
		}
	}
	if envIP := os.Getenv("DREVANTA_SSH_HOST"); envIP != "" {
		ip = envIP
	}
	user = ""
	switch {
	case os.Getenv("DREVANTA_SSH_USER") != "":
		user = os.Getenv("DREVANTA_SSH_USER")
	case os.Getenv("SUDO_USER") != "" && os.Getenv("SUDO_USER") != "root":
		user = os.Getenv("SUDO_USER")
	case os.Geteuid() == 0 && ip != "":
		user = "root" // this session IS root over SSH, not a guess
	}
	return ip, user, ip != "" && user != ""
}

// humanTTL renders the TTL for the setup screen; the locked UX copy says
// "30 minutes", other values fall back to the Go duration string.
func humanTTL(d time.Duration) string {
	if d == defaultClaimTTL {
		return "30 minutes"
	}
	return d.String()
}

// printClaimSetupScreen writes the locked first-time-setup instructions
// to w. verb selects the headline wording: "init" for the installer's
// first token, "regenerate" for the operator-issued replacement. When
// public HTTPS is live the setup link uses https://<host>/ directly; it
// falls back to the SSH-tunnel link otherwise. The token is only ever
// printed to the operator's own terminal (stderr of the claim command),
// never logged or persisted.
func printClaimSetupScreen(w io.Writer, token string, ttl time.Duration, daemonPort, verb string) {
	ip, user, known := sshHint()
	if ip == "" {
		ip = "<your-vps-ip>"
	}
	if user == "" {
		user = "<ssh-user>"
	}

	headline := "✓ CLAIM TOKEN ISSUED"
	if verb == "regenerate" {
		headline = "✓ NEW CLAIM TOKEN ISSUED"
	}

	accessMode, httpsHost := httpsAccessState()
	var link string
	if accessMode == "https" {
		link = fmt.Sprintf("https://%s/onboarding#token=%s", httpsHost, token)
	} else {
		link = fmt.Sprintf("http://127.0.0.1:%s/onboarding#token=%s", setupLinkPort, token)
	}

	fmt.Fprintf(w, `
%s

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  Drevanta first-time setup

  STEP 1 — Open this link in your browser:

    %s
`, headline, link)

	if accessMode == "https" {
		fmt.Fprintf(w, `
  (If the link does not open, use the private SSH-tunnel fallback:
    ssh -L %s:127.0.0.1:%s %s@%s
   then open http://127.0.0.1:%s/onboarding#token=%s )
`, setupLinkPort, daemonPort, user, ip, setupLinkPort, token)
	} else {
		fmt.Fprintf(w, `
  STEP 2 — If the link does not open, first run this ON YOUR
           COMPUTER (not on the server) and keep it connected:

    ssh -N -L %s:127.0.0.1:%s %s@%s

  then open the link again.
`, setupLinkPort, daemonPort, user, ip)
	}

	fmt.Fprintf(w, `
  Token expires in %s.
  This link can be used only once.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  Raw token (fallback): %s

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
`, humanTTL(ttl), token)
	if verb == "regenerate" {
		fmt.Fprintln(w, "  The previous claim token (if any) is now invalid.")
	}
	if !known && accessMode != "https" {
		fmt.Fprintln(w, "  (replace the placeholder with this server's address and your SSH user)")
	}
}

// httpsAccessState reports the current public-HTTPS state from the
// access configuration and the proxy status file. Returns "https" and
// the target host when a valid certificate is being served, "tunnel"
// otherwise. Both files are secret-free; unreadable files mean tunnel.
func httpsAccessState() (mode, host string) {
	p := httpsd.RuntimePathsFromEnv()
	cfg, err := httpsd.LoadRuntimeConfig(p)
	if err != nil || !cfg.Enabled || cfg.Host == "" {
		return "tunnel", ""
	}
	st, err := httpsd.ReadStatus(p.StateDir)
	if err != nil || !st.CertValid(time.Now()) {
		return "tunnel", ""
	}
	return "https", cfg.Host
}
