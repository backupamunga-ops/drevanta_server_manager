// Identity and onboarding persistence: instance state machine, the
// administrator account, claim tokens, sessions, and recovery codes.
//
// Security notes:
//   - Claim tokens, session cookies, and recovery codes are stored ONLY
//     as SHA-256 hashes; the plaintext values never touch the database.
//   - Passwords are stored as argon2id PHC strings (see internal/auth).
//   - All timestamps are RFC3339Nano UTC strings and are parsed before
//     comparison (RFC3339Nano string ordering is not reliable because
//     trailing zeros are trimmed).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Onboarding statuses of the instance (instance_state.status).
const (
	StatusUnclaimed  = "unclaimed"
	StatusMFAPending = "mfa_pending"
	StatusActive     = "active"
)

// Session scopes.
const (
	ScopeOnboarding = "onboarding"
	ScopeFull       = "full"
	// ScopeRecovery is the limited session issued by recovery-code
	// sign-in (username + single-use recovery code, no password). It
	// permits exactly one action: setting a new password.
	ScopeRecovery = "recovery"
)

// ErrNotFound is returned when a queried row does not exist.
var ErrNotFound = errors.New("not found")

// ErrAlreadyClaimed is returned by CreateClaimTokenIfUnclaimed when token
// issuance is attempted after the first owner exists.
var ErrAlreadyClaimed = errors.New("instance already claimed")

// ErrEmailTaken is returned when an email is already bound to another
// account (case-insensitive).
var ErrEmailTaken = errors.New("email already in use")

// User is one operator account.
type User struct {
	ID           int64
	Username     string
	Email        string
	PasswordHash string
	MFASecret    string
	MFAEnabled   bool
	MFALastStep  int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// InstanceState is the singleton onboarding state row.
type InstanceState struct {
	Status       string
	AdminUserID  sql.NullInt64
	MFAMode      string // "totp" | "skipped" | ""
	ClaimedAt    time.Time
	MFADecidedAt time.Time
}

// ClaimToken is a stored (hashed) first-owner claim token.
type ClaimToken struct {
	ID         int64
	TokenHash  string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	ConsumedAt time.Time
	RevokedAt  time.Time
}

// Session is a stored (hashed) browser session.
type Session struct {
	ID         int64
	TokenHash  string
	UserID     int64
	Scope      string
	CreatedAt  time.Time
	AbsoluteAt time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func nowStr() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// ---------------------------------------------------------------------------
// Instance state
// ---------------------------------------------------------------------------

// InstanceState returns the singleton state row, creating the default
// unclaimed row on first access. The read-modify-write is guarded by the
// PRIMARY KEY constraint, so concurrent callers still end up with one row.
func (s *Store) InstanceState(ctx context.Context) (InstanceState, error) {
	var (
		st                      InstanceState
		claimedAt, mfaDecidedAt string
		adminID                 sql.NullInt64
	)
	err := s.db.QueryRowContext(ctx, `
SELECT status, admin_user_id, mfa_mode, claimed_at, mfa_decided_at
FROM instance_state WHERE id = 1`).Scan(&st.Status, &adminID, &st.MFAMode, &claimedAt, &mfaDecidedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, ierr := s.db.ExecContext(ctx, `
INSERT INTO instance_state (id, status, updated_at) VALUES (1, ?, ?)`,
			StatusUnclaimed, nowStr())
		if ierr != nil {
			return st, fmt.Errorf("init instance_state: %w", ierr)
		}
		return InstanceState{Status: StatusUnclaimed}, nil
	case err != nil:
		return st, fmt.Errorf("load instance_state: %w", err)
	}
	st.AdminUserID = adminID
	st.ClaimedAt = parseTime(claimedAt)
	st.MFADecidedAt = parseTime(mfaDecidedAt)
	return st, nil
}

// setStateStatus moves the state machine forward and returns an error if
// the current status does not match want (optimistic transition guard).
func (s *Store) setStateStatus(ctx context.Context, want, to, mfaMode string, adminID *int64) error {
	now := nowStr()
	var adminArg any
	if adminID != nil {
		adminArg = *adminID
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE instance_state
SET status = ?, mfa_mode = ?, admin_user_id = COALESCE(?, admin_user_id),
    claimed_at = CASE WHEN ? = 'mfa_pending' THEN ? ELSE claimed_at END,
    mfa_decided_at = CASE WHEN ? = 'active' THEN ? ELSE mfa_decided_at END,
    updated_at = ?
WHERE id = 1 AND status = ?`,
		to, mfaMode, adminArg, to, now, to, now, now, want)
	if err != nil {
		return fmt.Errorf("update instance_state: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("instance_state rows affected: %w", err)
	}
	if n == 0 {
		cur, gerr := s.InstanceState(ctx)
		if gerr != nil {
			return fmt.Errorf("transition %s -> %s: state unreadable: %w", want, to, gerr)
		}
		return fmt.Errorf("transition %s -> %s rejected: instance is %s", want, to, cur.Status)
	}
	return nil
}

// ClaimInstance transitions unclaimed -> mfa_pending, binding the freshly
// created administrator account. It fails if the instance was already
// claimed (first-owner semantics).
func (s *Store) ClaimInstance(ctx context.Context, adminUserID int64) error {
	return s.setStateStatus(ctx, StatusUnclaimed, StatusMFAPending, "", &adminUserID)
}

// FinishOnboarding transitions mfa_pending -> active, recording whether
// MFA was enabled ("totp") or explicitly skipped ("skipped").
func (s *Store) FinishOnboarding(ctx context.Context, mfaMode string) error {
	return s.setStateStatus(ctx, StatusMFAPending, StatusActive, mfaMode, nil)
}

// SetMFAMode records the MFA decision on an already-active instance — the
// post-completion re-enrolment path (operator enables MFA from the
// completion checklist after having skipped it). It refuses on anything
// but the active singleton: first-run completion must go through
// FinishOnboarding's guarded mfa_pending -> active transition instead,
// and there is no legitimate way to touch an unclaimed instance here.
func (s *Store) SetMFAMode(ctx context.Context, mfaMode string) error {
	now := nowStr()
	res, err := s.db.ExecContext(ctx, `
UPDATE instance_state
SET mfa_mode = ?, mfa_decided_at = ?, updated_at = ?
WHERE id = 1 AND status = ?`, mfaMode, now, now, StatusActive)
	if err != nil {
		return fmt.Errorf("set mfa mode: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set mfa mode rows: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("set mfa mode rejected: instance is not active")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

// CreateUser inserts an administrator account. email may be empty (the
// operator can set it later from Account settings).
func (s *Store) CreateUser(ctx context.Context, username, passwordHash, email string) (User, error) {
	var u User
	now := nowStr()
	res, err := s.db.ExecContext(ctx, `
INSERT INTO users (username, password_hash, email, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)`, username, passwordHash, email, now, now)
	if err != nil {
		return u, fmt.Errorf("create user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return u, fmt.Errorf("create user id: %w", err)
	}
	return s.UserByID(ctx, id)
}

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var (
		u            User
		mfaEnabled   int
		mfaLastStep  sql.NullInt64
		created, upd string
	)
	err := row.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.MFASecret, &mfaEnabled, &mfaLastStep, &created, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	if err != nil {
		return u, err
	}
	u.MFAEnabled = mfaEnabled != 0
	u.MFALastStep = mfaLastStep.Int64
	u.CreatedAt = parseTime(created)
	u.UpdatedAt = parseTime(upd)
	return u, nil
}

const userCols = `id, username, email, password_hash, mfa_secret, mfa_enabled, mfa_last_step, created_at, updated_at`

// UserByID fetches one account.
func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx,
		`SELECT `+userCols+` FROM users WHERE id = ?`, id))
	if err != nil {
		return u, fmt.Errorf("user %d: %w", id, err)
	}
	return u, nil
}

// UserByUsername fetches one account by login name.
func (s *Store) UserByUsername(ctx context.Context, username string) (User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx,
		`SELECT `+userCols+` FROM users WHERE username = ?`, username))
	if err != nil {
		return u, fmt.Errorf("user %q: %w", username, err)
	}
	return u, nil
}

// SoleUser returns the only account, or an error when there are none
// or several (used by the break-glass CLI where the operator has not
// specified a username).
func (s *Store) SoleUser(ctx context.Context) (User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY id`)
	if err != nil {
		return User{}, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		u, serr := scanUser(rows)
		if serr != nil {
			return User{}, serr
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return User{}, err
	}
	switch len(users) {
	case 1:
		return users[0], nil
	case 0:
		return User{}, ErrNotFound
	default:
		return User{}, fmt.Errorf("%d accounts exist", len(users))
	}
}

// UserByIdentifier fetches one account by username OR by email
// (case-insensitive). The email match only considers non-empty emails,
// so an empty identifier column can never resolve.
func (s *Store) UserByIdentifier(ctx context.Context, ident string) (User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx,
		`SELECT `+userCols+` FROM users
		 WHERE username = ? OR (email != '' AND lower(email) = lower(?))`, ident, ident))
	if err != nil {
		return u, fmt.Errorf("user %q: %w", ident, err)
	}
	return u, nil
}

// SetUserEmail binds (or clears, with "") the account email. It fails
// with ErrEmailTaken when another account already holds the address.
func (s *Store) SetUserEmail(ctx context.Context, userID int64, email string) error {
	if email != "" {
		var other int64
		err := s.db.QueryRowContext(ctx,
			`SELECT id FROM users WHERE lower(email) = lower(?) AND id != ?`, email, userID).Scan(&other)
		if errors.Is(err, sql.ErrNoRows) {
			// free
		} else if err != nil {
			return fmt.Errorf("check email: %w", err)
		} else {
			return ErrEmailTaken
		}
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET email = ?, updated_at = ? WHERE id = ?`, email, nowStr(), userID)
	if err != nil {
		return fmt.Errorf("set email: %w", err)
	}
	return nil
}

// UpdatePasswordHash replaces the stored argon2id string.
func (s *Store) UpdatePasswordHash(ctx context.Context, userID int64, passwordHash string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`,
		passwordHash, nowStr(), userID)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	return nil
}

// SetMFASecret stores the base32 TOTP secret (setup started, not yet enabled).
func (s *Store) SetMFASecret(ctx context.Context, userID int64, secret string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET mfa_secret = ?, mfa_enabled = 0, mfa_last_step = -1, updated_at = ? WHERE id = ?`,
		secret, nowStr(), userID)
	if err != nil {
		return fmt.Errorf("set mfa secret: %w", err)
	}
	return nil
}

// EnableMFA marks the TOTP secret as confirmed and records the accepted
// step for the replay guard.
func (s *Store) EnableMFA(ctx context.Context, userID int64, acceptedStep int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET mfa_enabled = 1, mfa_last_step = ?, updated_at = ? WHERE id = ?`,
		acceptedStep, nowStr(), userID)
	if err != nil {
		return fmt.Errorf("enable mfa: %w", err)
	}
	return nil
}

// AcceptMFAStep updates the replay-guard step after a successful verify.
func (s *Store) AcceptMFAStep(ctx context.Context, userID int64, step int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET mfa_last_step = ?, updated_at = ? WHERE id = ?`,
		step, nowStr(), userID)
	if err != nil {
		return fmt.Errorf("accept mfa step: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Claim tokens
// ---------------------------------------------------------------------------

// CreateClaimToken stores a new claim token (hashed) with the given TTL.
// Only one unconsumed/unrevoked token may exist at a time; the previous
// active token is revoked (rotation), which keeps "single active claim
// invitation" semantics auditable.
func (s *Store) CreateClaimToken(ctx context.Context, tokenHash string, ttl time.Duration) (ClaimToken, error) {
	var ct ClaimToken
	now := time.Now().UTC()
	exp := now.Add(ttl)
	_, err := s.db.ExecContext(ctx, `
UPDATE claim_tokens SET revoked_at = ?
WHERE consumed_at = '' AND revoked_at = ''`, nowStr())
	if err != nil {
		return ct, fmt.Errorf("revoke previous claim tokens: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `
INSERT INTO claim_tokens (token_hash, created_at, expires_at) VALUES (?, ?, ?)`,
		tokenHash, now.Format(time.RFC3339Nano), exp.Format(time.RFC3339Nano))
	if err != nil {
		return ct, fmt.Errorf("create claim token: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return ct, fmt.Errorf("claim token id: %w", err)
	}
	ct = ClaimToken{ID: id, TokenHash: tokenHash, CreatedAt: now, ExpiresAt: exp}
	return ct, nil
}

// CreateClaimTokenIfUnclaimed issues a claim token only while the
// instance is still unclaimed. This is the authoritative guard behind
// `drevantad claim init`: once the first owner exists, claim tokens can
// never be issued or regenerated — there is no path back to unclaimed.
func (s *Store) CreateClaimTokenIfUnclaimed(ctx context.Context, tokenHash string, ttl time.Duration) (ClaimToken, error) {
	st, err := s.InstanceState(ctx)
	if err != nil {
		return ClaimToken{}, fmt.Errorf("check instance state: %w", err)
	}
	if st.Status != StatusUnclaimed {
		return ClaimToken{}, ErrAlreadyClaimed
	}
	return s.CreateClaimToken(ctx, tokenHash, ttl)
}

// ActiveClaimToken returns the currently valid (unconsumed, unrevoked,
// unexpired) token, or ErrNotFound.
func (s *Store) ActiveClaimToken(ctx context.Context) (ClaimToken, error) {
	return s.scanClaimToken(s.db.QueryRowContext(ctx, `
SELECT id, token_hash, created_at, expires_at, consumed_at, revoked_at
FROM claim_tokens
WHERE consumed_at = '' AND revoked_at = ''
ORDER BY id DESC LIMIT 1`))
}

// ConsumeClaimToken marks the token identified by hash as used, but only
// if it is still unconsumed, unrevoked and unexpired. It reports whether
// the consume succeeded; callers turn "false" into a precise error.
func (s *Store) ConsumeClaimToken(ctx context.Context, tokenHash string) (bool, error) {
	ct, err := s.ActiveClaimToken(ctx)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if ct.TokenHash != tokenHash {
		return false, nil
	}
	if !ct.ExpiresAt.After(time.Now().UTC()) {
		return false, nil
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE claim_tokens SET consumed_at = ? WHERE id = ? AND consumed_at = ''`,
		nowStr(), ct.ID)
	if err != nil {
		return false, fmt.Errorf("consume claim token: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// ClaimTokenByHash fetches one stored claim token by its hash — used to
// turn a rejected claim into a precise error (invalid vs expired vs used).
func (s *Store) ClaimTokenByHash(ctx context.Context, tokenHash string) (ClaimToken, error) {
	var (
		ct                      ClaimToken
		created, exp, cons, rev string
	)
	err := s.db.QueryRowContext(ctx, `
SELECT id, token_hash, created_at, expires_at, consumed_at, revoked_at
FROM claim_tokens WHERE token_hash = ?`, tokenHash).
		Scan(&ct.ID, &ct.TokenHash, &created, &exp, &cons, &rev)
	if errors.Is(err, sql.ErrNoRows) {
		return ct, ErrNotFound
	}
	if err != nil {
		return ct, err
	}
	ct.CreatedAt = parseTime(created)
	ct.ExpiresAt = parseTime(exp)
	ct.ConsumedAt = parseTime(cons)
	ct.RevokedAt = parseTime(rev)
	return ct, nil
}

// RevokeClaimToken revokes one token by row id (operator rotation).
func (s *Store) RevokeClaimToken(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE claim_tokens SET revoked_at = ? WHERE id = ? AND consumed_at = ''`, nowStr(), id)
	if err != nil {
		return fmt.Errorf("revoke claim token %d: %w", id, err)
	}
	return nil
}

func (s *Store) scanClaimToken(row *sql.Row) (ClaimToken, error) {
	var (
		ct                      ClaimToken
		created, exp, cons, rev string
	)
	err := row.Scan(&ct.ID, &ct.TokenHash, &created, &exp, &cons, &rev)
	if errors.Is(err, sql.ErrNoRows) {
		return ct, ErrNotFound
	}
	if err != nil {
		return ct, err
	}
	ct.CreatedAt = parseTime(created)
	ct.ExpiresAt = parseTime(exp)
	ct.ConsumedAt = parseTime(cons)
	ct.RevokedAt = parseTime(rev)
	return ct, nil
}

// ---------------------------------------------------------------------------
// Sessions
// ---------------------------------------------------------------------------

// CreateSession stores a new session (hashed cookie value).
func (s *Store) CreateSession(ctx context.Context, tokenHash string, userID int64, scope string, sliding, absolute time.Duration) (Session, error) {
	var sess Session
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO sessions (token_hash, user_id, scope, created_at, absolute_at, expires_at, last_seen_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		tokenHash, userID, scope,
		now.Format(time.RFC3339Nano), now.Add(absolute).Format(time.RFC3339Nano),
		now.Add(sliding).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return sess, fmt.Errorf("create session: %w", err)
	}
	return s.SessionByHash(ctx, tokenHash)
}

// SessionByHash looks up a live session by cookie-token hash. Expired
// sessions are treated as absent and pruned lazily.
func (s *Store) SessionByHash(ctx context.Context, tokenHash string) (Session, error) {
	var (
		sess                                 Session
		userID                               int64
		created, absolute, expires, lastSeen string
	)
	err := s.db.QueryRowContext(ctx, `
SELECT id, token_hash, user_id, scope, created_at, absolute_at, expires_at, last_seen_at
FROM sessions WHERE token_hash = ?`, tokenHash).
		Scan(&sess.ID, &sess.TokenHash, &userID, &sess.Scope, &created, &absolute, &expires, &lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return sess, ErrNotFound
	}
	if err != nil {
		return sess, fmt.Errorf("lookup session: %w", err)
	}
	now := time.Now().UTC()
	sess.CreatedAt = parseTime(created)
	sess.AbsoluteAt = parseTime(absolute)
	sess.ExpiresAt = parseTime(expires)
	sess.LastSeenAt = parseTime(lastSeen)
	if !sess.ExpiresAt.After(now) || !sess.AbsoluteAt.After(now) {
		_ = s.DeleteSession(ctx, tokenHash)
		return sess, ErrNotFound
	}
	sess.UserID = userID
	return sess, nil
}

// TouchSession slides the expiry window forward from now. The hard
// absolute cap is enforced separately in SessionByHash, so simply
// overwriting expires_at keeps sliding-window semantics without relying
// on SQL string comparison of RFC3339Nano timestamps.
func (s *Store) TouchSession(ctx context.Context, tokenHash string, sliding time.Duration) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE sessions SET expires_at = ?, last_seen_at = ? WHERE token_hash = ?`,
		time.Now().UTC().Add(sliding).Format(time.RFC3339Nano), nowStr(), tokenHash)
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

// DeleteSession removes one session (logout).
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteOtherSessions ends every session of the user except keepHash
// (used after a password change: other devices must re-authenticate).
func (s *Store) DeleteOtherSessions(ctx context.Context, userID int64, keepHash string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE user_id = ? AND token_hash != ?`, userID, keepHash)
	if err != nil {
		return 0, fmt.Errorf("delete other sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, nil
}

// PruneSessions deletes expired sessions; called opportunistically.
func (s *Store) PruneSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
DELETE FROM sessions WHERE expires_at <= ? OR absolute_at <= ?`, nowStr(), nowStr())
	if err != nil {
		return fmt.Errorf("prune sessions: %w", err)
	}
	return nil
}

// DeleteUser removes an account; used as the compensating action when a
// claim transition fails after user creation.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete user %d: %w", id, err)
	}
	return nil
}

// SetSessionScope upgrades an onboarding session to full scope once
// onboarding completes.
func (s *Store) SetSessionScope(ctx context.Context, tokenHash, scope string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET scope = ? WHERE token_hash = ?`, scope, tokenHash)
	if err != nil {
		return fmt.Errorf("set session scope: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Recovery codes
// ---------------------------------------------------------------------------

// ReplaceRecoveryCodes stores a fresh set of hashed recovery codes for a
// user, replacing any previous set (codes are single-use per set).
func (s *Store) ReplaceRecoveryCodes(ctx context.Context, userID int64, codeHashes []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin recovery replace: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_codes WHERE user_id = ?`, userID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("clear recovery codes: %w", err)
	}
	for _, h := range codeHashes {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO recovery_codes (user_id, code_hash) VALUES (?, ?)`, userID, h); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("insert recovery code: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit recovery replace: %w", err)
	}
	return nil
}

// ConsumeRecoveryCode marks the first unused code matching codeHash as
// used and reports whether a match was found.
func (s *Store) ConsumeRecoveryCode(ctx context.Context, userID int64, codeHash string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
UPDATE recovery_codes SET used_at = ?
WHERE id = (
        SELECT id FROM recovery_codes
        WHERE user_id = ? AND code_hash = ? AND used_at = ''
        ORDER BY id LIMIT 1
)`, nowStr(), userID, codeHash)
	if err != nil {
		return false, fmt.Errorf("consume recovery code: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// UnusedRecoveryCodeCount reports how many single-use codes remain.
func (s *Store) UnusedRecoveryCodeCount(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM recovery_codes WHERE user_id = ? AND used_at = ''`, userID).Scan(&n)
	return n, err
}
