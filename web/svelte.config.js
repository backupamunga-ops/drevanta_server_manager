import adapter from '@sveltejs/adapter-static';

/** @type {import('@sveltejs/kit').Config} */
const config = {
	kit: {
		// Static export: the dashboard is embedded into the drevantad
		// binary and served by the Go daemon itself. strict is false
		// because /org/[id] is deliberately client-rendered (it needs the
		// operator session and the daemon API), which strict mode would
		// reject.
		adapter: adapter({
			pages: 'build',
			assets: 'build',
			fallback: undefined,
			strict: false
		})
	}
};

export default config;
