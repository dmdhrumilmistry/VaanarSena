// VaanarSena console. Vanilla JS, no build step. All untrusted data is
// rendered through textContent (the h() helper), never innerHTML.
"use strict";

const state = { me: null, info: null, catalogue: [] };

function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === null || v === undefined || v === false) continue;
    if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (k === "class") el.className = v;
    else el.setAttribute(k, v === true ? "" : v);
  }
  for (const c of children.flat()) {
    if (c === null || c === undefined || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

async function api(method, path, body) {
  const res = await fetch("/api/v1" + path, {
    method,
    headers: { "Content-Type": "application/json", "X-Requested-With": "vaanarsena" },
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: "same-origin",
  });
  if (res.status === 401 && path !== "/auth/login") {
    // Only an expired session re-renders (to show the login page). On the
    // login page itself state.me is already null, and re-rendering here would
    // loop: render -> /auth/me -> 401 -> render.
    const hadSession = state.me !== null;
    state.me = null;
    if (hadSession) render();
    throw new Error("signed out");
  }
  if (res.status === 204) return null;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

const fmtTime = (t) => (t ? new Date(t).toLocaleString() : "-");
const ago = (t) => {
  if (!t) return "never";
  const s = (Date.now() - new Date(t).getTime()) / 1000;
  if (s < 90) return "just now";
  if (s < 5400) return Math.round(s / 60) + " min ago";
  if (s < 129600) return Math.round(s / 3600) + " h ago";
  return Math.round(s / 86400) + " d ago";
};
const can = (role) => ({ auditor: 1, operator: 2, admin: 3 }[state.me?.role] || 0) >= ({ auditor: 1, operator: 2, admin: 3 }[role]);
const ownershipBadge = (o) => h("span", { class: "badge " + (o === "personal" ? "personal" : "") }, o === "personal" ? "BYOD" : "corporate");
const compliantBadge = (c) => c === true ? h("span", { class: "badge ok" }, "compliant") : c === false ? h("span", { class: "badge bad" }, "non-compliant") : h("span", { class: "badge" }, "unknown");
const errorBox = (e) => h("div", { class: "error" }, e.message || String(e));

// --- routing ---

const routes = {
  "": dashboard, devices, device, enroll, policies, policy: policyEdit, groups, blueprints, apply: applyView, users, audit, account,
};

function nav() {
  const cur = location.hash.slice(2).split("/")[0];
  const link = (href, label, role) => (role && !can(role)) ? null :
    h("a", { href: "#/" + href, class: cur === href ? "active" : "" }, label);
  return h("nav", {},
    h("div", { class: "logo" }, "Vaanar", h("span", {}, "Sena")),
    link("", "Dashboard"), link("devices", "Devices"), link("enroll", "Enroll", "operator"),
    link("policies", "Policies"), link("groups", "Groups"), link("blueprints", "Blueprints"), link("apply", "Apply manifest", "admin"),
    link("users", "Users", "admin"),
    link("audit", "Audit log"), link("account", "Account"),
    h("div", { class: "spacer" }),
    h("div", { class: "who" }, state.me.email, h("br"), state.me.role),
    h("a", { href: "#", onclick: async (e) => { e.preventDefault(); await api("POST", "/auth/logout"); state.me = null; render(); } }, "Sign out"));
}

async function render() {
  const root = document.getElementById("app");
  if (!state.me) {
    try { state.me = await api("GET", "/auth/me"); } catch { state.me = null; }
  }
  if (!state.me) { root.replaceChildren(loginView()); return; }
  if (!state.info) {
    [state.info, state.catalogue] = await Promise.all([api("GET", "/info"), api("GET", "/commands/catalogue").then((c) => c.commands)]);
  }
  const [name, ...args] = location.hash.slice(2).split("/");
  const view = routes[name] || dashboard;
  const main = h("main", {}, h("p", { class: "muted" }, "Loading..."));
  root.replaceChildren(h("div", { class: "layout" }, nav(), main));
  // Views return null for optional sections; replaceChildren would print them.
  try { main.replaceChildren(...[].concat(await view(...args)).filter((n) => n !== null && n !== undefined && n !== false)); } catch (e) { main.replaceChildren(errorBox(e)); }
}
window.addEventListener("hashchange", render);

function loginView() {
  const email = h("input", { type: "email", placeholder: "Email", autocomplete: "username", required: true });
  const pw = h("input", { type: "password", placeholder: "Password", autocomplete: "current-password", required: true });
  const err = h("div");
  const form = h("form", { class: "panel", onsubmit: async (e) => {
    e.preventDefault();
    try {
      const r = await api("POST", "/auth/login", { email: email.value, password: pw.value });
      state.me = r.user; render();
    } catch (ex) { err.replaceChildren(errorBox(ex)); }
  } }, h("h1", {}, "VaanarSena"), h("p", { class: "muted" }, "Sign in to manage your fleet."), email, pw, err, h("button", { type: "submit" }, "Sign in"));
  return h("div", { class: "login" }, form);
}

// --- dashboard ---

async function dashboard() {
  const s = await api("GET", "/stats");
  const tile = (n, l) => h("div", { class: "panel tile" }, h("div", { class: "n" }, n), h("div", { class: "l" }, l));
  const platforms = Object.entries(state.info.platforms).map(([p, on]) =>
    h("tr", {}, h("td", {}, p === "ios" ? "Apple (iOS, iPadOS, macOS)" : p), h("td", {}, on ? h("span", { class: "badge ok" }, "enabled") : h("span", { class: "badge" }, "not configured"))));
  return [
    h("h1", {}, state.info.org),
    h("div", { class: "tiles" },
      tile(s.total, "devices"), tile(s.byStatus.enrolled || 0, "enrolled"), tile(s.byOwnership.personal || 0, "BYOD"),
      tile(s.nonCompliant, "non-compliant"), tile(s.stale7d, "not seen in 7 days")),
    h("div", { class: "grid2" },
      h("div", {}, h("h2", {}, "By platform"), table(["Platform", "Devices"], Object.entries(s.byPlatform).map(([k, v]) => [k, v]))),
      h("div", {}, h("h2", {}, "Platforms"), h("table", {}, h("tbody", {}, platforms)))),
    h("h2", {}, "Server"),
    h("div", { class: "panel kv" }, h("div", {}, "Version"), h("div", {}, state.info.version),
      h("div", {}, "Public URL"), h("div", { class: "mono" }, state.info.publicUrl),
      h("div", {}, "CA fingerprint (SHA-256)"), h("div", { class: "mono" }, state.info.caFingerprint)),
  ];
}

function table(headers, rows, onclick) {
  return h("table", {}, h("thead", {}, h("tr", {}, headers.map((x) => h("th", {}, x)))),
    h("tbody", {}, rows.length ? rows.map((r, i) => h("tr", { class: onclick ? "click" : "", onclick: onclick ? () => onclick(i) : null }, r.map((c) => h("td", {}, c))))
      : h("tr", {}, h("td", { colspan: headers.length, class: "muted" }, "Nothing here yet."))));
}

// --- devices ---

async function devices() {
  const q = new URLSearchParams(location.hash.split("?")[1] || "");
  const search = h("input", { placeholder: "Search name, serial, user", value: q.get("q") || "" });
  const sel = (name, opts) => {
    const s = h("select", {}, h("option", { value: "" }, "any " + name), opts.map((o) => h("option", { value: o, selected: q.get(name) === o }, o)));
    s.dataset.name = name; return s;
  };
  const filters = [sel("platform", ["ios", "ipados", "macos", "windows", "android", "chromeos", "linux"]), sel("ownership", ["corporate", "personal"]), sel("status", ["enrolling", "enrolled", "retired", "wiped"])];
  const apply = () => {
    const p = new URLSearchParams();
    if (search.value) p.set("q", search.value);
    for (const f of filters) if (f.value) p.set(f.dataset.name, f.value);
    location.hash = "#/devices?" + p.toString();
  };
  filters.forEach((f) => f.addEventListener("change", apply));
  search.addEventListener("keydown", (e) => { if (e.key === "Enter") apply(); });
  const res = await api("GET", "/devices?" + q.toString());
  return [
    h("h1", {}, "Devices ", h("span", { class: "muted" }, "(" + res.total + ")")),
    h("div", { class: "toolbar" }, search, filters),
    table(["Name", "Platform", "Ownership", "Status", "User", "OS", "Compliance", "Last seen"],
      res.devices.map((d) => [d.name || d.serial || d.nativeId.slice(0, 16), d.platform, ownershipBadge(d.ownership), d.status, d.assignee, d.osVersion, compliantBadge(d.compliant), ago(d.lastSeenAt)]),
      (i) => { location.hash = "#/device/" + res.devices[i].id; }),
  ];
}

async function device(id) {
  const [res, cmds] = await Promise.all([api("GET", "/devices/" + id), api("GET", "/devices/" + id + "/commands")]);
  const d = res.device;
  const allowed = state.catalogue.filter((c) => c.platforms.includes(d.platform) && (d.ownership !== "personal" || c.personal) && can(c.minRole));
  const typeSel = h("select", {}, allowed.map((c) => h("option", { value: c.type }, c.description + (c.destructive ? " (destructive)" : ""))));
  const params = h("input", { placeholder: 'params JSON, e.g. {"message":"Call IT"}' });
  const out = h("div");
  const send = h("button", { onclick: async () => {
    const spec = allowed.find((c) => c.type === typeSel.value);
    if (spec.destructive && prompt(`Type ${d.id.slice(0, 8)} to confirm: ${spec.description}`) !== d.id.slice(0, 8)) {
      out.replaceChildren(errorBox(new Error("Cancelled: confirmation did not match the first 8 characters of the device ID."))); return;
    }
    try {
      const p = params.value.trim() ? JSON.parse(params.value) : {};
      await api("POST", `/devices/${d.id}/commands`, { type: typeSel.value, params: p });
      render();
    } catch (e) { out.replaceChildren(errorBox(e)); }
  } }, "Send");
  const kv = (pairs) => h("div", { class: "panel kv" }, pairs.flatMap(([k, v]) => [h("div", {}, k), h("div", {}, v)]));
  return [
    h("h1", {}, d.name || d.nativeId, " ", ownershipBadge(d.ownership)),
    d.ownership === "personal" ? h("div", { class: "notice" }, "Personally owned device. Commands are limited to the managed work container; use Retire to remove corporate data and management.") : null,
    h("div", { class: "grid2" },
      kv([["Platform", d.platform], ["Status", d.status], ["Model", d.model], ["OS", d.osVersion], ["Serial", d.serial || "-"], ["User", d.assignee || "-"],
        ["Compliance", compliantBadge(d.compliant)], ["Enrolled", fmtTime(d.enrolledAt)], ["Last seen", fmtTime(d.lastSeenAt)], ["Native ID", h("span", { class: "mono" }, d.nativeId)], ["Device ID", h("span", { class: "mono" }, d.id)]]),
      h("div", {}, can("operator") && allowed.length ? h("div", { class: "panel" }, h("h2", {}, "Send command"), h("div", { class: "toolbar" }, typeSel, params, send), out) : null,
        h("h2", {}, "Effective policy"), h("pre", {}, JSON.stringify(res.effectivePolicy, null, 2)))),
    can("operator") ? await deviceGroupsPanel(d, res.groups) : null,
    h("h2", {}, "Commands"),
    table(["Command", "Status", "Queued", "Completed", "Error"], cmds.commands.map((c) => [c.type, c.status, fmtTime(c.createdAt), fmtTime(c.completedAt), c.error || ""])),
    h("h2", {}, "Inventory"), h("pre", {}, JSON.stringify(d.facts, null, 2)),
  ];
}

// Tags (which feed smart groups) and static group membership for a device.
async function deviceGroupsPanel(d, memberOf) {
  const all = (await api("GET", "/groups")).groups;
  const tags = h("input", { value: (d.tags || []).join(", "), placeholder: "e.g. sales, emea, kiosk" });
  const statics = all.filter((g) => g.kind === "static" && !memberOf.includes(g.id));
  const pick = h("select", {}, statics.map((g) => h("option", { value: g.id }, g.name)));
  const out = h("div");
  const names = memberOf.map((id) => all.find((g) => g.id === id)).filter(Boolean);
  return h("div", { class: "panel" },
    h("h2", {}, "Groups and tags"),
    h("p", {}, names.length ? names.map((g) => h("span", { class: "badge" + (g.kind === "smart" ? " ok" : "") }, g.name, " ")) : h("span", { class: "muted" }, "Not in any group.")),
    h("div", { class: "toolbar" }, h("label", {}, "Tags"), tags, h("button", { class: "secondary", onclick: async () => {
      try { await api("PUT", `/devices/${d.id}/tags`, { tags: tags.value.split(",").map((t) => t.trim()).filter(Boolean) }); render(); } catch (e) { out.replaceChildren(errorBox(e)); }
    } }, "Save tags")),
    statics.length ? h("div", { class: "toolbar" }, h("label", {}, "Static group"), pick, h("button", { class: "secondary", onclick: async () => {
      try { await api("POST", `/groups/${pick.value}/devices`, { deviceId: d.id }); render(); } catch (e) { out.replaceChildren(errorBox(e)); }
    } }, "Add")) : null,
    out);
}

// --- enrollment ---

async function enroll() {
  const [toks, groupsRes] = await Promise.all([api("GET", "/enrollment-tokens"), api("GET", "/groups")]);
  const platform = h("select", {}, ["apple", "windows", "android", "linux"].map((p) => h("option", { value: p }, p)));
  const ownership = h("select", {}, h("option", { value: "corporate" }, "corporate"), h("option", { value: "personal" }, "personal (BYOD)"));
  const assignee = h("input", { placeholder: "user@example.com (Managed Apple ID for Apple BYOD)" });
  const group = h("select", {}, h("option", { value: "" }, "none"), groupsRes.groups.map((g) => h("option", { value: g.id }, g.name)));
  const uses = h("input", { type: "number", value: "1", min: "1" });
  const hours = h("input", { type: "number", value: "72", min: "1" });
  const result = h("div");
  const create = h("button", { onclick: async () => {
    try {
      const r = await api("POST", "/enrollment-tokens", { platform: platform.value, ownership: ownership.value, assignee: assignee.value, groupId: group.value || null, maxUses: +uses.value, expiresInHours: +hours.value });
      const rows = Object.entries(r.instructions).filter(([k]) => k !== "qrCode").map(([k, v]) => [h("div", {}, k), h("div", { class: "mono" }, String(v))]);
      result.replaceChildren(h("div", { class: "secret" },
        h("p", {}, h("strong", {}, "Token (shown once): "), h("span", { class: "mono" }, r.token)),
        h("div", { class: "kv" }, rows.flat()),
        r.instructions.qrCode ? h("p", {}, h("img", { class: "qr", alt: "Enrollment QR code", src: `/api/v1/enrollment-tokens/${r.meta.id}/qr.png` })) : null));
    } catch (e) { result.replaceChildren(errorBox(e)); }
  } }, "Create enrollment token");
  const disabled = Object.entries(state.info.platforms).filter(([, on]) => !on).map(([p]) => p);
  return [
    h("h1", {}, "Enroll devices"),
    disabled.length ? h("div", { class: "notice" }, "Not configured on this server: " + disabled.join(", ") + ". See docs/platforms for setup.") : null,
    h("div", { class: "panel form" },
      h("label", {}, "Platform"), platform, h("label", {}, "Ownership"), ownership, h("label", {}, "Assignee"), assignee,
      h("label", {}, "Group"), group, h("label", {}, "Max uses"), uses, h("label", {}, "Expires in (hours)"), hours, h("span"), create),
    result,
    h("h2", {}, "Tokens"),
    table(["Platform", "Ownership", "Assignee", "Uses", "Expires", "State", ""], toks.tokens.map((t) => [t.platform, ownershipBadge(t.ownership), t.assignee, `${t.uses}/${t.maxUses}`, fmtTime(t.expiresAt),
      t.revoked ? "revoked" : new Date(t.expiresAt) < new Date() ? "expired" : t.uses >= t.maxUses ? "used" : "active",
      !t.revoked ? h("button", { class: "secondary", onclick: async (e) => { e.stopPropagation(); await api("DELETE", "/enrollment-tokens/" + t.id); render(); } }, "Revoke") : ""])),
  ];
}

// --- policies ---

const policyTemplate = {
  passcode: { required: true, minLength: 8, complex: true, maxInactivityMinutes: 5, maxFailedAttempts: 10 },
  encryption: { required: true },
  restrictions: { camera: true, screenCapture: true, usbStorage: false },
  wifi: [],
  osUpdates: { autoInstall: true, deferDays: 7 },
  apps: [],
};

async function policies() {
  const res = await api("GET", "/policies");
  return [
    h("h1", {}, "Policies"),
    h("p", { class: "muted" }, "Policies are platform neutral. Lower priority numbers win when several apply. Each platform applies what it supports; personal devices receive only work-profile scoped settings."),
    can("admin") ? h("div", { class: "toolbar" }, h("button", { onclick: () => { location.hash = "#/policy/new"; } }, "New policy")) : null,
    table(["Name", "Priority", "Version", "Groups", "Devices", "Updated"], res.policies.map((p) => [p.name, p.priority, p.version, p.groupIds.length, p.deviceIds.length, fmtTime(p.updatedAt)]),
      (i) => { location.hash = "#/policy/" + res.policies[i].id; }),
  ];
}

async function policyEdit(id) {
  const isNew = id === "new";
  const [p, groupsRes] = await Promise.all([isNew ? Promise.resolve({ name: "", description: "", priority: 100, document: policyTemplate, groupIds: [], deviceIds: [] }) : api("GET", "/policies/" + id), api("GET", "/groups")]);
  const name = h("input", { value: p.name });
  const desc = h("input", { value: p.description });
  const prio = h("input", { type: "number", value: String(p.priority) });
  const doc = h("textarea", {}, JSON.stringify(p.document, null, 2));
  const groupBoxes = groupsRes.groups.map((g) => h("label", {}, h("input", { type: "checkbox", value: g.id, checked: p.groupIds.includes(g.id) }), " " + g.name));
  const out = h("div");
  const ro = !can("admin");
  const save = h("button", { disabled: ro, onclick: async () => {
    try {
      const body = { name: name.value, description: desc.value, priority: +prio.value, document: JSON.parse(doc.value),
        groupIds: groupBoxes.map((l) => l.firstChild).filter((c) => c.checked).map((c) => c.value), deviceIds: p.deviceIds };
      const saved = await api(isNew ? "POST" : "PUT", isNew ? "/policies" : "/policies/" + id, body);
      location.hash = "#/policy/" + saved.id;
    } catch (e) { out.replaceChildren(errorBox(e)); }
  } }, "Save and push");
  const del = !isNew && !ro ? h("button", { class: "danger", onclick: async () => { if (confirm("Delete this policy?")) { await api("DELETE", "/policies/" + id); location.hash = "#/policies"; } } }, "Delete") : null;
  return [
    h("h1", {}, isNew ? "New policy" : p.name),
    h("div", { class: "panel form" }, h("label", {}, "Name"), name, h("label", {}, "Description"), desc, h("label", {}, "Priority"), prio,
      h("label", {}, "Assigned groups"), h("div", {}, groupBoxes.length ? groupBoxes : h("span", { class: "muted" }, "Create a group first."))),
    h("h2", {}, "Document"), doc, out, h("div", { class: "toolbar" }, save, del),
  ];
}

// --- groups ---

const ruleExample = { match: "all", conditions: [
  { field: "platform", op: "in", value: ["ios", "ipados"] },
  { field: "osVersion", op: "version_lt", value: "17.0" },
] };

const managedBadge = (m) => m && m !== "-" ? h("span", { class: "badge" }, "managed by " + m) : null;

async function groups(id) {
  if (id) return groupEdit(id);
  const res = await api("GET", "/groups");
  return [
    h("h1", {}, "Groups"),
    h("p", { class: "muted" }, "Static groups are managed by hand (or by manifests). Smart groups compute membership from rules over device fields, tags and inventory facts, and update automatically every minute."),
    can("admin") ? h("div", { class: "toolbar" }, h("button", { onclick: () => { location.hash = "#/groups/new"; } }, "New group")) : null,
    table(["Name", "Kind", "Devices", "Source", "Updated"], res.groups.map((g) => [g.name,
      h("span", { class: "badge" + (g.kind === "smart" ? " ok" : "") }, g.kind), g.deviceCount, managedBadge(g.managedBy) || "console", fmtTime(g.updatedAt)]),
      (i) => { location.hash = "#/groups/" + res.groups[i].id; }),
  ];
}

async function groupEdit(id) {
  const isNew = id === "new";
  const [res, schema] = await Promise.all([isNew ? Promise.resolve({ group: { name: "", description: "", kind: "smart", rules: ruleExample }, members: [] }) : api("GET", "/groups/" + id), api("GET", "/groups/schema")]);
  const g = res.group;
  const name = h("input", { value: g.name });
  const desc = h("input", { value: g.description });
  const kind = h("select", {}, ["static", "smart"].map((k) => h("option", { value: k, selected: g.kind === k }, k)));
  const rules = h("textarea", {}, JSON.stringify(g.rules || ruleExample, null, 2));
  const out = h("div");
  const preview = h("div");
  const rulesBox = h("div", {},
    h("h2", {}, "Rules"),
    h("p", { class: "muted" }, "Fields: " + schema.fields.join(", ") + ", or facts.<path> (e.g. facts.linux.diskEncrypted). Ops: " + schema.ops.join(", ") + ". Nest {match, conditions, rules} for AND/OR."),
    rules,
    h("div", { class: "toolbar" }, h("button", { class: "secondary", onclick: async () => {
      try {
        const r = await api("POST", "/groups/preview", JSON.parse(rules.value));
        preview.replaceChildren(h("p", {}, r.total + " device(s) match"), table(["Name", "Platform", "OS", "Ownership"], r.devices.map((d) => [d.name || d.nativeId, d.platform, d.osVersion, ownershipBadge(d.ownership)])));
      } catch (e) { preview.replaceChildren(errorBox(e)); }
    } }, "Preview matches")), preview);
  const sync = () => { rulesBox.hidden = kind.value !== "smart"; };
  kind.addEventListener("change", sync); sync();
  const save = h("button", { disabled: !can("admin"), onclick: async () => {
    try {
      const body = { name: name.value, description: desc.value, kind: kind.value };
      if (kind.value === "smart") body.rules = JSON.parse(rules.value);
      const saved = await api(isNew ? "POST" : "PUT", isNew ? "/groups" : "/groups/" + id, body);
      location.hash = "#/groups/" + saved.id;
    } catch (e) { out.replaceChildren(errorBox(e)); }
  } }, "Save");
  const del = !isNew && can("admin") ? h("button", { class: "danger", onclick: async () => { if (confirm("Delete this group? Policies and blueprints targeting it stop applying to its devices.")) { await api("DELETE", "/groups/" + id); location.hash = "#/groups"; } } }, "Delete") : null;
  return [
    h("h1", {}, isNew ? "New group" : g.name, " ", managedBadge(g.managedBy)),
    g.managedBy && g.managedBy !== "-" ? h("div", { class: "notice" }, "This group is managed by manifests (" + g.managedBy + "). Edits here are overwritten by the next apply from that source.") : null,
    h("div", { class: "panel form" }, h("label", {}, "Name"), name, h("label", {}, "Description"), desc, h("label", {}, "Kind"), kind),
    rulesBox, out, h("div", { class: "toolbar" }, save, del),
    isNew ? null : h("div", {}, h("h2", {}, "Members (" + res.members.length + ")"),
      g.kind === "static" ? h("p", { class: "muted" }, "Add devices from a device's page, via manifests, or via the API.") : null,
      h("p", {}, h("a", { href: "#/devices?group=" + g.id }, "View member devices"))),
  ];
}

// --- blueprints ---

const blueprintTemplate = {
  policies: [],
  policy: { passcode: { required: true, minLength: 6 } },
  onEnroll: [{ type: "refresh" }],
};

async function blueprints(id) {
  if (id) return blueprintEdit(id);
  const res = await api("GET", "/blueprints");
  return [
    h("h1", {}, "Blueprints"),
    h("p", { class: "muted" }, "A blueprint bundles policies, an inline policy (including custom payloads) and onboarding steps, and targets groups. Onboarding steps run once per device when it first falls in scope: at enrollment, or when it later joins a targeted group."),
    can("admin") ? h("div", { class: "toolbar" }, h("button", { onclick: () => { location.hash = "#/blueprints/new"; } }, "New blueprint")) : null,
    table(["Name", "Priority", "Version", "Groups", "Source", "Updated"], res.blueprints.map((b) => [b.name, b.priority, b.version, b.groupIds.length, managedBadge(b.managedBy) || "console", fmtTime(b.updatedAt)]),
      (i) => { location.hash = "#/blueprints/" + res.blueprints[i].id; }),
  ];
}

async function blueprintEdit(id) {
  const isNew = id === "new";
  const [b, gs] = await Promise.all([isNew ? Promise.resolve({ name: "", description: "", priority: 100, spec: blueprintTemplate, groupIds: [] }) : api("GET", "/blueprints/" + id), api("GET", "/groups")]);
  const name = h("input", { value: b.name });
  const desc = h("input", { value: b.description });
  const prio = h("input", { type: "number", value: String(b.priority) });
  const spec = h("textarea", {}, JSON.stringify(b.spec, null, 2));
  const boxes = gs.groups.map((g) => h("label", {}, h("input", { type: "checkbox", value: g.id, checked: b.groupIds.includes(g.id) }), " " + g.name + " (" + g.kind + ")"));
  const out = h("div");
  const save = h("button", { disabled: !can("admin"), onclick: async () => {
    try {
      const saved = await api(isNew ? "POST" : "PUT", isNew ? "/blueprints" : "/blueprints/" + id, { name: name.value, description: desc.value, priority: +prio.value,
        groupIds: boxes.map((l) => l.firstChild).filter((c) => c.checked).map((c) => c.value), spec: JSON.parse(spec.value) });
      location.hash = "#/blueprints/" + saved.id;
    } catch (e) { out.replaceChildren(errorBox(e)); }
  } }, "Save and push");
  const del = !isNew && can("admin") ? h("button", { class: "danger", onclick: async () => { if (confirm("Delete this blueprint?")) { await api("DELETE", "/blueprints/" + id); location.hash = "#/blueprints"; } } }, "Delete") : null;
  return [
    h("h1", {}, isNew ? "New blueprint" : b.name, " ", managedBadge(b.managedBy)),
    h("div", { class: "panel form" }, h("label", {}, "Name"), name, h("label", {}, "Description"), desc, h("label", {}, "Priority"), prio,
      h("label", {}, "Target groups"), h("div", {}, boxes.length ? boxes : h("span", { class: "muted" }, "Create a group first."))),
    h("h2", {}, "Spec"),
    h("p", { class: "muted" }, "policies: names of existing policies; policy: an inline policy document (supports custom payloads); onEnroll: [{type, params}] commands. Wipe and retire are not allowed as onboarding steps, and the BYOD guard still applies."),
    spec, out, h("div", { class: "toolbar" }, save, del),
  ];
}

// --- declarative apply ---

const manifestExample = `apiVersion: vaanarsena.io/v1
kind: Group
metadata:
  name: ios-needs-update
spec:
  kind: smart
  rules:
    match: all
    conditions:
      - {field: platform, op: in, value: [ios, ipados]}
      - {field: osVersion, op: version_lt, value: "17.0"}
---
apiVersion: vaanarsena.io/v1
kind: Blueprint
metadata:
  name: ios-update-push
spec:
  groups: [ios-needs-update]
  onEnroll:
    - {type: os_update}
`;

async function applyView() {
  const text = h("textarea", {}, manifestExample);
  const owner = h("input", { value: "console-apply", placeholder: "owner label" });
  const prune = h("input", { type: "checkbox" });
  const out = h("div");
  const run = (dry) => async () => {
    try {
      const q = new URLSearchParams({ owner: owner.value, dryRun: String(dry), prune: String(prune.checked) });
      const res = await fetch("/api/v1/apply?" + q, { method: "POST", credentials: "same-origin",
        headers: { "Content-Type": "application/yaml", "X-Requested-With": "vaanarsena" }, body: text.value });
      const data = await res.json();
      if (!res.ok) { out.replaceChildren(errorBox(new Error(data.error)), h("ul", {}, (data.problems || []).map((p) => h("li", {}, p)))); return; }
      out.replaceChildren(h("div", { class: "notice" }, data.dryRun ? "Dry run: nothing was changed." : "Applied."),
        table(["Action", "Kind", "Name"], data.changes.map((c) => [c.action, c.kind, c.name])));
    } catch (e) { out.replaceChildren(errorBox(e)); }
  };
  return [
    h("h1", {}, "Apply manifest"),
    h("p", { class: "muted" }, "Paste YAML or JSON resources (Group, Policy, Blueprint). The whole set is validated before anything is written. The same endpoint backs vsctl and GitOps; see docs/manifests.md."),
    text,
    h("div", { class: "toolbar" }, h("label", {}, "Owner "), owner, h("label", {}, prune, " prune resources of this owner that are not in the manifest"),
      h("button", { class: "secondary", onclick: run(true) }, "Dry run"), h("button", { onclick: run(false) }, "Apply")),
    out,
    h("h2", {}, "Export"),
    h("p", {}, h("a", { href: "/api/v1/export", download: "vaanarsena-export.yaml" }, "Download the current configuration as YAML"), h("span", { class: "muted" }, " (includes secrets such as Wi-Fi passphrases)")),
  ];
}

// --- users ---

async function users() {
  const res = await api("GET", "/users");
  const email = h("input", { type: "email", placeholder: "email" });
  const pw = h("input", { type: "password", placeholder: "initial password (12+ chars)" });
  const role = h("select", {}, ["auditor", "operator", "admin"].map((r) => h("option", { value: r }, r)));
  const out = h("div");
  return [
    h("h1", {}, "Users"),
    h("div", { class: "toolbar" }, email, pw, role, h("button", { onclick: async () => {
      try { await api("POST", "/users", { email: email.value, password: pw.value, role: role.value, name: "" }); render(); } catch (e) { out.replaceChildren(errorBox(e)); }
    } }, "Add user")), out,
    table(["Email", "Role", "State", "Last login", ""], res.users.map((u) => [u.email, u.role, u.disabled ? "disabled" : "active", fmtTime(u.lastLoginAt),
      u.id === state.me.id ? "" : h("button", { class: "secondary", onclick: async () => {
        try { await api("PATCH", "/users/" + u.id, { disabled: !u.disabled }); render(); } catch (e) { out.replaceChildren(errorBox(e)); }
      } }, u.disabled ? "Enable" : "Disable")])),
  ];
}

// --- audit ---

async function audit() {
  const res = await api("GET", "/audit?limit=200");
  return [
    h("h1", {}, "Audit log"),
    h("p", { class: "muted" }, "Append only. The database rejects updates and deletes on this table."),
    table(["Time", "Actor", "Action", "Target", "Details", "IP"], res.entries.map((e) => [fmtTime(e.at), e.actor, e.action, h("span", { class: "mono" }, e.target), h("span", { class: "mono" }, JSON.stringify(e.details)), e.remoteIp])),
  ];
}

// --- account ---

async function account() {
  const toks = await api("GET", "/api-tokens");
  const cur = h("input", { type: "password", placeholder: "current password", autocomplete: "current-password" });
  const nw = h("input", { type: "password", placeholder: "new password (12+ chars)", autocomplete: "new-password" });
  const tname = h("input", { placeholder: "token name, e.g. terraform" });
  const out = h("div");
  return [
    h("h1", {}, "Account"),
    h("h2", {}, "Change password"),
    h("div", { class: "toolbar" }, cur, nw, h("button", { onclick: async () => {
      try { await api("POST", "/auth/password", { current: cur.value, new: nw.value }); out.replaceChildren(h("div", { class: "notice" }, "Password changed.")); } catch (e) { out.replaceChildren(errorBox(e)); }
    } }, "Change")),
    h("h2", {}, "API tokens"),
    h("p", { class: "muted" }, "Tokens act with your role. Send them as: Authorization: Bearer <token>"),
    h("div", { class: "toolbar" }, tname, h("button", { onclick: async () => {
      try { const r = await api("POST", "/api-tokens", { name: tname.value, expiresInDays: 90 }); out.replaceChildren(h("div", { class: "secret" }, "New token (shown once): ", h("span", { class: "mono" }, r.token))); } catch (e) { out.replaceChildren(errorBox(e)); }
    } }, "Create token")), out,
    table(["Name", "Created", "Expires", "Last used", ""], toks.tokens.map((t) => [t.name, fmtTime(t.createdAt), fmtTime(t.expiresAt), fmtTime(t.lastUsedAt),
      h("button", { class: "secondary", onclick: async () => { await api("DELETE", "/api-tokens/" + t.id); render(); } }, "Delete")])),
  ];
}

render();
