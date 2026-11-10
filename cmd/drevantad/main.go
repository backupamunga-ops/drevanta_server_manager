// drevantad is the Drevanta server management daemon.
//
// Subcommands:
//
//	drevantad serve              run the HTTP daemon (default when no subcommand)
//	drevantad claim init         create/rotate the first-owner claim token (prints it ONCE)
//	drevantad claim status       show claim token / onboarding state (no secrets)
//	drevantad claim revoke       revoke the active claim token
//	drevantad version            print the version
//
// Design invariants:
//   - Loopback-only by default: the only network surface is 127.0.0.1.
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

	"github.com/backupamunga-ops/drevanta_server_manager/internal/store"
)

// version is stamped via -ldflags "-X main.version=..." in release builds.
var version = "0.3.1-onboard-ux"

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
			os.Exit(cmdClaim(os.Args[2:]))
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
		fmt.Fprint(os.Stderr, `usage: drevantad claim <init|status|revoke> [flags]

  init     create a new first-owner claim token (revokes any previous
           unconsumed token), print it once and show the ready-to-open
           setup link. Only allowed while the instance is unclaimed.
  status   print the onboarding state and whether an active, unexpired
           claim token exists (no secrets).
  revoke   revoke the active claim token.

flags:
  --data-dir DIR   state directory holding drevanta.db (default /var/lib/drevanta)
  --ttl DURATION   claim token time-to-live for init (default 30m)
  --port PORT      daemon loopback port shown in the tunnel hint (default 8080)
`)
	default:
		fmt.Fprint(os.Stderr, `usage: drevantad [serve|claim|version] [flags]

  serve           run the HTTP daemon (default)
  claim init      issue the first-owner claim token and show the setup link
  claim status    show onboarding state (no secrets)
  claim revoke    revoke the active claim token
  version         print the version
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

func cmdClaim(args []string) int {
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
	case "init":
		state, err := st.InstanceState(ctx)
		if err != nil {
			slogError("drevantad claim: state", err)
			return 1
		}
		if state.Status != "unclaimed" {
			fmt.Fprintln(os.Stderr, "instance is already claimed; no claim token is issued")
			return 1
		}
		token, hash, err := newToken()
		if err != nil {
			slogError("drevantad claim: generate", err)
			return 1
		}
		// Authoritative first-owner guard: refuses once claimed, even
		// if the state read above raced with a claim.
		ct, err := st.CreateClaimTokenIfUnclaimed(ctx, hash, *ttl)
		if err != nil {
			if errors.Is(err, store.ErrAlreadyClaimed) {
				fmt.Fprintln(os.Stderr, "instance is already claimed; no claim token is issued")
				return 1
			}
			slogError("drevantad claim: store", err)
			return 1
		}
		auditEvent(ctx, st, "drevantad", "claim_token_created", "claim",
			map[string]any{"ttl_minutes": ttl.Minutes(), "expires_at": ct.ExpiresAt.Format(time.RFC3339)})
		// The plaintext token is printed exactly once: line 1 of stdout
		// (machine-readable) plus the setup screen on stderr. It is never
		// stored or logged anywhere else.
		fmt.Println(token)
		printClaimSetupScreen(os.Stderr, token, *ttl, *port)
		return 0
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
		fmt.Printf("instance_status=%s mfa_mode=%q claim_token_active=%t\n",
			state.Status, state.MFAMode, tokenActive)
		return 0
	case "revoke":
		ct, err := st.ActiveClaimToken(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "no active claim token")
			return 1
		}
		if err := st.RevokeClaimToken(ctx, ct.ID); err != nil {
			slogError("drevantad claim: revoke", err)
			return 1
		}
		auditEvent(ctx, st, "drevantad", "claim_token_revoked", "claim", nil)
		fmt.Println("claim token revoked")
		return 0
	default:
		usage("claim")
		return 2
	}
}

// sshHint derives the SSH tunnel target from the current session when it
// is reliably known: SSH_CONNECTION carries the server address the
// operator is connected to, and the invoking identity tells the SSH
// username. When either is unknown, a clearly labelled placeholder is
// returned instead of a guess.
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
	if su := os.Getenv("SUDO_USER"); su != "" && su != "root" {
		user = su
	} else if os.Geteuid() == 0 && ip != "" {
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
// (tunnel command, one-time setup link, raw fallback token) to w. It is
// only ever printed to the operator's own terminal (stderr of the claim
// command), never logged or persisted.
func printClaimSetupScreen(w io.Writer, token string, ttl time.Duration, daemonPort string) {
	ip, user, known := sshHint()
	if ip == "" {
		ip = "<your-vps-ip>"
	}
	if user == "" {
		user = "<ssh-user>"
	}
	link := fmt.Sprintf("http://127.0.0.1:%s/onboarding#token=%s", setupLinkPort, token)
	fmt.Fprintf(w, `
✓ CLAIM TOKEN ISSUED

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  Drevanta first-time setup

  STEP 1 — On your computer, run:

    ssh -N -L %s:127.0.0.1:%s %s@%s

  STEP 2 — Open this link:

    %s

  Token expires in %s.
  This link can be used only once.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  Raw token (fallback): %s

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
`, setupLinkPort, daemonPort, user, ip, link, humanTTL(ttl), token)
	if !known {
		fmt.Fprintln(w, "  (replace the placeholder with this server's address and your SSH user)")
	}
}
