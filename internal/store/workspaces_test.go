package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/auth"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/workspaces"
)

// newUser is a minimal account for workspace tests.
func newUser(t *testing.T, s *Store, username string) User {
	t.Helper()
	hash, err := auth.HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.CreateUser(context.Background(), username, hash, "")
	if err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}
	return u
}

func mustMigrate(t *testing.T, s *Store) {
	t.Helper()
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
}

func TestMigrationSeedsRegistryAndServerWorkspace(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	ctx := context.Background()
	mustMigrate(t, s)

	ws, err := s.ServerWorkspace(ctx)
	if err != nil {
		t.Fatalf("server workspace missing after migrate: %v", err)
	}
	if ws.Type != workspaces.TypeServer || ws.State != WorkspaceActive {
		t.Fatalf("unexpected server workspace: %+v", ws)
	}

	// Registry rows: the shipped permission keys and one role set per
	// template must exist.
	var perms, roles int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM permissions`).Scan(&perms); err != nil {
		t.Fatal(err)
	}
	if perms != len(workspaces.AllPermissions) {
		t.Fatalf("permissions = %d, want %d", perms, len(workspaces.AllPermissions))
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM roles`).Scan(&roles); err != nil {
		t.Fatal(err)
	}
	if roles != len(workspaces.DefaultRoles) {
		t.Fatalf("roles = %d, want %d", roles, len(workspaces.DefaultRoles))
	}

	// Spot-check a binding: server owner must hold the documented §5.6
	// rule 1 capability, and nothing must have granted it to org staff.
	ownerHolds, err := s.roleHoldsPerm(ctx, workspaces.TypeServer, workspaces.RoleServerOwner, workspaces.PHostingOrgCreate)
	if err != nil {
		t.Fatal(err)
	}
	if !ownerHolds {
		t.Fatal("server owner lacks hosting.org.create")
	}
	staffHolds, err := s.roleHoldsPerm(ctx, workspaces.TypeHostingOrg, workspaces.RoleOrgStaff, workspaces.PHostingOrgCreate)
	if err != nil {
		t.Fatal(err)
	}
	if staffHolds {
		t.Fatal("org staff must not hold hosting.org.create")
	}
}

// roleHoldsPerm is a test helper reading role_permissions directly.
func (s *Store) roleHoldsPerm(ctx context.Context, wsType, role, perm string) (bool, error) {
	var n int
	err := s.db.QueryRow(`
SELECT COUNT(1) FROM role_permissions rp
JOIN roles r ON r.id = rp.role_id
WHERE r.workspace_type = ? AND r.name = ? AND rp.permission_key = ?`,
		wsType, role, perm).Scan(&n)
	return n > 0, err
}

func TestEnsureServerMembershipWrapsClaimedOwner(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	ctx := context.Background()
	mustMigrate(t, s)

	u := newUser(t, s, "admin")
	// Claim-flow hook: idempotent, grants server owner.
	if err := s.EnsureServerMembership(ctx, u.ID); err != nil {
		t.Fatalf("ensure membership: %v", err)
	}
	if err := s.EnsureServerMembership(ctx, u.ID); err != nil {
		t.Fatalf("ensure membership (again): %v", err)
	}
	ms, err := s.MembershipsForUser(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 {
		t.Fatalf("memberships = %d, want 1 (UNIQUE(user,workspace) must hold)", len(ms))
	}
	m := ms[0]
	if m.WorkspaceType != workspaces.TypeServer || m.RoleName != workspaces.RoleServerOwner {
		t.Fatalf("unexpected membership: %+v", m)
	}
	// Owner holds system capabilities but never org-customer management.
	ok, err := s.Can(ctx, u.ID, m.WorkspaceID, workspaces.PSystemMetricsRead)
	if err != nil || !ok {
		t.Fatalf("owner Can(metrics) = %v, %v", ok, err)
	}
	ok, err = s.Can(ctx, u.ID, m.WorkspaceID, workspaces.PHostingCustomersMgmt)
	if err != nil || ok {
		t.Fatalf("server owner must NOT hold hosting.customers.manage (rule 1), got %v, %v", ok, err)
	}
}

func TestHostingOrgBoundaries(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	ctx := context.Background()
	mustMigrate(t, s)

	admin := newUser(t, s, "admin")
	if err := s.EnsureServerMembership(ctx, admin.ID); err != nil {
		t.Fatal(err)
	}
	other := newUser(t, s, "agency-owner")
	if err := s.EnsureServerMembership(ctx, other.ID); err != nil {
		t.Fatal(err)
	}

	// Admin creates an agency org (PRD §5.6 rule 1); creator becomes owner.
	org, err := s.CreateHostingOrg(ctx, "Acme Hosting", workspaces.BusinessAgency, admin.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if org.Type != workspaces.TypeHostingOrg || org.BusinessType != workspaces.BusinessAgency {
		t.Fatalf("unexpected org: %+v", org)
	}
	if org.ParentID == 0 {
		t.Fatal("org must be parented to the server workspace")
	}

	// The creator is org owner AND still server admin — two memberships,
	// one account (rule 5).
	ms, err := s.MembershipsForUser(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 {
		t.Fatalf("admin memberships = %d, want 2", len(ms))
	}

	// Server owner can see the org exists (hosting.organization.read) but
	// never gains customer management (rule 1: customer-private data
	// requires a grant).
	ok, err := s.Can(ctx, admin.ID, org.ID, workspaces.PHostingOrgRead)
	if err != nil || !ok {
		t.Fatalf("org owner Can(org read) = %v, %v", ok, err)
	}

	// A different account with server ownership but no org membership
	// has NO access to the org — membership is the only path (rule 6).
	ok, err = s.Can(ctx, other.ID, org.ID, workspaces.PWorkspaceRead)
	if err != nil || ok {
		t.Fatalf("non-member Can(org read) = %v, %v", ok, err)
	}
	if _, err := s.MembershipFor(ctx, other.ID, org.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-member MembershipFor = %v, want ErrNotFound", err)
	}

	// Org roles never touch the server (rule 2).
	ok, err = s.Can(ctx, admin.ID, org.ID, workspaces.PSystemFirewallModify)
	if err != nil || ok {
		t.Fatalf("org context must not grant server capabilities, got %v, %v", ok, err)
	}

	// Invalid business_type is rejected by the schema CHECK.
	if _, err := s.CreateHostingOrg(ctx, "Bad Org", "mafia", admin.ID); err == nil {
		t.Fatal("invalid business_type must be rejected")
	}
}

func TestRevocationIsImmediateAndLastOwnerProtected(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	ctx := context.Background()
	mustMigrate(t, s)

	admin := newUser(t, s, "admin")
	if err := s.EnsureServerMembership(ctx, admin.ID); err != nil {
		t.Fatal(err)
	}
	org, err := s.CreateHostingOrg(ctx, "Acme Hosting", workspaces.BusinessReseller, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	staff := newUser(t, s, "staff")
	roleID, err := s.RoleIDFor(ctx, workspaces.TypeHostingOrg, workspaces.RoleOrgStaff)
	if err != nil {
		t.Fatal(err)
	}
	now := nowStr()
	if _, err := s.db.Exec(`
INSERT INTO workspace_memberships (user_id, workspace_id, role_id, state, created_at, updated_at)
VALUES (?, ?, ?, 'active', ?, ?)`, staff.ID, org.ID, roleID, now, now); err != nil {
		t.Fatal(err)
	}

	ok, err := s.Can(ctx, staff.ID, org.ID, workspaces.PWorkspaceRead)
	if err != nil || !ok {
		t.Fatalf("staff Can(read) = %v, %v", ok, err)
	}

	// Revocation bites immediately — no cached permissions (PRD §21.3).
	if err := s.SetMembershipState(ctx, org.ID, staff.ID, MembershipRevoked); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	ok, err = s.Can(ctx, staff.ID, org.ID, workspaces.PWorkspaceRead)
	if err != nil || ok {
		t.Fatalf("revoked staff Can(read) = %v, %v", ok, err)
	}
	if _, err := s.MembershipFor(ctx, staff.ID, org.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked membership must resolve as ErrNotFound, got %v", err)
	}

	// The sole org owner cannot be removed.
	if err := s.SetMembershipState(ctx, org.ID, admin.ID, MembershipRevoked); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("last-owner revoke = %v, want ErrLastOwner", err)
	}
}

func TestInvitationLifecycle(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	ctx := context.Background()
	mustMigrate(t, s)

	admin := newUser(t, s, "admin")
	if err := s.EnsureServerMembership(ctx, admin.ID); err != nil {
		t.Fatal(err)
	}
	org, err := s.CreateHostingOrg(ctx, "Acme Hosting", workspaces.BusinessHostingProvider, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	managerRole, err := s.RoleIDFor(ctx, workspaces.TypeHostingOrg, workspaces.RoleOrgManager)
	if err != nil {
		t.Fatal(err)
	}
	const ttl = 7 * 24 * time.Hour // matches the API's invitationTTL

	token, hash, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	inv, err := s.CreateInvitation(ctx, org.ID, managerRole, "manager@example.com", admin.ID, hash, ttl)
	if err != nil {
		t.Fatalf("create invitation: %v", err)
	}
	if inv.Email != "manager@example.com" || inv.RoleName != workspaces.RoleOrgManager {
		t.Fatalf("unexpected invitation: %+v", inv)
	}

	invitee := newUser(t, s, "manager")
	m, err := s.AcceptInvitation(ctx, auth.TokenHash(token), invitee.ID)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if m.RoleName != workspaces.RoleOrgManager || m.WorkspaceID != org.ID {
		t.Fatalf("unexpected membership: %+v", m)
	}

	// Second use is rejected — the token is single-use (like claim
	// tokens, PRD §5.4).
	if _, err := s.AcceptInvitation(ctx, token, invitee.ID); err == nil {
		t.Fatal("reused invitation token must be rejected")
	}

	// Revoked invitations are dead on arrival.
	token2, hash2, _ := auth.NewToken()
	inv2, err := s.CreateInvitation(ctx, org.ID, managerRole, "x@example.com", admin.ID, hash2, ttl)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeInvitation(ctx, org.ID, inv2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptInvitation(ctx, token2, invitee.ID); err == nil {
		t.Fatal("revoked invitation must be rejected")
	}
}

func TestSessionActiveWorkspaceIsAdvisory(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	ctx := context.Background()
	mustMigrate(t, s)

	admin := newUser(t, s, "admin")
	if err := s.EnsureServerMembership(ctx, admin.ID); err != nil {
		t.Fatal(err)
	}
	_, hash, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSession(ctx, hash, admin.ID, ScopeFull, time.Hour, 2*time.Hour); err != nil {
		t.Fatal(err)
	}
	org, err := s.CreateHostingOrg(ctx, "Acme Hosting", workspaces.BusinessAgency, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionActiveWorkspace(ctx, hash, org.ID); err != nil {
		t.Fatal(err)
	}
	sess, err := s.SessionByHash(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if sess.ActiveWorkspaceID != org.ID {
		t.Fatalf("ActiveWorkspaceID = %d, want %d", sess.ActiveWorkspaceID, org.ID)
	}
	// The advisory field changes nothing about authorization: Can() is
	// driven by memberships only (rule 6 — switching never escalates).
	ok, err := s.Can(ctx, admin.ID, org.ID, workspaces.PHostingCustomersMgmt)
	if err != nil || !ok {
		t.Fatalf("org owner Can(customers) = %v, %v", ok, err)
	}
	ok, err = s.Can(ctx, admin.ID, org.ID, workspaces.PSystemFirewallModify)
	if err != nil || ok {
		t.Fatalf("org context must not grant system.firewall.modify, got %v, %v", ok, err)
	}
}
