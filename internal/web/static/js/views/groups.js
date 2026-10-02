import { h, api, page, panel, body, link, table, can, emptyState, go, toast, confirmDialog, errorNotice, notice, icon, groupsCache,
  platformTag, ownershipTag, complianceTag, withBusy, skeleton } from "../core.js";
import { input, field, segmented } from "../forms.js";
import { ruleBuilder } from "../rules.js";
import { saveBar } from "../policy-form.js";
import { managedNote, sourceTag, listPanel, updatedCell } from "./policies.js";

export const kindBadge = (kind) => kind === "smart" ? h("span", { class: "badge info" }, "Smart") : h("span", { class: "badge" }, "Static");

const countRules = (r) => r ? (r.conditions || []).length + (r.rules || []).reduce((n, x) => n + countRules(x), 0) : 0;

export async function list() {
  const groups = await groupsCache(true);
  return page({ title: "Groups", sub: "Static groups are curated by hand. Smart groups fill themselves from rules and update every minute.",
    actions: can("admin") && groups.length ? [link("/groups/new", h("span", { class: "btn primary" }, icon("plus"), "New group"))] : [] },
  listPanel({ items: groups, noun: "group", label: "Groups", text: (g) => g.name + " " + (g.description || ""),
    columns: [
      { label: "Group", render: (g) => h("div", {}, h("div", { class: "primary-cell" }, g.name), g.description ? h("div", { class: "secondary-cell" }, g.description) : null) },
      { label: "Type", render: (g) => kindBadge(g.kind) },
      { label: "Membership", render: (g) => g.kind === "smart" ? h("span", { class: "muted" }, countRules(g.rules) + (countRules(g.rules) === 1 ? " condition" : " conditions")) : h("span", { class: "muted" }, "Hand picked") },
      { label: "Devices", cls: "num", render: (g) => g.deviceCount },
      { label: "Source", render: (g) => sourceTag(g.managedBy) },
      { label: "Updated", render: (g) => updatedCell(g.updatedAt) },
    ], onRow: (g) => go("/groups/" + g.id),
    empty: emptyState("No groups yet", "Smart groups such as \"iPhones below iOS 17\" or \"laptops without encryption\" keep configuration on target as the fleet changes.",
      can("admin") ? link("/groups/new", h("span", { class: "btn primary" }, icon("plus"), "Create a group")) : null, "groups") }));
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

  // Live preview: re-runs shortly after the rules change, or on demand.
  const preview = h("div");
  const total = h("span", { class: "badge" }, "-");
  let seq = 0, timer = null;
  const runPreview = async (auto) => {
    clearTimeout(timer);
    const my = ++seq;
    let rules;
    try { rules = builder.get(); } catch (e) {
      if (my === seq) { total.textContent = "-"; preview.replaceChildren(auto ? h("p", { class: "help" }, "Fix the rules to see matching devices.") : errorNotice(e)); }
      return;
    }
    if (!preview.firstChild) preview.replaceChildren(skeleton(2));
    try {
      const r = await api("POST", "/groups/preview", rules);
      if (my !== seq) return;
      total.textContent = String(r.total);
      preview.replaceChildren(h("p", { class: "muted preview-count" }, r.total === 1 ? "1 device matches right now." : r.total + " devices match right now."),
        ...(r.total > 50 ? [h("p", { class: "help" }, "Showing the first 50.")] : []),
        table([
          { label: "Device", render: (d) => d.name || d.serial || d.nativeId.slice(0, 16) },
          { label: "Platform", render: (d) => h("span", { class: "plat-os" }, platformTag(d.platform), " ", d.osVersion) },
          { label: "Ownership", render: (d) => ownershipTag(d.ownership) },
          { label: "Compliance", render: (d) => complianceTag(d.compliant) },
        ], r.devices.slice(0, 50), { onRow: (d) => go("/devices/" + d.id), label: "Matching devices", empty: h("div", { class: "empty" }, "No device matches these rules yet.") }));
    } catch (e) { if (my === seq) { total.textContent = "-"; preview.replaceChildren(auto ? h("p", { class: "help" }, "Preview unavailable: " + e.message) : errorNotice(e)); } }
  };
  const refresh = h("button", { type: "button", class: "btn small", onclick: (e) => withBusy(e.currentTarget, () => runPreview(false)) }, icon("refresh"), "Refresh preview");
  const previewPanel = panel({ title: "Matching devices", sub: "Devices that match the rules right now, updated as you edit.", actions: [total, refresh] }, body(preview));

  const rulesPanel = panel({ title: "Rules", sub: "Membership is computed from these conditions." }, body(builder));
  rulesPanel.addEventListener("input", () => { clearTimeout(timer); timer = setTimeout(() => runPreview(true), 700); });
  rulesPanel.addEventListener("click", () => { clearTimeout(timer); timer = setTimeout(() => runPreview(true), 700); });
  const kindSel = segmented([["smart", "Smart: rule based"], ["static", "Static: hand picked"]], kind, (v) => { kind = v; rulesPanel.hidden = v !== "smart"; previewPanel.hidden = v !== "smart"; if (v === "smart") runPreview(true); });
  kindSel.setAttribute("aria-label", "Group type");
  rulesPanel.hidden = kind !== "smart";
  previewPanel.hidden = kind !== "smart";

  const bar = saveBar({
    isNew, createLabel: "Create group", saveLabel: "Save changes",
    snapshot: () => JSON.stringify([name.value, desc.value, kind, kind === "smart" ? builder.get() : null]),
    onDiscard: () => (isNew ? go("/groups") : window.dispatchEvent(new Event("vs:navigate"))),
    onSave: async () => {
      const b = { name: name.get(), description: desc.get(), kind };
      if (kind === "smart") b.rules = builder.get();
      const saved = await api(isNew ? "POST" : "PUT", isNew ? "/groups" : "/groups/" + id, b);
      toast(isNew ? "Group created" : "Group saved");
      await groupsCache(true);
      bar.dispose();
      go("/groups/" + saved.id + "?_=" + Date.now());
    },
  });
  const del = !isNew && !ro ? h("button", { class: "btn", onclick: async () => {
    if (!(await confirmDialog({ title: `Delete "${g.name}"?`, body: "Policies and blueprints that target it stop applying to its devices.", confirmLabel: "Delete group", danger: true }))) return;
    bar.dispose();
    await api("DELETE", "/groups/" + id); await groupsCache(true); toast("Group deleted"); go("/groups");
  } }, icon("trash"), "Delete") : null;

  if (kind === "smart") setTimeout(() => runPreview(true));
  const root = page({ title: isNew ? "New group" : g.name, crumb: link("/groups", "Groups"), actions: [del].filter(Boolean),
    sub: isNew ? "Pick how devices join: by rules that keep themselves current, or by hand." : g.deviceCount + (g.deviceCount === 1 ? " device" : " devices") + " in this group." },
  managedNote(g.managedBy),
  ro ? notice("gold", "You can view groups; only admins can change them.") : null,
  panel({ title: "About", sub: "How the group is named and filled." }, body(h("div", { class: "form" }, field("Name", name), field("Description", desc),
    field("Type", kindSel, kind === "static" && !isNew ? "Switching to smart drops hand-picked members; rules decide instead." : null)))),
  rulesPanel,
  previewPanel,
  !isNew && g.kind === "static" ? panel({ title: "Members", sub: g.deviceCount + " devices. Add devices from the device list (select, then Add to group) or a device's page." },
    body(link("/devices?group=" + g.id, h("span", { class: "btn" }, "View member devices")))) : null,
  ro ? null : bar.el);
  if (!ro) bar.attach(root);
  return root;
}
