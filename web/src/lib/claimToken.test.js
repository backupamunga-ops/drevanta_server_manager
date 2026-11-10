// Unit tests for the one-time setup link helpers (node --test, no browser).
// Run: npm test   (see package.json)
import { test } from 'node:test';
import assert from 'node:assert/strict';

import { hashFromUrl, tokenFromHash, urlWithoutHash } from './claimToken.js';

const TOKEN = 'drv1_' + 'A'.repeat(43); // realistic 48-char token shape

test('extracts a well-formed setup token from the fragment', () => {
        assert.equal(tokenFromHash(`#token=${TOKEN}`), TOKEN);
});

test('extracts tokens containing every base64url character class', () => {
        const t = 'drv1_' + 'azAZ09-_' + 'B'.repeat(35);
        assert.equal(tokenFromHash(`#token=${t}`), t);
});

test('accepts short alpha values (shape validation only; the daemon decides validity)', () => {
        assert.equal(tokenFromHash('#token=drv1_short'), 'drv1_short');
});

test('rejects wrong keys, extra parts, and foreign characters', () => {
        assert.equal(tokenFromHash('#tok=drv1_x'), '');
        assert.equal(tokenFromHash('#TOKEN=drv1_x'), '');
        assert.equal(tokenFromHash(`#token=${TOKEN}&extra=1`), '');
        assert.equal(tokenFromHash(`#token=${TOKEN}/../evil`), '');
        assert.equal(tokenFromHash(`#token=${TOKEN}%20x`), '');
        assert.equal(tokenFromHash('#token=<script>alert(1)</script>'), '');
        assert.equal(tokenFromHash('token=drv1_x'), '');
        assert.equal(tokenFromHash(undefined), '');
        assert.equal(tokenFromHash(null), '');
});

test('urlWithoutHash keeps path and query, drops the fragment', () => {
        assert.equal(urlWithoutHash({ pathname: '/onboarding', search: '' }), '/onboarding');
        assert.equal(urlWithoutHash({ pathname: '/onboarding', search: '?x=1' }), '/onboarding?x=1');
        assert.equal(urlWithoutHash({ pathname: '/', search: '' }), '/');
});

test('urlWithoutHash tolerates malformed input', () => {
        assert.equal(urlWithoutHash(null), '');
        assert.equal(urlWithoutHash({}), '');
});

test('hashFromUrl reads the fragment from a full navigation URL', () => {
        assert.equal(hashFromUrl(`http://127.0.0.1:18080/onboarding#token=${TOKEN}`), `#token=${TOKEN}`);
        assert.equal(hashFromUrl('http://127.0.0.1:18080/onboarding'), '');
        assert.equal(hashFromUrl('http://127.0.0.1:18080/onboarding#other=1'), '#other=1');
        assert.equal(hashFromUrl(''), '');
        assert.equal(hashFromUrl('not-a-url'), '');
        assert.equal(hashFromUrl(null), '');
});

test('full round trip: fragment parse then clean URL', () => {
        const loc = { pathname: '/onboarding', search: '', hash: `#token=${TOKEN}` };
        const token = tokenFromHash(loc.hash);
        assert.ok(token.startsWith('drv1_'));
        assert.equal(urlWithoutHash(loc), '/onboarding');
});
