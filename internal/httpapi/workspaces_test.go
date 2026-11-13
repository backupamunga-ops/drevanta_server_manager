package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/auth"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/store"
)

// claimAdmin drives the API claim path (claim token → account → skip
// MFA with explicit acknowledgement) and returns the session header.
// The claim provisions the server-workspace owner membership (RFC §6).
func claimAdmin(t *testing.T, h http.Handler, st *store.Store, username string) map[string]string {
	t.Helper()
	token, _, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateClaimToken(t.Context(), auth.TokenHash(token), time.Hour); err != nil {
		t.Fatal(err)
	}
	rec := do(t, h, http.MethodPost, "/api/v1/onboarding/claim",
		fmt.Sprintf(`{"token":%q,"username":%q,"email":%q,"password":"correct horse battery"}`,
			token, username, username+"@example.com"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("claim = %d: %s", rec.Code, rec.Body.String())
	}
	hdr := cookieHeaderOf(rec)
	rec = do(t, h, http.MethodPost, "/api/v1/onboarding/mfa/skip",
		`{"acknowledge_risk":true}`, hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("mfa skip = %d: %s", rec.Code, rec.Body.String())
	}
	return hdr
}

// cookieHeaderOf turns a response's session cookie into a header map.
func cookieHeaderOf(rec *httptest.ResponseRecorder) map[string]string {
	c := cookieOf(rec)
	return map[string]string{"Cookie": c.Name + "=" + c.Value}
}

// loginAs signs an existing account in and returns its session header.
func loginAs(t *testing.T, h http.Handler, username string) map[string]string {
	t.Helper()
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login",
		fmt.Sprintf(`{"username":%q,"password":"correct horse battery"}`, username), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %s = %d: %s", username, rec.Code, rec.Body.String())
	}
	return cookieHeaderOf(rec)
}

// serverWorkspaceID reads the wrapped server workspace's id from the
// owner's workspace list.
func serverWorkspaceID(t *testing.T, h http.Handler, hdr map[string]string) int64 {
	t.Helper()
	rec := do(t, h, http.MethodGet, "/api/v1/workspaces", "", hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("workspaces = %d: %s", rec.Code, rec.Body.String())
	}
	m := decodeMap(t, rec)
	list := m["workspaces"].([]any)
	for _, raw := range list {
		ws := raw.(map[string]any)
		if ws["type"] == "server" {
			return int64(ws["id"].(float64))
		}
	}
	t.Fatalf("no server workspace in %v", m)
	return 0
}

func TestWorkspacesListForOwner(t *testing.T) {
	h, st := newTestHandler(t)
	hdr := claimAdmin(t, h, st, "owner")

	rec := do(t, h, http.MethodGet, "/api/v1/workspaces", "", hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d: %s", rec.Code, rec.Body.String())
	}
	m := decodeMap(t, rec)
	list, _ := m["workspaces"].([]any)
	if len(list) != 1 {
		t.Fatalf("workspaces = %d, want exactly the server workspace", len(list))
	}
	ws := list[0].(map[string]any)
	if ws["type"] != "server" || ws["role"] != "owner" {
		t.Fatalf("unexpected workspace view: %v", ws)
	}
	perms, _ := ws["permissions"].([]any)
	found := map[string]bool{}
	for _, p := range perms {
		found[p.(string)] = true
	}
	// RFC §6: the owner maps ScopeFull's capabilities; §5.6 rule 1 adds
	// hosting.org.create.
	for _, key := range []string{"workspace.read", "system.metrics.read", "hosting.org.create"} {
		if !found[key] {
			t.Fatalf("owner permissions missing %s: %v", key, found)
		}
	}
	if found["hosting.customers.manage"] {
		t.Fatal("server owner must not hold hosting.customers.manage (rule 1)")
	}
	if m["active_workspace_id"].(float64) != ws["id"].(float64) {
		t.Fatalf("active workspace %v, want the server workspace %v",
			m["active_workspace_id"], ws["id"])
	}
}

func TestCreateOrgSwitchAndServerGate(t *testing.T) {
	h, st := newTestHandler(t)
	hdr := claimAdmin(t, h, st, "owner")
	serverID := serverWorkspaceID(t, h, hdr)

	// Non-administrators cannot create orgs — but here the caller IS the
	// server owner, so creation succeeds with a business_type.
	rec := do(t, h, http.MethodPost, "/api/v1/workspaces",
		`{"name":"Acme Hosting","business_type":"agency"}`, hdr)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create org = %d: %s", rec.Code, rec.Body.String())
	}
	org := decodeMap(t, rec)["workspace"].(map[string]any)
	orgID := int64(org["id"].(float64))
	if org["business_type"] != "agency" || org["role"] != "owner" {
		t.Fatalf("unexpected org view: %v", org)
	}

	// Invalid business types are refused.
	rec = do(t, h, http.MethodPost, "/api/v1/workspaces",
		`{"name":"Bad Org","business_type":"mafia"}`, hdr)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad business_type = %d, want 400", rec.Code)
	}

	// Switch into the org context (PRD §5.3: re-authorized server-side).
	rec = do(t, h, http.MethodPost, fmt.Sprintf("/api/v1/workspaces/%d/enter", orgID), "", hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("enter = %d: %s", rec.Code, rec.Body.String())
	}

	// The switch is real: the server surface refuses in org context —
	// switching never grants privileges and never hides the boundary
	// (PRD §5.6 rule 6).
	rec = do(t, h, http.MethodGet, "/api/v1/server/metrics", "", hdr)
	if rec.Code != http.StatusForbidden || errCode(decodeMap(t, rec)) != "workspace_mismatch" {
		t.Fatalf("metrics in org context = %d: %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, http.MethodPost, "/api/v1/access/domain/check",
		`{"domain":"example.com"}`, hdr)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("domain check in org context = %d, want 403", rec.Code)
	}

	// Switching back restores the server surface (metrics need /proc, so
	// only assert the full round-trip on Linux).
	rec = do(t, h, http.MethodPost, fmt.Sprintf("/api/v1/workspaces/%d/enter", serverID), "", hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("enter server = %d: %s", rec.Code, rec.Body.String())
	}
	if runtime.GOOS == "linux" {
		rec = do(t, h, http.MethodGet, "/api/v1/server/metrics", "", hdr)
		if rec.Code != http.StatusOK {
			t.Fatalf("metrics in server context = %d: %s", rec.Code, rec.Body.String())
		}
	}

	// Switching into a workspace the caller does not belong to is 404 —
	// indistinguishable from non-existence (PRD §5.5).
	rec = do(t, h, http.MethodPost, "/api/v1/workspaces/999999/enter", "", hdr)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("enter unknown workspace = %d, want 404", rec.Code)
	}
}

func TestOrgMembersBoundariesAndRoles(t *testing.T) {
	h, st := newTestHandler(t)
	ownerHdr := claimAdmin(t, h, st, "owner")
	serverID := serverWorkspaceID(t, h, ownerHdr)

	rec := do(t, h, http.MethodPost, "/api/v1/workspaces",
		`{"name":"Acme Hosting","business_type":"reseller"}`, ownerHdr)
	orgID := int64(decodeMap(t, rec)["workspace"].(map[string]any)["id"].(float64))

	// Invite a manager (delegation per PRD §5.4; token shown once).
	rec = do(t, h, http.MethodPost, fmt.Sprintf("/api/v1/workspaces/%d/invitations", orgID),
		`{"email":"manager@example.com","role":"manager"}`, ownerHdr)
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite = %d: %s", rec.Code, rec.Body.String())
	}
	inv := decodeMap(t, rec)["invitation"].(map[string]any)
	token := inv["token"].(string)
	if !strings.Contains(inv["url"].(string), "/invite?token=") {
		t.Fatalf("invitation url missing: %v", inv)
	}

	// Preview works without a session (the token is the credential).
	rec = do(t, h, http.MethodGet, "/api/v1/workspaces/invitations/"+token, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview = %d: %s", rec.Code, rec.Body.String())
	}
	if decodeMap(t, rec)["workspace_name"] != "Acme Hosting" {
		t.Fatalf("preview body: %s", rec.Body.String())
	}

	// The invitee has no account yet: acceptance creates it and grants
	// the membership (one flow, PRD §5.4).
	rec = do(t, h, http.MethodPost, "/api/v1/workspaces/invitations/accept",
		fmt.Sprintf(`{"token":%q,"username":"manager","password":"correct horse battery"}`, token), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("accept = %d: %s", rec.Code, rec.Body.String())
	}
	if decodeMap(t, rec)["account_created"] != true {
		t.Fatalf("accept body: %s", rec.Body.String())
	}

	// Reusing the token fails (single-use).
	rec = do(t, h, http.MethodPost, "/api/v1/workspaces/invitations/accept",
		fmt.Sprintf(`{"token":%q,"username":"someoneelse","password":"correct horse battery"}`, token), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("reused token = %d, want 404", rec.Code)
	}

	// The manager signs in and works in the org.
	mgrHdr := loginAs(t, h, "manager")
	rec = do(t, h, http.MethodGet, fmt.Sprintf("/api/v1/workspaces/%d", orgID), "", mgrHdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("org detail = %d: %s", rec.Code, rec.Body.String())
	}
	if decodeMap(t, rec)["my_role"] != "manager" {
		t.Fatalf("detail body: %s", rec.Body.String())
	}

	// Cross-tenant: the server workspace does not exist for the manager
	// — no membership, no existence leak (PRD §5.5 / rule 2).
	rec = do(t, h, http.MethodGet, fmt.Sprintf("/api/v1/workspaces/%d", serverID), "", mgrHdr)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("server workspace for org member = %d, want 404", rec.Code)
	}
	rec = do(t, h, http.MethodPost, fmt.Sprintf("/api/v1/workspaces/%d/enter", serverID), "", mgrHdr)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("enter server workspace as org member = %d, want 404", rec.Code)
	}

	// Rule 2 continued: an org manager without server membership cannot
	// create hosting organizations on the host.
	rec = do(t, h, http.MethodPost, "/api/v1/workspaces",
		`{"name":"Sneaky Org","business_type":"agency"}`, mgrHdr)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("org manager create org = %d, want 403", rec.Code)
	}

	// The manager may invite staff but not owners (role elevation guard).
	rec = do(t, h, http.MethodPost, fmt.Sprintf("/api/v1/workspaces/%d/invitations", orgID),
		`{"email":"staff@example.com","role":"staff"}`, mgrHdr)
	if rec.Code != http.StatusCreated {
		t.Fatalf("manager invite staff = %d: %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, http.MethodPost, fmt.Sprintf("/api/v1/workspaces/%d/invitations", orgID),
		`{"email":"co@example.com","role":"owner"}`, mgrHdr)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("manager invite owner = %d, want 403", rec.Code)
	}

	// Unknown workspace is 404, never a leak of anything else.
	rec = do(t, h, http.MethodGet, "/api/v1/workspaces/424242", "", mgrHdr)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown workspace = %d, want 404", rec.Code)
	}

	// Owner removal guard: the org's sole owner cannot be removed by the
	// owner themself (last active owner, PRD §21.3 continuity).
	rec = do(t, h, http.MethodDelete, fmt.Sprintf("/api/v1/workspaces/%d/members/1", orgID), "", ownerHdr)
	if rec.Code != http.StatusConflict || errCode(decodeMap(t, rec)) != "last_owner" {
		t.Fatalf("remove last owner = %d: %s", rec.Code, rec.Body.String())
	}

	// Managers cannot remove members (need workspace.members.manage).
	rec = do(t, h, http.MethodDelete, fmt.Sprintf("/api/v1/workspaces/%d/members/1", orgID), "", mgrHdr)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("manager remove member = %d, want 403", rec.Code)
	}
}

func TestMembershipRevocationBitesImmediately(t *testing.T) {
	h, st := newTestHandler(t)
	ownerHdr := claimAdmin(t, h, st, "owner")

	rec := do(t, h, http.MethodPost, "/api/v1/workspaces",
		`{"name":"Acme Hosting","business_type":"agency"}`, ownerHdr)
	orgID := int64(decodeMap(t, rec)["workspace"].(map[string]any)["id"].(float64))
	orgStr := fmt.Sprintf("%d", orgID)

	// Invite a viewer and let them accept with a fresh account.
	rec = do(t, h, http.MethodPost, "/api/v1/workspaces/"+orgStr+"/invitations",
		`{"email":"viewer@example.com","role":"viewer"}`, ownerHdr)
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite = %d: %s", rec.Code, rec.Body.String())
	}
	token := decodeMap(t, rec)["invitation"].(map[string]any)["token"].(string)
	rec = do(t, h, http.MethodPost, "/api/v1/workspaces/invitations/accept",
		fmt.Sprintf(`{"token":%q,"username":"viewer","password":"correct horse battery"}`, token), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("accept = %d: %s", rec.Code, rec.Body.String())
	}

	// The viewer can read the workspace...
	viewerHdr := loginAs(t, h, "viewer")
	rec = do(t, h, http.MethodGet, "/api/v1/workspaces/"+orgStr, "", viewerHdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer detail = %d: %s", rec.Code, rec.Body.String())
	}

	// ...but cannot invite (viewer role has no members.invite), and the
	// 403 never confirms which workspaces exist for others.
	rec = do(t, h, http.MethodPost, "/api/v1/workspaces/"+orgStr+"/invitations",
		`{"email":"x@example.com","role":"staff"}`, viewerHdr)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer invite = %d, want 403", rec.Code)
	}

	// Owner revokes the viewer (user id 2: admin is 1, viewer is 2).
	rec = do(t, h, http.MethodDelete, "/api/v1/workspaces/"+orgStr+"/members/2", "", ownerHdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove member = %d: %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, http.MethodGet, "/api/v1/workspaces/"+orgStr, "", viewerHdr)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("revoked viewer detail = %d, want 404 (immediate, no cache)", rec.Code)
	}
}

func TestCrossOrgIsolation(t *testing.T) {
	h, st := newTestHandler(t)
	ownerHdr := claimAdmin(t, h, st, "owner")

	// Two organizations under the same server — the manager of org 1
	// learns nothing about org 2 (PRD §5.5: hosting org A cannot
	// enumerate organization B).
	rec := do(t, h, http.MethodPost, "/api/v1/workspaces",
		`{"name":"Org One","business_type":"reseller"}`, ownerHdr)
	org1 := int64(decodeMap(t, rec)["workspace"].(map[string]any)["id"].(float64))
	rec = do(t, h, http.MethodPost, "/api/v1/workspaces",
		`{"name":"Org Two","business_type":"hosting_provider"}`, ownerHdr)
	org2 := int64(decodeMap(t, rec)["workspace"].(map[string]any)["id"].(float64))

	rec = do(t, h, http.MethodPost, fmt.Sprintf("/api/v1/workspaces/%d/invitations", org1),
		`{"email":"m@example.com","role":"manager"}`, ownerHdr)
	token := decodeMap(t, rec)["invitation"].(map[string]any)["token"].(string)
	rec = do(t, h, http.MethodPost, "/api/v1/workspaces/invitations/accept",
		fmt.Sprintf(`{"token":%q,"username":"mgr1","password":"correct horse battery"}`, token), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("accept = %d: %s", rec.Code, rec.Body.String())
	}

	mgrHdr := loginAs(t, h, "mgr1")
	rec = do(t, h, http.MethodGet, fmt.Sprintf("/api/v1/workspaces/%d", org2), "", mgrHdr)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("org2 detail for org1 manager = %d, want 404", rec.Code)
	}
	rec = do(t, h, http.MethodPost, fmt.Sprintf("/api/v1/workspaces/%d/enter", org2), "", mgrHdr)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("org2 enter for org1 manager = %d, want 404", rec.Code)
	}
	// Their own org works.
	rec = do(t, h, http.MethodGet, fmt.Sprintf("/api/v1/workspaces/%d", org1), "", mgrHdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("org1 detail = %d: %s", rec.Code, rec.Body.String())
	}
}
