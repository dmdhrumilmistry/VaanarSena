import { h, api, page, panel, body, table, toast, errorNotice, notice, icon } from "../core.js";
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

export async function view() {
  const text = textarea(EXAMPLE, { rows: 20, "aria-label": "Manifest" });
  const owner = input("console-apply", { class: "short" });
  owner.style.maxWidth = "220px";
  const prune = check(false, "Delete resources from this source that are not in the manifest");
  const out = h("div");
  const run = (dry) => async () => {
    out.replaceChildren();
    try {
      const q = new URLSearchParams({ owner: owner.get(), dryRun: String(dry), prune: String(prune.get()) });
      const res = await api("POST", "/apply?" + q, text.value, { raw: true, contentType: "application/yaml" });
      const changed = res.changes.filter((c) => c.action !== "unchanged").length;
      out.replaceChildren(
        notice(dry ? "gold" : "ok", dry ? `Dry run: ${changed} of ${res.changes.length} resources would change. Nothing was written.` : `Applied: ${changed} of ${res.changes.length} resources changed.`),
        h("section", { class: "panel block" }, table([
          { label: "Change", render: (c) => h("span", { class: "status" }, h("span", { class: "dot " + ({ created: "ok", updated: "warn", deleted: "bad" }[c.action] || "") }), c.action.charAt(0).toUpperCase() + c.action.slice(1)) },
          { label: "Kind", render: (c) => c.kind },
          { label: "Name", render: (c) => c.name },
        ], res.changes)));
      if (!dry) toast("Manifest applied");
    } catch (e) { out.replaceChildren(errorNotice(e)); }
  };
  return page({ title: "Manifests", sub: "Groups, policies and blueprints as YAML or JSON. The same format works with vsctl, the API and a GitOps directory." },
    panel({ title: "Apply", sub: "The whole set is validated before anything is written.",
      actions: [filePicker(".yaml,.yml,.json", "Open file", async (f) => { text.value = await fileToText(f); })] },
      body(h("div", { class: "form" }, text,
        h("div", { class: "grid2" }, field("Source name", owner, "Recorded on every resource this applies, and used to scope deletes."), h("div", {}, prune)),
        h("div", { class: "row" }, h("button", { class: "btn", onclick: run(true) }, "Dry run"), h("button", { class: "btn primary", onclick: run(false) }, "Apply"))))),
    out,
    panel({ title: "Export", sub: "Everything as manifests: keep it in Git and apply it anywhere. It includes secrets such as Wi-Fi passphrases." },
      body(h("a", { class: "btn", href: "/api/v1/export", download: "vaanarsena.yaml" }, icon("download"), "Download vaanarsena.yaml"))),
    panel({ title: "From the command line" }, body(h("pre", { class: "code" },
      "export VS_SERVER=" + location.origin + "\nexport VS_TOKEN=vsat_...   # Account > API tokens\n\nvsctl diff  -f fleet/\nvsctl apply -f fleet/ --owner git --prune\nvsctl export > fleet.yaml"))));
}
