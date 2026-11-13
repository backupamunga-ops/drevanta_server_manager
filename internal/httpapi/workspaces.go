// P2.0 workspace endpoints (PRD §5, §20; RFC
// docs/phase-2/P2.0-identity-workspaces.md).
//
// Authorization rules enforced here:
//   - Membership is checked per request against the database; the
//     session's active-workspace is advisory UI state only (RFC §7).
//   - A workspace the caller does not belong to is answered with 404,
//     indistinguishable from a nonexistent one — no existence leak
//     across tenant boundaries (PRD §5.5).
//   - Switching workspaces never grants permissions: the switch only
//     changes the advisory context and is audited; every downstream
//     capability still comes from that workspace's role.
package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/auth"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/store"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/workspaces"
)

// invitationTTL is how long an invitation link stays valid (PRD §5.4:
// optional expiry on delegation — 7 days is generous but bounded).
const invitationTTL = 7 * 24 * time.Hour

// ---------------------------------------------------------------------------
// Authorization middleware
// ---------------------------------------------------------------------------

// workspaceIDFromPath parses the {id} path segment.
func workspaceIDFromPath(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// requireWorkspacePermission gates a workspace-scoped endpoint on the
// caller holding perm in the {id} workspace right now. Non-membership
// and non-existence both yield 404; membership without the permission
// yields 403.
func (s *server) requireWorkspacePermission(perm string, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess := sessionFrom(r.Context())
		id, ok := workspaceIDFromPath(r)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "no such workspace")
			return
		}
		allowed, err := s.st.Can(r.Context(), sess.rec.UserID, id, perm)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "authorization check failed")
			return
		}
		if !allowed {
			// Member but lacking the permission vs. not a member at all:
			// distinguish for the member's benefit — outsiders always see
			// 404, so the 403 itself never leaks workspace existence.
			if _, merr := s.st.MembershipFor(r.Context(), sess.rec.UserID, id); errors.Is(merr, store.ErrNotFound) || merr != nil {
				writeError(w, http.StatusNotFound, "not_found", "no such workspace")
			} else {
				writeError(w, http.StatusForbidden, "forbidden", "your role does not permit this action")
			}
			return
		}
		next(w, r)
	})
}

// requireServerContext gates server-scoped Phase 1 endpoints (metrics,
// access/domain) on the session's active context being the server
// workspace. This is what makes a workspace switch REAL: in a hosting
// organization context the server surface answers 403 and the shell
// shows the organization view instead. Advisory active_workspace_id ==
// 0 means the default (server) context, so every pre-0.5.0 client and
// every fresh session keeps working unchanged.
func (s *server) requireServerContext(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess := sessionFrom(r.Context())
		if sess.rec.ActiveWorkspaceID == 0 {
			next(w, r)
			return
		}
		serverWS, err := s.st.ServerWorkspace(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "workspace resolution failed")
			return
		}
		if sess.rec.ActiveWorkspaceID == serverWS.ID {
			next(w, r)
			return
		}
		writeError(w, http.StatusForbidden, "workspace_mismatch",
			"the server surface is not available in the current workspace context")
	})
}

// ---------------------------------------------------------------------------
// List + enter
// ---------------------------------------------------------------------------

type workspaceView struct {
	ID           int64    `json:"id"`
	Type         string   `json:"type"`
	Name         string   `json:"name"`
	BusinessType string   `json:"business_type,omitempty"`
	Role         string   `json:"role"`
	State        string   `json:"state"`
	Permissions  []string `json:"permissions"`
}

// buildWorkspaceList renders the caller's live memberships with their
// effective permissions (PRD §5.3: the selector shows workspace name,
// context, role and status; grouping by type happens in the UI).
func (s *server) buildWorkspaceList(ctx context.Context, userID int64, activeID int64) (map[string]any, error) {
	memberships, err := s.st.MembershipsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]workspaceView, 0, len(memberships))
	for _, m := range memberships {
		perms, err := s.st.PermissionsForUser(ctx, userID, m.WorkspaceID)
		if err != nil {
			return nil, err
		}
		if perms == nil {
			perms = []string{}
		}
		out = append(out, workspaceView{
			ID:           m.WorkspaceID,
			Type:         m.WorkspaceType,
			Name:         m.WorkspaceName,
			BusinessType: m.BusinessType,
			Role:         m.RoleName,
			State:        m.WorkspaceState,
			Permissions:  perms,
		})
	}
	return map[string]any{
		"active_workspace_id": activeID,
		"workspaces":          out,
	}, nil
}

func (s *server) handleWorkspacesList(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	activeID := sess.rec.ActiveWorkspaceID
	if activeID == 0 {
		serverWS, err := s.st.ServerWorkspace(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "workspace resolution failed")
			return
		}
		activeID = serverWS.ID
	}
	resp, err := s.buildWorkspaceList(r.Context(), sess.rec.UserID, activeID)
	if err != nil {
		slog.Error("workspace list failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "could not list workspaces")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleWorkspaceEnter(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	id, ok := workspaceIDFromPath(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such workspace")
		return
	}
	// PRD §6.3: fresh authorization on every entry — the workspace ID in
	// the URL is never trusted as proof of access.
	allowed, err := s.st.Can(r.Context(), sess.rec.UserID, id, workspaces.PWorkspaceRead)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "authorization check failed")
		return
	}
	if !allowed {
		if _, merr := s.st.MembershipFor(r.Context(), sess.rec.UserID, id); errors.Is(merr, store.ErrNotFound) || merr != nil {
			writeError(w, http.StatusNotFound, "not_found", "no such workspace")
			return
		}
		writeError(w, http.StatusForbidden, "forbidden", "your role does not permit this action")
		return
	}
	previous := sess.rec.ActiveWorkspaceID
	if err := s.st.SetSessionActiveWorkspace(r.Context(), auth.TokenHash(sess.raw), id); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not switch workspace")
		return
	}
	detail := map[string]any{"to": id}
	if previous != 0 && previous != id {
		detail["from"] = previous
	}
	audit(r.Context(), s.st, sess.username, "workspace_switched", "workspace", detail)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "active_workspace_id": id})
}

// ---------------------------------------------------------------------------
// Create hosting organization (PRD §5.6 rule 1)
// ---------------------------------------------------------------------------

type createOrgRequest struct {
	Name         string `json:"name"`
	BusinessType string `json:"business_type"`
}

func (s *server) handleWorkspaceCreate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var req createOrgRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if len(req.Name) < 3 || len(req.Name) > 64 {
		writeError(w, http.StatusBadRequest, "invalid_input", "name must be 3-64 characters")
		return
	}
	if !workspaces.ValidBusinessType(req.BusinessType) {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"business_type must be one of: reseller, hosting_provider, agency")
		return
	}
	// The capability lives on the SERVER workspace (PRD §5.6 rule 1),
	// never on the caller's current context: switching into an org does
	// not let an org member create more orgs on the host.
	serverWS, err := s.st.ServerWorkspace(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "workspace resolution failed")
		return
	}
	allowed, err := s.st.Can(r.Context(), sess.rec.UserID, serverWS.ID, workspaces.PHostingOrgCreate)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "authorization check failed")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "forbidden",
			"only server administrators can create hosting organizations")
		return
	}
	ws, err := s.st.CreateHostingOrg(r.Context(), req.Name, req.BusinessType, sess.rec.UserID)
	if err != nil {
		slog.Error("hosting org creation failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "could not create the hosting organization")
		return
	}
	audit(r.Context(), s.st, sess.username, "hosting_org_created", "workspace",
		map[string]any{"workspace_id": ws.ID, "name": ws.Name, "business_type": ws.BusinessType})
	writeJSON(w, http.StatusCreated, map[string]any{
		"workspace": map[string]any{
			"id":            ws.ID,
			"type":          ws.Type,
			"name":          ws.Name,
			"business_type": ws.BusinessType,
			"role":          workspaces.RoleOrgOwner,
		},
	})
}

// ---------------------------------------------------------------------------
// Workspace detail: members (+ invitations for managers)
// ---------------------------------------------------------------------------

func (s *server) handleWorkspaceDetail(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	id, ok := workspaceIDFromPath(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such workspace")
		return
	}
	m, err := s.st.MembershipFor(r.Context(), sess.rec.UserID, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "no such workspace")
		return
	}
	if err != nil {
		slog.Error("membership lookup failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "workspace lookup failed")
		return
	}
	members, err := s.st.MembersOf(r.Context(), id)
	if err != nil {
		slog.Error("member listing failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "could not list members")
		return
	}
	memberViews := make([]map[string]any, 0, len(members))
	for _, mem := range members {
		memberViews = append(memberViews, map[string]any{
			"user_id":   mem.UserID,
			"username":  mem.Username,
			"role":      mem.RoleName,
			"joined_at": mem.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	perms, err := s.st.PermissionsForUser(r.Context(), sess.rec.UserID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "permission resolution failed")
		return
	}
	if perms == nil {
		perms = []string{}
	}
	resp := map[string]any{
		"workspace": map[string]any{
			"id":            m.WorkspaceID,
			"type":          m.WorkspaceType,
			"name":          m.WorkspaceName,
			"business_type": m.BusinessType,
			"state":         m.WorkspaceState,
		},
		"my_role":     m.RoleName,
		"permissions": perms,
		"members":     memberViews,
	}
	// Pending invitations are only visible to members who may manage them.
	if canManage, _ := s.st.Can(r.Context(), sess.rec.UserID, id, workspaces.PWorkspaceMembersManage); canManage {
		invites, err := s.st.ActiveInvitations(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "could not list invitations")
			return
		}
		inviteViews := make([]map[string]any, 0, len(invites))
		for _, inv := range invites {
			inviteViews = append(inviteViews, map[string]any{
				"id":         inv.ID,
				"email":      inv.Email,
				"role":       inv.RoleName,
				"expires_at": inv.ExpiresAt.UTC().Format(time.RFC3339),
			})
		}
		resp["invitations"] = inviteViews
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// Invitations (PRD §5.4: normal delegation)
// ---------------------------------------------------------------------------

type inviteRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (s *server) handleWorkspaceInvite(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	id, ok := workspaceIDFromPath(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such workspace")
		return
	}
	var req inviteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	ws, err := s.st.WorkspaceByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "no such workspace")
		return
	}
	if req.Role == "" || !workspaces.ValidRole(ws.Type, req.Role) {
		writeError(w, http.StatusBadRequest, "invalid_input", "unknown role for this workspace type")
		return
	}
	// Owner invitations may only be issued by an owner (role elevation
	// guard); everything else needs workspace.members.invite.
	if req.Role == workspaces.RoleOrgOwner {
		isOwner, err := s.st.Can(r.Context(), sess.rec.UserID, id, workspaces.PWorkspaceMembersManage)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "authorization check failed")
			return
		}
		m, err := s.st.MembershipFor(r.Context(), sess.rec.UserID, id)
		if err != nil || m.RoleName != workspaces.RoleOrgOwner || !isOwner {
			writeError(w, http.StatusForbidden, "forbidden", "only owners can invite other owners")
			return
		}
	}
	roleID, err := s.st.RoleIDFor(r.Context(), ws.Type, req.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "role resolution failed")
		return
	}
	token, hash, err := auth.NewToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not generate invitation")
		return
	}
	inv, err := s.st.CreateInvitation(r.Context(), id, roleID, req.Email, sess.rec.UserID, hash, invitationTTL)
	if err != nil {
		slog.Error("invitation creation failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "could not create invitation")
		return
	}
	audit(r.Context(), s.st, sess.username, "workspace_invitation_created", "workspace",
		map[string]any{"workspace_id": id, "invitation_id": inv.ID, "role": req.Role})
	writeJSON(w, http.StatusCreated, map[string]any{
		"invitation": map[string]any{
			"id":         inv.ID,
			"email":      inv.Email,
			"role":       inv.RoleName,
			"expires_at": inv.ExpiresAt.UTC().Format(time.RFC3339),
			// Shown once to the inviting operator; delivery is manual in
			// Phase 2 (the link contains the one-time token).
			"url":   "/invite?token=" + token,
			"token": token,
		},
	})
}

func (s *server) handleWorkspaceInvitationRevoke(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	id, ok := workspaceIDFromPath(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such workspace")
		return
	}
	invID, err := strconv.ParseInt(r.PathValue("invID"), 10, 64)
	if err != nil || invID <= 0 {
		writeError(w, http.StatusNotFound, "not_found", "no such invitation")
		return
	}
	if err := s.st.RevokeInvitation(r.Context(), id, invID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no such invitation")
			return
		}
		slog.Error("invitation revocation failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "could not revoke invitation")
		return
	}
	audit(r.Context(), s.st, sess.username, "workspace_invitation_revoked", "workspace",
		map[string]any{"workspace_id": id, "invitation_id": invID})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) handleWorkspaceMemberRemove(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	id, ok := workspaceIDFromPath(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such workspace")
		return
	}
	target, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil || target <= 0 {
		writeError(w, http.StatusNotFound, "not_found", "no such member")
		return
	}
	if err := s.st.SetMembershipState(r.Context(), id, target, store.MembershipRevoked); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusNotFound, "not_found", "no such member")
		case errors.Is(err, store.ErrLastOwner):
			writeError(w, http.StatusConflict, "last_owner",
				"the last active owner cannot be removed")
		default:
			slog.Error("membership revocation failed", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "could not remove member")
		}
		return
	}
	audit(r.Context(), s.st, sess.username, "workspace_membership_revoked", "workspace",
		map[string]any{"workspace_id": id, "target_user_id": target})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------------------------------------------------------------------
// Invitation preview + acceptance (token = the secret; 256-bit, hashed
// at rest). Acceptance works for an existing session (one login, many
// memberships — RFC §4) or creates the account when the invitee has none.
// ---------------------------------------------------------------------------

func (s *server) handleInvitationPreview(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.Allow(clientKey(r, "invite-preview")) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts, slow down")
		return
	}
	token := r.PathValue("token")
	if token == "" {
		writeError(w, http.StatusNotFound, "not_found", "no such invitation")
		return
	}
	inv, err := s.st.InvitationByHash(r.Context(), auth.TokenHash(token))
	valid := err == nil && inv.ExpiresAt.After(time.Now().UTC()) &&
		inv.ConsumedAt.IsZero() && inv.RevokedAt.IsZero()
	if !valid {
		writeError(w, http.StatusNotFound, "not_found", "this invitation is not valid")
		return
	}
	ws, err := s.st.WorkspaceByID(r.Context(), inv.WorkspaceID)
	if err != nil || ws.State != store.WorkspaceActive {
		writeError(w, http.StatusNotFound, "not_found", "this invitation is not valid")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"workspace_name": ws.Name,
		"workspace_type": ws.Type,
		"business_type":  ws.BusinessType,
		"role":           inv.RoleName,
		"email":          inv.Email,
		"expires_at":     inv.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

type acceptInvitationRequest struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email"`
}

func (s *server) handleInvitationAccept(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.Allow(clientKey(r, "invite-accept")) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts, slow down")
		return
	}
	var req acceptInvitationRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Token == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "token required")
		return
	}
	tokenHash := auth.TokenHash(req.Token)
	inv, err := s.st.InvitationByHash(r.Context(), tokenHash)
	invalid := func(reason string) {
		audit(r.Context(), s.st, "invite:"+req.Username, "invitation_rejected", "workspace",
			map[string]any{"reason": reason})
		writeError(w, http.StatusNotFound, "not_found", "this invitation is not valid")
	}
	if err != nil || inv.ExpiresAt.Before(time.Now().UTC()) ||
		!inv.ConsumedAt.IsZero() || !inv.RevokedAt.IsZero() {
		invalid("invalid_token")
		return
	}

	// Existing session: attach the membership (one login, many
	// memberships). No new credentials touch the request.
	if sess := sessionFrom(r.Context()); sess != nil {
		m, err := s.st.AcceptInvitation(r.Context(), tokenHash, sess.rec.UserID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				invalid("invalid_token")
				return
			}
			slog.Error("invitation acceptance failed", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "could not accept invitation")
			return
		}
		audit(r.Context(), s.st, sess.username, "workspace_invitation_accepted", "workspace",
			map[string]any{"workspace_id": m.WorkspaceID, "role": m.RoleName})
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":              true,
			"workspace_id":    m.WorkspaceID,
			"role":            m.RoleName,
			"account_created": false,
			"next":            "/org/" + strconv.FormatInt(m.WorkspaceID, 10),
		})
		return
	}

	// No session: create the account with the invitee's chosen
	// credentials, then attach the membership. Validation mirrors the
	// claim flow (username 3-32 [a-zA-Z0-9._-], password >= 12 chars).
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.TrimSpace(req.Email)
	if !validUsername(req.Username) || !validPassword(req.Password) {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"username 3-32 chars [a-zA-Z0-9._-] and a password of at least 12 characters are required")
		return
	}
	// Bounded argon2id concurrency, exactly like login/claim.
	select {
	case s.argonSlots <- struct{}{}:
		defer func() { <-s.argonSlots }()
	default:
		writeError(w, http.StatusServiceUnavailable, "busy", "too many concurrent attempts")
		return
	}
	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not hash password")
		return
	}
	user, err := s.st.CreateUser(r.Context(), req.Username, passwordHash, req.Email)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeError(w, http.StatusConflict, "username_taken", "username already exists — sign in first, then open the invitation link again")
			return
		}
		slog.Error("invitee account creation failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "could not create account")
		return
	}
	m, err := s.st.AcceptInvitation(r.Context(), tokenHash, user.ID)
	if err != nil {
		// Compensate: never leave a credential-only account behind.
		_ = s.st.DeleteUser(r.Context(), user.ID)
		if errors.Is(err, store.ErrNotFound) {
			invalid("invalid_token")
			return
		}
		slog.Error("invitation acceptance failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "could not accept invitation")
		return
	}
	audit(r.Context(), s.st, req.Username, "workspace_invitation_accepted", "workspace",
		map[string]any{"workspace_id": m.WorkspaceID, "role": m.RoleName, "account_created": true})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"workspace_id":    m.WorkspaceID,
		"role":            m.RoleName,
		"account_created": true,
		"next":            "/org/" + strconv.FormatInt(m.WorkspaceID, 10),
	})
}
