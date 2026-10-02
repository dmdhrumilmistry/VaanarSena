import { h, api, page, panel, body, table, toast, errorNotice, notice, icon, badge, withBusy, copyButton } from "../core.js";
import { input, field, textarea, check, filePicker, fileToText } from "../forms.js";

const EXAMPLE = `apiVersion: vaanarsena.io/v1
kind: Group
metadata:
  name: ios-needs-update
spec:
  kind: smart
  rules:
    match: all
    conditions:
      - {field: platform, op: in, value: [ios, ipados]}
      - {field: osVersion, op: version_lt, value: "17.0"}
---
apiVersion: vaanarsena.io/v1
kind: Blueprint
metadata:
  name: ios-update-push
spec:
  groups: [ios-needs-update]
  onEnroll:
    - {type: os_update}
`;

const TONE = { created: "ok", updated: "warn", deleted: "bad", unchanged: "" };
const cap = (s) => s.charAt(0).toUpperCase() + s.slice(1);

// Mono editor with a line-number gutter and a status line.
function editor(initial) {
  const ta = textarea(initial, { "aria-label": "Manifest", class: "textarea mf-editor-ta", wrap: "off", spellcheck: "false" });
  const gutter = h("pre", { class: "mf-editor-gutter", "aria-hidden": "true" });
  const stat = h("span", {});
  const update = () => {
    const n = ta.value.split("\n").length;
    if (gutter.dataset.n !== String(n)) {
      gutter.dataset.n = String(n);
      gutter.textContent = Array.from({ length: n }, (_, i) => i + 1).join("\n");
    }
    stat.textContent = n + (n === 1 ? " line" : " lines") + ", " + ta.value.length + " characters";
  };
  ta.addEventListener("input", update);
  ta.addEventListener("scroll", () => { gutter.scrollTop = ta.scrollTop; });
  update();
  const el = h("div", { class: "mf-editor" }, h("div", { class: "mf-editor-main" }, gutter, ta),
    h("div", { class: "mf-editor-foot" }, stat, h("span", {}, "YAML or JSON, several documents separated by ---")));
  el.ta = ta;
  el.refresh = update;
  return el;
}

export async function view() {
  const ed = editor(EXAMPLE);
  const text = ed.ta;
  const owner = input("console-apply", { class: "source-input" });
  const prune = check(false, "Delete resources from this source that are not in the manifest");
  const out = h("div");
  const dryBtn = h("button", { class: "btn", type: "button" }, icon("check"), "Dry run");
  const applyBtn = h("button", { class: "btn primary", type: "button" }, icon("upload"), "Apply");
  const run = (dry, btn) => () => withBusy(btn, async () => {
    out.replaceChildren();
    try {
      const q = new URLSearchParams({ owner: owner.get(), dryRun: String(dry), prune: String(prune.get()) });
      const res = await api("POST", "/apply?" + q, text.value, { raw: true, contentType: "application/yaml" });
      const changed = res.changes.filter((c) => c.action !== "unchanged").length;
      const counts = {};
      res.changes.forEach((c) => { counts[c.action] = (counts[c.action] || 0) + 1; });
      const summary = h("div", { class: "change-summary" },
        ["created", "updated", "deleted", "unchanged"].map((k) => h("span", { class: "badge " + TONE[k] }, h("b", {}, String(counts[k] || 0)), cap(k))));
      out.replaceChildren(h("div", { class: "result-stack" },
        notice(dry ? "gold" : "ok", dry ? `Dry run: ${changed} of ${res.changes.length} resources would change. Nothing was written.` : `Applied: ${changed} of ${res.changes.length} resources changed.`),
        h("section", { class: "panel" }, h("div", { class: "panel-head" }, h("div", {}, h("h2", {}, dry ? "Dry run result" : "Applied changes"))), summary, table([
          { label: "Change", render: (c) => badge(cap(c.action), TONE[c.action] || "") },
          { label: "Kind", render: (c) => c.kind },
          { label: "Name", render: (c) => h("span", { class: "mono" }, c.name) },
        ], res.changes, { label: "Changes", dense: true }))));
      if (!dry) toast("Manifest applied");
      out.scrollIntoView({ behavior: "smooth", block: "nearest" });
    } catch (e) { out.replaceChildren(errorNotice(e)); }
  });
  dryBtn.addEventListener("click", run(true, dryBtn));
  applyBtn.addEventListener("click", run(false, applyBtn));

  const cli = "export VS_SERVER=" + location.origin + "\nexport VS_TOKEN=vsat_...   # Account > API tokens\n\nvsctl diff  -f fleet/\nvsctl apply -f fleet/ --owner git --prune\nvsctl export > fleet.yaml";
  return page({ title: "Manifests", sub: "Groups, policies and blueprints as YAML or JSON. The same format works with vsctl, the API and a GitOps directory.",
    actions: [h("a", { class: "btn", href: "/api/v1/export", download: "vaanarsena.yaml" }, icon("download"), "Export")] },
  panel({ title: "Apply", sub: "The whole set is validated before anything is written.",
    actions: [filePicker(".yaml,.yml,.json", "Open file", async (f) => { text.value = await fileToText(f); ed.refresh(); })] },
  body(h("div", { class: "form" }, ed,
    h("div", { class: "grid2" }, field("Source name", owner, "Recorded on every resource this applies, and used to scope deletes."), h("div", {}, prune)),
    h("div", { class: "run-bar" }, dryBtn, applyBtn, h("span", { class: "help" }, "Dry run shows what would change and writes nothing."))))),
  out,
  h("div", { class: "grid2" },
    panel({ title: "Export", sub: "Everything as manifests: keep it in Git and apply it anywhere." },
      body(h("div", { class: "stack" }, notice("warn", "The export includes secrets such as Wi-Fi passphrases. Store it like a credential."),
        h("div", {}, h("a", { class: "btn", href: "/api/v1/export", download: "vaanarsena.yaml" }, icon("download"), "Download vaanarsena.yaml"))))),
    panel({ title: "From the command line", actions: [copyButton(cli, "Copy")] }, body(h("pre", { class: "code" }, cli)))));
}
