// Pure client-side validation for the onboarding claim form.
//
// Mirrors the daemon's rules (internal/httpapi/auth.go):
//   username: 3-32 chars, first char alphanumeric, then [a-zA-Z0-9._-]
//   password: 12-128 chars
// plus the approved prototype's extra client-side requirement that the
// password differ from the username. The backend remains authoritative:
// every message here only prevents a round trip, never relaxes a rule.
//
// All functions return '' for valid input and a human-readable message
// otherwise, so they can be unit-tested without a browser.

import { isValidTokenShape } from './claimToken.js';

export const USERNAME_MIN = 3;
export const USERNAME_MAX = 32;
export const PASSWORD_MIN = 12;
export const PASSWORD_MAX = 128;

const USERNAME_RE = /^[a-zA-Z0-9][a-zA-Z0-9._-]*$/;

/**
 * @param {unknown} v - raw username input
 * @returns {string} error message, or '' when valid
 */
export function usernameError(v) {
        const u = typeof v === 'string' ? v.trim() : '';
        if (u === '') return 'Choose an administrator username.';
        if (u.length < USERNAME_MIN) return 'Use at least 3 characters.';
        if (u.length > USERNAME_MAX) return 'Keep it to 32 characters or fewer.';
        if (!USERNAME_RE.test(u)) return 'Letters, numbers, dots, dashes and underscores only.';
        return '';
}

/**
 * @param {unknown} v - raw password input
 * @param {unknown} username - raw username input (case-insensitive check)
 * @returns {string} error message, or '' when valid
 */
export function passwordError(v, username) {
        const p = typeof v === 'string' ? v : '';
        if (p === '') return 'Create a password.';
        if (p.length < PASSWORD_MIN) {
                const n = PASSWORD_MIN - p.length;
                return `Needs ${n} more character${n === 1 ? '' : 's'}.`;
        }
        if (p.length > PASSWORD_MAX) return 'Keep it to 128 characters or fewer.';
        const u = typeof username === 'string' ? username.trim().toLowerCase() : '';
        if (u !== '' && p.toLowerCase() === u) return 'Password cannot match your username.';
        return '';
}

/**
 * @param {unknown} confirm - repeated password
 * @param {unknown} password - original password
 * @returns {string} error message, or '' when valid
 */
export function confirmError(confirm, password) {
        const c = typeof confirm === 'string' ? confirm : '';
        if (c === '') return 'Repeat the password.';
        if (c !== (typeof password === 'string' ? password : '')) return 'Passwords do not match yet.';
        return '';
}

/**
 * Manual-entry token error. The token is never verified here — a
 * well-shaped token still goes to the backend, which decides.
 *
 * @param {unknown} v - raw token / pasted setup link
 * @returns {string} error message, or '' when the shape is acceptable
 */
export function tokenError(v) {
        const t = typeof v === 'string' ? v.trim() : '';
        if (t === '') return 'Paste your claim token, or reopen the setup link.';
        if (!isValidTokenShape(t)) return 'That doesn\u2019t look like a claim token \u2014 it starts with \u201cdrv1_\u201d.';
        return '';
}

/**
 * 0-4 password strength score, exactly as in the approved prototype:
 * too short scores 1; otherwise character-class coverage and length
 * decide between 2, 3 and 4.
 *
 * @param {string} v
 * @returns {number} 0 (empty) .. 4 (excellent)
 */
export function scorePassword(v) {
        const p = typeof v === 'string' ? v : '';
        if (p === '') return 0;
        if (p.length < PASSWORD_MIN) return 1;
        const classes = [/[a-z]/, /[A-Z]/, /[0-9]/, /[^A-Za-z0-9]/].filter((r) => r.test(p)).length;
        if (p.length >= 16 && classes >= 3) return 4;
        if (classes >= 3 || p.length >= 18) return 3;
        return 2;
}

/** Display labels indexed by scorePassword(); index 0 is unused. */
export const STRENGTH_LABELS = ['', 'Too short', 'Fair', 'Strong', 'Excellent'];
