import { h, api, page, panel, body, link, table, fmtTime, can, emptyState, go, toast, confirmDialog, errorNotice, notice, icon, groupsCache } from "../core.js";
import { input, field, picklist } from "../forms.js";
import { policyEditor } from "../policy-form.js";

export const managedNote = (m) => m && m !== "-" ? notice("gold", `Managed by manifests from "${m}". Changes you save here make the console its owner; the next apply from that source takes it back.`) : null;
export const sourceTag = (m) => m && m !== "-" ? h("span", { class: "badge" }, "Manifest: " + m) : h("span", { class: "muted" }, "Console");

const SECTIONS = { passcode: "Passcode", encryption: "Encryption", restrictions: "Restrictions", wifi: "Wi-Fi", osUpdates: "OS updates", apps: "Apps", custom: "Custom payloads" };

export async function list() {
  const [res, groups] = await Promise.all([api("GET", "/policies"), groupsCache()]);
  const gname = Object.fromEntries(groups.map((g) => [g.id, g.name]));
  return page({ title: "Policies", sub: "Settings written once and translated for each platform. When several apply, the lower priority number wins.",
    actions: can("admin") ? [link("/policies/new", h("span", { class: "btn primary" }, icon("plus"), "New policy"))] : [] },
    h("section", { class: "panel" }, table([
      { label: "Policy", render: (p) => h("div", {}, h("div", { class: "primary-cell" }, p.name), p.description ? h("div", { class: "secondary-cell" }, p.description) : null) },
      { label: "Covers", render: (p) => h("span", { class: "small" }, Object.keys(p.document || {}).map((k) => SECTIONS[k] || k).join(", ") || "Nothing yet") },
      { label: "Applies to", render: (p) => p.groupIds.length ? p.groupIds.map((g) => gname[g]).filter(Boolean).join(", ") : h("span", { class: "muted" }, p.deviceIds.length ? p.deviceIds.length + " devices" : "No groups") },
      { label: "Priority", cls: "num", render: (p) => p.priority },
      { label: "Source", render: (p) => sourceTag(p.managedBy) },
      { label: "Updated", render: (p) => fmtTime(p.updatedAt) },
    ], res.policies, { onRow: (p) => go("/policies/" + p.id),
      empty: emptyState("No policies yet", "Start with a baseline: a passcode, encryption and a few restrictions for corporate devices.",
        can("admin") ? link("/policies/new", h("span", { class: "btn primary" }, "Create a policy")) : null) })));
}

export async function edit(id) {
  const isNew = id === "new";
  const [p, groups] = await Promise.all([
    isNew ? { name: "", description: "", priority: 100, document: {}, groupIds: [], deviceIds: [] } : api("GET", "/policies/" + id),
    groupsCache(true),
  ]);
  const ro = !can("admin");
  const name = input(p.name, { placeholder: "Baseline security", disabled: ro });
  const desc = input(p.description, { placeholder: "What this policy is for", disabled: ro });
  const prio = input(p.priority, { type: "number", min: 1, class: "short", disabled: ro });
  const targets = picklist(groups.map((g) => [g.id, g.name, g.kind === "smart" ? "smart, " + g.deviceCount + " devices" : g.deviceCount + " devices"]), p.groupIds, "Create a group first, then assign this policy to it.");
  const editor = policyEditor(p.document);
  const err = h("div");
  const save = h("button", { class: "btn primary", disabled: ro, onclick: async () => {
    err.replaceChildren();
    if (!name.get()) { err.replaceChildren(errorNotice(new Error("Give the policy a name."))); return; }
    try {
      const doc = editor.get();
      const saved = await api(isNew ? "POST" : "PUT", isNew ? "/policies" : "/policies/" + id,
        { name: name.get(), description: desc.get(), priority: prio.get() || 100, document: doc, groupIds: targets.get(), deviceIds: p.deviceIds });
      toast(isNew ? "Policy created and pushed to its devices" : "Policy saved and pushed to its devices");
      if (isNew) go("/policies/" + saved.id);
    } catch (e) { err.replaceChildren(errorNotice(e)); err.scrollIntoView({ block: "center" }); }
  } }, isNew ? "Create policy" : "Save and push");
  const del = !isNew && !ro ? h("button", { class: "btn quiet", onclick: async () => {
    if (!(await confirmDialog({ title: `Delete "${p.name}"?`, body: "Devices it applied to receive their remaining policies again.", confirmLabel: "Delete policy", danger: true }))) return;
    await api("DELETE", "/policies/" + id); toast("Policy deleted"); go("/policies");
  } }, "Delete") : null;

  return page({ title: isNew ? "New policy" : p.name, crumb: link("/policies", "Policies"), actions: [del, save].filter(Boolean) },
    managedNote(p.managedBy),
    ro ? notice("gold", "You can view policies; only admins can change them.") : null,
    h("div", { class: "grid2 block" },
      panel({ title: "About" }, body(h("div", { class: "form" }, field("Name", name), field("Description", desc), field("Priority", prio, "1 is the most important. Defaults to 100.")))),
      panel({ title: "Applies to", sub: "Groups whose devices receive this policy." }, body(targets))),
    h("div", { class: "block" }, h("h2", { class: "section-title" }, "Settings"), editor),
    h("div", { class: "block" }, err, h("div", { class: "row", style: { marginTop: "14px" } }, save)));
}
