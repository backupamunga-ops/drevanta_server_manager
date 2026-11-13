// Workspace pages are fully client-side: they need the operator's
// session and the daemon API, so they are excluded from prerendering
// (adapter-static then ships an empty shell that hydrates in the
// browser).
export const prerender = false;
export const ssr = false;
export const csr = true;
