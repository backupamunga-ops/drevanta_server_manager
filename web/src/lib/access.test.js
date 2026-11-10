import assert from 'node:assert/strict';
import { test } from 'node:test';

import { accessLabel, accessLabelTitle, domainHint } from './access.js';

test('accessLabel: https mode', () => {
	assert.equal(accessLabel({ mode: 'https', https: { host: '1.2.3.4' } }), 'HTTPS secured');
});

test('accessLabel: private mode', () => {
	assert.equal(accessLabel({ mode: 'private' }), 'Private · SSH tunnel');
});

test('accessLabel: unknown stays empty (never a guessed claim)', () => {
	assert.equal(accessLabel(null), '');
	assert.equal(accessLabel(undefined), '');
});

test('accessLabelTitle mentions the host for https', () => {
	assert.match(accessLabelTitle({ mode: 'https', https: { host: '1.2.3.4' } }), /1\.2\.3\.4/);
	assert.match(accessLabelTitle({ mode: 'private' }), /SSH tunnel/);
	assert.match(accessLabelTitle(null), /checking/);
});

test('domainHint accepts certifiable hostnames', () => {
	for (const good of ['vpn.example.com', 'VPN.Example.COM.', 'a-b.c.example.co.uk', 'd.dev']) {
		assert.equal(domainHint(good), '', `domainHint(${good}) should be empty`);
	}
});

test('domainHint rejects shapes the backend would refuse', () => {
	const bad = {
		'': 'empty',
		'localhost': 'two parts',
		'203.0.113.7': 'IP',
		'*.example.com': 'wildcard',
		'my_server.example.com': 'underscore',
		'-x.example.com': 'leading hyphen',
		'x-.example.com': 'trailing hyphen',
		'exa mple.com': 'space',
		'a/b.com': 'slash',
		'mail.example.com:443': 'port',
		'x@y.com': 'at',
	};
	for (const [inValue, why] of Object.entries(bad)) {
		assert.notEqual(domainHint(inValue), '', `domainHint(${inValue}) should fail (${why})`);
	}
});
