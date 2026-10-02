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
export function go(path) {
  if (location.pathname + location.search === path) return;
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
  const t = h("div", { class: "toast " + (kind === "bad" ? "bad" : "") }, icon(kind === "bad" ? "alert" : "check"), message);
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

export function notice(kind, ...children) {
  const ic = { warn: "alert", bad: "alert", ok: "check", gold: "info" }[kind] || "info";
  return h("div", { class: "notice " + kind }, icon(ic), h("div", {}, ...children));
}

// Modal dialog. Resolves true on confirm. With typeToConfirm the confirm
// button stays disabled until the exact text is typed.
export function confirmDialog({ title, body, confirmLabel = "Confirm", danger = false, typeToConfirm = null }) {
  return new Promise((resolve) => {
    const input = typeToConfirm ? h("input", { class: "input", "aria-label": "Confirmation text", autocomplete: "off" }) : null;
    const ok = h("button", { class: "btn " + (danger ? "danger" : "primary"), disabled: !!typeToConfirm }, confirmLabel);
    const cancel = h("button", { class: "btn" }, "Cancel");
    const dlg = h("dialog", {},
      h("div", { class: "dlg-body stack" }, h("h3", {}, title), body ? h("div", { class: "muted" }, body) : null,
        typeToConfirm ? h("div", { class: "field" }, h("label", {}, "Type ", h("b", {}, typeToConfirm), " to confirm"), input) : null),
      h("div", { class: "dlg-actions" }, cancel, ok));
    if (input) input.addEventListener("input", () => { ok.disabled = input.value.trim() !== typeToConfirm; });
    const done = (v) => { dlg.close(); dlg.remove(); resolve(v); };
    ok.addEventListener("click", () => done(true));
    cancel.addEventListener("click", () => done(false));
    dlg.addEventListener("cancel", (e) => { e.preventDefault(); done(false); });
    document.body.append(dlg);
    dlg.showModal();
    (input || ok).focus();
  });
}

export function copyButton(text, label = "Copy") {
  const b = h("button", { class: "btn small", type: "button", onclick: async () => {
    try { await navigator.clipboard.writeText(text); toast("Copied"); } catch { toast("Copy failed: select the text and copy it manually", "bad"); }
  } }, icon("copy"), label);
  return b;
}

export function copyLine(text) { return h("div", { class: "copyline" }, h("code", {}, text), copyButton(text)); }

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
      h("div", {}, crumb ? h("div", { class: "crumb" }, crumb) : null, h("h1", {}, title), sub ? h("p", { class: "sub" }, sub) : null),
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
export function table(columns, rows, { onRow, empty, selectable } = {}) {
  if (!rows.length) return empty || h("div", { class: "empty" }, "Nothing here yet.");
  const head = h("tr", {}, selectable ? h("th", {}, selectable.header) : null, columns.map((c) => h("th", { class: c.cls || "" }, c.label)));
  const tb = h("tbody");
  rows.forEach((r) => {
    const tr = h("tr", { class: onRow ? "clickable" : "", tabindex: onRow ? "0" : null });
    if (onRow) {
      tr.addEventListener("click", (e) => { if (!e.target.closest("input,button,a")) onRow(r, e); });
      tr.addEventListener("keydown", (e) => { if (e.key === "Enter") onRow(r, e); });
    }
    if (selectable) tr.append(h("td", {}, selectable.cell(r, tr)));
    columns.forEach((c) => tr.append(h("td", { class: c.cls || "" }, c.render(r))));
    tb.append(tr);
  });
  return h("div", { class: "table-wrap" }, h("table", {}, h("thead", {}, head), tb));
}

export function emptyState(title, text, action) {
  return h("div", { class: "empty" }, h("strong", {}, title), h("div", {}, text), action || null);
}

export function tabs(items, initial) {
  let current = initial || items[0].id;
  const host = h("div", { class: "tabbody" });
  const bar = h("div", { class: "tabs", role: "tablist" });
  const draw = async () => {
    bar.querySelectorAll("button").forEach((b) => b.setAttribute("aria-selected", String(b.dataset.id === current)));
    const it = items.find((i) => i.id === current);
    host.replaceChildren(h("div", { class: "muted" }, "Loading..."));
    try { host.replaceChildren(...[].concat(await it.render()).filter(Boolean)); } catch (e) { host.replaceChildren(errorNotice(e)); }
  };
  items.forEach((it) => {
    const b = h("button", { role: "tab", type: "button", "data-id": it.id, onclick: () => { if (it.beforeLeave && it.beforeLeave() === false) return; current = it.id; draw(); } }, it.label);
    bar.append(b);
  });
  draw();
  return h("div", {}, bar, host);
}

export async function groupsCache(force) {
  if (!state.groups || force) state.groups = (await api("GET", "/groups")).groups;
  return state.groups;
}
