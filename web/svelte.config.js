import adapter from '@sveltejs/adapter-static';

/** @type {import('@sveltejs/kit').Config} */
const config = {
	kit: {
		// Static export: the dashboard is embedded into the drevantad
		// binary and served by the Go daemon itself.
		adapter: adapter({
			pages: 'build',
			assets: 'build',
			fallback: undefined,
			strict: true
		})
	}
};

export default config;
