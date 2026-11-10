// One-time setup link helpers (locked first-time-install UX).
//
// The installer hands out `http://127.0.0.1:18080/onboarding#token=drv1_…`.
// URL fragments never leave the browser — the daemon never sees the token
// in any request — but the fragment lingers in the address bar and the
// current history entry unless it is actively erased. These helpers are
// pure so they can be unit-tested without a browser.

/**
 * Extract the setup token from a URL fragment string.
 * Accepts exactly `#token=<drv1_-alphabet value>`; anything else (empty,
 * malformed, other keys, foreign characters, non-strings) yields ''.
 *
 * @param {unknown} hash - value of location.hash, e.g. '#token=drv1_x'
 * @returns {string} the token, or '' when the fragment is not a valid setup link
 */
export function tokenFromHash(hash) {
        if (typeof hash !== 'string') return '';
        const m = /^#token=([A-Za-z0-9_-]+)$/.exec(hash);
        return m ? m[1] : '';
}

/**
 * Extract the raw '#fragment' from a full URL string (e.g. a Performance
 * Navigation entry name). Yields '' for empty or malformed input.
 *
 * @param {unknown} url - absolute URL string
 * @returns {string} e.g. '#token=drv1_…' or ''
 */
export function hashFromUrl(url) {
        if (typeof url !== 'string' || url === '') return '';
        try {
                return new URL(url).hash;
        } catch {
                return '';
        }
}

/**
 * The URL without its fragment (same path + query). Used with
 * history.replaceState so the token disappears from the address bar AND
 * the current history entry without adding a new one. Malformed input
 * yields ''.
 *
 * @param {{ pathname?: unknown, search?: unknown } | null | undefined} loc
 * @returns {string} e.g. '/onboarding' or '/onboarding?x=1'
 */
export function urlWithoutHash(loc) {
        if (!loc || typeof loc.pathname !== 'string') return '';
        return loc.pathname + (typeof loc.search === 'string' ? loc.search : '');
}

/**
 * Shape check for a claim token: `drv1_` followed by base64url
 * characters. Deliberately loose about length — the daemon decides
 * validity and this only catches obvious garbage so the UI can warn
 * before a round trip. No token is ever verified here.
 *
 * @param {unknown} value
 * @returns {boolean}
 */
export function isValidTokenShape(value) {
        return typeof value === 'string' && /^drv1_[A-Za-z0-9_-]{16,}$/.test(value);
}

/**
 * Extract a claim token from free-form pasted input: either the raw
 * token or a complete setup link (`…#token=…` or `…?token=…`, as the
 * installer prints it). Whitespace a mail client or terminal wrapped
 * into the value is stripped; everything from the next parameter or
 * quote delimiter onwards is cut. Lenient by design — the backend
 * remains the authority on whether the value is usable.
 *
 * @param {unknown} raw - pasted text
 * @returns {string}
 */
export function tokenFromPastedInput(raw) {
        let v = typeof raw === 'string' ? raw.trim() : '';
        if (v === '') return '';
        const idx = v.indexOf('token=');
        if (idx !== -1) {
                // Complete setup link: take everything after the first
                // `token=` occurrence (tokens themselves can never contain
                // the '=' sign), rejoin wrapped lines, cut at delimiters.
                v = v.slice(idx + 'token='.length).replace(/\s+/g, '');
                v = v.split(/[&#"']/)[0];
        } else {
                v = v.replace(/\s+/g, '');
        }
        return v;
}

/**
 * The raw value of the `token` parameter in a fragment, however it
 * parses. Used to distinguish "no token in the URL" from "a malformed
 * token in the URL" so the UI can show a helpful error instead of
 * silently falling back to manual entry. '' when the fragment carries
 * no token parameter at all.
 *
 * @param {unknown} hash - value of location.hash
 * @returns {string}
 */
export function rawTokenParamFromHash(hash) {
        if (typeof hash !== 'string' || hash === '') return '';
        const h = hash.startsWith('#') ? hash.slice(1) : '';
        if (h === '') return '';
        const params = new URLSearchParams(h);
        return params.get('token') ?? '';
}

/**
 * Mask a token for display in the compact token row, in the locked
 * format `drv1_•••XXXX` (prefix, fixed dot run, last four characters).
 * The full value is never rendered once it has been loaded from a link.
 *
 * @param {unknown} value
 * @returns {string} e.g. `drv1_•••a1b2`, or an em dash for empty input
 */
export function maskToken(value) {
        if (typeof value !== 'string' || value === '') return '—';
        return 'drv1_•••' + value.slice(-4);
}
