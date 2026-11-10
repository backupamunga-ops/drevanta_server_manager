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
	"flag"
	"fmt"
	"os"
	"time"
)

// version is stamped via -ldflags "-X main.version=..." in release builds.
var version = "0.2.2-sprint2"

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
           unconsumed token) and print it once. Only allowed while the
           instance is unclaimed.
  status   print the onboarding state and whether an active, unexpired
           claim token exists (no secrets).
  revoke   revoke the active claim token.

flags:
  --data-dir DIR   state directory holding drevanta.db (default /var/lib/drevanta)
  --ttl DURATION   claim token time-to-live for init (default 24h)
`)
	default:
		fmt.Fprint(os.Stderr, `usage: drevantad [serve|claim|version] [flags]

  serve           run the HTTP daemon (default)
  claim init      issue the first-owner claim token (printed once)
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
	ttl := fs.Duration("ttl", 24*time.Hour, "claim token TTL for init")
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
		ct, err := st.CreateClaimToken(ctx, hash, *ttl)
		if err != nil {
			slogError("drevantad claim: store", err)
			return 1
		}
		auditEvent(ctx, st, "drevantad", "claim_token_created", "claim",
			map[string]any{"ttl_hours": ttl.Hours(), "expires_at": ct.ExpiresAt.Format(time.RFC3339)})
		// The plaintext token is printed exactly once, to stdout, and is
		// never stored or logged anywhere.
		fmt.Println(token)
		fmt.Fprintf(os.Stderr, "claim token valid until %s (ttl %s)\n", ct.ExpiresAt.Format(time.RFC3339), *ttl)
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
