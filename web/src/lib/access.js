// Pure helpers for the trusted access-mode label. The dashboard and the
// sign-in page must never guess the access mode from the browser URL:
// the daemon reports it from the root-written access configuration and
// the proxy's secret-free status file (GET /api/v1/access/status).

/**
 * accessLabel renders the header/footer status chip text.
 * Unknown/unavailable (null) renders empty so the UI never claims a
 * mode it has not verified.
 */
/** @param {{ mode: 'https' | 'private'; https?: { host?: string } | null } | null | undefined} status - partial view; null/undefined (unknown) renders empty */
export function accessLabel(status) {
	if (!status) return '';
	if (status.mode === 'https') return 'HTTPS secured';
	return 'Private · SSH tunnel';
}

/**
 * accessLabelTitle is the accessible long-form of the chip.
 */
/** @param {{ mode: 'https' | 'private'; https?: { host?: string } | null } | null | undefined} status - partial view; null/undefined (unknown) renders empty */
export function accessLabelTitle(status) {
	if (!status) return 'checking access mode…';
	if (status.mode === 'https') {
		const host = status.https && status.https.host ? status.https.host : '';
		return host ? `Public HTTPS is active for ${host} (publicly trusted certificate)` : 'Public HTTPS is active (publicly trusted certificate)';
	}
	return 'Public HTTPS is not enabled. Access is private over an SSH tunnel.';
}

/**
 * domainHint validates a custom domain client-side (mirrors the
 * backend's ValidHostname; the backend re-checks everything).
 * Returns '' when acceptable, otherwise a short reason.
 */
/** @param {unknown} input */
export function domainHint(input) {
	const d = String(input || '').trim().toLowerCase().replace(/\.$/, '');
	if (d === '') return 'Enter your domain, like vpn.example.com.';
	if (d.length > 253) return 'That domain is too long.';
	if (/^\d{1,3}(\.\d{1,3}){3}$/.test(d) || d.includes(':')) {
		return 'That is an IP address — the server IP is already the default access address.';
	}
	if (d.includes('*')) return 'Wildcard certificates are not supported.';
	if (/\s|\/|\\|@|,|_/.test(d)) return 'Enter a bare hostname like vpn.example.com.';
	const labels = d.split('.');
	if (labels.length < 2) return 'The domain needs at least two parts, like example.com.';
	for (const label of labels) {
		if (label === '') return 'The domain has an empty part — check the dots.';
		if (label.length > 63) return 'One of the domain parts is too long.';
		for (let i = 0; i < label.length; i++) {
			const c = label[i];
			const ok = (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || (c === '-' && i > 0 && i < label.length - 1);
			if (!ok) return 'Only letters, digits and interior hyphens are allowed.';
		}
	}
	return '';
}
