import { h, api, page, panel, body, link, table, ago, fmtTime, can, emptyState, go, toast, confirmDialog, notice, icon, groupsCache } from "../core.js";
import { input, field, picklist } from "../forms.js";
import { policyEditor, saveBar } from "../policy-form.js";

export const managedNote = (m) => m && m !== "-" ? notice("gold", `Managed by manifests from "${m}". Changes you save here make the console its owner; the next apply from that source takes it back.`) : null;
export const sourceTag = (m) => m && m !== "-" ? h("span", { class: "badge" }, "Manifest: " + m) : h("span", { class: "muted" }, "Console");

const SECTIONS = { passcode: "Passcode", encryption: "Encryption", restrictions: "Restrictions", wifi: "Wi-Fi", osUpdates: "OS updates", apps: "Apps", custom: "Custom payloads" };

// pills(names, max): a short run of neutral badges with a "+n" overflow.
export function pills(names, max = 3, none = "None") {
  if (!names.length) return h("span", { class: "muted" }, none);
  const shown = names.slice(0, max);
  return h("div", { class: "pills" }, shown.map((n) => h("span", { class: "badge" }, n)), names.length > max ? h("span", { class: "pills-more" }, "+" + (names.length - max)) : null);
}

export const updatedCell = (t) => h("span", { class: "muted", title: fmtTime(t) }, ago(t));

// listPanel({ items, columns, onRow, noun, text, empty }): searchable table card. text(item)
// returns the string the search box matches against.
export function listPanel({ items, columns, onRow, noun, plural = noun + "s", text, empty, label }) {
  if (!items.length) return h("section", { class: "panel" }, empty);
  const host = h("div");
  const count = h("span", { class: "help list-count" });
  const search = h("input", { class: "input", type: "search", placeholder: "Search " + plural, "aria-label": "Search " + plural, autocomplete: "off" });
  const draw = () => {
    const q = search.value.trim().toLowerCase();
    const rows = q ? items.filter((i) => text(i).toLowerCase().includes(q)) : items;
    count.textContent = rows.length === items.length ? items.length + " " + (items.length === 1 ? noun : plural) : rows.length + " of " + items.length;
    host.replaceChildren(table(columns, rows, { onRow, label, empty: emptyState("No " + plural + " match", "Try a different name.", null, "search") }));
  };
  search.addEventListener("input", draw);
  draw();
  return h("section", { class: "panel" },
    h("div", { class: "list-toolbar" }, h("div", { class: "search-field" }, icon("search"), search), count), host);
}

const newBtn = (href, label) => link(href, h("span", { class: "btn primary" }, icon("plus"), label));

export async function list() {
  const [res, groups] = await Promise.all([api("GET", "/policies"), groupsCache()]);
  const gname = Object.fromEntries(groups.map((g) => [g.id, g.name]));
  return page({ title: "Policies", sub: "Settings written once and translated for each platform. When several apply, the lower priority number wins.",
    actions: can("admin") && res.policies.length ? [newBtn("/policies/new", "New policy")] : [] },
  listPanel({ items: res.policies, noun: "policy", plural: "policies", label: "Policies", text: (p) => p.name + " " + (p.description || ""),
    columns: [
      { label: "Policy", render: (p) => h("div", {}, h("div", { class: "primary-cell" }, p.name), p.description ? h("div", { class: "secondary-cell" }, p.description) : null) },
      { label: "Covers", render: (p) => pills(Object.keys(p.document || {}).map((k) => SECTIONS[k] || k), 3, "Nothing yet") },
      { label: "Applies to", render: (p) => p.groupIds.length ? pills(p.groupIds.map((g) => gname[g]).filter(Boolean), 2, "No groups") : h("span", { class: "muted" }, p.deviceIds.length ? p.deviceIds.length + " devices" : "No groups") },
      { label: "Priority", cls: "num", render: (p) => p.priority },
      { label: "Source", render: (p) => sourceTag(p.managedBy) },
      { label: "Updated", render: (p) => updatedCell(p.updatedAt) },
    ], onRow: (p) => go("/policies/" + p.id),
    empty: emptyState("No policies yet", "Start with a baseline: a passcode, encryption and a few restrictions for corporate devices.",
      can("admin") ? link("/policies/new", h("span", { class: "btn primary" }, icon("plus"), "Create a policy")) : null, "policy") }));
}

// Sticky left navigation for the editor sections: shows each section's On/Off state and
// follows the scroll position. entries() returns [{ id, label, isOn() | text() }].
function sectionNav(entries) {
  const links = new Map();
  const nav = h("nav", { class: "editor-nav", "aria-label": "Sections" });
  const reduce = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  const jump = (id) => { const t = document.getElementById(id); if (t) t.scrollIntoView({ behavior: reduce ? "auto" : "smooth", block: "start" }); };
  function build() {
    links.clear();
    nav.replaceChildren(...entries().map((s) => {
      const state = h("span", { class: "nav-state" });
      const a = h("a", { href: "#" + s.id, onclick: (e) => { e.preventDefault(); jump(s.id); } }, h("span", {}, s.label), state);
      links.set(s.id, { a, state, s });
      return a;
    }));
    paint();
  }
  function paint() {
    for (const { state, s } of links.values()) {
      const t = s.text ? s.text() : (s.isOn() ? "On" : "Off");
      state.textContent = t;
      state.className = "nav-state" + (t === "On" ? " on" : "");
    }
  }
  let queued = false;
  function spy() {
    queued = false;
    if (!nav.isConnected) { window.removeEventListener("scroll", onScroll); return; }
    let cur = null;
    for (const id of links.keys()) {
      const t = document.getElementById(id);
      if (t && t.getBoundingClientRect().top <= 150) cur = id;
    }
    if (!cur && links.size) cur = links.keys().next().value;
    if (links.size && window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 4) cur = [...links.keys()].pop();
    for (const [id, { a }] of links) { if (id === cur) a.setAttribute("aria-current", "true"); else a.removeAttribute("aria-current"); }
  }
  function onScroll() { if (!queued) { queued = true; requestAnimationFrame(spy); } }
  window.addEventListener("scroll", onScroll, { passive: true });
  build();
  setTimeout(spy, 50);
  nav.refresh = () => { if (entries().length !== links.size) build(); else paint(); spy(); };
  return nav;
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
  const prio = input(p.priority, { type: "number", min: 1, disabled: ro });
  const targets = picklist(groups.map((g) => [g.id, g.name, g.kind === "smart" ? "smart, " + g.deviceCount + " devices" : g.deviceCount + " devices"]), p.groupIds, "Create a group first, then assign this policy to it.");
  const editor = policyEditor(p.document);

  const bar = saveBar({
    isNew, createLabel: "Create policy", saveLabel: "Save and push",
    snapshot: () => JSON.stringify([name.value, desc.value, prio.value, targets.get(), editor.get()]),
    onDiscard: () => (isNew ? go("/policies") : window.dispatchEvent(new Event("vs:navigate"))),
    onSave: async () => {
      if (!name.get()) { name.focus(); throw new Error("Give the policy a name."); }
      const doc = editor.get();
      const saved = await api(isNew ? "POST" : "PUT", isNew ? "/policies" : "/policies/" + id,
        { name: name.get(), description: desc.get(), priority: prio.get() || 100, document: doc, groupIds: targets.get(), deviceIds: p.deviceIds });
      toast(isNew ? "Policy created and pushed to its devices" : "Policy saved and pushed to its devices");
      if (isNew) go("/policies/" + saved.id);
    },
  });
  const del = !isNew && !ro ? h("button", { class: "btn", onclick: async () => {
    if (!(await confirmDialog({ title: `Delete "${p.name}"?`, body: "Devices it applied to receive their remaining policies again.", confirmLabel: "Delete policy", danger: true }))) return;
    bar.dispose();
    await api("DELETE", "/policies/" + id); toast("Policy deleted"); go("/policies");
  } }, icon("trash"), "Delete") : null;

  const about = panel({ title: "About", sub: "Name, purpose and precedence." }, body(h("div", { class: "form" },
    field("Name", name), field("Description", desc), field("Priority", prio, "1 is the most important. Defaults to 100."))));
  about.id = "sec-about";
  const applies = panel({ title: "Applies to", sub: "Groups whose devices receive this policy." }, body(targets));
  applies.id = "sec-targets";
  const nav = sectionNav(() => [
    { id: "sec-about", label: "About", text: () => "" },
    { id: "sec-targets", label: "Applies to", text: () => { const n = targets.get().length; return n ? String(n) : "None"; } },
    ...editor.sections(),
  ]);
  const root = page({ title: isNew ? "New policy" : p.name, crumb: link("/policies", "Policies"), actions: [del].filter(Boolean),
    sub: isNew ? "Choose what to manage. Anything you leave off is not touched on devices." : "Last updated " + fmtTime(p.updatedAt) + ". Each platform applies the settings it supports." },
  managedNote(p.managedBy),
  ro ? notice("gold", "You can view policies; only admins can change them.") : null,
  h("div", { class: "editor-layout" }, nav, h("div", { class: "editor-main" }, about, applies, editor)),
  ro ? null : bar.el);
  if (!ro) bar.attach(root);
  for (const t of ["input", "change", "click"]) root.addEventListener(t, () => setTimeout(nav.refresh));
  return root;
}
