// Core helpers for the VaanarSena console. All untrusted text is rendered with
// textContent through h(); nothing here uses innerHTML with data.

export const state = { me: null, info: null, catalogue: [], groups: null };

export function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === null || v === undefined || v === false) continue;
    if (k.startsWith("on") && typeof v === "function") el.addEventListener(k.slice(2), v);
    else if (k === "class") el.className = v;
    else if (k === "value") el.value = v;
    else if (k === "checked") el.checked = !!v;
    else if (k === "style") Object.assign(el.style, v);
    else el.setAttribute(k, v === true ? "" : v);
  }
  append(el, children);
  return el;
}

export function append(el, children) {
  for (const c of children.flat(Infinity)) {
    if (c === null || c === undefined || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

// ---------- icons (24px stroke icons) ----------
const ICONS = {
  dashboard: "M3 13h8V3H3zM13 21h8v-8h-8zM13 3v6h8V3zM3 21h8v-4H3z",
  devices: "M4 5h16v10H4zM2 19h20M9 15v4M15 15v4",
  groups: "M16 11a4 4 0 1 0-8 0M4 20a8 6 0 0 1 16 0M18 8a3 3 0 0 1 3 3M3 11a3 3 0 0 1 3-3",
  policy: "M12 3 4 6v6c0 5 3.5 8 8 9 4.5-1 8-4 8-9V6z M9 12l2 2 4-4",
  blueprint: "M12 3 3 8l9 5 9-5zM3 13l9 5 9-5M3 17l9 5 9-5",
  manifest: "M8 7l-5 5 5 5M16 7l5 5-5 5M14 4l-4 16",
  enroll: "M12 5v14M5 12h14",
  users: "M16 20v-1a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v1M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8M22 20v-1a4 4 0 0 0-3-3.9M16 3.1a4 4 0 0 1 0 7.8",
  audit: "M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9zM14 3v6h6M8 13h8M8 17h5",
  settings: "M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z",
  account: "M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2M12 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8",
  search: "M11 19a8 8 0 1 0 0-16 8 8 0 0 0 0 16zM21 21l-4.3-4.3",
  copy: "M9 9h11v11H9zM5 15H4V4h11v1",
  trash: "M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6M10 11v6M14 11v6",
  check: "M20 6 9 17l-5-5",
  alert: "M12 9v4M12 17h.01M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z",
  info: "M12 16v-4M12 8h.01M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20z",
  chevron: "m9 18 6-6-6-6",
  x: "M18 6 6 18M6 6l12 12",
  plus: "M12 5v14M5 12h14",
  menu: "M3 6h18M3 12h18M3 18h18",
  download: "M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M7 10l5 5 5-5M12 15V3",
  upload: "M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M17 8l-5-5-5 5M12 3v12",
  moon: "M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z",
  sun: "M12 17a5 5 0 1 0 0-10 5 5 0 0 0 0 10zM12 1v2M12 21v2M4.2 4.2l1.4 1.4M18.4 18.4l1.4 1.4M1 12h2M21 12h2M4.2 19.8l1.4-1.4M18.4 5.6l1.4-1.4",
  logout: "M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9",
  external: "M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6M15 3h6v6M10 14 21 3",
  apple: "M16 3c0 2-1.5 3.5-3.4 3.5-.2-1.9 1.4-3.5 3.4-3.5zM12.5 7.5c1 0 2-.7 3.3-.7 1.5 0 2.8.8 3.6 2-3 1.7-2.6 6 .6 7.2-.7 1.7-1.6 3.4-3 4.6-1 .9-2 .8-3 .4-1-.4-1.9-.4-3 0-1 .4-2 .5-3-.4C5 17.8 3.6 13 5.5 9.8 6.4 8.3 7.9 7.5 9.3 7.5c1.1 0 2.2.7 3.2.7z",
  windows: "M3 5.5 10.5 4.5v7H3zM12.5 4.2 21 3v8.5h-8.5zM3 12.5h7.5v7L3 18.5zM12.5 12.5H21V21l-8.5-1.2z",
  android: "M7 10h10v7a2 2 0 0 1-2 2H9a2 2 0 0 1-2-2zM7 10a5 5 0 0 1 10 0M5 11v5M19 11v5M9 7.5l-1.5-2.5M15 7.5l1.5-2.5M10 19v2M14 19v2",
  chromeos: "M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20zM12 16a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM12 8h9M8.5 14l-4.5 7.5M15.5 14 11 21.8",
  more: "M12 13a1 1 0 1 0 0-2 1 1 0 0 0 0 2zM19 13a1 1 0 1 0 0-2 1 1 0 0 0 0 2zM5 13a1 1 0 1 0 0-2 1 1 0 0 0 0 2z",
  edit: "M12 20h9M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z",
  refresh: "M21 12a9 9 0 1 1-3-6.7L21 8M21 3v5h-5",
  lock: "M5 11h14v10H5zM8 11V7a4 4 0 0 1 8 0v4",
  send: "M22 2 11 13M22 2l-7 20-4-9-9-4z",
  filter: "M22 3H2l8 9.5V19l4 2v-8.5z",
  inbox: "M22 12h-6l-2 3h-4l-2-3H2M5.5 5.1 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.5-6.9A2 2 0 0 0 16.8 4H7.2a2 2 0 0 0-1.7 1.1z",
  monitor: "M3 4h18v12H3zM8 20h8M12 16v4",
  chevronDown: "m6 9 6 6 6-6",
  chevronLeft: "m15 18-6-6 6-6",
  arrowRight: "M5 12h14M13 6l6 6-6 6",
  clock: "M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20zM12 6v6l4 2",
  shield: "M12 3 4 6v6c0 5 3.5 8 8 9 4.5-1 8-4 8-9V6z",
  software: "M21 8 12 3 3 8v8l9 5 9-5zM3 8l9 5 9-5M12 13v8",
  linux: "M12 3c-2 0-3 2-3 4.5 0 2-1.5 3.5-2.5 5.5S5 17 6 18.5 9 20 12 20s5 .1 6-1.5.5-3.5-.5-5.5S15 9.5 15 7.5C15 5 14 3 12 3zM10 8h.01M14 8h.01M10.5 11h3",
};

export function icon(name, cls) {
  const ns = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(ns, "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "1.8");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("aria-hidden", "true");
  if (cls) svg.setAttribute("class", cls);
  const p = document.createElementNS(ns, "path");
  p.setAttribute("d", ICONS[name] || ICONS.info);
  svg.append(p);
  return svg;
}

// ---------- API ----------
export class ApiError extends Error {
  constructor(message, status, data) { super(message); this.status = status; this.data = data || {}; }
}

export async function api(method, path, body, opts = {}) {
  const headers = { "X-Requested-With": "vaanarsena" };
  let payload;
  if (body !== undefined) {
    if (opts.raw) { payload = body; headers["Content-Type"] = opts.contentType || "text/plain"; }
    else { payload = JSON.stringify(body); headers["Content-Type"] = "application/json"; }
  }
  const res = await fetch("/api/v1" + path, { method, headers, body: payload, credentials: "same-origin" });
  if (res.status === 401 && path !== "/auth/login") {
    const had = state.me !== null;
    state.me = null;
    if (had) window.dispatchEvent(new Event("vs:signedout"));
    throw new ApiError("Your session ended. Sign in again.", 401);
  }
  if (res.status === 204) return null;
  const ct = res.headers.get("Content-Type") || "";
  const data = ct.includes("json") ? await res.json().catch(() => ({})) : await res.text();
  if (!res.ok) throw new ApiError((data && data.error) || res.statusText, res.status, data);
  return data;
}

// ---------- navigation ----------
// An editor with unsaved changes sets state.leaveGuard = { isDirty(), confirmLeave(path), dispose() }.
// go() and the popstate handler in app.js ask it before leaving the page.
export function go(path) {
  if (location.pathname + location.search === path) return;
  const g = state.leaveGuard;
  if (g && g.isDirty()) { g.confirmLeave(path); return; }
  history.pushState(null, "", path);
  window.dispatchEvent(new Event("vs:navigate"));
}

export function link(href, ...children) {
  return h("a", { href, onclick: (e) => {
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
    e.preventDefault(); go(href);
  } }, ...children);
}

// ---------- feedback ----------
let toastHost;
export function toast(message, kind = "ok") {
  if (!toastHost) { toastHost = h("div", { class: "toasts", role: "status", "aria-live": "polite" }); document.body.append(toastHost); }
  const t = h("div", { class: "toast " + (kind === "bad" ? "bad" : "") }, icon(kind === "bad" ? "alert" : "check"), h("span", {}, message));
  toastHost.append(t);
  setTimeout(() => t.remove(), kind === "bad" ? 7000 : 3500);
}

export function errorNotice(err) {
  const d = (err && err.data) || {};
  const box = h("div", { class: "notice bad", role: "alert" }, icon("alert"),
    h("div", {}, h("div", {}, err.message || String(err)),
      d.problems && d.problems.length ? h("ul", {}, d.problems.map((p) => h("li", {}, p))) : null,
      d.setup ? h("div", { class: "small" }, link(d.setup, "Open setup")) : null));
  return box;
}

// notice(kind, ...children): kind is ok, warn, bad, info or gold (gold = personal device tone).
export function notice(kind, ...children) {
  const ic = { warn: "alert", bad: "alert", ok: "check", gold: "info", info: "info" }[kind] || "info";
  return h("div", { class: "notice " + kind }, icon(ic), h("div", {}, ...children));
}

let dialogSeq = 0;

// Modal dialog. Resolves true on confirm. With typeToConfirm the confirm
// button stays disabled until the exact text is typed.
export function confirmDialog({ title, body, confirmLabel = "Confirm", danger = false, typeToConfirm = null }) {
  return new Promise((resolve) => {
    const prev = document.activeElement;
    const titleId = "dlg-t" + ++dialogSeq;
    const input = typeToConfirm ? h("input", { class: "input", "aria-label": "Confirmation text", autocomplete: "off" }) : null;
    const ok = h("button", { class: "btn " + (danger ? "danger" : "primary"), disabled: !!typeToConfirm }, confirmLabel);
    const cancel = h("button", { class: "btn" }, "Cancel");
    const dlg = h("dialog", { "aria-labelledby": titleId },
      h("div", { class: "dlg-body stack" }, h("h3", { id: titleId }, title), body ? h("div", { class: "muted" }, body) : null,
        typeToConfirm ? h("div", { class: "field" }, h("label", {}, "Type ", h("b", {}, typeToConfirm), " to confirm"), input) : null),
      h("div", { class: "dlg-actions" }, cancel, ok));
    if (input) input.addEventListener("input", () => { ok.disabled = input.value.trim() !== typeToConfirm; });
    const done = (v) => { dlg.close(); dlg.remove(); if (prev && prev.isConnected) prev.focus(); resolve(v); };
    ok.addEventListener("click", () => done(true));
    cancel.addEventListener("click", () => done(false));
    dlg.addEventListener("cancel", (e) => { e.preventDefault(); done(false); });
    dlg.addEventListener("click", (e) => { if (e.target === dlg) done(false); });
    document.body.append(dlg);
    dlg.showModal();
    (input || ok).focus();
  });
}

// drawer({ title, sub, body, footer, onClose }): right-side panel on a modal <dialog>.
// Esc, the close button and a click on the scrim all close it; focus returns to the
// element that opened it. It opens immediately. Returns { el, body, footer, close }.
export function drawer({ title, sub, body, footer, onClose } = {}) {
  const prev = document.activeElement;
  const titleId = "dlg-t" + ++dialogSeq;
  const bodyEl = h("div", { class: "drawer-body" }, body);
  const footEl = footer ? h("div", { class: "drawer-foot" }, footer) : null;
  let closed = false;
  function close() {
    if (closed) return;
    closed = true;
    if (dlg.open) dlg.close();
    dlg.remove();
    if (prev && prev.isConnected) prev.focus();
    if (onClose) onClose();
  }
  const dlg = h("dialog", { class: "drawer", "aria-labelledby": titleId },
    h("div", { class: "drawer-head" },
      h("div", { class: "grow" }, h("h2", { id: titleId }, title), sub ? h("p", { class: "help" }, sub) : null),
      h("button", { type: "button", class: "iconbtn", "aria-label": "Close", onclick: () => close() }, icon("x"))),
    bodyEl, footEl);
  dlg.addEventListener("cancel", (e) => { e.preventDefault(); close(); });
  dlg.addEventListener("click", (e) => { if (e.target === dlg) close(); });
  document.body.append(dlg);
  dlg.showModal();
  const first = bodyEl.querySelector("input:not([type=hidden]),select,textarea");
  if (first) first.focus();
  return { el: dlg, body: bodyEl, footer: footEl, close };
}

// menu(trigger, items, { align }): popover menu opened by `trigger`.
// items is an array (or a function returning one) of
//   { label, icon, onClick, href, danger, disabled, hint, current } or { separator: true }.
// Arrow keys, Home/End, Esc (returns focus), Tab and a click outside close it.
// Returns { open, close }.
export function menu(trigger, items, { align = "end" } = {}) {
  let pop = null;
  trigger.setAttribute("aria-haspopup", "menu");
  trigger.setAttribute("aria-expanded", "false");
  const onOutside = (e) => { if (pop && !pop.contains(e.target) && !trigger.contains(e.target)) close(false); };
  const onDismiss = (e) => {
    if (!pop) return;
    if (e.type === "scroll") {
      if (pop.contains(e.target)) return;
      const r = trigger.getBoundingClientRect();
      if (r.bottom > 0 && r.top < window.innerHeight) { place(); return; }
    }
    close(false);
  };
  function place() {
    const r = trigger.getBoundingClientRect();
    const w = pop.offsetWidth, hh = pop.offsetHeight;
    let left = align === "end" ? r.right - w : r.left;
    left = Math.max(8, Math.min(left, window.innerWidth - w - 8));
    let top = r.bottom + 4;
    if (top + hh > window.innerHeight - 8 && r.top - hh - 4 > 8) top = r.top - hh - 4;
    pop.style.left = left + "px"; pop.style.top = top + "px";
  }
  function close(restore = true) {
    if (!pop) return;
    pop.remove(); pop = null;
    trigger.setAttribute("aria-expanded", "false");
    document.removeEventListener("pointerdown", onOutside, true);
    window.removeEventListener("resize", onDismiss);
    window.removeEventListener("scroll", onDismiss, true);
    if (restore) trigger.focus();
  }
  function open() {
    if (pop) return;
    const list = (typeof items === "function" ? items() : items).filter(Boolean);
    pop = h("div", { class: "menu", role: "menu", "aria-label": trigger.getAttribute("aria-label") || null });
    const live = [];
    for (const it of list) {
      if (it.separator) { pop.append(h("div", { class: "menu-sep", role: "separator" })); continue; }
      const b = h("button", { type: "button", role: "menuitem", tabindex: "-1", disabled: !!it.disabled,
        class: "menu-item" + (it.danger ? " danger" : "") + (it.current ? " current" : ""),
        onclick: () => { close(true); if (it.href) go(it.href); else if (it.onClick) it.onClick(); } },
      it.icon ? icon(it.icon) : null, h("span", { class: "grow" }, it.label), it.hint ? h("span", { class: "menu-hint" }, it.hint) : null);
      if (!it.disabled) live.push(b);
      pop.append(b);
    }
    const focusAt = (i) => { if (live.length) live[(i + live.length) % live.length].focus(); };
    pop.addEventListener("keydown", (e) => {
      const i = live.indexOf(document.activeElement);
      if (e.key === "ArrowDown") { e.preventDefault(); focusAt(i + 1); }
      else if (e.key === "ArrowUp") { e.preventDefault(); focusAt(i < 0 ? -1 : i - 1); }
      else if (e.key === "Home") { e.preventDefault(); focusAt(0); }
      else if (e.key === "End") { e.preventDefault(); focusAt(-1); }
      else if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); close(true); }
      else if (e.key === "Tab") close(false);
    });
    (trigger.closest("dialog") || document.body).append(pop);
    place();
    trigger.setAttribute("aria-expanded", "true");
    document.addEventListener("pointerdown", onOutside, true);
    window.addEventListener("resize", onDismiss);
    window.addEventListener("scroll", onDismiss, true);
    focusAt(0);
  }
  trigger.addEventListener("click", () => (pop ? close(true) : open()));
  trigger.addEventListener("keydown", (e) => { if (e.key === "ArrowDown" && !pop) { e.preventDefault(); open(); } });
  return { open, close };
}

export function copyButton(text, label = "Copy") {
  const b = h("button", { class: "btn small", type: "button", onclick: async () => {
    try { await navigator.clipboard.writeText(text); toast("Copied"); } catch { toast("Copy failed: select the text and copy it manually", "bad"); }
  } }, icon("copy"), label);
  return b;
}

export function copyLine(text) { return h("div", { class: "copyline" }, h("code", {}, text), copyButton(text)); }

// withBusy(button, fn): marks the button aria-busy and disabled while fn() runs.
export async function withBusy(btn, fn) {
  btn.setAttribute("aria-busy", "true"); btn.disabled = true;
  try { return await fn(); } finally { btn.removeAttribute("aria-busy"); btn.disabled = false; }
}

// skeleton(lines): loading placeholder (use instead of "Loading..." text).
export function skeleton(lines = 3) {
  return h("div", { class: "skeleton", role: "status", "aria-busy": "true" }, h("span", { class: "sr-only" }, "Loading"),
    Array.from({ length: lines }, () => h("div", { class: "sk-line" })));
}

// chip(label, onRemove): removable filter chip.
export function chip(label, onRemove) {
  return h("span", { class: "chip" }, h("span", {}, label),
    onRemove ? h("button", { type: "button", class: "chip-x", "aria-label": "Remove filter: " + label, onclick: onRemove }, icon("x")) : null);
}

// kpi({ label, value, delta, tone, href }): stat card. tone colors the delta: ok, warn, bad, info, personal.
export function kpi({ label, value, delta, tone, href }) {
  const parts = [h("span", { class: "kpi-label" }, label), h("span", { class: "kpi-value" }, String(value)),
    delta ? h("span", { class: "kpi-delta " + (tone || "") }, delta) : null];
  if (!href) return h("div", { class: "kpi" }, parts);
  const a = link(href, ...parts);
  a.className = "kpi link-card";
  return a;
}

// badge(label, tone): pill. tone is "", ok, bad, warn, info or personal.
export const badge = (label, tone = "") => h("span", { class: "badge " + tone }, label);

export function initials(name) {
  const parts = String(name || "?").replace(/@.*/, "").split(/[\s._-]+/).filter(Boolean);
  return ((parts[0] || "?")[0] + (parts.length > 1 ? parts[parts.length - 1][0] : "")).toUpperCase();
}

// downloadCsv(filename, head, rows): quotes every cell and neutralizes spreadsheet
// formulas (a leading = + - @ tab or CR gets a quote prefix) so untrusted text is safe to open.
function csvCell(v) {
  let t = v === null || v === undefined ? "" : String(v);
  if (/^[=+\-@\t\r]/.test(t)) t = "'" + t;
  return '"' + t.replace(/"/g, '""') + '"';
}
export function downloadCsv(filename, head, rows) {
  const lines = [head.map(csvCell).join(",")].concat(rows.map((r) => r.map(csvCell).join(",")));
  const url = URL.createObjectURL(new Blob([lines.join("\r\n") + "\r\n"], { type: "text/csv" }));
  const a = h("a", { href: url, download: filename });
  document.body.append(a); a.click(); a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

// ---------- formatting ----------
export const fmtTime = (t) => (t ? new Date(t).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" }) : "Never");
export function ago(t) {
  if (!t) return "Never";
  const s = (Date.now() - new Date(t).getTime()) / 1000;
  if (s < 0) return "just now";
  if (s < 90) return "just now";
  if (s < 5400) return Math.round(s / 60) + " min ago";
  if (s < 129600) return Math.round(s / 3600) + " h ago";
  return Math.round(s / 86400) + " days ago";
}
const RANK = { auditor: 1, operator: 2, admin: 3 };
export const can = (role) => (RANK[state.me?.role] || 0) >= RANK[role];

export const PLATFORM = {
  ios: { label: "iOS", icon: "apple" }, ipados: { label: "iPadOS", icon: "apple" }, macos: { label: "macOS", icon: "apple" },
  windows: { label: "Windows", icon: "windows" }, android: { label: "Android", icon: "android" },
  chromeos: { label: "ChromeOS", icon: "chromeos" }, linux: { label: "Linux", icon: "linux" },
};
export const platformLabel = (p) => (PLATFORM[p] || { label: p }).label;
export const platformTag = (p) => h("span", { class: "plat" }, icon((PLATFORM[p] || {}).icon || "devices"), platformLabel(p));
export const ownershipTag = (o) => o === "personal" ? h("span", { class: "badge personal" }, "Personal") : h("span", { class: "badge" }, "Corporate");
export function complianceTag(c) {
  if (c === true) return h("span", { class: "status" }, h("span", { class: "dot ok" }), "Compliant");
  if (c === false) return h("span", { class: "status" }, h("span", { class: "dot bad" }), "Not compliant");
  return h("span", { class: "status muted" }, h("span", { class: "dot" }), "Unknown");
}
export function statusTag(s) {
  const map = { enrolled: ["ok", "Enrolled"], enrolling: ["warn", "Enrolling"], retired: ["", "Retired"], wiped: ["bad", "Wiped"],
    queued: ["", "Queued"], sent: ["warn", "Sent"], acknowledged: ["ok", "Done"], error: ["bad", "Failed"], not_now: ["warn", "Deferred"], cancelled: ["", "Cancelled"] };
  const [k, label] = map[s] || ["", s];
  return h("span", { class: "status" }, h("span", { class: "dot " + k }), label);
}


// ---------- layout ----------
export function page({ title, sub, crumb, actions }, ...content) {
  return h("div", { class: "page" },
    h("div", { class: "page-head" },
      h("div", { class: "page-head-text" }, crumb ? h("div", { class: "crumb" }, crumb) : null, h("h1", {}, title), sub ? h("p", { class: "sub" }, sub) : null),
      actions && actions.length ? h("div", { class: "actions" }, actions) : null),
    content);
}

export function panel({ title, sub, actions } = {}, ...body) {
  return h("section", { class: "panel" },
    title ? h("div", { class: "panel-head" }, h("div", {}, h("h2", {}, title), sub ? h("p", {}, sub) : null), actions ? h("div", { class: "row" }, actions) : null) : null,
    body);
}

export const body = (...c) => h("div", { class: "panel-body" }, ...c);

// table(columns, rows, opts): columns are {label, render(row), cls}.
// opts: onRow(row, event) makes rows clickable and keyboard reachable, empty is shown
// with no rows, selectable is {header, cell(row, tr)} for a checkbox column, dense
// tightens the rows, stickyHead keeps the header visible inside a scrolling wrapper,
// label sets the table's aria-label, rowClass(row) adds a class to a row.
// Add class "selected" to a tr for the selected-row style.
export function table(columns, rows, { onRow, empty, selectable, dense, stickyHead, label, rowClass } = {}) {
  if (!rows.length) return empty || h("div", { class: "empty" }, "Nothing here yet.");
  const head = h("tr", {}, selectable ? h("th", { class: "sel" }, selectable.header) : null, columns.map((c) => h("th", { class: c.cls || "", scope: "col" }, c.label)));
  const tb = h("tbody");
  rows.forEach((r) => {
    const extra = rowClass ? rowClass(r) : "";
    const tr = h("tr", { class: (onRow ? "clickable " : "") + (extra || ""), tabindex: onRow ? "0" : null });
    if (onRow) {
      tr.addEventListener("click", (e) => { if (!e.target.closest("input,button,a,select,textarea,label")) onRow(r, e); });
      tr.addEventListener("keydown", (e) => { if (e.key === "Enter" && e.target === tr) onRow(r, e); });
    }
    if (selectable) tr.append(h("td", { class: "sel" }, selectable.cell(r, tr)));
    columns.forEach((c) => tr.append(h("td", { class: c.cls || "" }, c.render(r))));
    tb.append(tr);
  });
  return h("div", { class: "table-wrap" + (stickyHead ? " sticky-head" : "") },
    h("table", { class: dense ? "dense" : "", "aria-label": label || null }, h("thead", {}, head), tb));
}

// emptyState(title, text, action, iconName): icon, title, one line and a primary action.
export function emptyState(title, text, action, iconName = "inbox") {
  return h("div", { class: "empty" }, h("span", { class: "empty-icon" }, icon(iconName)), h("strong", {}, title), text ? h("div", { class: "empty-text" }, text) : null, action || null);
}

// tabBar({ items: [{ id, label, count, beforeLeave }], value, onChange, label }): underline
// tab list with roving tabindex and arrow-key navigation. The element has .select(id, opts).
let tabSeq = 0;
export function tabBar({ items, value, onChange, label }) {
  let current = value || items[0].id;
  const pre = "tab" + ++tabSeq;
  const bar = h("div", { class: "tabs", role: "tablist", id: pre, "aria-label": label || null });
  const draw = () => bar.querySelectorAll("[role=tab]").forEach((b) => {
    const on = b.dataset.id === current;
    b.setAttribute("aria-selected", String(on)); b.tabIndex = on ? 0 : -1;
  });
  const select = (id, { focus = false, silent = false } = {}) => {
    const it = items.find((i) => i.id === id);
    if (!it) return;
    if (id !== current && it.beforeLeave && it.beforeLeave() === false) return;
    current = id; draw();
    if (focus) bar.querySelector(`[data-id="${CSS.escape(id)}"]`).focus();
    if (!silent && onChange) onChange(id, it);
  };
  items.forEach((it) => {
    bar.append(h("button", { role: "tab", type: "button", id: pre + "-" + it.id, "data-id": it.id, onclick: () => select(it.id) },
      it.label, it.count !== undefined && it.count !== null ? h("span", { class: "count" }, String(it.count)) : null));
  });
  bar.addEventListener("keydown", (e) => {
    const i = items.findIndex((x) => x.id === current);
    let n = -1;
    if (e.key === "ArrowRight") n = (i + 1) % items.length;
    else if (e.key === "ArrowLeft") n = (i - 1 + items.length) % items.length;
    else if (e.key === "Home") n = 0;
    else if (e.key === "End") n = items.length - 1;
    if (n < 0) return;
    e.preventDefault(); select(items[n].id, { focus: true });
  });
  bar.select = select;
  draw();
  return bar;
}

// tabs(items, initial): items are {id, label, render(), beforeLeave?}. Renders the active
// tab's content asynchronously below the bar.
export function tabs(items, initial) {
  let current = initial || items[0].id;
  let drawSeq = 0;
  const host = h("div", { class: "tabbody", role: "tabpanel" });
  const bar = tabBar({ items, value: current, onChange: (id) => { current = id; draw(); } });
  async function draw() {
    const it = items.find((i) => i.id === current);
    host.setAttribute("aria-labelledby", bar.id + "-" + current);
    const my = ++drawSeq;
    host.replaceChildren(skeleton(3));
    try {
      const out = [].concat(await it.render()).filter(Boolean);
      if (my === drawSeq) host.replaceChildren(...out);
    } catch (e) { if (my === drawSeq) host.replaceChildren(errorNotice(e)); }
  }
  draw();
  return h("div", {}, bar, host);
}


export async function groupsCache(force) {
  if (!state.groups || force) state.groups = (await api("GET", "/groups")).groups;
  return state.groups;
}
