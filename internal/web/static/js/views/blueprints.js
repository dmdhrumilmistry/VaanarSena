import { h, api, state, page, panel, body, link, table, fmtTime, can, emptyState, go, toast, confirmDialog, errorNotice, icon, groupsCache } from "../core.js";
import { input, field, picklist, select, repeater } from "../forms.js";
import { policyEditor } from "../policy-form.js";
import { paramsForm, commandLabel } from "../commands.js";
import { managedNote, sourceTag } from "./policies.js";

export async function list() {
  const [res, groups] = await Promise.all([api("GET", "/blueprints"), groupsCache()]);
  const gname = Object.fromEntries(groups.map((g) => [g.id, g.name]));
  return page({ title: "Blueprints", sub: "A bundle of policies, settings and onboarding steps for the groups it targets. Each device is onboarded once, when it first falls in scope.",
    actions: can("admin") ? [link("/blueprints/new", h("span", { class: "btn primary" }, icon("plus"), "New blueprint"))] : [] },
    h("section", { class: "panel" }, table([
      { label: "Blueprint", render: (b) => h("div", {}, h("div", { class: "primary-cell" }, b.name), b.description ? h("div", { class: "secondary-cell" }, b.description) : null) },
      { label: "Targets", render: (b) => b.groupIds.map((g) => gname[g]).filter(Boolean).join(", ") || h("span", { class: "muted" }, "No groups") },
      { label: "Onboarding", render: (b) => ((b.spec || {}).onEnroll || []).map((s) => commandLabel(s.type)).join(", ") || h("span", { class: "muted" }, "None") },
      { label: "Priority", cls: "num", render: (b) => b.priority },
      { label: "Source", render: (b) => sourceTag(b.managedBy) },
      { label: "Updated", render: (b) => fmtTime(b.updatedAt) },
    ], res.blueprints, { onRow: (b) => go("/blueprints/" + b.id),
      empty: emptyState("No blueprints yet", "For example: every corporate laptop gets the baseline policy, the office Wi-Fi and an OS update on day one.",
        can("admin") ? link("/blueprints/new", h("span", { class: "btn primary" }, "Create a blueprint")) : null) })));
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
  const prio = input(b.priority, { type: "number", min: 1, class: "short", disabled: ro });
  const targets = picklist(groups.map((g) => [g.id, g.name, g.kind === "smart" ? "smart, " + g.deviceCount + " devices" : g.deviceCount + " devices"]), b.groupIds, "Create a group first.");
  const policies = picklist(pols.policies.map((p) => [p.name, p.name, "priority " + p.priority]), spec.policies || [], "No policies yet. You can still add settings below.");
  const editor = policyEditor(spec.policy || {});
  const steps = repeater(spec.onEnroll || [], stepRow, { addLabel: "Add step", empty: "No onboarding steps.", newItem: () => ({ type: "refresh" }) });
  const err = h("div");
  const save = h("button", { class: "btn primary", disabled: ro, onclick: async () => {
    err.replaceChildren();
    try {
      const doc = editor.get();
      const s = {};
      const pl = policies.get(); if (pl.length) s.policies = pl;
      if (Object.keys(doc).length) s.policy = doc;
      const st = steps.get(); if (st.length) s.onEnroll = st;
      const saved = await api(isNew ? "POST" : "PUT", isNew ? "/blueprints" : "/blueprints/" + id,
        { name: name.get(), description: desc.get(), priority: prio.get() || 100, groupIds: targets.get(), spec: s });
      toast(isNew ? "Blueprint created" : "Blueprint saved and pushed");
      if (isNew) go("/blueprints/" + saved.id);
    } catch (e) { err.replaceChildren(errorNotice(e)); err.scrollIntoView({ block: "center" }); }
  } }, isNew ? "Create blueprint" : "Save and push");
  const del = !isNew && !ro ? h("button", { class: "btn quiet", onclick: async () => {
    if (!(await confirmDialog({ title: `Delete "${b.name}"?`, body: "Its settings stop applying. Onboarding steps already run are not undone.", confirmLabel: "Delete blueprint", danger: true }))) return;
    await api("DELETE", "/blueprints/" + id); toast("Blueprint deleted"); go("/blueprints");
  } }, "Delete") : null;

  return page({ title: isNew ? "New blueprint" : b.name, crumb: link("/blueprints", "Blueprints"), actions: [del, save].filter(Boolean) },
    managedNote(b.managedBy),
    h("div", { class: "grid2 block" },
      panel({ title: "About" }, body(h("div", { class: "form" }, field("Name", name), field("Description", desc), field("Priority", prio, "Lower wins when blueprints and policies overlap.")))),
      panel({ title: "Targets", sub: "Devices in these groups get this blueprint." }, body(targets))),
    panel({ title: "Policies to include", sub: "Reuse existing policies by name." }, body(policies)),
    h("div", { class: "block" }, h("h2", { class: "section-title" }, "Additional settings"), editor),
    panel({ title: "Onboarding steps", sub: "Commands that run once on each device when it first falls in scope. Erase and retire are not allowed here." }, body(steps)),
    h("div", { class: "block" }, err, h("div", { class: "row", style: { marginTop: "14px" } }, save)));
}
