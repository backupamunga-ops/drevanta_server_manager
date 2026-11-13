// Workspace persistence: the P2.0 identity foundation (PRD §21.2,
// finalized in docs/phase-2/P2.0-identity-workspaces.md).
//
// Authorization model:
//   - A membership row (user, workspace, role, state) is the ONLY way a
//     user gets access to a workspace; there is no implicit inheritance
//     from parent workspaces or personas (PRD §5.6 rule 6).
//   - Can() re-checks the database on every call — permissions are never
//     cached across requests, so a revoked or expired membership bites
//     immediately (PRD §21.3).
//   - Non-membership is indistinguishable from non-existence to callers:
//     both return ErrNotFound so the API can answer 404 without leaking
//     which workspaces exist.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/workspaces"
)

// Membership states.
const (
	MembershipActive  = "active"
	MembershipRevoked = "revoked"
)

// Workspace states.
const (
	WorkspaceActive   = "active"
	WorkspaceArchived = "archived"
)

// ErrLastOwner is returned when a revocation would leave a workspace
// without any active owner.
var ErrLastOwner = errors.New("cannot remove the last active owner")

// Workspace is one authorization context (PRD §5.1: personas are UX
// categories, not inherited security roles).
type Workspace struct {
	ID           int64
	Type         string
	Name         string
	BusinessType string // "" unless type = hosting_org
	ParentID     int64  // 0 = none
	State        string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Membership is one (user, workspace, role) grant.
type Membership struct {
	ID             int64
	UserID         int64
	WorkspaceID    int64
	RoleID         int64
	RoleName       string
	WorkspaceType  string
	WorkspaceName  string
	BusinessType   string
	WorkspaceState string
	State          string
	ExpiresAt      time.Time // zero = no expiry
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Invitation is one pending workspace invitation (hashed token).
type Invitation struct {
	ID          int64
	WorkspaceID int64
	RoleID      int64
	RoleName    string
	Email       string
	TokenHash   string
	InvitedBy   int64
	CreatedAt   time.Time
	ExpiresAt   time.Time
	ConsumedAt  time.Time
	RevokedAt   time.Time
}

const workspaceCols = `id, type, name, business_type, parent_workspace_id, state, created_at, updated_at`

func scanWorkspace(row interface{ Scan(...any) error }) (Workspace, error) {
	var (
		w            Workspace
		businessType sql.NullString
		parentID     sql.NullInt64
		created, upd string
	)
	err := row.Scan(&w.ID, &w.Type, &w.Name, &businessType, &parentID, &w.State, &created, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return w, ErrNotFound
	}
	if err != nil {
		return w, err
	}
	w.BusinessType = businessType.String
	w.ParentID = parentID.Int64
	w.CreatedAt = parseTime(created)
	w.UpdatedAt = parseTime(upd)
	return w, nil
}

// ServerWorkspace returns the singleton server workspace for this
// instance. Phase 2 manages exactly one local server (PRD OD-003), so
// type='server' identifies it.
func (s *Store) ServerWorkspace(ctx context.Context) (Workspace, error) {
	w, err := scanWorkspace(s.db.QueryRowContext(ctx,
		`SELECT `+workspaceCols+` FROM workspaces WHERE type = ? ORDER BY id LIMIT 1`, workspaces.TypeServer))
	if err != nil {
		return w, fmt.Errorf("server workspace: %w", err)
	}
	return w, nil
}

// ensureServerWorkspace returns the server workspace, creating it when
// the instance has none yet (fresh install before the first claim).
func (s *Store) ensureServerWorkspace(ctx context.Context) (Workspace, error) {
	w, err := s.ServerWorkspace(ctx)
	if !errors.Is(err, ErrNotFound) {
		return w, err
	}
	now := nowStr()
	res, cerr := s.db.ExecContext(ctx, `
INSERT INTO workspaces (type, name, state, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)`, workspaces.TypeServer, "Primary server", WorkspaceActive, now, now)
	if cerr != nil {
		return w, fmt.Errorf("create server workspace: %w", cerr)
	}
	id, cerr := res.LastInsertId()
	if cerr != nil {
		return w, fmt.Errorf("server workspace id: %w", cerr)
	}
	return s.WorkspaceByID(ctx, id)
}

// WorkspaceByID fetches one workspace.
func (s *Store) WorkspaceByID(ctx context.Context, id int64) (Workspace, error) {
	w, err := scanWorkspace(s.db.QueryRowContext(ctx,
		`SELECT `+workspaceCols+` FROM workspaces WHERE id = ?`, id))
	if err != nil {
		return w, fmt.Errorf("workspace %d: %w", id, err)
	}
	return w, nil
}

// roleIDFor resolves a seeded role template's id.
func (s *Store) roleIDFor(ctx context.Context, workspaceType, name string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM roles WHERE workspace_type = ? AND name = ?`, workspaceType, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// RoleIDFor resolves a seeded role template's id (exported for the API
// layer, which must map requested role names to ids when inviting).
func (s *Store) RoleIDFor(ctx context.Context, workspaceType, name string) (int64, error) {
	return s.roleIDFor(ctx, workspaceType, name)
}

// EnsureServerMembership wraps userID into the server workspace as an
// owner: it creates the workspace if missing (fresh install) and
// inserts the membership unless an active one already exists. This is
// the claim-flow hook that keeps "the Phase 1 owner's access becomes
// their server workspace" true (RFC §6).
func (s *Store) EnsureServerMembership(ctx context.Context, userID int64) error {
	w, err := s.ensureServerWorkspace(ctx)
	if err != nil {
		return fmt.Errorf("ensure server workspace: %w", err)
	}
	roleID, err := s.roleIDFor(ctx, workspaces.TypeServer, workspaces.RoleServerOwner)
	if err != nil {
		return fmt.Errorf("server owner role: %w", err)
	}
	var existing int64
	err = s.db.QueryRowContext(ctx, `
SELECT id FROM workspace_memberships WHERE user_id = ? AND workspace_id = ? AND state = ?`,
		userID, w.ID, MembershipActive).Scan(&existing)
	if err == nil {
		return nil // already an active member
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check membership: %w", err)
	}
	now := nowStr()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO workspace_memberships (user_id, workspace_id, role_id, state, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (user_id, workspace_id) DO UPDATE SET
        role_id = excluded.role_id, state = 'active', updated_at = excluded.updated_at`,
		userID, w.ID, roleID, MembershipActive, now, now)
	if err != nil {
		return fmt.Errorf("insert server membership: %w", err)
	}
	return nil
}

// CreateHostingOrg creates a hosting_org workspace (business_type ∈
// reseller | hosting_provider | agency) as a child of the server
// workspace, granting creatorUserID the org owner role. This is the
// PRD §5.6 rule 1 server-administrator capability; callers must have
// verified hosting.org.create on the server workspace first.
func (s *Store) CreateHostingOrg(ctx context.Context, name, businessType string, creatorUserID int64) (Workspace, error) {
	var w Workspace
	server, err := s.ensureServerWorkspace(ctx)
	if err != nil {
		return w, fmt.Errorf("server workspace: %w", err)
	}
	roleID, err := s.roleIDFor(ctx, workspaces.TypeHostingOrg, workspaces.RoleOrgOwner)
	if err != nil {
		return w, fmt.Errorf("org owner role: %w", err)
	}
	now := nowStr()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return w, fmt.Errorf("begin org create: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `
INSERT INTO workspaces (type, name, business_type, parent_workspace_id, state, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		workspaces.TypeHostingOrg, name, businessType, server.ID, WorkspaceActive, now, now)
	if err != nil {
		return w, fmt.Errorf("insert hosting org: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return w, fmt.Errorf("hosting org id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO workspace_memberships (user_id, workspace_id, role_id, state, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		creatorUserID, id, roleID, MembershipActive, now, now); err != nil {
		return w, fmt.Errorf("insert owner membership: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return w, fmt.Errorf("commit org create: %w", err)
	}
	return s.WorkspaceByID(ctx, id)
}

// membershipFrom / membershipWhere compose every live-membership query:
// the caller inserts additional JOINs (role_permissions) BETWEEN them
// and appends extra WHERE terms after.
const membershipFrom = `
FROM workspace_memberships m
JOIN workspaces w ON w.id = m.workspace_id
JOIN roles r      ON r.id = m.role_id`

const membershipWhere = `
WHERE m.user_id = ?
  AND m.state = 'active'
  AND w.state = 'active'
  AND (m.expires_at = '' OR m.expires_at > ?)`

func scanMembership(row interface{ Scan(...any) error }) (Membership, error) {
	var (
		m            Membership
		businessType sql.NullString
		expires      string
		created, upd string
	)
	err := row.Scan(&m.ID, &m.UserID, &m.WorkspaceID, &m.RoleID, &m.RoleName,
		&m.WorkspaceType, &m.WorkspaceName, &businessType, &m.WorkspaceState,
		&m.State, &expires, &created, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	if err != nil {
		return m, err
	}
	m.BusinessType = businessType.String
	m.ExpiresAt = parseTime(expires)
	m.CreatedAt = parseTime(created)
	m.UpdatedAt = parseTime(upd)
	return m, nil
}

// MembershipsForUser lists the caller's live memberships (PRD §5.3:
// the selector shows only workspaces with a valid membership).
func (s *Store) MembershipsForUser(ctx context.Context, userID int64) ([]Membership, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT m.id, m.user_id, m.workspace_id, m.role_id, r.name,
       w.type, w.name, w.business_type, w.state,
       m.state, m.expires_at, m.created_at, m.updated_at
`+membershipFrom+`
`+membershipWhere+`
ORDER BY w.type, w.name`, userID, nowStr())
	if err != nil {
		return nil, fmt.Errorf("memberships for user %d: %w", userID, err)
	}
	defer rows.Close()
	var out []Membership
	for rows.Next() {
		m, err := scanMembership(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MembershipFor resolves one live membership, or ErrNotFound when the
// user holds none for that workspace (also covers revoked/expired and
// archived workspaces).
func (s *Store) MembershipFor(ctx context.Context, userID, workspaceID int64) (Membership, error) {
	m, err := scanMembership(s.db.QueryRowContext(ctx, `
SELECT m.id, m.user_id, m.workspace_id, m.role_id, r.name,
       w.type, w.name, w.business_type, w.state,
       m.state, m.expires_at, m.created_at, m.updated_at
`+membershipFrom+`
`+membershipWhere+` AND m.workspace_id = ?`, userID, nowStr(), workspaceID))
	if err != nil {
		return m, fmt.Errorf("membership user %d workspace %d: %w", userID, workspaceID, err)
	}
	return m, nil
}

// PermissionsForUser returns the effective permission keys for a user
// in a workspace, resolved through their active membership's role.
func (s *Store) PermissionsForUser(ctx context.Context, userID, workspaceID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT rp.permission_key
`+membershipFrom+`
JOIN role_permissions rp ON rp.role_id = m.role_id
`+membershipWhere+` AND m.workspace_id = ?
ORDER BY rp.permission_key`, userID, nowStr(), workspaceID)
	if err != nil {
		return nil, fmt.Errorf("permissions user %d workspace %d: %w", userID, workspaceID, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("scan permission: %w", err)
		}
		out = append(out, key)
	}
	return out, rows.Err()
}

// Can reports whether userID holds perm in workspaceID right now. It is
// the per-request authorization primitive (PRD §6.3 step 5): every call
// reads the live rows, so revocation is effective immediately and the
// browser-selected workspace can never smuggle access through.
func (s *Store) Can(ctx context.Context, userID, workspaceID int64, perm string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(1)
`+membershipFrom+`
JOIN role_permissions rp ON rp.role_id = m.role_id AND rp.permission_key = ?
`+membershipWhere+` AND m.workspace_id = ?`,
		perm, userID, nowStr(), workspaceID).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("can(user %d, workspace %d, %s): %w", userID, workspaceID, perm, err)
	}
	return n > 0, nil
}

// WorkspaceMembers lists a workspace's active members with roles.
type WorkspaceMember struct {
	UserID    int64
	Username  string
	RoleName  string
	State     string
	CreatedAt time.Time
}

// MembersOf lists the active members of one workspace.
func (s *Store) MembersOf(ctx context.Context, workspaceID int64) ([]WorkspaceMember, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT m.user_id, u.username, r.name, m.state, m.created_at
FROM workspace_memberships m
JOIN users u ON u.id = m.user_id
JOIN roles r ON r.id = m.role_id
WHERE m.workspace_id = ? AND m.state = 'active'
ORDER BY m.created_at, m.id`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("members of workspace %d: %w", workspaceID, err)
	}
	defer rows.Close()
	var out []WorkspaceMember
	for rows.Next() {
		var m WorkspaceMember
		var created string
		if err := rows.Scan(&m.UserID, &m.Username, &m.RoleName, &m.State, &created); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		m.CreatedAt = parseTime(created)
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetMembershipState flips a membership's state (revoke / re-activate).
// Re-activation after revocation is allowed only for an existing row —
// there is no "revoked history" beyond the row itself.
func (s *Store) SetMembershipState(ctx context.Context, workspaceID, userID int64, state string) error {
	now := nowStr()
	if state == MembershipRevoked {
		// Last-owner guard: an organization always keeps one active owner.
		var owners int
		if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(1) FROM workspace_memberships m
JOIN roles r ON r.id = m.role_id
WHERE m.workspace_id = ? AND m.state = 'active' AND r.name = 'owner'`,
			workspaceID).Scan(&owners); err != nil {
			return fmt.Errorf("count owners: %w", err)
		}
		var role string
		if err := s.db.QueryRowContext(ctx, `
SELECT r.name FROM workspace_memberships m
JOIN roles r ON r.id = m.role_id
WHERE m.workspace_id = ? AND m.user_id = ? AND m.state = 'active'`,
			workspaceID, userID).Scan(&role); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("target role: %w", err)
		}
		if role == workspaces.RoleOrgOwner && owners <= 1 {
			return ErrLastOwner
		}
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE workspace_memberships SET state = ?, updated_at = ?
WHERE workspace_id = ? AND user_id = ? AND state != ?`,
		state, now, workspaceID, userID, state)
	if err != nil {
		return fmt.Errorf("set membership state: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------------------------------------------------------------------------
// Invitations
// ---------------------------------------------------------------------------

// CreateInvitation stores a hashed invitation token for one workspace
// + role. The plaintext token is returned to the caller exactly once
// (shown to the inviting operator for manual delivery in Phase 2 —
// SMTP wiring for invitations is a later concern).
func (s *Store) CreateInvitation(ctx context.Context, workspaceID, roleID int64, email string, invitedBy int64, tokenHash string, ttl time.Duration) (Invitation, error) {
	var inv Invitation
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO workspace_invitations
        (workspace_id, role_id, email, token_hash, invited_by, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		workspaceID, roleID, email, tokenHash, invitedBy,
		now.Format(time.RFC3339Nano), now.Add(ttl).Format(time.RFC3339Nano))
	if err != nil {
		return inv, fmt.Errorf("create invitation: %w", err)
	}
	return s.InvitationByHash(ctx, tokenHash)
}

// ActiveInvitations lists a workspace's pending invitations.
func (s *Store) ActiveInvitations(ctx context.Context, workspaceID int64) ([]Invitation, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT i.id, i.workspace_id, i.role_id, r.name, i.email, i.token_hash,
       i.invited_by, i.created_at, i.expires_at, i.consumed_at, i.revoked_at
FROM workspace_invitations i
JOIN roles r ON r.id = i.role_id
WHERE i.workspace_id = ? AND i.consumed_at = '' AND i.revoked_at = '' AND i.expires_at > ?
ORDER BY i.id DESC`, workspaceID, nowStr())
	if err != nil {
		return nil, fmt.Errorf("invitations for workspace %d: %w", workspaceID, err)
	}
	defer rows.Close()
	var out []Invitation
	for rows.Next() {
		inv, err := scanInvitation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

func scanInvitation(row interface{ Scan(...any) error }) (Invitation, error) {
	var (
		inv                     Invitation
		created, exp, cons, rev string
	)
	err := row.Scan(&inv.ID, &inv.WorkspaceID, &inv.RoleID, &inv.RoleName, &inv.Email,
		&inv.TokenHash, &inv.InvitedBy, &created, &exp, &cons, &rev)
	if errors.Is(err, sql.ErrNoRows) {
		return inv, ErrNotFound
	}
	if err != nil {
		return inv, err
	}
	inv.CreatedAt = parseTime(created)
	inv.ExpiresAt = parseTime(exp)
	inv.ConsumedAt = parseTime(cons)
	inv.RevokedAt = parseTime(rev)
	return inv, nil
}

// InvitationByHash resolves a stored invitation by token hash without
// consuming it (used by the preview endpoint).
func (s *Store) InvitationByHash(ctx context.Context, tokenHash string) (Invitation, error) {
	inv, err := scanInvitation(s.db.QueryRowContext(ctx, `
SELECT i.id, i.workspace_id, i.role_id, r.name, i.email, i.token_hash,
       i.invited_by, i.created_at, i.expires_at, i.consumed_at, i.revoked_at
FROM workspace_invitations i
JOIN roles r ON r.id = i.role_id
WHERE i.token_hash = ?`, tokenHash))
	if err != nil {
		return inv, fmt.Errorf("invitation by hash: %w", err)
	}
	return inv, nil
}

// RevokeInvitation marks one pending invitation revoked.
func (s *Store) RevokeInvitation(ctx context.Context, workspaceID, invitationID int64) error {
	res, err := s.db.ExecContext(ctx, `
UPDATE workspace_invitations SET revoked_at = ?
WHERE id = ? AND workspace_id = ? AND consumed_at = '' AND revoked_at = ''`,
		nowStr(), invitationID, workspaceID)
	if err != nil {
		return fmt.Errorf("revoke invitation: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AcceptInvitation atomically consumes the invitation and grants the
// membership with the invited role (PRD §5.4: accept, verify, audit).
// If the user already holds a membership row for the workspace it is
// re-activated with the invited role; otherwise one is inserted. The
// whole exchange is one transaction, so a double-submitted token can
// only succeed once.
func (s *Store) AcceptInvitation(ctx context.Context, tokenHash string, userID int64) (Membership, error) {
	var m Membership
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return m, fmt.Errorf("begin accept: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	inv, err := scanInvitation(tx.QueryRowContext(ctx, `
SELECT i.id, i.workspace_id, i.role_id, r.name, i.email, i.token_hash,
       i.invited_by, i.created_at, i.expires_at, i.consumed_at, i.revoked_at
FROM workspace_invitations i
JOIN roles r ON r.id = i.role_id
WHERE i.token_hash = ?`, tokenHash))
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	if err != nil {
		return m, fmt.Errorf("load invitation: %w", err)
	}
	if !inv.ExpiresAt.After(now) {
		return m, fmt.Errorf("invitation expired")
	}
	if !inv.ConsumedAt.IsZero() || !inv.RevokedAt.IsZero() {
		return m, fmt.Errorf("invitation no longer active")
	}
	var wsState string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM workspaces WHERE id = ?`, inv.WorkspaceID).Scan(&wsState); err != nil {
		return m, fmt.Errorf("load workspace: %w", err)
	}
	if wsState != WorkspaceActive {
		return m, fmt.Errorf("workspace is not active")
	}
	res, err := tx.ExecContext(ctx, `
INSERT INTO workspace_memberships (user_id, workspace_id, role_id, state, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (user_id, workspace_id) DO UPDATE SET
        role_id = excluded.role_id, state = 'active', updated_at = excluded.updated_at`,
		userID, inv.WorkspaceID, inv.RoleID, MembershipActive,
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return m, fmt.Errorf("grant membership: %w", err)
	}
	if _, err := res.RowsAffected(); err != nil {
		return m, err
	}
	res2, err := tx.ExecContext(ctx, `
UPDATE workspace_invitations SET consumed_at = ? WHERE id = ? AND consumed_at = '' AND revoked_at = ''`,
		nowStr(), inv.ID)
	if err != nil {
		return m, fmt.Errorf("consume invitation: %w", err)
	}
	if n, _ := res2.RowsAffected(); n != 1 {
		return m, fmt.Errorf("invitation no longer active")
	}
	if err := tx.Commit(); err != nil {
		return m, fmt.Errorf("commit accept: %w", err)
	}
	return s.MembershipFor(ctx, userID, inv.WorkspaceID)
}

// SetSessionActiveWorkspace records the advisory active-workspace on a
// session (RFC §7: UI state only — it contributes nothing to
// authorization; every request re-checks membership).
func (s *Store) SetSessionActiveWorkspace(ctx context.Context, tokenHash string, workspaceID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET active_workspace_id = ? WHERE token_hash = ?`, workspaceID, tokenHash)
	if err != nil {
		return fmt.Errorf("set active workspace: %w", err)
	}
	return nil
}
