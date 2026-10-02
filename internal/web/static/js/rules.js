// Visual builder for smart group rules: nested any/all groups of conditions.
import { h, icon } from "./core.js";
import { select, jsonEditor, segmented } from "./forms.js";

const FIELD_LABELS = {
  platform: "Platform", ownership: "Ownership", status: "Status", name: "Device name", serial: "Serial number",
  model: "Model", osVersion: "OS version", assignee: "User", compliant: "Compliance", tags: "Tags",
  enrolledDays: "Days since enrollment", lastSeenDays: "Days since last check-in",
};
const OP_LABELS = {
  eq: "is", ne: "is not", in: "is one of", not_in: "is none of", contains: "contains", not_contains: "does not contain",
  starts_with: "starts with", ends_with: "ends with", matches: "matches regex", gt: "is more than", gte: "is at least",
  lt: "is less than", lte: "is at most", version_lt: "version below", version_lte: "version at most",
  version_gt: "version above", version_gte: "version at least", exists: "is set", not_exists: "is not set",
};
const VALUES = {
  platform: ["ios", "ipados", "macos", "windows", "android", "chromeos", "linux"],
  ownership: ["corporate", "personal"],
  status: ["enrolling", "enrolled", "retired", "wiped"],
  compliant: ["true", "false"],
};
const NUMERIC = new Set(["enrolledDays", "lastSeenDays"]);
const FACT = "__fact";

function coerce(field, op, raw) {
  if (op === "exists" || op === "not_exists") return undefined;
  if (op === "in" || op === "not_in") return raw.split(",").map((s) => s.trim()).filter(Boolean);
  if (field === "compliant" || raw === "true" || raw === "false") {
    if (raw === "true") return true;
    if (raw === "false") return false;
  }
  if ((NUMERIC.has(field) || ["gt", "gte", "lt", "lte"].includes(op)) && raw !== "" && !isNaN(Number(raw))) return Number(raw);
  return raw;
}

function condRow(c, schema, onRemove) {
  const isFact = c.field && c.field.startsWith("facts.");
  const fieldSel = select([...schema.fields.map((f) => [f, FIELD_LABELS[f] || f]), [FACT, "Inventory fact..."]], isFact ? FACT : c.field || "platform", { "aria-label": "Field" });
  const factPath = h("input", { class: "input factpath", placeholder: "facts.linux.diskEncrypted", value: isFact ? c.field : "facts.", "aria-label": "Inventory fact path" });
  const opSel = select(schema.ops.map((o) => [o, OP_LABELS[o] || o]), c.op || "eq", { "aria-label": "Operator" });
  const valueHost = h("div");
  let valueEl;
  const rm = h("button", { type: "button", class: "iconbtn", "aria-label": "Remove condition", title: "Remove condition", onclick: onRemove }, icon("x"));
  const initial = Array.isArray(c.value) ? c.value.join(", ") : c.value === undefined ? "" : String(c.value);
  const drawValue = (keep) => {
    const f = fieldSel.value, op = opSel.value;
    const prev = keep !== undefined ? keep : valueEl ? valueEl.value : initial;
    if (op === "exists" || op === "not_exists") { valueEl = null; valueHost.replaceChildren(h("span", { class: "muted small" }, "No value needed")); return; }
    const opts = VALUES[f];
    if (opts && op !== "in" && op !== "not_in" && op !== "matches") {
      valueEl = select(opts.map((v) => [v, f === "compliant" ? (v === "true" ? "Compliant" : "Not compliant") : v]), prev || opts[0], { "aria-label": "Value" });
    } else {
      const ph = op === "in" || op === "not_in" ? (opts ? opts.slice(0, 2).join(", ") : "a, b, c") : NUMERIC.has(f) ? "7" : f === "osVersion" ? "17.0" : "value";
      valueEl = h("input", { class: "input", placeholder: ph, value: prev, "aria-label": "Value", type: NUMERIC.has(f) ? "number" : "text" });
    }
    valueHost.replaceChildren(valueEl);
  };
  const syncFact = () => { factPath.hidden = fieldSel.value !== FACT; };
  fieldSel.addEventListener("change", () => { syncFact(); drawValue(""); });
  opSel.addEventListener("change", () => drawValue());
  syncFact();
  drawValue(initial);
  const el = h("div", { class: "stack" }, h("div", { class: "cond" }, fieldSel, opSel, valueHost, rm), factPath);
  el.get = () => {
    const field = fieldSel.value === FACT ? factPath.value.trim() : fieldSel.value;
    const out = { field, op: opSel.value };
    const v = coerce(field, opSel.value, valueEl ? String(valueEl.value).trim() : "");
    if (v !== undefined) out.value = v;
    return out;
  };
  return el;
}

function groupBox(rule, schema, depth, onRemove) {
  let match = rule.match || "all";
  const items = h("div", { class: "stack" });
  const children = [];
  const addCond = (c) => {
    const row = condRow(c || {}, schema, () => { children.splice(children.indexOf(row), 1); row.remove(); });
    children.push(row); items.append(row);
  };
  const addGroup = (g) => {
    const box = groupBox(g || { match: "any", conditions: [{ field: "platform", op: "eq", value: "ios" }] }, schema, depth + 1, () => {
      children.splice(children.indexOf(box), 1); box.remove();
    });
    box.isGroup = true;
    children.push(box); items.append(box);
  };
  (rule.conditions || []).forEach(addCond);
  (rule.rules || []).forEach(addGroup);
  const matchSel = segmented([["all", "all"], ["any", "any"]], match, (v) => { match = v; });
  const el = h("div", { class: "rulegroup" },
    h("div", { class: "row spread" },
      h("div", { class: "row" }, h("span", {}, depth === 0 ? "Devices that match" : "Match"), matchSel, h("span", {}, "of these")),
      onRemove ? h("button", { type: "button", class: "iconbtn", "aria-label": "Remove group", title: "Remove group", onclick: onRemove }, icon("trash")) : null),
    items,
    h("div", { class: "row" },
      h("button", { type: "button", class: "btn small", onclick: () => addCond({ field: "platform", op: "eq" }) }, icon("plus"), "Condition"),
      depth < 4 ? h("button", { type: "button", class: "btn small quiet", onclick: () => addGroup() }, icon("plus"), "Nested group") : null));
  el.get = () => ({
    match,
    conditions: children.filter((c) => !c.isGroup).map((c) => c.get()),
    rules: children.filter((c) => c.isGroup).map((c) => c.get()),
  });
  return el;
}

function clean(r) {
  const out = { match: r.match };
  if (r.conditions && r.conditions.length) out.conditions = r.conditions;
  if (r.rules && r.rules.length) out.rules = r.rules.map(clean);
  return out;
}

export function ruleBuilder(rule, schema) {
  let current = rule || { match: "all", conditions: [{ field: "platform", op: "eq", value: "ios" }] };
  let mode = "visual";
  const host = h("div");
  let tree;
  const draw = () => { tree = groupBox(current, schema, 0, null); host.replaceChildren(tree); };
  draw();
  const json = jsonEditor(current, { rows: 14, label: "Rules" });
  const jsonHost = h("div", { hidden: true }, json);
  const err = h("div");
  const sw = segmented([["visual", "Builder"], ["json", "JSON"]], "visual", (v) => {
    err.replaceChildren();
    try {
      if (v === "json") json.set(clean(tree.get()));
      else { current = json.get() || { match: "all" }; draw(); }
      mode = v; host.hidden = v !== "visual"; jsonHost.hidden = v !== "json";
    } catch (e) { err.replaceChildren(h("div", { class: "notice bad" }, icon("alert"), e.message)); }
  });
  const el = h("div", { class: "stack" }, h("div", { class: "row spread" }, h("span", { class: "muted small" }, "Rules are re-evaluated every minute, at enrollment, and when tags change."), sw), err, host, jsonHost);
  el.get = () => (mode === "json" ? json.get() : clean(tree.get()));
  return el;
}
