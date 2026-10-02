import { h, api, page, panel, body, link, table, fmtTime, can, emptyState, go, toast, confirmDialog, errorNotice, notice, icon, groupsCache,
  platformTag, ownershipTag, complianceTag } from "../core.js";
import { input, field, segmented } from "../forms.js";
import { ruleBuilder } from "../rules.js";
import { managedNote, sourceTag } from "./policies.js";

export async function list() {
  const groups = await groupsCache(true);
  return page({ title: "Groups", sub: "Static groups are curated by hand. Smart groups fill themselves from rules and update every minute.",
    actions: can("admin") ? [link("/groups/new", h("span", { class: "btn primary" }, icon("plus"), "New group"))] : [] },
    h("section", { class: "panel" }, table([
      { label: "Group", render: (g) => h("div", {}, h("div", { class: "primary-cell" }, g.name), g.description ? h("div", { class: "secondary-cell" }, g.description) : null) },
      { label: "Kind", render: (g) => g.kind === "smart" ? h("span", { class: "badge ok" }, "Smart") : h("span", { class: "badge" }, "Static") },
      { label: "Devices", cls: "num", render: (g) => g.deviceCount },
      { label: "Source", render: (g) => sourceTag(g.managedBy) },
      { label: "Updated", render: (g) => fmtTime(g.updatedAt) },
    ], groups, { onRow: (g) => go("/groups/" + g.id),
      empty: emptyState("No groups yet", "Smart groups such as \"iPhones below iOS 17\" or \"laptops without encryption\" keep configuration on target as the fleet changes.",
        can("admin") ? link("/groups/new", h("span", { class: "btn primary" }, "Create a group")) : null) })));
}

export async function edit(id) {
  const isNew = id === "new";
  const [res, schema] = await Promise.all([
    isNew ? { group: { name: "", description: "", kind: "smart", rules: null }, members: [] } : api("GET", "/groups/" + id),
    api("GET", "/groups/schema"),
  ]);
  const g = res.group;
  const ro = !can("admin");
  let kind = g.kind;
  const name = input(g.name, { placeholder: "iPhones below iOS 17", disabled: ro });
  const desc = input(g.description, { placeholder: "Optional", disabled: ro });
  const builder = ruleBuilder(g.rules || { match: "all", conditions: [{ field: "platform", op: "in", value: ["ios", "ipados"] }, { field: "osVersion", op: "version_lt", value: "17.0" }] }, schema);
  const preview = h("div");
  const runPreview = async () => {
    try {
      const r = await api("POST", "/groups/preview", builder.get());
      preview.replaceChildren(h("p", { class: "muted" }, r.total === 1 ? "1 device matches right now." : r.total + " devices match right now."),
        table([
          { label: "Device", render: (d) => d.name || d.serial || d.nativeId.slice(0, 16) },
          { label: "Platform", render: (d) => h("span", {}, platformTag(d.platform), " ", d.osVersion) },
          { label: "Ownership", render: (d) => ownershipTag(d.ownership) },
          { label: "Compliance", render: (d) => complianceTag(d.compliant) },
        ], r.devices.slice(0, 50), { onRow: (d) => go("/devices/" + d.id), empty: h("div", { class: "empty" }, "No device matches these rules yet.") }));
    } catch (e) { preview.replaceChildren(errorNotice(e)); }
  };
  const rulesPanel = panel({ title: "Rules", sub: "Membership is computed from these conditions." },
    body(h("div", { class: "stack" }, builder, h("div", {}, h("button", { class: "btn", onclick: runPreview }, "Preview matching devices")), preview)));
  const kindSel = segmented([["smart", "Smart: rule based"], ["static", "Static: hand picked"]], kind, (v) => { kind = v; rulesPanel.hidden = v !== "smart"; });
  rulesPanel.hidden = kind !== "smart";
  const err = h("div");
  const save = h("button", { class: "btn primary", disabled: ro, onclick: async () => {
    err.replaceChildren();
    try {
      const b = { name: name.get(), description: desc.get(), kind };
      if (kind === "smart") b.rules = builder.get();
      const saved = await api(isNew ? "POST" : "PUT", isNew ? "/groups" : "/groups/" + id, b);
      toast(isNew ? "Group created" : "Group saved");
      await groupsCache(true);
      go("/groups/" + saved.id + "?_=" + Date.now());
    } catch (e) { err.replaceChildren(errorNotice(e)); }
  } }, isNew ? "Create group" : "Save");
  const del = !isNew && !ro ? h("button", { class: "btn quiet", onclick: async () => {
    if (!(await confirmDialog({ title: `Delete "${g.name}"?`, body: "Policies and blueprints that target it stop applying to its devices.", confirmLabel: "Delete group", danger: true }))) return;
    await api("DELETE", "/groups/" + id); await groupsCache(true); toast("Group deleted"); go("/groups");
  } }, "Delete") : null;

  if (!isNew && g.kind === "smart") setTimeout(runPreview);
  return page({ title: isNew ? "New group" : g.name, crumb: link("/groups", "Groups"), actions: [del, save].filter(Boolean) },
    managedNote(g.managedBy),
    panel({ title: "About" }, body(h("div", { class: "form" }, field("Name", name), field("Description", desc), field("Kind", kindSel, kind === "static" && !isNew ? "Switching to smart drops hand-picked members; rules decide instead." : null)))),
    rulesPanel,
    !isNew && g.kind === "static" ? panel({ title: "Members", sub: g.deviceCount + " devices. Add devices from the device list (select, then Add to group) or a device's page." },
      body(link("/devices?group=" + g.id, h("span", { class: "btn" }, "View member devices")))) : null,
    h("div", { class: "block" }, err, h("div", { class: "row", style: { marginTop: "14px" } }, save)));
}
