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

	constructor(status: number, code: string, message: string) {
		super(message);
		this.status = status;
		this.code = code;
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
			typeof data['message'] === 'string' ? (data['message'] as string) : `HTTP ${res.status}`
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

export function accountMfaSetup(): Promise<MfaSetup> {
	return postJSON<MfaSetup>('/api/v1/account/mfa/setup', {});
}

export function accountMfaVerify(code: string): Promise<MfaVerified> {
	return postJSON<MfaVerified>('/api/v1/account/mfa/verify', { code });
}
