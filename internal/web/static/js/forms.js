// Form controls. Each returns a DOM element with a .get() (and sometimes
// .set()) so editors can read values back without global state.
import { h, icon } from "./core.js";

let uid = 0;
const nextId = () => "f" + ++uid;

// field(label, control, help, { inline }): label, control, help text and an error slot.
// The returned element has .setError(message) (pass "" or nothing to clear), which sets
// .invalid, aria-invalid and aria-describedby on the control.
export function field(label, control, help, opts = {}) {
  const id = control.id || (control.id = nextId());
  const helpEl = help ? h("div", { class: "help", id: id + "-help" }, help) : null;
  const errEl = h("div", { class: "field-error", id: id + "-err", role: "alert", hidden: true });
  const el = h("div", { class: "field" + (opts.inline ? " inline" : "") },
    h("label", { for: id }, label), control, helpEl, errEl);
  const target = control.matches && control.matches("input,select,textarea") ? control : control.querySelector && control.querySelector("input,select,textarea");
  const describe = (ids) => { if (target) { if (ids.length) target.setAttribute("aria-describedby", ids.join(" ")); else target.removeAttribute("aria-describedby"); } };
  describe(helpEl ? [helpEl.id] : []);
  el.setError = (msg) => {
    errEl.textContent = msg || "";
    errEl.hidden = !msg;
    el.classList.toggle("invalid", !!msg);
    if (target) { if (msg) target.setAttribute("aria-invalid", "true"); else target.removeAttribute("aria-invalid"); }
    describe([helpEl && helpEl.id, msg ? errEl.id : null].filter(Boolean));
  };
  return el;
}

export function input(value = "", attrs = {}) {
  const el = h("input", { type: attrs.type || "text", ...attrs, class: "input " + (attrs.class || ""), value: value ?? "" });
  el.get = () => (attrs.type === "number" ? (el.value === "" ? undefined : Number(el.value)) : el.value.trim());
  return el;
}

export function select(options, value, attrs = {}) {
  const el = h("select", { class: "select", ...attrs },
    options.map((o) => {
      const [v, label] = Array.isArray(o) ? o : [o, o];
      return h("option", { value: v, selected: String(v) === String(value) }, label);
    }));
  el.get = () => el.value;
  return el;
}

export function toggle(checked, label, onchange) {
  const cb = h("input", { type: "checkbox", role: "switch", checked: !!checked, onchange: () => onchange && onchange(cb.checked) });
  const el = h("label", { class: "switch" }, cb, h("span", { class: "track", "aria-hidden": "true" }), label ? h("span", {}, label) : null);
  el.get = () => cb.checked;
  el.set = (v) => { cb.checked = !!v; };
  el.input = cb;
  return el;
}

export function check(checked, label) {
  const cb = h("input", { type: "checkbox", checked: !!checked });
  const el = h("label", { class: "check" }, cb, label);
  el.get = () => cb.checked;
  return el;
}

// Three states for a restriction: not managed (undefined), allowed (true),
// blocked (false).
export function tristate(value, labels = ["Not managed", "Allow", "Block"]) {
  let v = value === true ? true : value === false ? false : undefined;
  const opts = [[undefined, labels[0], ""], [true, labels[1], "allow"], [false, labels[2], "block"]];
  const el = h("div", { class: "seg", role: "group" });
  const draw = () => el.querySelectorAll("button").forEach((b, i) => b.setAttribute("aria-pressed", String(opts[i][0] === v)));
  opts.forEach(([val, label, cls]) => el.append(h("button", { type: "button", class: cls, onclick: () => { v = val; draw(); } }, label)));
  draw();
  el.get = () => v;
  return el;
}

export function segmented(options, value, onchange) {
  let v = value;
  const el = h("div", { class: "seg", role: "group" });
  const draw = () => el.querySelectorAll("button").forEach((b) => b.setAttribute("aria-pressed", String(b.dataset.v === String(v))));
  options.forEach(([val, label]) => el.append(h("button", { type: "button", "data-v": String(val), onclick: () => { v = val; draw(); onchange && onchange(v); } }, label)));
  draw();
  el.get = () => v;
  return el;
}

export function textarea(value = "", attrs = {}) {
  const el = h("textarea", { class: "textarea", spellcheck: "false", ...attrs });
  el.value = value;
  el.get = () => el.value;
  return el;
}

// JSON editor that validates as you type. get() throws a readable error.
export function jsonEditor(value, { rows = 10, label = "JSON", object = true } = {}) {
  const ta = textarea(value === undefined ? "" : JSON.stringify(value, null, 2), { rows });
  const msg = h("div", { class: "help" });
  const check = () => {
    if (!ta.value.trim()) { ta.classList.remove("invalid"); msg.textContent = ""; return; }
    try {
      const v = JSON.parse(ta.value);
      if (object && (typeof v !== "object" || v === null || Array.isArray(v))) throw new Error("expected a JSON object");
      ta.classList.remove("invalid"); msg.textContent = "";
    } catch (e) { ta.classList.add("invalid"); msg.textContent = e.message; }
  };
  ta.addEventListener("input", check);
  const el = h("div", { class: "field" }, ta, msg);
  el.get = () => {
    if (!ta.value.trim()) return undefined;
    try { return JSON.parse(ta.value); } catch (e) { throw new Error(label + ": " + e.message); }
  };
  el.set = (v) => { ta.value = v === undefined ? "" : JSON.stringify(v, null, 2); check(); };
  return el;
}

// Repeater: a list of rows built by makeRow(item) -> element with .get().
export function repeater(items, makeRow, { addLabel = "Add", newItem = () => ({}), empty } = {}) {
  const list = h("div", { class: "repeater" });
  const rows = [];
  const emptyEl = empty ? h("div", { class: "muted small" }, empty) : null;
  const refresh = () => { if (emptyEl) emptyEl.hidden = rows.length > 0; };
  const add = (item) => {
    const row = makeRow(item);
    const rm = h("button", { type: "button", class: "iconbtn", "aria-label": "Remove", title: "Remove", onclick: () => {
      rows.splice(rows.indexOf(wrap), 1); wrap.remove(); refresh();
    } }, icon("trash"));
    const wrap = h("div", { class: "item" }, h("div", { class: "item-line" }, h("div", { class: "item-main" }, row), rm));
    wrap.get = row.get;
    rows.push(wrap);
    list.append(wrap);
    refresh();
  };
  (items || []).forEach(add);
  const el = h("div", { class: "stack" }, emptyEl, list,
    h("button", { type: "button", class: "btn small", onclick: () => add(newItem()) }, icon("plus"), addLabel));
  refresh();
  el.get = () => rows.map((r) => r.get()).filter((v) => v !== null && v !== undefined);
  return el;
}

export function tagsInput(values = [], placeholder = "Type and press Enter") {
  const tags = [...values];
  const box = h("div", { class: "tags" });
  const inp = h("input", { placeholder, "aria-label": placeholder });
  const draw = () => {
    box.replaceChildren(...tags.map((t, i) => h("span", { class: "tag" }, t,
      h("button", { type: "button", "aria-label": "Remove " + t, onclick: () => { tags.splice(i, 1); draw(); } }, "x"))), inp);
  };
  const commit = () => {
    for (const part of inp.value.split(",")) {
      const t = part.trim();
      if (t && !tags.includes(t)) tags.push(t);
    }
    inp.value = ""; draw(); inp.focus();
  };
  inp.addEventListener("keydown", (e) => {
    if (e.key === "Enter" || e.key === ",") { e.preventDefault(); commit(); }
    if (e.key === "Backspace" && !inp.value && tags.length) { tags.pop(); draw(); inp.focus(); }
  });
  inp.addEventListener("blur", () => { if (inp.value.trim()) commit(); });
  draw();
  box.get = () => { if (inp.value.trim()) commit(); return [...tags]; };
  return box;
}

// Checkbox list of [value, label, hint] choices.
export function picklist(options, selected = [], emptyText = "Nothing to choose from yet.") {
  if (!options.length) { const e = h("div", { class: "muted small" }, emptyText); e.get = () => []; return e; }
  const boxes = [];
  const el = h("div", { class: "picklist" }, options.map(([v, label, hint]) => {
    const cb = h("input", { type: "checkbox", value: v, checked: selected.includes(v) });
    boxes.push(cb);
    return h("label", {}, cb, h("span", {}, label), hint ? h("span", { class: "muted small" }, hint) : null);
  }));
  el.get = () => boxes.filter((b) => b.checked).map((b) => b.value);
  return el;
}

export function fileToBase64(file) {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result).split(",")[1] || "");
    r.onerror = () => reject(r.error);
    r.readAsDataURL(file);
  });
}

export function fileToText(file) {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result));
    r.onerror = () => reject(r.error);
    r.readAsText(file);
  });
}

export function filePicker(accept, label, onfile) {
  const inp = h("input", { type: "file", accept, class: "sr-only", onchange: () => { if (inp.files[0]) onfile(inp.files[0]); inp.value = ""; } });
  const btn = h("button", { type: "button", class: "btn small", onclick: () => inp.click() }, icon("upload"), label);
  return h("span", {}, inp, btn);
}

// Drop empty values so documents only contain what the admin set.
export function prune(obj) {
  if (Array.isArray(obj)) return obj.length ? obj : undefined;
  if (obj && typeof obj === "object") {
    const out = {};
    for (const [k, v] of Object.entries(obj)) {
      const pv = v && typeof v === "object" && !Array.isArray(v) ? prune(v) : v;
      if (pv === undefined || pv === "" || (Array.isArray(pv) && !pv.length)) continue;
      out[k] = pv;
    }
    return Object.keys(out).length ? out : undefined;
  }
  return obj;
}
