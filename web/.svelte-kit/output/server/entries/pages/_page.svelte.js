import { a5 as attr_class, e as escape_html, a6 as head } from "../../chunks/index.js";
function StatusPill($$renderer, $$props) {
  let { label, ok } = $$props;
  $$renderer.push(`<span${attr_class("pill svelte-1swmi23", void 0, { "ok": ok, "bad": !ok })}><span class="dot svelte-1swmi23"></span>${escape_html(label)}</span>`);
}
function _page($$renderer, $$props) {
  $$renderer.component(($$renderer2) => {
    let liveness = "down";
    let readiness = "not-ready";
    head("1uha8ag", $$renderer2, ($$renderer3) => {
      $$renderer3.title(($$renderer4) => {
        $$renderer4.push(`<title>Drevanta — Server Console</title>`);
      });
      $$renderer3.push(`<meta name="description" content="Private VPS management console"/>`);
    });
    $$renderer2.push(`<div class="toolbar"><div class="pills">`);
    StatusPill($$renderer2, { label: "healthz", ok: liveness === "up" });
    $$renderer2.push(`<!----> `);
    StatusPill($$renderer2, { label: "readyz", ok: readiness === "ready" });
    $$renderer2.push(`<!----></div> <button class="btn">${escape_html("Pause")}</button></div> `);
    {
      $$renderer2.push("<!--[-1-->");
    }
    $$renderer2.push(`<!--]--> `);
    {
      $$renderer2.push(`<!--[1--><div class="loading">connecting to daemon…</div>`);
    }
    $$renderer2.push(`<!--]-->`);
  });
}
export {
  _page as default
};
