// Typed client for the frozen Sprint 1 API surface.

export interface CpuMetrics {
        cores: number;
        usage_percent: number;
        load_1m: number;
        load_5m: number;
        load_15m: number;
}

export interface MemoryMetrics {
        total_bytes: number;
        available_bytes: number;
        used_bytes: number;
        used_percent: number;
        buffers_bytes: number;
        cached_bytes: number;
        swap_total_bytes: number;
        swap_used_bytes: number;
}

export interface DiskMetrics {
        path: string;
        total_bytes: number;
        used_bytes: number;
        available_bytes: number;
        used_percent: number;
}

export interface MetricsSnapshot {
        timestamp: string;
        cpu: CpuMetrics;
        memory: MemoryMetrics;
        disk: DiskMetrics[];
        uptime_seconds: number;
}

async function getJSON<T>(url: string): Promise<T> {
        const res = await fetch(url, { headers: { accept: 'application/json' } });
        if (!res.ok) {
                throw new Error(`${url} -> HTTP ${res.status}`);
        }
        return (await res.json()) as T;
}

export function fetchMetrics(): Promise<MetricsSnapshot> {
        return getJSON<MetricsSnapshot>('/api/v1/server/metrics');
}

export async function isUp(url: string): Promise<boolean> {
        try {
                const res = await fetch(url);
                return res.ok;
        } catch {
                return false;
        }
}

// --- Sprint 2: identity & onboarding ---

export type InstanceStatus = 'unclaimed' | 'mfa_pending' | 'active';

export interface SessionInfo {
        authenticated: boolean;
        instance_status: InstanceStatus;
        mfa_enabled: boolean;
        username: string;
        scope: string;
}

export interface OnboardingStatus extends SessionInfo {
        mfa_mode: string;
        claim_token_active: boolean;
}

/** API error with the daemon's machine-readable code. */
export class ApiError extends Error {
        status: number;
        code: string;
        /** Raw parsed body fields — e.g. mfa_challenge on totp_required. */
        data?: Record<string, unknown>;

        constructor(status: number, code: string, message: string, data?: Record<string, unknown>) {
                super(message);
                this.status = status;
                this.code = code;
                this.data = data;
        }
}

export async function fetchSession(): Promise<SessionInfo> {
        const res = await fetch('/api/v1/auth/session', { headers: { accept: 'application/json' } });
        if (!res.ok) throw new ApiError(res.status, 'http_error', `HTTP ${res.status}`);
        return (await res.json()) as SessionInfo;
}

export async function fetchOnboardingStatus(): Promise<OnboardingStatus> {
        const res = await fetch('/api/v1/onboarding/status', { headers: { accept: 'application/json' } });
        if (!res.ok) throw new ApiError(res.status, 'http_error', `HTTP ${res.status}`);
        return (await res.json()) as OnboardingStatus;
}

/** POST JSON and parse the reply; non-2xx raises ApiError with the daemon's code. */
export async function postJSON<T>(url: string, body: unknown): Promise<T> {
        const res = await fetch(url, {
                method: 'POST',
                headers: { 'content-type': 'application/json', accept: 'application/json' },
                body: JSON.stringify(body ?? {})
        });
        const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
        if (!res.ok) {
                throw new ApiError(
                        res.status,
                        typeof data['error'] === 'string' ? (data['error'] as string) : 'unknown_error',
                        typeof data['message'] === 'string' ? (data['message'] as string) : `HTTP ${res.status}`,
                        data
                );
        }
        return data as T;
}

export interface LoginResult {
        user: { username: string; mfa_enabled: boolean };
        instance_status: InstanceStatus;
        scope: string;
}

export interface MfaSetup {
        secret: string;
        otpauth_uri: string;
}

export interface MfaVerified {
        recovery_codes: string[];
        next: string;
}

export async function logout(): Promise<void> {
        await postJSON('/api/v1/auth/logout', {}).catch(() => undefined);
}

// --- Post-install access (trusted status + custom domain) ---

/** Trusted access mode as reported by the daemon (never guessed from the URL). */
export interface DomainStatusInfo {
        host: string;
        phase: string;
        cert_valid: boolean;
        not_after?: string;
        last_error?: string;
}

export interface HttpsStatusInfo {
        enabled: boolean;
        host?: string;
        phase?: string;
        cert_valid: boolean;
        not_after?: string;
        staging?: boolean;
        domain?: DomainStatusInfo | null;
}

export interface AccessStatus {
        mode: 'https' | 'private';
        https?: HttpsStatusInfo | null;
}

export async function fetchAccessStatus(): Promise<AccessStatus> {
        const res = await fetch('/api/v1/access/status', { headers: { accept: 'application/json' } });
        if (!res.ok) throw new ApiError(res.status, 'http_error', `HTTP ${res.status}`);
        return (await res.json()) as AccessStatus;
}

export interface DomainCheckResult {
        domain: string;
        resolved?: string[];
        expected?: string[];
        matches: boolean;
        message: string;
}

export function checkDomain(domain: string): Promise<DomainCheckResult> {
        return postJSON<DomainCheckResult>('/api/v1/access/domain/check', { domain });
}

export interface DomainApplyResult {
        ok: boolean;
        domain: string;
        not_after?: string;
        url?: string;
}

export function applyDomain(domain: string): Promise<DomainApplyResult> {
        return postJSON<DomainApplyResult>('/api/v1/access/domain/apply', { domain });
}

export function removeDomain(): Promise<{ ok: boolean }> {
        return postJSON<{ ok: boolean }>('/api/v1/access/domain/remove', { confirm: true });
}

// --- Optional MFA ("Skip for now") + Account Security ---

export interface SkipResult {
        user: { username: string };
        instance_status: InstanceStatus;
        mfa_mode: string;
        next: string;
}

export function skipMfa(): Promise<SkipResult> {
        return postJSON<SkipResult>('/api/v1/onboarding/mfa/skip', { acknowledge_risk: true });
}

export interface AccountMfaStatus {
        mfa_enabled: boolean;
        mfa_mode: string;
        recovery_codes_unused: number;
}

export function fetchAccountMfa(): Promise<AccountMfaStatus> {
        return getJSON<AccountMfaStatus>('/api/v1/account/mfa');
}

// --- Account profile (email + password) ---

export interface AccountProfile {
        username: string;
        email: string;
}

export function fetchAccountProfile(): Promise<AccountProfile> {
        return getJSON<AccountProfile>('/api/v1/account/profile');
}

export function setAccountEmail(email: string): Promise<{ ok: boolean; email: string }> {
        return postJSON<{ ok: boolean; email: string }>('/api/v1/account/email', { email });
}

export interface PasswordChangeResult {
        ok: boolean;
        other_sessions_ended: number;
}

export function changeAccountPassword(
        currentPassword: string,
        newPassword: string
): Promise<PasswordChangeResult> {
        return postJSON<PasswordChangeResult>('/api/v1/account/password', {
                current_password: currentPassword,
                new_password: newPassword
        });
}

// --- Recovery paths (v0.4.17) ---

export interface RecoveryLoginResult {
        user: { username: string; mfa_enabled: boolean };
        instance_status: InstanceStatus;
        scope: string;
        next: string;
}

export function recoveryLogin(identifier: string, code: string): Promise<RecoveryLoginResult> {
        return postJSON<RecoveryLoginResult>('/api/v1/auth/recovery-login', {
                username: identifier,
                code
        });
}

export interface ForgotResult {
        ok: boolean;
        message: string;
}

export function forgotPassword(identifier: string): Promise<ForgotResult> {
        return postJSON<ForgotResult>('/api/v1/auth/forgot', { username: identifier });
}

export function resetPassword(token: string, newPassword: string): Promise<{ ok: boolean }> {
        return postJSON<{ ok: boolean }>('/api/v1/auth/reset', { token, new_password: newPassword });
}

// --- Mail delivery (SMTP) settings ---

export interface SmtpStatus {
        enabled: boolean;
        host: string;
        port: number;
        username: string;
        from_addr: string;
        password_set: boolean;
}

export function fetchSmtp(): Promise<SmtpStatus> {
        return getJSON<SmtpStatus>('/api/v1/account/smtp');
}

export interface SmtpSaveInput {
        enabled: boolean;
        host: string;
        port: number;
        username: string;
        password?: string; // empty = keep the stored one
        from_addr: string;
}

export function saveSmtp(cfg: SmtpSaveInput): Promise<{ ok: boolean }> {
        return postJSON<{ ok: boolean }>('/api/v1/account/smtp', cfg);
}

export function testSmtp(to?: string): Promise<{ ok: boolean; error?: string }> {
        return postJSON<{ ok: boolean; error?: string }>('/api/v1/account/smtp/test', { to: to ?? '' });
}

export function signOutEverywhere(): Promise<{ ok: boolean; ended: number }> {
        return postJSON<{ ok: boolean; ended: number }>('/api/v1/account/sessions/out', {});
}

export function accountMfaSetup(): Promise<MfaSetup> {
        return postJSON<MfaSetup>('/api/v1/account/mfa/setup', {});
}

export function accountMfaVerify(code: string): Promise<MfaVerified> {
        return postJSON<MfaVerified>('/api/v1/account/mfa/verify', { code });
}

// --- P2.0 workspaces (identity foundation) ---

export type WorkspaceType = 'server' | 'hosting_org' | 'website' | 'developer_project';

export interface WorkspaceView {
	id: number;
	type: WorkspaceType;
	name: string;
	business_type?: string;
	role: string;
	state: string;
	permissions: string[];
}

export interface WorkspacesList {
	active_workspace_id: number;
	workspaces: WorkspaceView[];
}

export interface WorkspaceMember {
	user_id: number;
	username: string;
	role: string;
	joined_at: string;
}

export interface WorkspaceInvitation {
	id: number;
	email: string;
	role: string;
	expires_at: string;
}

export interface WorkspaceDetail {
	workspace: {
		id: number;
		type: WorkspaceType;
		name: string;
		business_type?: string;
		state: string;
	};
	my_role: string;
	permissions: string[];
	members: WorkspaceMember[];
	invitations?: WorkspaceInvitation[];
}

export interface InvitationCreated {
	invitation: {
		id: number;
		email: string;
		role: string;
		expires_at: string;
		url: string;
		token: string;
	};
}

export interface InvitationPreview {
	workspace_name: string;
	workspace_type: WorkspaceType;
	business_type?: string;
	role: string;
	email: string;
	expires_at: string;
}

export interface AcceptResult {
	ok: boolean;
	workspace_id: number;
	role: string;
	account_created: boolean;
	next: string;
}

/** The caller's memberships; null when anonymous (selector stays hidden). */
export async function fetchWorkspaces(): Promise<WorkspacesList | null> {
	const res = await fetch('/api/v1/workspaces', { headers: { accept: 'application/json' } });
	if (res.status === 401 || res.status === 403) return null;
	if (!res.ok) throw new ApiError(res.status, 'http_error', `HTTP ${res.status}`);
	return (await res.json()) as WorkspacesList;
}

/**
 * Switch the session's advisory workspace context. Re-authorized
 * server-side on every call — switching never grants privileges.
 */
export function enterWorkspace(id: number): Promise<{ ok: boolean; active_workspace_id: number }> {
	return postJSON(`/api/v1/workspaces/${id}/enter`, {});
}

export function createHostingOrg(
	name: string,
	businessType: string
): Promise<{ workspace: { id: number; name: string; business_type: string; role: string } }> {
	return postJSON('/api/v1/workspaces', { name, business_type: businessType });
}

export function fetchWorkspaceDetail(id: number): Promise<WorkspaceDetail> {
	return getJSON<WorkspaceDetail>(`/api/v1/workspaces/${id}`);
}

export function inviteMember(
	id: number,
	email: string,
	role: string
): Promise<InvitationCreated> {
	return postJSON(`/api/v1/workspaces/${id}/invitations`, { email, role });
}

export async function deleteJSON<T>(url: string): Promise<T> {
	const res = await fetch(url, {
		method: 'DELETE',
		headers: { accept: 'application/json' }
	});
	const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
	if (!res.ok) {
		throw new ApiError(
			res.status,
			typeof data['error'] === 'string' ? (data['error'] as string) : 'unknown_error',
			typeof data['message'] === 'string' ? (data['message'] as string) : `HTTP ${res.status}`,
			data
		);
	}
	return data as T;
}

export function revokeInvitation(id: number, invitationId: number): Promise<{ ok: boolean }> {
	return deleteJSON(`/api/v1/workspaces/${id}/invitations/${invitationId}`);
}

export function removeMember(id: number, userId: number): Promise<{ ok: boolean }> {
	return deleteJSON(`/api/v1/workspaces/${id}/members/${userId}`);
}

export function fetchInvitationPreview(token: string): Promise<InvitationPreview> {
	return getJSON<InvitationPreview>(`/api/v1/workspaces/invitations/${encodeURIComponent(token)}`);
}

export function acceptInvitation(
	token: string,
	username?: string,
	password?: string
): Promise<AcceptResult> {
	return postJSON('/api/v1/workspaces/invitations/accept', {
		token,
		...(username !== undefined ? { username } : {}),
		...(password !== undefined ? { password } : {})
	});
}

/** Human label for a workspace type/persona (PRD §4). */
export function personaLabel(type: WorkspaceType): string {
	switch (type) {
		case 'server':
			return 'Server';
		case 'hosting_org':
			return 'Hosting organization';
		case 'website':
			return 'Website';
		case 'developer_project':
			return 'Developer project';
	}
}

/** Shipped role templates per workspace type (PRD §6.1). */
export const ROLES_BY_TYPE: Record<WorkspaceType, string[]> = {
	server: ['owner', 'administrator', 'operator', 'viewer'],
	hosting_org: ['owner', 'manager', 'staff', 'viewer'],
	website: ['owner', 'manager', 'viewer'],
	developer_project: ['owner', 'maintainer', 'viewer']
};

// --- P2.1 observability (read-only; RFC phase-2/P2.1-observability-rfc.md) ---

export interface HistoryPoint {
	t: string;
	cpu_avg: number;
	cpu_max: number;
	mem_avg_bytes: number;
	mem_max_bytes: number;
	disk_worst: number;
	resolution: 'raw' | '5m';
}

export interface HealthAlert {
	id: number;
	kind: string;
	subject: string;
	severity: 'warn' | 'crit';
	message: string;
	value: number;
	threshold: number;
	state: 'open' | 'resolved';
	first_seen_at: string;
	last_seen_at: string;
	resolved_at: string;
}

export interface SystemdSummary {
	total: number;
	running: number;
	failed: number;
}

export interface OverviewResponse {
	snapshot: MetricsSnapshot;
	history: HistoryPoint[] | null;
	alerts: HealthAlert[] | null;
	systemd: SystemdSummary | null;
	worst_disk: number;
	worst_path: string;
}

export interface ProcessRow {
	pid: number;
	ppid: number;
	user: string;
	state: string;
	rss_bytes: number;
	cpu_percent: number;
	cmdline: string;
}

export interface ProcessesResponse {
	measured_over_ms: number;
	processes: ProcessRow[];
}

export interface ServiceUnit {
	unit: string;
	load: string;
	active: string;
	sub: string;
	description: string;
	enabled: string;
}

export interface ServicesResponse {
	summary: SystemdSummary;
	units: ServiceUnit[];
}

export interface PreflightCheck {
	name: string;
	ok: boolean;
	value: number;
	threshold: number;
	message: string;
}

export interface PreflightResponse {
	kind: string;
	ok: boolean;
	checks: PreflightCheck[];
}

/** The console's one-load overview call (live snapshot + 24 h + alerts). */
export function fetchOverview(): Promise<OverviewResponse> {
	return getJSON<OverviewResponse>('/api/v1/server/overview');
}

export function fetchHistory(window: '1h' | '24h' | '7d' | '14d' = '24h'): Promise<{ window: string; points: HistoryPoint[] }> {
	return getJSON(`/api/v1/server/metrics/history?window=${encodeURIComponent(window)}`);
}

export function fetchProcesses(): Promise<ProcessesResponse> {
	return getJSON<ProcessesResponse>('/api/v1/server/processes');
}

export function fetchServices(): Promise<ServicesResponse> {
	return getJSON<ServicesResponse>('/api/v1/server/services');
}

export function fetchAlerts(): Promise<{ alerts: HealthAlert[] }> {
	return getJSON('/api/v1/server/alerts');
}

export function fetchPreflight(kind: string): Promise<PreflightResponse> {
	return getJSON(`/api/v1/server/preflight?kind=${encodeURIComponent(kind)}`);
}
