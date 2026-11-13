// Package workspaces holds the P2.0 identity model's authorization
// registry: workspace types, permission keys, role templates and their
// permission sets (PRD §5, §6.1, §6.2 — finalized in
// docs/phase-2/P2.0-identity-workspaces.md).
//
// This package is the SINGLE SOURCE OF TRUTH for shipped defaults. The
// SQLite migration seeds permissions/roles/role_permissions rows from
// it (store.migrations calls SeedSchema), so the database and the Go
// registry can never drift apart for a given build. Runtime checks
// read the database (role_permissions), not this map, so operators can
// inspect the effective policy with plain SQL.
//
// Ground rules implemented here (PRD §5.6):
//
//  1. A Server Administrator can create and manage hosting organizations
//     on their server, but accessing customer-private data requires an
//     appropriate grant or audited support access.
//  2. A Reseller can manage their own hosting customers and assigned
//     websites, but cannot manage the underlying Linux server.
//  3. A Website Owner controls only their assigned websites.
//  4. A Developer controls only their assigned applications or projects.
//  5. A user can hold multiple roles across different workspaces without
//     needing separate accounts.
//  6. The platform must never equate workspace switching with privilege
//     escalation.
//
// Note on `hosting.org.create`: PRD §6.2 does not list it, but PRD §5.6
// rule 1 requires the server administrator to be able to create hosting
// organizations, so it is added as a documented extension of the
// namespace rather than a hardcoded role check.
package workspaces

import (
	"context"
	"database/sql"
	"fmt"
)

// Workspace types (PRD §5.1).
const (
	TypeServer           = "server"
	TypeHostingOrg       = "hosting_org"
	TypeWebsite          = "website"
	TypeDeveloperProject = "developer_project"
)

// hosting_org business types (RFC §2): one workspace type, three
// business models. Decided v1.1 instead of three persona systems.
const (
	BusinessReseller        = "reseller"
	BusinessHostingProvider = "hosting_provider"
	BusinessAgency          = "agency"
)

// ValidBusinessType reports whether s is an allowed business_type value.
func ValidBusinessType(s string) bool {
	switch s {
	case BusinessReseller, BusinessHostingProvider, BusinessAgency:
		return true
	}
	return false
}

// Permission keys. The workspace.* and system.* sets follow PRD §6.2
// verbatim; hosting.org.create is the documented §5.6-rule-1 extension.
const (
	PWorkspaceRead          = "workspace.read"
	PWorkspaceMembersInvite = "workspace.members.invite"
	PWorkspaceMembersManage = "workspace.members.manage"
	PWorkspaceSupportReq    = "workspace.support.request"
	PWorkspaceSupportAllow  = "workspace.support.approve"

	PSystemOverviewRead    = "system.overview.read"
	PSystemMetricsRead     = "system.metrics.read"
	PSystemProcessesRead   = "system.processes.read"
	PSystemLogsRead        = "system.logs.read"
	PSystemServicesRead    = "system.services.read"
	PSystemServicesRestart = "system.services.restart"
	PSystemServicesStart   = "system.services.start"
	PSystemServicesStop    = "system.services.stop"
	PSystemServicesEnable  = "system.services.enable"
	PSystemFilesRead       = "system.files.read"
	PSystemFilesWrite      = "system.files.write"
	PSystemFirewallRead    = "system.firewall.read"
	PSystemFirewallModify  = "system.firewall.modify"
	PSystemSSHRead         = "system.ssh.read"
	PSystemSSHModify       = "system.ssh.modify"
	PSystemUsersRead       = "system.users.read"
	PSystemUsersManage     = "system.users.manage"
	PSystemScheduleRead    = "system.schedule.read"
	PSystemScheduleManage  = "system.schedule.manage"
	PSystemPackagesRead    = "system.packages.read"
	PSystemPackagesUpgrade = "system.packages.upgrade"
	PSystemBackupsRead     = "system.backups.read"
	PSystemBackupsCreate   = "system.backups.create"
	PSystemBackupsRestore  = "system.backups.restore"
	PSystemAuditRead       = "system.audit.read"
	PSystemPolicyManage    = "system.policy.manage"

	PHostingOrgRead       = "hosting.organization.read"
	PHostingOrgCreate     = "hosting.org.create" // documented §5.6 rule 1 extension
	PHostingCustomersMgmt = "hosting.customers.manage"

	PWebsiteRead   = "website.workspace.read"
	PDeveloperRead = "developer.project.read"
)

// Perm describes one registry row (seeded into permissions).
type Perm struct {
	Key         string
	Description string
}

// AllPermissions is the ordered registry (PRD §6.2 namespace plus the
// documented hosting.org.create extension). Keys are never renamed or
// removed once shipped; new entries are appended.
var AllPermissions = []Perm{
	{PWorkspaceRead, "See the workspace and its context"},
	{PWorkspaceMembersInvite, "Invite members to the workspace"},
	{PWorkspaceMembersManage, "Change or revoke workspace memberships"},
	{PWorkspaceSupportReq, "Request audited support access to a customer context"},
	{PWorkspaceSupportAllow, "Approve support access to this workspace"},
	{PSystemOverviewRead, "See the server overview"},
	{PSystemMetricsRead, "Read server metrics snapshots"},
	{PSystemProcessesRead, "See the process list"},
	{PSystemLogsRead, "Read service and system logs"},
	{PSystemServicesRead, "See systemd service state"},
	{PSystemServicesRestart, "Restart systemd services"},
	{PSystemServicesStart, "Start systemd services"},
	{PSystemServicesStop, "Stop systemd services"},
	{PSystemServicesEnable, "Enable or disable systemd services"},
	{PSystemFilesRead, "Read managed file roots"},
	{PSystemFilesWrite, "Edit managed file roots"},
	{PSystemFirewallRead, "Read firewall rules"},
	{PSystemFirewallModify, "Change firewall rules"},
	{PSystemSSHRead, "Read SSH settings"},
	{PSystemSSHModify, "Change SSH settings"},
	{PSystemUsersRead, "Read Linux users and groups"},
	{PSystemUsersManage, "Manage Linux users and groups"},
	{PSystemScheduleRead, "Read scheduled tasks"},
	{PSystemScheduleManage, "Manage scheduled tasks"},
	{PSystemPackagesRead, "Read package inventory and updates"},
	{PSystemPackagesUpgrade, "Run approved package upgrades"},
	{PSystemBackupsRead, "Read backups and manifests"},
	{PSystemBackupsCreate, "Create backups"},
	{PSystemBackupsRestore, "Restore backups"},
	{PSystemAuditRead, "Read the audit log"},
	{PSystemPolicyManage, "Manage server-wide policy (privileged-operation policy)"},
	{PHostingOrgRead, "See the hosting organization and its customers"},
	{PHostingOrgCreate, "Create hosting organizations on this server (PRD §5.6 rule 1)"},
	{PHostingCustomersMgmt, "Manage assigned hosting customers and their resources"},
	{PWebsiteRead, "See the assigned website workspace"},
	{PDeveloperRead, "See the assigned developer project"},
}

// Role names per workspace type (PRD §6.1 default templates).
const (
	RoleServerOwner  = "owner"
	RoleServerAdmin  = "administrator"
	RoleServerOper   = "operator"
	RoleServerViewer = "viewer"

	RoleOrgOwner   = "owner"
	RoleOrgManager = "manager"
	RoleOrgStaff   = "staff"
	RoleOrgViewer  = "viewer"

	RoleSiteOwner  = "owner"
	RoleSiteMgr    = "manager"
	RoleSiteViewer = "viewer"

	RoleDevOwner      = "owner"
	RoleDevMaintainer = "maintainer"
	RoleDevViewer     = "viewer"
)

// Role describes one seeded template row.
type Role struct {
	WorkspaceType string
	Name          string
	Permissions   []string
}

// DefaultRoles are the shipped role templates. They are starting
// defaults, not proof of universal access (PRD §6.1); rows live in
// role_permissions and can be inspected — and later, via OD-004
// custom roles — extended.
//
// Server roles (PRD §6.1): owner controls membership/policy;
// administrator manages allowed host tasks; operator handles
// operational tasks; viewer read-only.
var serverOwnerPerms = []string{
	PWorkspaceRead, PWorkspaceMembersInvite, PWorkspaceMembersManage,
	PWorkspaceSupportReq, PWorkspaceSupportAllow,
	PHostingOrgRead, PHostingOrgCreate,
	// All system capabilities, including high-risk ones and policy.
	PSystemOverviewRead, PSystemMetricsRead, PSystemProcessesRead, PSystemLogsRead,
	PSystemServicesRead, PSystemServicesRestart, PSystemServicesStart,
	PSystemServicesStop, PSystemServicesEnable,
	PSystemFilesRead, PSystemFilesWrite,
	PSystemFirewallRead, PSystemFirewallModify,
	PSystemSSHRead, PSystemSSHModify,
	PSystemUsersRead, PSystemUsersManage,
	PSystemScheduleRead, PSystemScheduleManage,
	PSystemPackagesRead, PSystemPackagesUpgrade,
	PSystemBackupsRead, PSystemBackupsCreate, PSystemBackupsRestore,
	PSystemAuditRead, PSystemPolicyManage,
}

var serverAdminPerms = []string{
	PWorkspaceRead, PHostingOrgRead,
	// Host tasks: everything operational including writes, but not
	// membership control and not server-wide policy.
	PSystemOverviewRead, PSystemMetricsRead, PSystemProcessesRead, PSystemLogsRead,
	PSystemServicesRead, PSystemServicesRestart, PSystemServicesStart,
	PSystemServicesStop, PSystemServicesEnable,
	PSystemFilesRead, PSystemFilesWrite,
	PSystemFirewallRead, PSystemFirewallModify,
	PSystemSSHRead, PSystemSSHModify,
	PSystemUsersRead, PSystemUsersManage,
	PSystemScheduleRead, PSystemScheduleManage,
	PSystemPackagesRead, PSystemPackagesUpgrade,
	PSystemBackupsRead, PSystemBackupsCreate, PSystemBackupsRestore,
	PSystemAuditRead,
}

var serverOperatorPerms = []string{
	PWorkspaceRead,
	// Operational tasks: service actions and backup creation, plus all
	// reads. No file writes, no firewall/SSH/user changes, no upgrades.
	PSystemOverviewRead, PSystemMetricsRead, PSystemProcessesRead, PSystemLogsRead,
	PSystemServicesRead, PSystemServicesRestart, PSystemServicesStart,
	PSystemServicesStop, PSystemServicesEnable,
	PSystemFilesRead, PSystemFirewallRead, PSystemSSHRead,
	PSystemUsersRead, PSystemScheduleRead, PSystemPackagesRead,
	PSystemBackupsRead, PSystemBackupsCreate, PSystemAuditRead,
}

var serverViewerPerms = []string{
	PWorkspaceRead,
	PSystemOverviewRead, PSystemMetricsRead, PSystemProcessesRead, PSystemLogsRead,
	PSystemServicesRead, PSystemFilesRead, PSystemFirewallRead, PSystemSSHRead,
	PSystemUsersRead, PSystemScheduleRead, PSystemPackagesRead,
	PSystemBackupsRead, PSystemAuditRead,
	PHostingOrgRead,
}

// DefaultRoles is the ordered template list. Hosting org roles manage
// org/team/customer relationships only — never the underlying Linux
// server (PRD §5.6 rule 2). Website and developer roles are foundation
// placeholders whose functionality expands in Phase 3/4 (PRD §6.1).
var DefaultRoles = []Role{
	{TypeServer, RoleServerOwner, serverOwnerPerms},
	{TypeServer, RoleServerAdmin, serverAdminPerms},
	{TypeServer, RoleServerOper, serverOperatorPerms},
	{TypeServer, RoleServerViewer, serverViewerPerms},

	{TypeHostingOrg, RoleOrgOwner, []string{
		PWorkspaceRead, PWorkspaceMembersInvite, PWorkspaceMembersManage,
		PHostingOrgRead, PHostingCustomersMgmt,
	}},
	{TypeHostingOrg, RoleOrgManager, []string{
		PWorkspaceRead, PWorkspaceMembersInvite, PHostingOrgRead, PHostingCustomersMgmt,
	}},
	{TypeHostingOrg, RoleOrgStaff, []string{PWorkspaceRead, PHostingOrgRead}},
	{TypeHostingOrg, RoleOrgViewer, []string{PWorkspaceRead, PHostingOrgRead}},

	{TypeWebsite, RoleSiteOwner, []string{
		PWorkspaceRead, PWorkspaceMembersInvite, PWebsiteRead,
	}},
	{TypeWebsite, RoleSiteMgr, []string{PWorkspaceRead, PWebsiteRead}},
	{TypeWebsite, RoleSiteViewer, []string{PWorkspaceRead, PWebsiteRead}},

	{TypeDeveloperProject, RoleDevOwner, []string{
		PWorkspaceRead, PWorkspaceMembersInvite, PDeveloperRead,
	}},
	{TypeDeveloperProject, RoleDevMaintainer, []string{PWorkspaceRead, PDeveloperRead}},
	{TypeDeveloperProject, RoleDevViewer, []string{PWorkspaceRead, PDeveloperRead}},
}

// ValidRole reports whether workspaceType/name is a shipped template.
// Only shipped templates can be attached to memberships in Phase 2
// (custom roles are OD-004).
func ValidRole(workspaceType, name string) bool {
	for _, r := range DefaultRoles {
		if r.WorkspaceType == workspaceType && r.Name == name {
			return true
		}
	}
	return false
}

// OwnerRoleName returns the owner role name for a workspace type.
func OwnerRoleName(workspaceType string) (string, error) {
	switch workspaceType {
	case TypeServer, TypeHostingOrg:
		return RoleServerOwner, nil
	case TypeWebsite:
		return RoleSiteOwner, nil
	case TypeDeveloperProject:
		return RoleDevOwner, nil
	}
	return "", fmt.Errorf("no owner role for workspace type %q", workspaceType)
}

// SeedSchema inserts the registry rows inside the migration transaction:
// permissions, role templates and their permission sets. It is called
// by the store migration runner after the DDL, so schema + defaults land
// atomically.
func SeedSchema(ctx context.Context, tx *sql.Tx) error {
	for _, p := range AllPermissions {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO permissions (key, description) VALUES (?, ?)`,
			p.Key, p.Description); err != nil {
			return fmt.Errorf("seed permission %s: %w", p.Key, err)
		}
	}
	for _, r := range DefaultRoles {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO roles (workspace_type, name) VALUES (?, ?)`,
			r.WorkspaceType, r.Name); err != nil {
			return fmt.Errorf("seed role %s/%s: %w", r.WorkspaceType, r.Name, err)
		}
		var roleID int64
		if err := tx.QueryRowContext(ctx,
			`SELECT id FROM roles WHERE workspace_type = ? AND name = ?`,
			r.WorkspaceType, r.Name).Scan(&roleID); err != nil {
			return fmt.Errorf("load role %s/%s: %w", r.WorkspaceType, r.Name, err)
		}
		for _, key := range r.Permissions {
			if _, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO role_permissions (role_id, permission_key) VALUES (?, ?)`,
				roleID, key); err != nil {
				return fmt.Errorf("seed role_permission %s/%s->%s: %w", r.WorkspaceType, r.Name, key, err)
			}
		}
	}
	return nil
}
