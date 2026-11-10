
// this file is generated — do not edit it


/// <reference types="@sveltejs/kit" />

/**
 * This module provides access to environment variables that are injected _statically_ into your bundle at build time and are limited to _private_ access.
 * 
 * |         | Runtime                                                                    | Build time                                                               |
 * | ------- | -------------------------------------------------------------------------- | ------------------------------------------------------------------------ |
 * | Private | [`$env/dynamic/private`](https://svelte.dev/docs/kit/$env-dynamic-private) | [`$env/static/private`](https://svelte.dev/docs/kit/$env-static-private) |
 * | Public  | [`$env/dynamic/public`](https://svelte.dev/docs/kit/$env-dynamic-public)   | [`$env/static/public`](https://svelte.dev/docs/kit/$env-static-public)   |
 * 
 * Static environment variables are [loaded by Vite](https://vitejs.dev/guide/env-and-mode.html#env-files) from `.env` files and `process.env` at build time and then statically injected into your bundle at build time, enabling optimisations like dead code elimination.
 * 
 * **_Private_ access:**
 * 
 * - This module cannot be imported into client-side code
 * - This module only includes variables that _do not_ begin with [`config.kit.env.publicPrefix`](https://svelte.dev/docs/kit/configuration#env) _and do_ start with [`config.kit.env.privatePrefix`](https://svelte.dev/docs/kit/configuration#env) (if configured)
 * 
 * For example, given the following build time environment:
 * 
 * ```env
 * ENVIRONMENT=production
 * PUBLIC_BASE_URL=http://site.com
 * ```
 * 
 * With the default `publicPrefix` and `privatePrefix`:
 * 
 * ```ts
 * import { ENVIRONMENT, PUBLIC_BASE_URL } from '$env/static/private';
 * 
 * console.log(ENVIRONMENT); // => "production"
 * console.log(PUBLIC_BASE_URL); // => throws error during build
 * ```
 * 
 * The above values will be the same _even if_ different values for `ENVIRONMENT` or `PUBLIC_BASE_URL` are set at runtime, as they are statically replaced in your code with their build time values.
 */
declare module '$env/static/private' {
	export const SVELTEKIT_FORK: string;
	export const NODE_ENV: string;
	export const EDITOR: string;
	export const INIT_CWD: string;
	export const FC_FUNCTION_HANDLER: string;
	export const COLOR: string;
	export const UV_RUN_RECURSION_DEPTH: string;
	export const UV: string;
	export const NO_COLOR: string;
	export const npm_config_prefix: string;
	export const npm_lifecycle_event: string;
	export const npm_config_local_prefix: string;
	export const BUN_INSTALL: string;
	export const npm_config_userconfig: string;
	export const LC_CTYPE: string;
	export const PATH: string;
	export const CLICOLOR_FORCE: string;
	export const FC_FUNCTION_NAME: string;
	export const CLAWHUB_WORKDIR: string;
	export const DATABASE_URL: string;
	export const _: string;
	export const SUPERVISE_MAIN_PID: string;
	export const npm_package_json: string;
	export const UV_CACHE_DIR: string;
	export const FC_ACCOUNT_ID: string;
	export const DEBIAN_FRONTEND: string;
	export const HOME: string;
	export const STEP_START_TIME: string;
	export const SHELL: string;
	export const npm_config_node_gyp: string;
	export const UV_PYTHON: string;
	export const FC_CONTAINER_ID: string;
	export const npm_config_allow_scripts: string;
	export const USER: string;
	export const FC_FUNCTION_MEMORY_SIZE: string;
	export const SHLVL: string;
	export const BUN_INSTALL_BIN: string;
	export const npm_node_execpath: string;
	export const FC_CUSTOM_LISTEN_PORT: string;
	export const OLDPWD: string;
	export const npm_config_user_agent: string;
	export const MAIL: string;
	export const npm_config_init_module: string;
	export const CLAWHUB_DISABLE_TELEMETRY: string;
	export const npm_config_npm_version: string;
	export const npm_config_globalconfig: string;
	export const npm_command: string;
	export const FC_INSTANCE_ID: string;
	export const LOGNAME: string;
	export const PYTHONDONTWRITEBYTECODE: string;
	export const FC_REGION: string;
	export const PYTHONUNBUFFERED: string;
	export const npm_config_cache: string;
	export const TERM: string;
	export const NODE: string;
	export const npm_package_name: string;
	export const npm_config_noproxy: string;
	export const SIGMA_APP_NAME: string;
	export const npm_lifecycle_script: string;
	export const BUN_RUNTIME_TRANSPILER_CACHE_PATH: string;
	export const npm_package_version: string;
	export const npm_config_global_prefix: string;
	export const npm_execpath: string;
	export const VIRTUAL_ENV: string;
	export const PWD: string;
	export const KATA_CONTAINER: string;
	export const CLICOLOR: string;
}

/**
 * This module provides access to environment variables that are injected _statically_ into your bundle at build time and are _publicly_ accessible.
 * 
 * |         | Runtime                                                                    | Build time                                                               |
 * | ------- | -------------------------------------------------------------------------- | ------------------------------------------------------------------------ |
 * | Private | [`$env/dynamic/private`](https://svelte.dev/docs/kit/$env-dynamic-private) | [`$env/static/private`](https://svelte.dev/docs/kit/$env-static-private) |
 * | Public  | [`$env/dynamic/public`](https://svelte.dev/docs/kit/$env-dynamic-public)   | [`$env/static/public`](https://svelte.dev/docs/kit/$env-static-public)   |
 * 
 * Static environment variables are [loaded by Vite](https://vitejs.dev/guide/env-and-mode.html#env-files) from `.env` files and `process.env` at build time and then statically injected into your bundle at build time, enabling optimisations like dead code elimination.
 * 
 * **_Public_ access:**
 * 
 * - This module _can_ be imported into client-side code
 * - **Only** variables that begin with [`config.kit.env.publicPrefix`](https://svelte.dev/docs/kit/configuration#env) (which defaults to `PUBLIC_`) are included
 * 
 * For example, given the following build time environment:
 * 
 * ```env
 * ENVIRONMENT=production
 * PUBLIC_BASE_URL=http://site.com
 * ```
 * 
 * With the default `publicPrefix` and `privatePrefix`:
 * 
 * ```ts
 * import { ENVIRONMENT, PUBLIC_BASE_URL } from '$env/static/public';
 * 
 * console.log(ENVIRONMENT); // => throws error during build
 * console.log(PUBLIC_BASE_URL); // => "http://site.com"
 * ```
 * 
 * The above values will be the same _even if_ different values for `ENVIRONMENT` or `PUBLIC_BASE_URL` are set at runtime, as they are statically replaced in your code with their build time values.
 */
declare module '$env/static/public' {
	
}

/**
 * This module provides access to environment variables set _dynamically_ at runtime and that are limited to _private_ access.
 * 
 * |         | Runtime                                                                    | Build time                                                               |
 * | ------- | -------------------------------------------------------------------------- | ------------------------------------------------------------------------ |
 * | Private | [`$env/dynamic/private`](https://svelte.dev/docs/kit/$env-dynamic-private) | [`$env/static/private`](https://svelte.dev/docs/kit/$env-static-private) |
 * | Public  | [`$env/dynamic/public`](https://svelte.dev/docs/kit/$env-dynamic-public)   | [`$env/static/public`](https://svelte.dev/docs/kit/$env-static-public)   |
 * 
 * Dynamic environment variables are defined by the platform you're running on. For example if you're using [`adapter-node`](https://github.com/sveltejs/kit/tree/main/packages/adapter-node) (or running [`vite preview`](https://svelte.dev/docs/kit/cli)), this is equivalent to `process.env`.
 * 
 * **_Private_ access:**
 * 
 * - This module cannot be imported into client-side code
 * - This module includes variables that _do not_ begin with [`config.kit.env.publicPrefix`](https://svelte.dev/docs/kit/configuration#env) _and do_ start with [`config.kit.env.privatePrefix`](https://svelte.dev/docs/kit/configuration#env) (if configured)
 * 
 * > [!NOTE] In `dev`, `$env/dynamic` includes environment variables from `.env`. In `prod`, this behavior will depend on your adapter.
 * 
 * > [!NOTE] To get correct types, environment variables referenced in your code should be declared (for example in an `.env` file), even if they don't have a value until the app is deployed:
 * >
 * > ```env
 * > MY_FEATURE_FLAG=
 * > ```
 * >
 * > You can override `.env` values from the command line like so:
 * >
 * > ```sh
 * > MY_FEATURE_FLAG="enabled" npm run dev
 * > ```
 * 
 * For example, given the following runtime environment:
 * 
 * ```env
 * ENVIRONMENT=production
 * PUBLIC_BASE_URL=http://site.com
 * ```
 * 
 * With the default `publicPrefix` and `privatePrefix`:
 * 
 * ```ts
 * import { env } from '$env/dynamic/private';
 * 
 * console.log(env.ENVIRONMENT); // => "production"
 * console.log(env.PUBLIC_BASE_URL); // => undefined
 * ```
 */
declare module '$env/dynamic/private' {
	export const env: {
		SVELTEKIT_FORK: string;
		NODE_ENV: string;
		EDITOR: string;
		INIT_CWD: string;
		FC_FUNCTION_HANDLER: string;
		COLOR: string;
		UV_RUN_RECURSION_DEPTH: string;
		UV: string;
		NO_COLOR: string;
		npm_config_prefix: string;
		npm_lifecycle_event: string;
		npm_config_local_prefix: string;
		BUN_INSTALL: string;
		npm_config_userconfig: string;
		LC_CTYPE: string;
		PATH: string;
		CLICOLOR_FORCE: string;
		FC_FUNCTION_NAME: string;
		CLAWHUB_WORKDIR: string;
		DATABASE_URL: string;
		_: string;
		SUPERVISE_MAIN_PID: string;
		npm_package_json: string;
		UV_CACHE_DIR: string;
		FC_ACCOUNT_ID: string;
		DEBIAN_FRONTEND: string;
		HOME: string;
		STEP_START_TIME: string;
		SHELL: string;
		npm_config_node_gyp: string;
		UV_PYTHON: string;
		FC_CONTAINER_ID: string;
		npm_config_allow_scripts: string;
		USER: string;
		FC_FUNCTION_MEMORY_SIZE: string;
		SHLVL: string;
		BUN_INSTALL_BIN: string;
		npm_node_execpath: string;
		FC_CUSTOM_LISTEN_PORT: string;
		OLDPWD: string;
		npm_config_user_agent: string;
		MAIL: string;
		npm_config_init_module: string;
		CLAWHUB_DISABLE_TELEMETRY: string;
		npm_config_npm_version: string;
		npm_config_globalconfig: string;
		npm_command: string;
		FC_INSTANCE_ID: string;
		LOGNAME: string;
		PYTHONDONTWRITEBYTECODE: string;
		FC_REGION: string;
		PYTHONUNBUFFERED: string;
		npm_config_cache: string;
		TERM: string;
		NODE: string;
		npm_package_name: string;
		npm_config_noproxy: string;
		SIGMA_APP_NAME: string;
		npm_lifecycle_script: string;
		BUN_RUNTIME_TRANSPILER_CACHE_PATH: string;
		npm_package_version: string;
		npm_config_global_prefix: string;
		npm_execpath: string;
		VIRTUAL_ENV: string;
		PWD: string;
		KATA_CONTAINER: string;
		CLICOLOR: string;
		[key: `PUBLIC_${string}`]: undefined;
		[key: `${string}`]: string | undefined;
	}
}

/**
 * This module provides access to environment variables set _dynamically_ at runtime and that are _publicly_ accessible.
 * 
 * |         | Runtime                                                                    | Build time                                                               |
 * | ------- | -------------------------------------------------------------------------- | ------------------------------------------------------------------------ |
 * | Private | [`$env/dynamic/private`](https://svelte.dev/docs/kit/$env-dynamic-private) | [`$env/static/private`](https://svelte.dev/docs/kit/$env-static-private) |
 * | Public  | [`$env/dynamic/public`](https://svelte.dev/docs/kit/$env-dynamic-public)   | [`$env/static/public`](https://svelte.dev/docs/kit/$env-static-public)   |
 * 
 * Dynamic environment variables are defined by the platform you're running on. For example if you're using [`adapter-node`](https://github.com/sveltejs/kit/tree/main/packages/adapter-node) (or running [`vite preview`](https://svelte.dev/docs/kit/cli)), this is equivalent to `process.env`.
 * 
 * **_Public_ access:**
 * 
 * - This module _can_ be imported into client-side code
 * - **Only** variables that begin with [`config.kit.env.publicPrefix`](https://svelte.dev/docs/kit/configuration#env) (which defaults to `PUBLIC_`) are included
 * 
 * > [!NOTE] In `dev`, `$env/dynamic` includes environment variables from `.env`. In `prod`, this behavior will depend on your adapter.
 * 
 * > [!NOTE] To get correct types, environment variables referenced in your code should be declared (for example in an `.env` file), even if they don't have a value until the app is deployed:
 * >
 * > ```env
 * > MY_FEATURE_FLAG=
 * > ```
 * >
 * > You can override `.env` values from the command line like so:
 * >
 * > ```sh
 * > MY_FEATURE_FLAG="enabled" npm run dev
 * > ```
 * 
 * For example, given the following runtime environment:
 * 
 * ```env
 * ENVIRONMENT=production
 * PUBLIC_BASE_URL=http://example.com
 * ```
 * 
 * With the default `publicPrefix` and `privatePrefix`:
 * 
 * ```ts
 * import { env } from '$env/dynamic/public';
 * console.log(env.ENVIRONMENT); // => undefined, not public
 * console.log(env.PUBLIC_BASE_URL); // => "http://example.com"
 * ```
 * 
 * ```
 * 
 * ```
 */
declare module '$env/dynamic/public' {
	export const env: {
		[key: `PUBLIC_${string}`]: string | undefined;
	}
}
