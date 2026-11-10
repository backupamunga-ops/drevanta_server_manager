export const manifest = (() => {
function __memo(fn) {
	let value;
	return () => value ??= (value = fn());
}

return {
	appDir: "_app",
	appPath: "_app",
	assets: new Set(["favicon.svg"]),
	mimeTypes: {".svg":"image/svg+xml"},
	_: {
		client: {start:"_app/immutable/entry/start.DpMdkXzV.js",app:"_app/immutable/entry/app.DmUY7R2b.js",imports:["_app/immutable/entry/start.DpMdkXzV.js","_app/immutable/chunks/Dv_8hkG4.js","_app/immutable/chunks/C7syVljc.js","_app/immutable/chunks/BeFfwil5.js","_app/immutable/entry/app.DmUY7R2b.js","_app/immutable/chunks/Dv_8hkG4.js","_app/immutable/chunks/DA6rJM9Y.js","_app/immutable/chunks/DJspNVGP.js","_app/immutable/chunks/BeFfwil5.js","_app/immutable/chunks/C_osQ6sm.js","_app/immutable/chunks/CsmCsU0-.js"],stylesheets:[],fonts:[],uses_env_dynamic_public:false},
		nodes: [
			__memo(() => import('./nodes/0.js')),
			__memo(() => import('./nodes/1.js')),
			__memo(() => import('./nodes/2.js'))
		],
		remotes: {
			
		},
		routes: [
			{
				id: "/",
				pattern: /^\/$/,
				params: [],
				page: { layouts: [0,], errors: [1,], leaf: 2 },
				endpoint: null
			}
		],
		prerendered_routes: new Set([]),
		matchers: async () => {
			
			return {  };
		},
		server_assets: {}
	}
}
})();
