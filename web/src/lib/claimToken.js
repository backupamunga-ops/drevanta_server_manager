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
