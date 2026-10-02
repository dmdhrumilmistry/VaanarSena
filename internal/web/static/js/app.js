// VaanarSena console shell: sidebar, top bar, command palette, routing and sign-in.
import { h, api, state, icon, go, link, errorNotice, can, menu, skeleton, initials, platformLabel } from "./core.js";
import * as dash from "./views/dashboard.js";
import * as devices from "./views/devices.js";
import * as enroll from "./views/enroll.js";
import * as policies from "./views/policies.js";
import * as groups from "./views/groups.js";
import * as blueprints from "./views/blueprints.js";
import * as manifests from "./views/manifests.js";
import * as admin from "./views/admin.js";
import * as settings from "./views/settings.js";

const NAV = [
  ["Fleet", [["/", "Overview", "dashboard"], ["/devices", "Devices", "devices"], ["/groups", "Groups", "groups"]]],
  ["Configure", [["/policies", "Policies", "policy"], ["/blueprints", "Blueprints", "blueprint"], ["/manifests", "Manifests", "manifest", "admin"]]],
  ["Onboard", [["/enroll", "Enroll devices", "enroll", "operator"]]],
  ["Govern", [["/users", "Users", "users", "admin"], ["/audit", "Audit log", "audit"], ["/settings/platforms", "Settings", "settings", "admin"]]],
];

const ROUTES = [
  [/^\/$/, dash.view],
  [/^\/devices$/, devices.list],
  [/^\/devices\/([\w-]+)$/, devices.detail],
  [/^\/enroll$/, enroll.view],
  [/^\/policies$/, policies.list],
  [/^\/policies\/([\w-]+)$/, policies.edit],
  [/^\/groups$/, groups.list],
  [/^\/groups\/([\w-]+)$/, groups.edit],
  [/^\/blueprints$/, blueprints.list],
  [/^\/blueprints\/([\w-]+)$/, blueprints.edit],
  [/^\/manifests$/, manifests.view],
  [/^\/users$/, admin.users],
  [/^\/audit$/, admin.audit],
  [/^\/account$/, admin.account],
  [/^\/settings\/(platforms|server)$/, settings.view],
];

// ---------- theme ----------
// The stored choice is "light", "dark" or nothing (follow the system).
function currentTheme() {
  try { return localStorage.getItem("vs-theme"); } catch { return null; }
}
function applyTheme(t) {
  if (t === "light" || t === "dark") document.documentElement.dataset.theme = t; else delete document.documentElement.dataset.theme;
}
function setTheme(t) {
  try { if (t) localStorage.setItem("vs-theme", t); else localStorage.removeItem("vs-theme"); } catch { /* storage unavailable */ }
  applyTheme(t);
  const btn = document.getElementById("theme-btn");
  if (btn) btn.replaceChildren(themeIcon());
}
function isDark() {
  const t = document.documentElement.dataset.theme;
  return t === "dark" || (!t && matchMedia("(prefers-color-scheme: dark)").matches);
}
function themeIcon() { return icon(isDark() ? "moon" : "sun"); }
applyTheme(currentTheme());

// ---------- sidebar ----------
function visibleNav() {
  return NAV.map(([name, items]) => [name, items.filter(([, , , role]) => !role || can(role))]).filter(([, items]) => items.length);
}

function sidebar() {
  const path = location.pathname;
  const isCurrent = (href) => (href === "/" ? path === "/" : path === href || path.startsWith(href.replace(/\/platforms$/, "") + "/") || path === href);
  const nav = h("nav", { class: "nav", "aria-label": "Main" });
  for (const [groupName, items] of visibleNav()) {
    nav.append(h("div", { class: "navgroup" }, groupName));
    for (const [href, label, ic] of items) {
      const a = link(href, icon(ic), h("span", {}, label));
      if (isCurrent(href)) a.setAttribute("aria-current", "page");
      nav.append(a);
    }
  }
  const org = state.info && state.info.org;
  const home = link("/", h("img", { src: "/favicon.svg", alt: "" }),
    h("span", { class: "brand-text" }, h("span", { class: "brand-name" }, "VaanarSena"), org ? h("span", { class: "brand-org" }, org) : null));
  home.className = "brand";
  home.setAttribute("aria-label", "VaanarSena overview");
  return h("aside", { class: "rail sidebar" }, home, nav, h("div", { class: "spacer" }),
    state.info && state.info.version ? h("div", { class: "rail-foot" }, "Version " + state.info.version) : null);
}

// ---------- top bar ----------
function topbar(shell) {
  const search = h("button", { type: "button", class: "searchbtn", "aria-label": "Search devices and pages", onclick: () => openPalette() },
    icon("search"), h("span", { class: "ph" }, "Search devices and pages"), h("kbd", {}, "Ctrl K"));

  const themeBtn = h("button", { type: "button", class: "iconbtn bordered", id: "theme-btn", "aria-label": "Theme" }, themeIcon());
  menu(themeBtn, () => {
    const cur = currentTheme();
    return [
      { label: "Light", icon: "sun", current: cur === "light", onClick: () => setTheme("light") },
      { label: "Dark", icon: "moon", current: cur === "dark", onClick: () => setTheme("dark") },
      { label: "System", icon: "monitor", current: !cur, onClick: () => setTheme(null) },
    ];
  });

  const who = state.me.name || state.me.email;
  const role = state.me.role.charAt(0).toUpperCase() + state.me.role.slice(1);
  const userBtn = h("button", { type: "button", class: "userbtn", "aria-label": "Account menu for " + who },
    h("span", { class: "avatar", "aria-hidden": "true" }, initials(who)), h("span", { class: "name" }, who), icon("chevronDown", "chev"));
  menu(userBtn, () => [
    { label: who + " (" + role + ")", disabled: true },
    { separator: true },
    { label: "Account", icon: "account", href: "/account" },
    { label: "Sign out", icon: "logout", onClick: signOut },
  ]);

  return h("header", { class: "topbar" },
    h("button", { type: "button", class: "iconbtn menubtn", "aria-label": "Open menu", "aria-expanded": "false", onclick: (e) => {
      const open = shell.classList.toggle("nav-open");
      e.currentTarget.setAttribute("aria-expanded", String(open));
    } }, icon("menu")),
    search, h("div", { class: "spacer" }), themeBtn, userBtn);
}

async function signOut() {
  try { await api("POST", "/auth/logout"); } catch { /* ignore */ }
  state.me = null; state.info = null; state.groups = null;
  history.replaceState(null, "", "/");
  render();
}

// ---------- command palette ----------
let paletteOpen = false;
function openPalette() {
  if (paletteOpen || !state.me) return;
  paletteOpen = true;
  const prev = document.activeElement;
  const pages = [];
  for (const [group, items] of visibleNav()) for (const [href, label, ic] of items) pages.push({ href, label, icon: ic, sub: group });
  pages.push({ href: "/account", label: "Account", icon: "account", sub: "You" });

  let results = [], active = 0, seq = 0, timer = null, devs = [], term = "";
  const input = h("input", { type: "text", role: "combobox", "aria-expanded": "true", "aria-controls": "palette-list", "aria-autocomplete": "list",
    "aria-label": "Search devices and pages", placeholder: "Search devices by name, serial or user, or jump to a page", autocomplete: "off", spellcheck: "false" });
  const list = h("div", { class: "palette-list", id: "palette-list", role: "listbox", "aria-label": "Results" });
  const dlg = h("dialog", { class: "palette", "aria-label": "Command palette" },
    h("div", { class: "palette-search" }, icon("search"), input),
    list,
    h("div", { class: "palette-foot" }, h("span", {}, "Up and down to move"), h("span", {}, "Enter to open"), h("span", {}, "Esc to close")));

  const close = () => {
    clearTimeout(timer); seq++;
    dlg.close(); dlg.remove(); paletteOpen = false;
    if (prev && prev.isConnected) prev.focus();
  };
  const choose = (r) => { if (!r) return; close(); go(r.href); };

  const draw = () => {
    const q = term.toLowerCase();
    const matchedPages = pages.filter((p) => !q || p.label.toLowerCase().includes(q));
    results = [...matchedPages.map((p) => ({ ...p, group: "Pages" })), ...devs.map((d) => ({
      href: "/devices/" + d.id, label: d.name || d.serial || d.id, icon: "devices", group: "Devices",
      sub: [platformLabel(d.platform), d.model, d.serial].filter(Boolean).join(" - "),
    }))];
    if (active >= results.length) active = Math.max(0, results.length - 1);
    list.replaceChildren();
    if (!results.length) {
      list.append(h("div", { class: "palette-empty" }, q.length >= 2 ? "No matching pages or devices." : "Type to search."));
      input.removeAttribute("aria-activedescendant");
      return;
    }
    let lastGroup = "";
    results.forEach((r, i) => {
      if (r.group !== lastGroup) { list.append(h("div", { class: "palette-group", role: "presentation" }, r.group)); lastGroup = r.group; }
      list.append(h("div", { class: "palette-item", role: "option", id: "pal-" + i, "aria-selected": String(i === active),
        onclick: () => choose(r), onmousemove: () => { if (active !== i) { active = i; mark(); } } },
      icon(r.icon), h("span", { class: "grow" }, r.label), r.sub ? h("span", { class: "palette-sub" }, r.sub) : null));
    });
    input.setAttribute("aria-activedescendant", "pal-" + active);
  };
  const mark = () => {
    list.querySelectorAll("[role=option]").forEach((el, i) => el.setAttribute("aria-selected", String(i === active)));
    input.setAttribute("aria-activedescendant", "pal-" + active);
    const el = document.getElementById("pal-" + active);
    if (el) el.scrollIntoView({ block: "nearest" });
  };

  input.addEventListener("input", () => {
    term = input.value.trim();
    active = 0;
    clearTimeout(timer);
    const my = ++seq;
    if (term.length < 2) { devs = []; draw(); return; }
    draw();
    timer = setTimeout(async () => {
      try {
        const r = await api("GET", "/devices?q=" + encodeURIComponent(term) + "&limit=8");
        if (my !== seq) return;
        devs = r.devices || [];
      } catch { if (my !== seq) return; devs = []; }
      draw();
    }, 200);
  });
  input.addEventListener("keydown", (e) => {
    if (e.key === "ArrowDown") { e.preventDefault(); if (results.length) { active = (active + 1) % results.length; mark(); } }
    else if (e.key === "ArrowUp") { e.preventDefault(); if (results.length) { active = (active - 1 + results.length) % results.length; mark(); } }
    else if (e.key === "Enter") { e.preventDefault(); choose(results[active]); }
  });
  dlg.addEventListener("cancel", (e) => { e.preventDefault(); close(); });
  dlg.addEventListener("click", (e) => { if (e.target === dlg) close(); });
  document.body.append(dlg);
  dlg.showModal();
  draw();
  input.focus();
}

document.addEventListener("keydown", (e) => {
  if ((e.ctrlKey || e.metaKey) && !e.altKey && e.key.toLowerCase() === "k") {
    e.preventDefault();
    if (!paletteOpen) openPalette();
  }
});

// ---------- sign in ----------
function loginView() {
  const email = h("input", { class: "input", type: "email", autocomplete: "username", required: true, id: "login-email" });
  const pw = h("input", { class: "input", type: "password", autocomplete: "current-password", required: true, id: "login-pw" });
  const err = h("div", { "aria-live": "polite" });
  const btn = h("button", { class: "btn primary", type: "submit" }, "Sign in");
  const form = h("form", { onsubmit: async (e) => {
    e.preventDefault(); btn.disabled = true; btn.setAttribute("aria-busy", "true"); err.replaceChildren();
    try {
      const r = await api("POST", "/auth/login", { email: email.value, password: pw.value });
      state.me = r.user; render();
    } catch (ex) { err.replaceChildren(errorNotice(ex)); btn.disabled = false; btn.removeAttribute("aria-busy"); }
  } },
    h("div", { class: "field" }, h("label", { for: "login-email" }, "Work email"), email),
    h("div", { class: "field" }, h("label", { for: "login-pw" }, "Password"), pw),
    err, btn);
  return h("div", { class: "login" },
    h("div", { class: "form-side" },
      h("div", { class: "form-wrap" },
        h("div", { class: "mark" }, h("img", { src: "/favicon.svg", alt: "" }), h("span", {}, "VaanarSena")),
        h("div", { class: "intro" }, h("h1", {}, "Sign in to your console"), h("p", {}, "Use the account your administrator created for you.")),
        form,
        h("p", { class: "fine" }, "Forgot your password? Ask an administrator to set a new one under Users."))),
    h("div", { class: "sky" },
      h("div", { class: "top" }, h("span", {}, "Open source device management")),
      h("img", { class: "hero", src: "/brand/lockup-dark.webp", alt: "VaanarSena: Hanuman leaping with the Dronagiri mountain raised in one hand" }),
      h("div", { class: "copy" },
        h("p", { class: "tagline" }, "Every device in your fleet, carried home safely."),
        h("p", { class: "tagline-sub" }, "Apple, Windows, Android, ChromeOS and Linux, company-owned and personal, on infrastructure you control."))));
}

// ---------- routing ----------
let renderSeq = 0;
export async function render() {
  const root = document.getElementById("app");
  if (state.leaveGuard) state.leaveGuard.dispose();
  shownPath = location.pathname + location.search;
  const seq = ++renderSeq;
  if (!state.me) {
    try { state.me = await api("GET", "/auth/me"); } catch { state.me = null; }
  }
  if (seq !== renderSeq) return;
  if (!state.me) { root.replaceChildren(loginView()); document.title = "Sign in - VaanarSena"; return; }
  if (!state.info) {
    try {
      [state.info, state.catalogue] = await Promise.all([api("GET", "/info"), api("GET", "/commands/catalogue").then((c) => c.commands)]);
    } catch (e) { root.replaceChildren(h("main", { id: "main" }, errorNotice(e))); return; }
  }
  const path = location.pathname;
  let view = null, args = [];
  for (const [re, fn] of ROUTES) {
    const m = path.match(re);
    if (m) { view = fn; args = m.slice(1); break; }
  }
  const main = h("main", { id: "main", tabindex: "-1" }, skeleton(4));
  const shell = h("div", { class: "shell" });
  const scrim = h("button", { type: "button", class: "scrim", "aria-label": "Close menu", tabindex: "-1", onclick: () => shell.classList.remove("nav-open") });
  scrim.hidden = true;
  shell.append(sidebar(), h("div", { class: "shell-main" }, topbar(shell), main));
  new MutationObserver(() => { scrim.hidden = !shell.classList.contains("nav-open"); }).observe(shell, { attributes: true, attributeFilter: ["class"] });
  shell.insertBefore(scrim, shell.firstChild);
  shell.addEventListener("click", (e) => { if (e.target.closest(".rail a")) shell.classList.remove("nav-open"); });
  shell.addEventListener("keydown", (e) => { if (e.key === "Escape" && shell.classList.contains("nav-open")) { shell.classList.remove("nav-open"); shell.querySelector(".menubtn").focus(); } });
  root.replaceChildren(h("a", { class: "skip-link", href: "#main", onclick: (e) => { e.preventDefault(); main.focus(); } }, "Skip to content"), shell);
  if (!view) {
    main.replaceChildren(h("div", { class: "page" }, h("div", { class: "page-head" }, h("div", { class: "page-head-text" }, h("h1", {}, "Page not found"))),
      h("p", {}, "There is nothing at this address. ", link("/", "Go to the overview"), ".")));
    return;
  }
  try {
    const out = await view(...args);
    if (seq !== renderSeq) return;
    main.replaceChildren(...[].concat(out).filter((n) => n !== null && n !== undefined && n !== false));
    const h1 = main.querySelector("h1");
    document.title = (h1 ? h1.textContent + " - " : "") + "VaanarSena";
  } catch (e) {
    if (seq === renderSeq) main.replaceChildren(h("div", { class: "page" }, errorNotice(e)));
  }
  window.scrollTo(0, 0);
}

// Remember the page on screen so a guarded back/forward can return to it.
let shownPath = location.pathname + location.search;
window.addEventListener("popstate", () => {
  const g = state.leaveGuard, target = location.pathname + location.search;
  if (g && g.isDirty()) { history.pushState(null, "", shownPath); g.confirmLeave(target); return; }
  render();
});
window.addEventListener("vs:navigate", render);
window.addEventListener("vs:signedout", render);
render();
