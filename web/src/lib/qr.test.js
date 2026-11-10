// Regression test: the QR SVG must carry explicit dimensions. The
// production bug was a dimension-less <svg viewBox> collapsing to 0x0
// inside the fit-content .oqr container (blank white pill instead of a
// scannable QR code on the MFA enrolment step).
import { test } from 'node:test';
import assert from 'node:assert/strict';

import { QR_SIZE, qrSvg } from './qr.js';

const URI = 'otpauth://totp/Drevanta:operator?secret=JBSWY3DPEHPK3PXP&issuer=Drevanta';

test('qrSvg emits an <svg> with explicit width and height', async () => {
        const svg = await qrSvg(URI);
        assert.match(svg, /^<svg\b/);
        assert.ok(svg.includes(`width="${QR_SIZE}"`), `missing explicit width="${QR_SIZE}"`);
        assert.ok(svg.includes(`height="${QR_SIZE}"`), `missing explicit height="${QR_SIZE}"`);
});

test('qrSvg carries non-trivial path data (a real matrix, not an empty shell)', async () => {
        const svg = await qrSvg(URI);
        // The first path is the white background; the matrix is the longest one.
        const paths = [...svg.matchAll(/ d="([^"]+)"/g)].map((m) => m[1]);
        const longest = Math.max(...paths.map((p) => p.length));
        assert.ok(longest > 100, 'QR matrix path data suspiciously short');
});

test('qrSvg honours a custom size', async () => {
        const svg = await qrSvg(URI, 240);
        assert.ok(svg.includes('width="240"'), 'custom width not applied');
        assert.ok(svg.includes('height="240"'), 'custom height not applied');
});
