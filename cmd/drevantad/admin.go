// admin subcommand: break-glass recovery for the operator (root on the
// box). Part of the three-path account recovery design in v0.4.17:
//
//	Path A — email reset link (self-service, needs SMTP)
//	Path B — recovery-code sign-in (self-service, needs a saved code)
//	Path C — drevantad admin reset-password (this command, needs SSH)
//
// The reset ends every session and is audited.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/auth"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/store"
	"golang.org/x/term"
)

func cmdAdmin(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "reset-password" {
		fmt.Fprintln(stderr, `usage: drevantad admin reset-password [flags]

flags:
  --data-dir DIR     state directory holding drevanta.db (default /var/lib/drevanta)
  --username USER    administrator username (optional when only one account exists)
  --password PASS    new password; when omitted it is read twice from the
                     terminal without echo (12-128 characters)`)
		return 2
	}
	fs := flag.NewFlagSet("admin reset-password", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dataDir := fs.String("data-dir", "/var/lib/drevanta", "state directory holding drevanta.db")
	username := fs.String("username", "", "administrator username (optional when only one account exists)")
	password := fs.String("password", "", "new password (else prompted twice)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	st, err := openStore(*dataDir)
	if err != nil {
		slogError("drevantad admin: open store", err)
		return 1
	}
	defer st.Close()
	ctx := context.Background()

	// Resolve the target account: by username, or the only account.
	var user store.User
	if strings.TrimSpace(*username) != "" {
		user, err = st.UserByIdentifier(ctx, strings.TrimSpace(*username))
		if err != nil {
			fmt.Fprintln(stderr, "drevantad admin: no such account")
			return 1
		}
	} else {
		user, err = st.SoleUser(ctx)
		if err != nil {
			fmt.Fprintln(stderr, "drevantad admin: several accounts exist — pass --username")
			return 1
		}
	}

	// Collect the new password.
	pass := strings.TrimSpace(*password)
	if pass == "" {
		fmt.Fprintln(stdout, "New password (input hidden):")
		first, rerr := readSecret()
		if rerr != nil {
			fmt.Fprintln(stderr, "drevantad admin: could not read the password")
			return 1
		}
		fmt.Fprintln(stdout, "Repeat it:")
		second, rerr2 := readSecret()
		if rerr2 != nil || first != second {
			fmt.Fprintln(stderr, "drevantad admin: the two entries did not match")
			return 1
		}
		pass = first
	}
	const minPass, maxPass = 12, 128 // mirrors httpapi.validPassword
	if len(pass) < minPass || len(pass) > maxPass {
		fmt.Fprintf(stderr, "drevantad admin: the password must be %d-%d characters\n", minPass, maxPass)
		return 1
	}

	hash, err := auth.HashPassword(pass)
	if err != nil {
		slogError("drevantad admin: hash", err)
		return 1
	}
	if err := st.UpdatePasswordHash(ctx, user.ID, hash); err != nil {
		slogError("drevantad admin: update", err)
		return 1
	}
	ended, _ := st.DeleteAllSessions(ctx, user.ID)
	auditEvent(ctx, st, user.Username, "password_reset", "auth", map[string]any{"via": "cli"})
	fmt.Fprintf(stdout, "Password reset for %s. %d session(s) ended. The account can sign in again now.\n", user.Username, ended)
	return 0
}

// readSecret reads a line from the terminal without echo, falling back
// to stdin when there is no TTY (piped input).
func readSecret() (string, error) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	line, _, err := bufio.NewReader(os.Stdin).ReadLine()
	if err != nil {
		return "", err
	}
	return string(line), nil
}
