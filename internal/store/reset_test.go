package store

// v0.4.17 store mechanics: SMTP config singleton, reset-token lifecycle,
// delete-all-sessions, and VACUUM INTO snapshots.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/auth"
)

func TestSMTPConfigSingleton(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	cfg, err := st.GetSMTPConfig(ctx)
	if err != nil || cfg.Enabled {
		t.Fatalf("default config: %+v %v", cfg, err)
	}

	err = st.SetSMTPConfig(ctx, SMTPConfig{
		Enabled: true, Host: "smtp.example.com", Port: 587,
		Username: "user", Password: "secret", FromAddr: "from@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err = st.GetSMTPConfig(ctx)
	if err != nil || !cfg.Enabled || cfg.Host != "smtp.example.com" || cfg.Password != "secret" || !cfg.PasswordSet {
		t.Fatalf("saved config: %+v %v", cfg, err)
	}

	// An update with an empty password keeps the stored one.
	err = st.SetSMTPConfig(ctx, SMTPConfig{
		Enabled: true, Host: "smtp2.example.com", Port: 465,
		Username: "user", Password: "", FromAddr: "from@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ = st.GetSMTPConfig(ctx)
	if cfg.Host != "smtp2.example.com" || cfg.Password != "secret" {
		t.Fatalf("password keep semantics broken: %+v", cfg)
	}

	// An explicit new password replaces it.
	err = st.SetSMTPConfig(ctx, SMTPConfig{
		Enabled: true, Host: "smtp2.example.com", Port: 465,
		Username: "user", Password: "secret2", FromAddr: "from@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ = st.GetSMTPConfig(ctx)
	if cfg.Password != "secret2" {
		t.Fatalf("password replace broken: %+v", cfg)
	}
}

func TestPasswordResetLifecycle(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := st.CreateUser(ctx, "admin", "hash", "op@example.com"); err != nil {
		t.Fatal(err)
	}

	newToken := func() string {
		tok, _, terr := auth.NewToken()
		if terr != nil {
			t.Fatal(terr)
		}
		return tok
	}

	// Normal issue + consume.
	tok := newToken()
	if err := st.CreatePasswordReset(ctx, auth.TokenHash(tok), 1, 30*time.Minute); err != nil {
		t.Fatal(err)
	}
	uid, err := st.ConsumePasswordReset(ctx, auth.TokenHash(tok))
	if err != nil || uid != 1 {
		t.Fatalf("consume: %d %v", uid, err)
	}
	// Single use.
	if _, err := st.ConsumePasswordReset(ctx, auth.TokenHash(tok)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replay err = %v, want ErrNotFound", err)
	}

	// A new issue invalidates the previous live token.
	t1, t2 := newToken(), newToken()
	if err := st.CreatePasswordReset(ctx, auth.TokenHash(t1), 1, 30*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := st.CreatePasswordReset(ctx, auth.TokenHash(t2), 1, 30*time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ConsumePasswordReset(ctx, auth.TokenHash(t1)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old token should be invalidated, err = %v", err)
	}
	if _, err := st.ConsumePasswordReset(ctx, auth.TokenHash(t2)); err != nil {
		t.Fatalf("new token should work: %v", err)
	}

	// Hourly cap: the three requests above already fill user 1's window;
	// a fourth is refused even though all previous tokens are consumed.
	err = st.CreatePasswordReset(ctx, auth.TokenHash(newToken()), 1, 30*time.Minute)
	if !errors.Is(err, ErrResetRateLimited) {
		t.Fatalf("rate limit err = %v, want ErrResetRateLimited", err)
	}

	// Expiry (a different account, so the cap above does not interfere).
	if _, err := st.CreateUser(ctx, "second", "hash", ""); err != nil {
		t.Fatal(err)
	}
	tok = newToken()
	if err := st.CreatePasswordReset(ctx, auth.TokenHash(tok), 2, -time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ConsumePasswordReset(ctx, auth.TokenHash(tok)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired token err = %v, want ErrNotFound", err)
	}
}

func TestDeleteAllSessions(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	admin, err := st.CreateUser(ctx, "admin", "hash", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := st.CreateSession(ctx, "hash-"+string(rune('a'+i)), admin.ID, ScopeFull, time.Hour, 24*time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	n, err := st.DeleteAllSessions(ctx, admin.ID)
	if err != nil || n != 3 {
		t.Fatalf("deleted = %d %v, want 3", n, err)
	}
	if _, err := st.SessionByHash(ctx, "hash-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session survived: %v", err)
	}
}

func TestBackupToProducesSQLiteFile(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := st.CreateUser(ctx, "admin", "hash", "op@example.com"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "snapshot.db")
	if err := st.BackupTo(ctx, path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 100 || !strings.HasPrefix(string(raw[:16]), "SQLite format 3") {
		t.Fatalf("snapshot is not a SQLite database (%d bytes)", len(raw))
	}
	// The copy contains the data: open it in its own directory.
	dst := t.TempDir()
	if err := os.WriteFile(filepath.Join(dst, "drevanta.db"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	st2, err := Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	u, err := st2.UserByIdentifier(ctx, "op@example.com")
	if err != nil || u.Username != "admin" {
		t.Fatalf("snapshot content: %v %v", u, err)
	}
}
