import "clsx";
function _layout($$renderer, $$props) {
  let { children } = $$props;
  $$renderer.push(`<div class="shell svelte-12qhfyh"><header class="svelte-12qhfyh"><div class="brand svelte-12qhfyh"><span class="brand-mark svelte-12qhfyh" aria-hidden="true"></span> <span class="brand-name svelte-12qhfyh">Drevanta</span> <span class="brand-sub svelte-12qhfyh">server console</span></div> <div class="conn svelte-12qhfyh">loopback-only · SSH tunnel</div></header> <main class="svelte-12qhfyh">`);
  children($$renderer);
  $$renderer.push(`<!----></main> <footer class="svelte-12qhfyh"><span>Drevanta · Sprint 1 walking skeleton</span> <span>private deployment — do not expose publicly</span></footer></div>`);
}
export {
  _layout as default
};
