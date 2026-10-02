// VaanarSena console shell: navigation rail, routing and sign-in.
import { h, api, state, icon, go, link, errorNotice, can } from "./core.js";
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
function currentTheme() {
  try { return localStorage.getItem("vs-theme"); } catch { return null; }
}
function applyTheme(t) {
  if (t) document.documentElement.dataset.theme = t; else delete document.documentElement.dataset.theme;
}
applyTheme(currentTheme());
function toggleTheme() {
  const dark = document.documentElement.dataset.theme === "dark" ||
    (!document.documentElement.dataset.theme && matchMedia("(prefers-color-scheme: dark)").matches);
  const next = dark ? "light" : "dark";
  try { localStorage.setItem("vs-theme", next); } catch { /* storage unavailable */ }
  applyTheme(next);
}

function brand(cls) {
  return h("span", { class: cls || "word" }, "Vaanar", h("span", {}, "Sena"));
}

function rail() {
  const path = location.pathname;
  const isCurrent = (href) => (href === "/" ? path === "/" : path === href || path.startsWith(href.replace(/\/platforms$/, "") + "/") || path === href);
  const nav = h("nav", { class: "nav", "aria-label": "Main" });
  for (const [groupName, items] of NAV) {
    const visible = items.filter(([, , , role]) => !role || can(role));
    if (!visible.length) continue;
    nav.append(h("div", { class: "navgroup" }, groupName));
    for (const [href, label, ic] of visible) {
      const a = link(href, icon(ic), label);
      if (isCurrent(href)) a.setAttribute("aria-current", "page");
      nav.append(a);
    }
  }
  const home = link("/", h("img", { src: "/brand/emblem.svg", alt: "" }), brand());
  home.className = "brand";
  home.setAttribute("aria-label", "VaanarSena overview");
  return h("aside", { class: "rail" },
    home,
    nav, h("div", { class: "spacer" }),
    h("div", { class: "me" },
      h("div", { class: "who" }, state.me.name || state.me.email),
      h("div", { class: "role" }, state.me.role.charAt(0).toUpperCase() + state.me.role.slice(1)),
      h("div", { class: "row" },
        h("button", { type: "button", onclick: () => go("/account") }, "Account"),
        h("button", { type: "button", onclick: signOut }, "Sign out"),
        h("button", { type: "button", class: "theme", onclick: toggleTheme, "aria-label": "Switch between light and dark", title: "Switch between light and dark" }, icon("moon")))));
}
rail.brandEl = brand;

async function signOut() {
  try { await api("POST", "/auth/logout"); } catch { /* ignore */ }
  state.me = null; state.info = null; state.groups = null;
  history.replaceState(null, "", "/");
  render();
}

// ---------- sign in ----------
function loginView() {
  const email = h("input", { class: "input", type: "email", autocomplete: "username", required: true, id: "login-email" });
  const pw = h("input", { class: "input", type: "password", autocomplete: "current-password", required: true, id: "login-pw" });
  const err = h("div");
  const btn = h("button", { class: "btn primary", type: "submit" }, "Sign in");
  const form = h("form", { onsubmit: async (e) => {
    e.preventDefault(); btn.disabled = true; err.replaceChildren();
    try {
      const r = await api("POST", "/auth/login", { email: email.value, password: pw.value });
      state.me = r.user; render();
    } catch (ex) { err.replaceChildren(errorNotice(ex)); btn.disabled = false; }
  } },
    h("h1", {}, "Sign in"),
    h("p", { class: "muted" }, "Use the account your administrator created for you."),
    h("div", { class: "field" }, h("label", { for: "login-email" }, "Email"), email),
    h("div", { class: "field" }, h("label", { for: "login-pw" }, "Password"), pw),
    err, btn);
  return h("div", { class: "login" },
    h("div", { class: "sky" },
      h("div", { class: "mark" }, brand()),
      h("img", { class: "hero", src: "/brand/emblem.svg", alt: "Hanuman leaping with the Dronagiri mountain raised in one hand" }),
      h("div", {},
        h("p", { class: "tagline" }, "Every device in your fleet, carried home safely."),
        h("p", { class: "tagline-sub" }, "Open source device management for Apple, Windows, Android, ChromeOS and Linux, on your own infrastructure."))),
    h("div", { class: "form-side" }, form));
}

// ---------- routing ----------
let renderSeq = 0;
export async function render() {
  const root = document.getElementById("app");
  const seq = ++renderSeq;
  if (!state.me) {
    try { state.me = await api("GET", "/auth/me"); } catch { state.me = null; }
  }
  if (seq !== renderSeq) return;
  if (!state.me) { root.replaceChildren(loginView()); document.title = "Sign in - VaanarSena"; return; }
  if (!state.info) {
    try {
      [state.info, state.catalogue] = await Promise.all([api("GET", "/info"), api("GET", "/commands/catalogue").then((c) => c.commands)]);
    } catch (e) { root.replaceChildren(h("main", {}, errorNotice(e))); return; }
  }
  const path = location.pathname;
  let view = null, args = [];
  for (const [re, fn] of ROUTES) {
    const m = path.match(re);
    if (m) { view = fn; args = m.slice(1); break; }
  }
  const main = h("main", { id: "main" }, h("p", { class: "muted" }, "Loading..."));
  const shell = h("div", { class: "shell" },
    h("div", { class: "topbar" },
      h("button", { class: "iconbtn", "aria-label": "Open menu", onclick: () => shell.classList.toggle("nav-open") }, icon("menu")),
      h("img", { src: "/brand/emblem.svg", alt: "" }), brand()),
    rail(), main);
  shell.addEventListener("click", (e) => { if (e.target.closest(".rail a")) shell.classList.remove("nav-open"); });
  root.replaceChildren(shell);
  if (!view) {
    main.replaceChildren(h("div", { class: "page" }, h("h1", { class: "page-title" }, "Page not found"),
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

window.addEventListener("popstate", render);
window.addEventListener("vs:navigate", render);
window.addEventListener("vs:signedout", render);
render();
