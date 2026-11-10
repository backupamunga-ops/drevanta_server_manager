package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/auth"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/store"
)

// tokenRe matches the drv1_ claim token format (prefix + 43 base64url chars).
var tokenRe = regexp.MustCompile(`^drv1_[A-Za-z0-9_-]{43}$`)

// clearSessionEnv isolates sshHint from the ambient environment: tests must
// see the labelled-placeholder path unless they explicitly set overrides.
func clearSessionEnv(t *testing.T) {
	t.Helper()
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("DREVANTA_SSH_HOST", "")
	t.Setenv("DREVANTA_SSH_USER", "")
	t.Setenv("SUDO_USER", "")
}

// freshStore opens a throwaway state dir (migrations applied).
func freshStore(t *testing.T) (string, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return dir, st
}

// TestClaimRegenerateHappyPath verifies the locked fallback UX: raw token on
// stdout line 1, full setup screen (tunnel, prefilled link, TTL, single-use
// note, rotation note) on stderr — nothing printed anywhere else.
func TestClaimRegenerateHappyPath(t *testing.T) {
	clearSessionEnv(t)
	dir, st := freshStore(t)
	st.Close() // cmdClaim opens its own store handle

	var stdout, stderr bytes.Buffer
	code := cmdClaim([]string{"regenerate", "--data-dir", dir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("regenerate exit=%d stderr=%s", code, stderr.String())
	}

	token := strings.TrimSpace(stdout.String())
	if !tokenRe.MatchString(token) {
		t.Fatalf("stdout line 1 is not a drv1_ token: %q", token)
	}
	if strings.Count(stdout.String(), "\n") != 1 {
		t.Fatalf("stdout must carry ONLY the token line, got %q", stdout.String())
	}
	errText := stderr.String()
	for _, want := range []string{
		"NEW CLAIM TOKEN ISSUED",
		"Drevanta first-time setup",
		"ssh -N -L 18080:127.0.0.1:8080 <ssh-user>@<your-vps-ip>",
		"http://127.0.0.1:18080/onboarding#token=" + token,
		"Token expires in 30 minutes.",
		"This link can be used only once.",
		"Raw token (fallback): " + token,
		"The previous claim token (if any) is now invalid.",
		"(replace the placeholder with this server's address and your SSH user)",
	} {
		if !strings.Contains(errText, want) {
			t.Errorf("setup screen missing %q\ngot:\n%s", want, errText)
		}
	}

	// The displayed token must be the live stored one (hash-only at rest).
	st2 := openStoreForTest(t, dir)
	ct, err := st2.ActiveClaimToken(context.Background())
	if err != nil {
		t.Fatalf("no active token after regenerate: %v", err)
	}
	if ct.TokenHash != auth.TokenHash(token) {
		t.Error("printed token is not the stored active token")
	}
}

// openStoreForTest reopens a store at dir (cmdClaim owns its own handle).
func openStoreForTest(t *testing.T, dir string) *store.Store {
	t.Helper()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// TestClaimRegenerateRotatesPreviousToken: the fallback must invalidate the
// link it replaces — after regenerate, the previous token is revoked in the
// database and the new token is the only active one.
func TestClaimRegenerateRotatesPreviousToken(t *testing.T) {
	clearSessionEnv(t)
	dir, _ := freshStore(t)

	var out1, err1 bytes.Buffer
	if code := cmdClaim([]string{"init", "--data-dir", dir}, &out1, &err1); code != 0 {
		t.Fatalf("init exit=%d", code)
	}
	first := strings.TrimSpace(out1.String())

	var out2, err2 bytes.Buffer
	if code := cmdClaim([]string{"regenerate", "--data-dir", dir}, &out2, &err2); code != 0 {
		t.Fatalf("regenerate exit=%d stderr=%s", code, err2.String())
	}
	second := strings.TrimSpace(out2.String())

	if first == second {
		t.Fatal("regenerate re-printed the same token; rotation did not happen")
	}
	if !tokenRe.MatchString(second) {
		t.Fatalf("regenerated token malformed: %q", second)
	}

	st := openStoreForTest(t, dir)
	ctx := context.Background()

	revoked, err := st.ClaimTokenByHash(ctx, auth.TokenHash(first))
	if err != nil {
		t.Fatalf("previous token not found by hash: %v", err)
	}
	if revoked.RevokedAt.IsZero() {
		t.Error("previous token was not revoked by regenerate")
	}

	active, err := st.ActiveClaimToken(ctx)
	if err != nil {
		t.Fatalf("no active token after regenerate: %v", err)
	}
	if active.TokenHash != auth.TokenHash(second) {
		t.Error("active token is not the regenerated one")
	}
	if active.ExpiresAt.Sub(time.Now()) <= 0 || active.ExpiresAt.Sub(time.Now()) > 31*time.Minute {
		t.Errorf("regenerated token TTL not ~30m: %v", active.ExpiresAt.Sub(time.Now()))
	}
}

// TestClaimRegenerateRejectedAfterClaiming: once the first owner exists the
// fallback is permanently refused — no token printed, non-zero exit, and the
// stored token set is untouched.
func TestClaimRegenerateRejectedAfterClaiming(t *testing.T) {
	clearSessionEnv(t)
	dir, st := freshStore(t)
	ctx := context.Background()

	var out1, err1 bytes.Buffer
	if code := cmdClaim([]string{"regenerate", "--data-dir", dir}, &out1, &err1); code != 0 {
		t.Fatalf("regenerate exit=%d", code)
	}
	token := strings.TrimSpace(out1.String())

	// Simulate the first owner having been created (claim succeeded).
	u, err := st.CreateUser(ctx, "owner", "argon2id$test")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.ClaimInstance(ctx, u.ID); err != nil {
		t.Fatalf("claim instance: %v", err)
	}

	var out2, err2 bytes.Buffer
	if code := cmdClaim([]string{"regenerate", "--data-dir", dir}, &out2, &err2); code != 1 {
		t.Fatalf("regenerate after claim: exit=%d want=1", code)
	}
	if strings.TrimSpace(out2.String()) != "" {
		t.Errorf("token printed after claim: %q", out2.String())
	}
	if !strings.Contains(err2.String(), "already claimed") {
		t.Errorf("rejection not explained, stderr=%q", err2.String())
	}

	// The pre-claim token is still the stored one; nothing was rotated.
	active, err := st.ActiveClaimToken(ctx)
	if err != nil {
		t.Fatalf("active token lookup: %v", err)
	}
	if active.TokenHash != auth.TokenHash(token) {
		t.Error("stored token changed by a refused regeneration")
	}

	// Same refusal for `init` — no path back to onboarding.
	var out3, err3 bytes.Buffer
	if code := cmdClaim([]string{"init", "--data-dir", dir}, &out3, &err3); code != 1 {
		t.Fatalf("init after claim: exit=%d want=1", code)
	}
	if strings.TrimSpace(out3.String()) != "" {
		t.Errorf("init printed a token after claim: %q", out3.String())
	}
}

// TestClaimInitAndRegenerateOutputConsistency: both verbs share one delivery
// contract — token on stdout line 1, the same setup screen on stderr with
// only the headline differing. Docs and the installer depend on this.
func TestClaimInitAndRegenerateOutputConsistency(t *testing.T) {
	clearSessionEnv(t)
	dir, _ := freshStore(t)

	run := func(verb string) (string, string) {
		var o, e bytes.Buffer
		if code := cmdClaim([]string{verb, "--data-dir", dir}, &o, &e); code != 0 {
			t.Fatalf("%s exit=%d stderr=%s", verb, code, e.String())
		}
		return strings.TrimSpace(o.String()), e.String()
	}
	initTok, initErr := run("init")
	regTok, regErr := run("regenerate")

	if !tokenRe.MatchString(initTok) || !tokenRe.MatchString(regTok) {
		t.Fatalf("token format regressed: init=%q regen=%q", initTok, regTok)
	}
	for _, frag := range []string{
		"ssh -N -L 18080:127.0.0.1:8080",
		"/onboarding#token=",
		"Token expires in 30 minutes.",
		"This link can be used only once.",
		"Raw token (fallback): ",
	} {
		if !strings.Contains(initErr, frag) || !strings.Contains(regErr, frag) {
			t.Errorf("screen fragment %q missing in one of the verbs", frag)
		}
	}
	if !strings.Contains(initErr, "CLAIM TOKEN ISSUED") {
		t.Errorf("init headline missing: %q", initErr)
	}
	if !strings.Contains(regErr, "NEW CLAIM TOKEN ISSUED") {
		t.Errorf("regenerate headline missing: %q", regErr)
	}
	// The link must embed the exact token printed on stdout, in both verbs.
	if !strings.Contains(initErr, "/onboarding#token="+initTok) {
		t.Error("init link does not match stdout token")
	}
	if !strings.Contains(regErr, "/onboarding#token="+regTok) {
		t.Error("regenerate link does not match stdout token")
	}
}

// TestClaimRegenerateRealSessionInfo: when the environment carries the
// operator's real SSH session (as the drevanta wrapper provides via utmp),
// the tunnel line uses it instead of placeholders.
func TestClaimRegenerateRealSessionInfo(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("DREVANTA_SSH_HOST", "203.0.113.7")
	t.Setenv("DREVANTA_SSH_USER", "amunga")
	t.Setenv("SUDO_USER", "root")

	dir, _ := freshStore(t)
	var o, e bytes.Buffer
	if code := cmdClaim([]string{"regenerate", "--data-dir", dir}, &o, &e); code != 0 {
		t.Fatalf("regenerate exit=%d", code)
	}
	if !strings.Contains(e.String(), "ssh -N -L 18080:127.0.0.1:8080 amunga@203.0.113.7") {
		t.Errorf("real session info not used:\n%s", e.String())
	}
	if strings.Contains(e.String(), "<your-vps-ip>") || strings.Contains(e.String(), "<ssh-user>") {
		t.Error("placeholders printed despite known session")
	}
}

// TestUsageListsRegenerate: help output must advertise the documented
// commands so `drevanta --help` and the installation guide agree.
func TestUsageListsRegenerate(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	usage("claim")
	usage("")
	w.Close()
	os.Stderr = orig
	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	help := string(buf[:n])

	for _, want := range []string{"regenerate", "claim init", "claim status", "claim revoke"} {
		if !strings.Contains(help, want) {
			t.Errorf("usage output missing %q", want)
		}
	}
}

// TestHumanTTL: the locked copy says "30 minutes" for the default TTL.
func TestHumanTTL(t *testing.T) {
	if got := humanTTL(30 * time.Minute); got != "30 minutes" {
		t.Errorf("humanTTL(30m)=%q", got)
	}
	if got := humanTTL(45 * time.Minute); got != "45m0s" {
		t.Errorf("humanTTL(45m)=%q", got)
	}
}

// TestSSHHintPrecedence: explicit wrapper-provided values win over the
// ambient session; unknown values stay empty so callers show placeholders.
func TestSSHHintPrecedence(t *testing.T) {
	t.Run("wrapper overrides win", func(t *testing.T) {
		t.Setenv("SSH_CONNECTION", "198.51.100.9 51000 198.51.100.1 22")
		t.Setenv("DREVANTA_SSH_HOST", "203.0.113.7")
		t.Setenv("DREVANTA_SSH_USER", "amunga")
		t.Setenv("SUDO_USER", "root")
		ip, user, ok := sshHint()
		if !ok || ip != "203.0.113.7" || user != "amunga" {
			t.Errorf("got %q %q ok=%v", ip, user, ok)
		}
	})
	t.Run("unknown stays empty", func(t *testing.T) {
		clearSessionEnv(t)
		ip, user, ok := sshHint()
		if ok || ip != "" || user != "" {
			t.Errorf("expected empty hint, got %q %q ok=%v", ip, user, ok)
		}
	})
	t.Run("non-root sudoer becomes the ssh user", func(t *testing.T) {
		t.Setenv("SSH_CONNECTION", "198.51.100.9 51000 203.0.113.7 22")
		t.Setenv("DREVANTA_SSH_HOST", "")
		t.Setenv("DREVANTA_SSH_USER", "")
		t.Setenv("SUDO_USER", "ubuntu")
		ip, user, ok := sshHint()
		if !ok || ip != "203.0.113.7" || user != "ubuntu" {
			t.Errorf("got %q %q ok=%v", ip, user, ok)
		}
	})
}

// TestRegenerateTTLFlagRespected: --ttl flows through to the screen copy and
// the stored expiry (operators may need a longer window; 30m stays default).
func TestRegenerateTTLFlagRespected(t *testing.T) {
	clearSessionEnv(t)
	dir, _ := freshStore(t)
	var o, e bytes.Buffer
	if code := cmdClaim([]string{"regenerate", "--data-dir", dir, "--ttl", "45m"}, &o, &e); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if !strings.Contains(e.String(), "Token expires in 45m0s.") {
		t.Errorf("custom TTL not shown: %q", e.String())
	}
	st := openStoreForTest(t, dir)
	ct, err := st.ActiveClaimToken(context.Background())
	if err != nil {
		t.Fatalf("active token: %v", err)
	}
	want := time.Now().Add(45 * time.Minute)
	if diff := ct.ExpiresAt.Sub(want); diff > 2*time.Minute || diff < -2*time.Minute {
		t.Errorf("stored expiry not ~45m from now: %v", ct.ExpiresAt)
	}
}

// TestStateDirCreated: claim commands tolerate a missing data dir (first run
// from the wrapper before any daemon start).
func TestStateDirCreated(t *testing.T) {
	clearSessionEnv(t)
	dir := filepath.Join(t.TempDir(), "nested", "state")
	var o, e bytes.Buffer
	if code := cmdClaim([]string{"regenerate", "--data-dir", dir}, &o, &e); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, e.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "drevanta.db")); err != nil {
		t.Errorf("database not created in fresh dir: %v", err)
	}
}
