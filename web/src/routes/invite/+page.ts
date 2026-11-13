// The invitation page reads its token from the URL and talks to the
// daemon API (preview + acceptance), so it renders in the browser only.
export const ssr = false;
export const csr = true;
