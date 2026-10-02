import { h, api, state, page, panel, body, link, can, emptyState, go, toast, confirmDialog, notice, icon, groupsCache } from "../core.js";
import { input, field, picklist, select } from "../forms.js";
import { policyEditor, saveBar } from "../policy-form.js";
import { paramsForm, commandLabel } from "../commands.js";
import { managedNote, sourceTag, listPanel, updatedCell, pills } from "./policies.js";

export async function list() {
  const [res, groups] = await Promise.all([api("GET", "/blueprints"), groupsCache()]);
  const gname = Object.fromEntries(groups.map((g) => [g.id, g.name]));
  return page({ title: "Blueprints", sub: "A bundle of policies, settings and onboarding steps for the groups it targets. Each device is onboarded once, when it first falls in scope.",
    actions: can("admin") && res.blueprints.length ? [link("/blueprints/new", h("span", { class: "btn primary" }, icon("plus"), "New blueprint"))] : [] },
  listPanel({ items: res.blueprints, noun: "blueprint", label: "Blueprints", text: (b) => b.name + " " + (b.description || ""),
    columns: [
      { label: "Blueprint", render: (b) => h("div", {}, h("div", { class: "primary-cell" }, b.name), b.description ? h("div", { class: "secondary-cell" }, b.description) : null) },
      { label: "Targets", render: (b) => pills(b.groupIds.map((g) => gname[g]).filter(Boolean), 2, "No groups") },
      { label: "Onboarding", render: (b) => pills(((b.spec || {}).onEnroll || []).map((s) => commandLabel(s.type)), 3, "None") },
      { label: "Priority", cls: "num", render: (b) => b.priority },
      { label: "Source", render: (b) => sourceTag(b.managedBy) },
      { label: "Updated", render: (b) => updatedCell(b.updatedAt) },
    ], onRow: (b) => go("/blueprints/" + b.id),
    empty: emptyState("No blueprints yet", "For example: every corporate laptop gets the baseline policy, the office Wi-Fi and an OS update on day one.",
      can("admin") ? link("/blueprints/new", h("span", { class: "btn primary" }, icon("plus"), "Create a blueprint")) : null, "blueprint") }));
}

const STEP_TYPES = () => state.catalogue.filter((c) => c.type !== "wipe" && c.type !== "retire");

function stepRow(s = { type: "refresh" }) {
  const types = STEP_TYPES();
  const typeSel = select(types.map((c) => [c.type, commandLabel(c.type)]), s.type, { "aria-label": "Step" });
  const host = h("div");
  const note = h("div", { class: "help" });
  let pf;
  const draw = (init) => {
    pf = paramsForm(typeSel.value, undefined, init || {});
    host.replaceChildren(pf);
    const spec = types.find((c) => c.type === typeSel.value);
    note.textContent = spec ? (spec.personal ? "" : "Skipped on personal devices. ") + "Runs on " + spec.platforms.join(", ") + "." : "";
  };
  typeSel.addEventListener("change", () => draw());
  draw(s.params);
  const el = h("div", { class: "form" }, field("Step", typeSel, note), host);
  el.get = () => { const p = pf.get(); return Object.keys(p).length ? { type: typeSel.value, params: p } : { type: typeSel.value }; };
  return el;
}

// stepList(items): numbered onboarding step cards with an add button (stepList.add) and
// icon remove buttons. get() returns the steps in order.
function stepList(items) {
  const list = h("div", { class: "repeater step-list" });
  const rows = [];
  const empty = h("div", { class: "payload-empty" }, "No onboarding steps. Devices are managed, but no command runs on first enrollment.");
  const sync = () => {
    empty.hidden = rows.length > 0;
    rows.forEach((r, i) => { r.num.textContent = String(i + 1); r.rm.setAttribute("aria-label", "Remove step " + (i + 1)); });
  };
  const add = (s) => {
    const row = stepRow(s);
    const num = h("span", { class: "bp-step-num", "aria-hidden": "true" });
    const rm = h("button", { type: "button", class: "iconbtn", title: "Remove step", onclick: () => { rows.splice(rows.indexOf(wrap), 1); wrap.remove(); sync(); } }, icon("trash"));
    const wrap = h("div", { class: "item step" }, num, h("div", { class: "item-main" }, row), rm);
    wrap.get = row.get; wrap.num = num; wrap.rm = rm;
    rows.push(wrap); list.append(wrap); sync();
    return wrap;
  };
  (items || []).forEach(add);
  const addBtn = h("button", { type: "button", class: "btn small", onclick: () => add({ type: "refresh" }) }, icon("plus"), "Add step");
  const el = h("div", { class: "stack" }, empty, list);
  el.addBtn = addBtn;
  el.get = () => rows.map((r) => r.get()).filter((v) => v !== null && v !== undefined);
  sync();
  return el;
}

export async function edit(id) {
  const isNew = id === "new";
  const [b, groups, pols] = await Promise.all([
    isNew ? { name: "", description: "", priority: 100, spec: {}, groupIds: [] } : api("GET", "/blueprints/" + id),
    groupsCache(true), api("GET", "/policies"),
  ]);
  const spec = b.spec || {};
  const ro = !can("admin");
  const name = input(b.name, { placeholder: "Standard laptop", disabled: ro });
  const desc = input(b.description, { placeholder: "Optional", disabled: ro });
  const prio = input(b.priority, { type: "number", min: 1, disabled: ro });
  const targets = picklist(groups.map((g) => [g.id, g.name, g.kind === "smart" ? "smart, " + g.deviceCount + " devices" : g.deviceCount + " devices"]), b.groupIds, "Create a group first.");
  const policies = picklist(pols.policies.map((p) => [p.name, p.name, "priority " + p.priority]), spec.policies || [], "No policies yet. You can still add settings below.");
  const editor = policyEditor(spec.policy || {});
  const steps = stepList(spec.onEnroll || []);

  const bar = saveBar({
    isNew, createLabel: "Create blueprint", saveLabel: "Save and push",
    snapshot: () => JSON.stringify([name.value, desc.value, prio.value, targets.get(), policies.get(), editor.get(), steps.get()]),
    onDiscard: () => (isNew ? go("/blueprints") : window.dispatchEvent(new Event("vs:navigate"))),
    onSave: async () => {
      if (!name.get()) { name.focus(); throw new Error("Give the blueprint a name."); }
      const doc = editor.get();
      const s = {};
      const pl = policies.get(); if (pl.length) s.policies = pl;
      if (Object.keys(doc).length) s.policy = doc;
      const st = steps.get(); if (st.length) s.onEnroll = st;
      const saved = await api(isNew ? "POST" : "PUT", isNew ? "/blueprints" : "/blueprints/" + id,
        { name: name.get(), description: desc.get(), priority: prio.get() || 100, groupIds: targets.get(), spec: s });
      toast(isNew ? "Blueprint created" : "Blueprint saved and pushed");
      if (isNew) go("/blueprints/" + saved.id);
    },
  });
  const del = !isNew && !ro ? h("button", { class: "btn", onclick: async () => {
    if (!(await confirmDialog({ title: `Delete "${b.name}"?`, body: "Its settings stop applying. Onboarding steps already run are not undone.", confirmLabel: "Delete blueprint", danger: true }))) return;
    bar.dispose();
    await api("DELETE", "/blueprints/" + id); toast("Blueprint deleted"); go("/blueprints");
  } }, icon("trash"), "Delete") : null;

  const extra = h("details", { class: "disclosure" },
    h("summary", {}, h("div", { class: "grow" }, h("h2", {}, "Additional settings"), h("p", {}, "Settings written directly into this blueprint, on top of the policies above.")), icon("chevron", "chev")),
    h("div", { class: "disclosure-body" }, editor));
  extra.open = Object.keys(spec.policy || {}).length > 0;

  const root = page({ title: isNew ? "New blueprint" : b.name, crumb: link("/blueprints", "Blueprints"), actions: [del].filter(Boolean),
    sub: isNew ? "Bundle policies, settings and first-day steps, then point them at groups." : "Last changed " + new Date(b.updatedAt).toLocaleDateString() + ". Devices are onboarded once, when they first fall in scope." },
  managedNote(b.managedBy),
  ro ? notice("gold", "You can view blueprints; only admins can change them.") : null,
  h("div", { class: "grid2" },
    panel({ title: "About", sub: "Name, purpose and precedence." }, body(h("div", { class: "form" }, field("Name", name), field("Description", desc), field("Priority", prio, "Lower wins when blueprints and policies overlap.")))),
    panel({ title: "Targets", sub: "Devices in these groups get this blueprint." }, body(targets))),
  panel({ title: "Policies to include", sub: "Reuse existing policies by name." }, body(policies)),
  extra,
  panel({ title: "Onboarding steps", sub: "Commands that run once on each device when it first falls in scope. Erase and retire are not allowed here.", actions: [steps.addBtn] }, body(steps)),
  ro ? null : bar.el);
  if (!ro) bar.attach(root);
  return root;
}
