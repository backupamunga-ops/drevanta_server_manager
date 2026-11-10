package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInstanceStateDefaultsAndTransitions(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	st, err := s.InstanceState(ctx)
	if err != nil {
		t.Fatalf("InstanceState: %v", err)
	}
	if st.Status != StatusUnclaimed {
		t.Fatalf("initial status = %q, want unclaimed", st.Status)
	}
	// Second read must be stable (singleton row, no duplicates).
	st2, err := s.InstanceState(ctx)
	if err != nil || st2.Status != StatusUnclaimed {
		t.Fatalf("second read: %v %+v", err, st2)
	}

	// A real administrator account must exist before claiming (FK).
	admin, err := s.CreateUser(ctx, "admin", "$argon2id$hash")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Legal transition chain unclaimed -> mfa_pending -> active.
	if err := s.ClaimInstance(ctx, admin.ID); err != nil {
		t.Fatalf("unclaimed->mfa_pending: %v", err)
	}
	st, _ = s.InstanceState(ctx)
	if st.Status != StatusMFAPending || !st.AdminUserID.Valid || st.AdminUserID.Int64 != admin.ID {
		t.Fatalf("after claim: %+v", st)
	}
	if st.ClaimedAt.IsZero() {
		t.Fatal("claimed_at not stamped")
	}
	if err := s.FinishOnboarding(ctx, "totp"); err != nil {
		t.Fatalf("mfa_pending->active: %v", err)
	}
	st, _ = s.InstanceState(ctx)
	if st.Status != StatusActive || st.MFAMode != "totp" || st.MFADecidedAt.IsZero() {
		t.Fatalf("after mfa: %+v", st)
	}

	// Illegal transitions are rejected.
	if err := s.ClaimInstance(ctx, admin.ID); err == nil {
		t.Fatal("re-claim of an active instance was accepted")
	}
	if err := s.FinishOnboarding(ctx, "skipped"); err == nil {
		t.Fatal("re-finish of an active instance was accepted")
	}
}

func TestClaimTokenLifecycle(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	ctx := context.Background()
	_ = s.Migrate(ctx)

	// Single use: first consume wins, reuse is refused.
	hash1 := "hash-one"
	if _, err := s.CreateClaimToken(ctx, hash1, time.Hour); err != nil {
		t.Fatalf("create: %v", err)
	}
	act, err := s.ActiveClaimToken(ctx)
	if err != nil || act.TokenHash != hash1 {
		t.Fatalf("active: %v %+v", err, act)
	}
	ok, err := s.ConsumeClaimToken(ctx, hash1)
	if err != nil || !ok {
		t.Fatalf("first consume: ok=%v err=%v", ok, err)
	}
	ok, err = s.ConsumeClaimToken(ctx, hash1)
	if err != nil || ok {
		t.Fatalf("reuse consumed: ok=%v err=%v", ok, err)
	}
	if _, err := s.ActiveClaimToken(ctx); err != ErrNotFound {
		t.Fatalf("active token after consumption: %v", err)
	}

	// Rotation: only one unconsumed token may be active at a time; the
	// previous one is revoked and becomes unusable.
	hashA, hashB := "hash-a", "hash-b"
	if _, err := s.CreateClaimToken(ctx, hashA, time.Hour); err != nil {
		t.Fatalf("create A: %v", err)
	}
	if _, err := s.CreateClaimToken(ctx, hashB, time.Hour); err != nil {
		t.Fatalf("create B: %v", err)
	}
	act, err = s.ActiveClaimToken(ctx)
	if err != nil || act.TokenHash != hashB {
		t.Fatalf("after rotation: %v %+v", err, act)
	}
	if ok, _ := s.ConsumeClaimToken(ctx, hashA); ok {
		t.Fatal("rotated-out token was consumed")
	}

	// Expiry: a token whose TTL has passed is not consumable.
	hashE := "hash-expired"
	if _, err := s.CreateClaimToken(ctx, hashE, -time.Minute); err != nil {
		t.Fatalf("create expired: %v", err)
	}
	ok, err = s.ConsumeClaimToken(ctx, hashE)
	if err != nil || ok {
		t.Fatalf("expired token consumed: ok=%v err=%v", ok, err)
	}

	// Unknown hashes are refused.
	if ok, _ := s.ConsumeClaimToken(ctx, "unknown"); ok {
		t.Fatal("unknown hash consumed")
	}
}

func TestSessionsLifecycle(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	ctx := context.Background()
	_ = s.Migrate(ctx)

	admin, err := s.CreateUser(ctx, "admin", "$argon2id$hash")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	const (
		sliding  = 24 * time.Hour
		absolute = 7 * 24 * time.Hour
	)

	if _, err := s.CreateSession(ctx, "hash-a", admin.ID, ScopeFull, sliding, absolute); err != nil {
		t.Fatalf("create session: %v", err)
	}
	sess, err := s.SessionByHash(ctx, "hash-a")
	if err != nil || sess.Scope != ScopeFull || sess.UserID != admin.ID {
		t.Fatalf("lookup: %v %+v", err, sess)
	}
	// Unknown hashes are not found.
	if _, err := s.SessionByHash(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown hash: %v", err)
	}
	// Touch slides expiry forward.
	before := sess.ExpiresAt
	time.Sleep(5 * time.Millisecond)
	if err := s.TouchSession(ctx, "hash-a", sliding); err != nil {
		t.Fatalf("touch: %v", err)
	}
	after, _ := s.SessionByHash(ctx, "hash-a")
	if !after.ExpiresAt.After(before) {
		t.Fatalf("expiry did not slide: %v -> %v", before, after.ExpiresAt)
	}
	// Delete (logout).
	if err := s.DeleteSession(ctx, "hash-a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.SessionByHash(ctx, "hash-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	// Expired sessions are refused and pruned (insert an expired row
	// directly to exercise the lookup-time and prune paths).
	past := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	old := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339Nano)
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO sessions (token_hash, user_id, scope, created_at, absolute_at, expires_at, last_seen_at)
VALUES ('hash-b', ?, 'full', ?, ?, ?, ?)`, admin.ID, old, old, past, old); err != nil {
		t.Fatalf("insert expired session: %v", err)
	}
	if _, err := s.SessionByHash(ctx, "hash-b"); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired session still valid")
	}
	if err := s.PruneSessions(ctx); err != nil {
		t.Fatalf("prune: %v", err)
	}
	// Sessions past the absolute cap are refused too.
	absPast := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO sessions (token_hash, user_id, scope, created_at, absolute_at, expires_at, last_seen_at)
VALUES ('hash-c', ?, 'full', ?, ?, ?, ?)`, admin.ID, old, absPast, future, old); err != nil {
		t.Fatalf("insert absolute-expired session: %v", err)
	}
	if _, err := s.SessionByHash(ctx, "hash-c"); !errors.Is(err, ErrNotFound) {
		t.Fatal("session past absolute cap still valid")
	}
}

func TestRecoveryCodes(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	ctx := context.Background()
	_ = s.Migrate(ctx)

	u1, err := s.CreateUser(ctx, "admin", "$argon2id$hash")
	if err != nil {
		t.Fatalf("create u1: %v", err)
	}
	u2, err := s.CreateUser(ctx, "second", "$argon2id$hash")
	if err != nil {
		t.Fatalf("create u2: %v", err)
	}

	hashes := []string{"h1", "h2", "h3"}
	if err := s.ReplaceRecoveryCodes(ctx, u1.ID, hashes); err != nil {
		t.Fatalf("replace: %v", err)
	}
	n, _ := s.UnusedRecoveryCodeCount(ctx, u1.ID)
	if n != 3 {
		t.Fatalf("unused = %d, want 3", n)
	}
	// Replacing swaps the whole set.
	if err := s.ReplaceRecoveryCodes(ctx, u1.ID, []string{"h4"}); err != nil {
		t.Fatalf("replace 2: %v", err)
	}
	n, _ = s.UnusedRecoveryCodeCount(ctx, u1.ID)
	if n != 1 {
		t.Fatalf("after replace unused = %d, want 1", n)
	}
	// Consumption is single-use per code.
	ok, err := s.ConsumeRecoveryCode(ctx, u1.ID, "h4")
	if err != nil || !ok {
		t.Fatalf("consume: ok=%v err=%v", ok, err)
	}
	ok, _ = s.ConsumeRecoveryCode(ctx, u1.ID, "h4")
	if ok {
		t.Fatal("code reused")
	}
	// Codes are per-user.
	if err := s.ReplaceRecoveryCodes(ctx, u2.ID, []string{"u2c"}); err != nil {
		t.Fatalf("replace u2: %v", err)
	}
	ok, _ = s.ConsumeRecoveryCode(ctx, u1.ID, "u2c")
	if ok {
		t.Fatal("another user's code accepted")
	}
}

func TestUserCRUDAndMFAFields(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	ctx := context.Background()
	_ = s.Migrate(ctx)

	u, err := s.CreateUser(ctx, "admin", "$argon2id$hash")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if u.ID == 0 || u.Username != "admin" || u.MFAEnabled {
		t.Fatalf("created: %+v", u)
	}
	if _, err := s.CreateUser(ctx, "admin", "x"); err == nil {
		t.Fatal("duplicate username accepted")
	}
	byName, err := s.UserByUsername(ctx, "admin")
	if err != nil || byName.ID != u.ID {
		t.Fatalf("by username: %v %+v", err, byName)
	}
	if _, err := s.UserByUsername(ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user: %v", err)
	}

	if err := s.SetMFASecret(ctx, u.ID, "SECRET32"); err != nil {
		t.Fatalf("set secret: %v", err)
	}
	u, _ = s.UserByID(ctx, u.ID)
	if u.MFASecret != "SECRET32" || u.MFAEnabled {
		t.Fatalf("after setup: %+v", u)
	}
	if err := s.EnableMFA(ctx, u.ID, 12345); err != nil {
		t.Fatalf("enable: %v", err)
	}
	u, _ = s.UserByID(ctx, u.ID)
	if !u.MFAEnabled || u.MFALastStep != 12345 {
		t.Fatalf("after enable: %+v", u)
	}
	if err := s.AcceptMFAStep(ctx, u.ID, 12346); err != nil {
		t.Fatalf("accept step: %v", err)
	}
	u, _ = s.UserByID(ctx, u.ID)
	if u.MFALastStep != 12346 {
		t.Fatalf("replay guard step: %d", u.MFALastStep)
	}
}
