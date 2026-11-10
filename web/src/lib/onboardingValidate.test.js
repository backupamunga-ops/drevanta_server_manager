// Unit tests for the onboarding claim-form validators (node --test).
// The client mirrors the daemon's rules; these tests pin that mirror so
// the two cannot drift silently. Run: npm test
import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
        PASSWORD_MAX,
        PASSWORD_MIN,
        STRENGTH_LABELS,
        confirmError,
        passwordError,
        scorePassword,
        tokenError,
        usernameError
} from './onboardingValidate.js';

// --- username ------------------------------------------------------------

test('username accepts the daemon charset and bounds', () => {
        assert.equal(usernameError('root.admin'), '');
        assert.equal(usernameError('a.b-c_d9'), '');
        assert.equal(usernameError('9starts-number'), '');
        assert.equal(usernameError('a'.repeat(32)), '');
});

test('username rejects out-of-bounds and foreign characters', () => {
        assert.equal(usernameError(''), 'Choose an administrator username.');
        assert.equal(usernameError('   '), 'Choose an administrator username.');
        assert.equal(usernameError('ab'), 'Use at least 3 characters.');
        assert.equal(usernameError('a'.repeat(33)), 'Keep it to 32 characters or fewer.');
        assert.equal(usernameError('.leading-dot'), 'Letters, numbers, dots, dashes and underscores only.');
        assert.equal(usernameError('has space'), 'Letters, numbers, dots, dashes and underscores only.');
        assert.equal(usernameError('caf\u00e9'), 'Letters, numbers, dots, dashes and underscores only.');
        assert.equal(usernameError('user/name'), 'Letters, numbers, dots, dashes and underscores only.');
        assert.equal(usernameError(null), 'Choose an administrator username.');
});

// --- password ------------------------------------------------------------

test('password accepts 12..128 characters', () => {
        assert.equal(passwordError('a'.repeat(PASSWORD_MIN), ''), '');
        assert.equal(passwordError('a'.repeat(PASSWORD_MAX), ''), '');
        assert.equal(passwordError('correct horse battery', 'admin'), '');
});

test('password enforces the daemon minimum with a helpful count', () => {
        assert.equal(passwordError('', ''), 'Create a password.');
        assert.equal(passwordError('a'.repeat(11), ''), 'Needs 1 more character.');
        assert.equal(passwordError('ab', ''), 'Needs 10 more characters.');
});

test('password enforces the daemon maximum', () => {
        assert.equal(
                passwordError('a'.repeat(PASSWORD_MAX + 1), ''),
                'Keep it to 128 characters or fewer.'
        );
});

test('password must differ from the username (client-side hardening)', () => {
        assert.equal(passwordError('AdministratorX', 'AdministratorX'), 'Password cannot match your username.');
        assert.equal(passwordError('administratorx', 'ADMINISTRATORX'), 'Password cannot match your username.');
        assert.equal(passwordError('AdministratorX', ''), ''); // no username yet: not applicable
        assert.equal(passwordError('AdministratorX', 'other'), '');
});

// --- confirm -------------------------------------------------------------

test('confirm requires an identical repeat', () => {
        assert.equal(confirmError('secret-pw-12345', 'secret-pw-12345'), '');
        assert.equal(confirmError('', 'secret-pw-12345'), 'Repeat the password.');
        assert.equal(confirmError('secret-pw-1234', 'secret-pw-12345'), 'Passwords do not match yet.');
        assert.equal(confirmError('secret-pw-12345', ''), 'Passwords do not match yet.');
});

// --- token (manual entry) ------------------------------------------------

test('tokenError passes well-shaped tokens to the backend', () => {
        assert.equal(tokenError('drv1_' + 'A'.repeat(43)), '');
});

test('tokenError explains empty and malformed values', () => {
        assert.equal(tokenError(''), 'Paste your claim token, or reopen the setup link.');
        assert.equal(tokenError('   '), 'Paste your claim token, or reopen the setup link.');
        assert.equal(
                tokenError('truncated'),
                'That doesn\u2019t look like a claim token \u2014 it starts with \u201cdrv1_\u201d.'
        );
        assert.equal(
                tokenError('drv1_' + 'x'.repeat(10)),
                'That doesn\u2019t look like a claim token \u2014 it starts with \u201cdrv1_\u201d.'
        );
        assert.equal(tokenError(null), 'Paste your claim token, or reopen the setup link.');
});

// --- strength ------------------------------------------------------------

test('scorePassword matches the approved prototype exactly', () => {
        assert.equal(scorePassword(''), 0);
        assert.equal(scorePassword('short'), 1); // below 12 chars
        assert.equal(scorePassword('aaaaaaaaaaaa'), 2); // 12 chars, one class
        assert.equal(scorePassword('aaaaaaaaaaaaaa'), 2); // 14 chars, one class
        assert.equal(scorePassword('Abcdefgh1234'), 3); // 12 chars, 3 classes
        assert.equal(scorePassword('abcdefghij1234567890'), 3); // 20 chars, 2 classes (length rule)
        assert.equal(scorePassword('Abcdefgh1234!@'), 3); // 4 classes but only 14 chars
});

test('scorePassword boundary between 3 and 4', () => {
        // 4 requires >= 16 chars AND >= 3 classes
        assert.equal(scorePassword('Abcdefgh12345!@'), 3); // 4 classes, 15 chars
        assert.equal(scorePassword('Abcdefgh1234!@#$'), 4); // 16 chars, 4 classes
});

test('strength labels cover every score', () => {
        assert.equal(STRENGTH_LABELS.length, 5);
        for (let i = 0; i < STRENGTH_LABELS.length; i++) assert.equal(typeof STRENGTH_LABELS[i], 'string');
        assert.equal(STRENGTH_LABELS[1], 'Too short');
        assert.equal(STRENGTH_LABELS[2], 'Fair');
        assert.equal(STRENGTH_LABELS[3], 'Strong');
        assert.equal(STRENGTH_LABELS[4], 'Excellent');
});
