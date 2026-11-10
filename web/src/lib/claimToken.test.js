// Unit tests for the one-time setup link helpers (node --test, no browser).
// Run: npm test   (see package.json)
import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
        hashFromUrl,
        isValidTokenShape,
        maskToken,
        rawTokenParamFromHash,
        tokenFromHash,
        tokenFromPastedInput,
        urlWithoutHash
} from './claimToken.js';

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

test('isValidTokenShape accepts real tokens and every base64url class', () => {
        assert.equal(isValidTokenShape(TOKEN), true);
        assert.equal(isValidTokenShape('drv1_' + 'azAZ09-_' + 'C'.repeat(35)), true);
        assert.equal(isValidTokenShape('drv1_short'), false); // below the 16-char floor
        assert.equal(isValidTokenShape('drv1_'), false);
        assert.equal(isValidTokenShape('not_a_token'), false);
        assert.equal(isValidTokenShape('DRV1_AAAAAAAAAAAAAAAA'), false);
        assert.equal(isValidTokenShape(`${TOKEN}/../evil`), false);
        assert.equal(isValidTokenShape(`${TOKEN} `), false);
        assert.equal(isValidTokenShape(''), false);
        assert.equal(isValidTokenShape(null), false);
        assert.equal(isValidTokenShape(undefined), false);
        assert.equal(isValidTokenShape(42), false);
});

test('tokenFromPastedInput accepts the raw token', () => {
        assert.equal(tokenFromPastedInput(TOKEN), TOKEN);
        assert.equal(tokenFromPastedInput(`  ${TOKEN}  `), TOKEN);
        assert.equal(tokenFromPastedInput(`${TOKEN}\n`), TOKEN);
});

test('tokenFromPastedInput accepts a complete setup link (fragment)', () => {
        assert.equal(tokenFromPastedInput(`http://127.0.0.1:18080/onboarding#token=${TOKEN}`), TOKEN);
        assert.equal(tokenFromPastedInput(`/onboarding#token=${TOKEN}`), TOKEN);
        assert.equal(tokenFromPastedInput(`#token=${TOKEN}`), TOKEN);
});

test('tokenFromPastedInput accepts a query-string link', () => {
        assert.equal(tokenFromPastedInput(`http://127.0.0.1:18080/onboarding?token=${TOKEN}`), TOKEN);
        assert.equal(tokenFromPastedInput(`x?token=${TOKEN}&next=1`), TOKEN);
});

test('tokenFromPastedInput strips interior whitespace a mail client may wrap in', () => {
        assert.equal(tokenFromPastedInput(`http://x/onboarding#token=${TOKEN.slice(0, 20)} ${TOKEN.slice(20)}`), TOKEN);
});

test('tokenFromPastedInput tolerates junk and hostile input', () => {
        assert.equal(tokenFromPastedInput(''), '');
        assert.equal(tokenFromPastedInput('   '), '');
        assert.equal(tokenFromPastedInput('hello world'), 'helloworld');
        // delimiters cut the value; the backend decides whether it is usable
        assert.equal(tokenFromPastedInput(`#token=${TOKEN}&extra=1`), TOKEN);
        assert.equal(tokenFromPastedInput(`?token=${TOKEN}#anchor`), TOKEN);
        assert.equal(tokenFromPastedInput(`#token=${TOKEN}/../evil`), `${TOKEN}/../evil`);
        assert.equal(tokenFromPastedInput(null), '');
        assert.equal(tokenFromPastedInput(undefined), '');
        assert.equal(tokenFromPastedInput(42), '');
});

test('rawTokenParamFromHash returns the raw value even when malformed', () => {
        assert.equal(rawTokenParamFromHash(`#token=${TOKEN}`), TOKEN);
        assert.equal(rawTokenParamFromHash('#token=drv1_short'), 'drv1_short');
        assert.equal(rawTokenParamFromHash('#token=drv1_a%20b'), 'drv1_a b'); // URL-decoded
        assert.equal(rawTokenParamFromHash('#tok=drv1_x'), '');
        assert.equal(rawTokenParamFromHash('#'), '');
        assert.equal(rawTokenParamFromHash(''), '');
        assert.equal(rawTokenParamFromHash(null), '');
        assert.equal(rawTokenParamFromHash(undefined), '');
});

test('maskToken shows only prefix and last four characters', () => {
        assert.equal(maskToken(TOKEN), 'drv1_AAAA…AAAA');
        assert.equal(maskToken('drv1_short'), 'drv1_short'); // short values shown as-is
        assert.equal(maskToken(''), '—');
        assert.equal(maskToken(null), '—');
        // the masked value must never contain the full token
        assert.ok(!maskToken(TOKEN).includes(TOKEN));
});
