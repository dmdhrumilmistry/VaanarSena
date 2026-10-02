import { h, api, state, link, table, platformTag, ownershipTag, complianceTag, statusTag, ago, fmtTime,
  can, emptyState, go, toast, confirmDialog, notice, tabs, tabBar, icon, groupsCache, copyButton, menu, chip, badge, PLATFORM, platformLabel, panel, body } from "../core.js";
import { select, tagsInput, field } from "../forms.js";
import { paramsForm, allowedCommands, commandLabel, commandDrawer, quickSend, INSTANT_TYPES, deliveryNote } from "../commands.js";

const RANK = { auditor: 1, operator: 2, admin: 3 };
const roleOK = (r) => RANK[state.me.role] >= RANK[r];
const PAGE_SIZE = 25;
const DAY = 864e5;

const deviceName = (d) => d.name || d.serial || "Unnamed device";
const issuesOf = (d) => (((d.facts || {}).compliance || {}).issues) || [];
const isStale = (d) => !d.lastSeenAt || Date.now() - new Date(d.lastSeenAt).getTime() > 7 * DAY;
export const needsAttention = (d) => d.status === "enrolled" && (d.compliant === false || isStale(d));
const isActive = (d) => d.status !== "retired" && d.status !== "wiped";

// Platform glyph in a rounded tile, used in lists and the device header.
export const platformTile = (d, big) => h("span", { class: "dev-tile" + (big ? " big" : "") }, icon((PLATFORM[d.platform] || {}).icon || "devices"));

// Why a device is in the needs attention list: the first compliance issue, or how long it has been silent.
export function attentionIssue(d) {
  if (d.compliant === false) {
    const is = issuesOf(d);
    return h("span", { class: "status" }, h("span", { class: "dot bad" }), is.length ? is[0] : "Not compliant", is.length > 1 ? h("span", { class: "muted" }, " +" + (is.length - 1)) : null);
  }
  const days = d.lastSeenAt ? Math.floor((Date.now() - new Date(d.lastSeenAt).getTime()) / DAY) : null;
  return h("span", { class: "status" }, h("span", { class: "dot warn" }), days === null ? "Never checked in" : "Silent for " + days + " days");
}

// Kebab menu for one device, shared by the device list and the overview.
export function rowActions(d) {
  const allowed = can("operator") && isActive(d) ? allowedCommands(d, roleOK) : [];
  const btn = h("button", { type: "button", class: "iconbtn", "aria-label": "Actions for " + deviceName(d) }, icon("more"));
  const refresh = allowed.find((c) => c.type === "refresh");
  menu(btn, () => [
    { label: "View details", icon: "arrowRight", href: "/devices/" + d.id },
    allowed.length ? { label: "Send command...", icon: "send", onClick: () => commandDrawer(d, allowed) } : null,
    refresh ? { label: "Refresh inventory", icon: "refresh", onClick: () => quickSend(d, refresh) } : null,
    { separator: true },
    { label: "Copy device ID", icon: "copy", onClick: () => copyText(d.id) },
  ]);
  return btn;
}

async function copyText(text) {
  try { await navigator.clipboard.writeText(text); toast("Copied"); } catch { toast("Copy failed: select the text and copy it manually", "bad"); }
}

function csvCell(v) {
  let t = v === null || v === undefined ? "" : String(v);
  if (/^[=+\-@\t\r]/.test(t)) t = "'" + t;
  return '"' + t.replace(/"/g, '""') + '"';
}

function exportCsv(devs) {
  const head = ["Name", "Serial", "Platform", "OS version", "Ownership", "Status", "Compliance", "User", "Last seen"];
  const lines = [head.map(csvCell).join(",")].concat(devs.map((d) => [d.name, d.serial, platformLabel(d.platform), d.osVersion, d.ownership, d.status,
    d.compliant === true ? "Compliant" : d.compliant === false ? "Not compliant" : "Unknown", d.assignee, d.lastSeenAt || ""].map(csvCell).join(",")));
  const url = URL.createObjectURL(new Blob([lines.join("\r\n") + "\r\n"], { type: "text/csv" }));
  const a = h("a", { href: url, download: "devices.csv" });
  document.body.append(a); a.click(); a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export async function list() {
  const q = new URLSearchParams(location.search);
  q.delete("_");
  const view = q.get("view") || "";
  const apiQ = new URLSearchParams(q);
  apiQ.delete("view");
  const [res, groups, stats] = await Promise.all([
    api("GET", "/devices?limit=500&" + apiQ.toString()), groupsCache(), api("GET", "/stats").catch(() => null)]);
  const devices = res.devices;
  const rows = view === "attention" ? devices.filter(needsAttention) : devices;
  const selected = new Set();
  let pg = 0;

  const navigate = (p) => { p.delete("_"); go("/devices" + (p.toString() ? "?" + p : "")); };
  const withParam = (mut) => { const p = new URLSearchParams(q); mut(p); return p; };

  // ---- saved views ----
  const tabValue = view === "attention" ? "attention"
    : q.get("ownership") === "personal" && !q.get("status") ? "personal"
    : q.get("status") === "retired" && !q.get("ownership") ? "retired" : "all";
  const viewBar = tabBar({
    label: "Device views", value: tabValue,
    items: [
      { id: "all", label: "All devices", count: stats ? stats.total : null },
      { id: "attention", label: "Needs attention" },
      { id: "personal", label: "Personal", count: stats ? stats.byOwnership.personal || 0 : null },
      { id: "retired", label: "Retired", count: stats ? stats.byStatus.retired || 0 : null },
    ],
    onChange: (id) => navigate(withParam((p) => {
      p.delete("view"); p.delete("ownership"); p.delete("status");
      if (id === "attention") p.set("view", "attention");
      else if (id === "personal") p.set("ownership", "personal");
      else if (id === "retired") p.set("status", "retired");
    })),
  });

  // ---- toolbar ----
  const search = h("input", { class: "input", type: "search", placeholder: "Filter by name, serial, user or model", value: q.get("q") || "", "aria-label": "Search devices" });
  const sel = (name, label, opts) => {
    const s = select([["", label], ...opts], q.get(name) || "", { "aria-label": label });
    s.dataset.name = name; return s;
  };
  const filters = [
    sel("platform", "All platforms", Object.entries(PLATFORM).map(([k, v]) => [k, v.label])),
    sel("ownership", "Any ownership", [["corporate", "Corporate"], ["personal", "Personal"]]),
    sel("status", "Any status", [["enrolled", "Enrolled"], ["enrolling", "Enrolling"], ["retired", "Retired"], ["wiped", "Wiped"]]),
    sel("group", "Any group", groups.map((g) => [g.id, g.name])),
  ];
  const apply = () => {
    const p = new URLSearchParams();
    if (search.value.trim()) p.set("q", search.value.trim());
    if (view) p.set("view", view);
    for (const f of filters) if (f.value) p.set(f.dataset.name, f.value);
    navigate(p);
  };
  filters.forEach((f) => f.addEventListener("change", apply));
  search.addEventListener("keydown", (e) => { if (e.key === "Enter") apply(); });

  const active = filters.filter((f) => f.value).length;
  const filterRow = h("div", { class: "dev-filterrow", id: "dev-filters", hidden: true }, filters);
  const filterBtn = h("button", { type: "button", class: "btn", "aria-expanded": "false", "aria-controls": "dev-filters",
    onclick: () => { filterRow.hidden = !filterRow.hidden; filterBtn.setAttribute("aria-expanded", String(!filterRow.hidden)); } },
  icon("filter"), "Filters", active ? h("span", { class: "count" }, String(active)) : null);

  const chips = [];
  const optLabel = (f) => f.options[f.selectedIndex].text;
  if (q.get("q")) chips.push(chip("Search: " + q.get("q"), () => navigate(withParam((p) => p.delete("q")))));
  for (const f of filters) {
    const n = f.dataset.name;
    if (!f.value) continue;
    if ((tabValue === "personal" && n === "ownership") || (tabValue === "retired" && n === "status")) continue;
    chips.push(chip(f.getAttribute("aria-label").replace(/^(All|Any) /, "").replace(/^./, (c) => c.toUpperCase()) + ": " + optLabel(f), () => navigate(withParam((p) => p.delete(n)))));
  }
  const chipRow = chips.length
    ? h("div", { class: "dev-chips" }, chips, h("button", { type: "button", class: "btn small ghost", onclick: () => navigate(withParam((p) => { for (const k of ["q", "platform", "ownership", "status", "group"]) p.delete(k); })) }, "Clear filters"))
    : null;

  // ---- bulk bar ----
  const chosen = () => devices.filter((d) => selected.has(d.id));
  const bulk = h("div", { class: "bulkbar", hidden: true, role: "region", "aria-label": "Bulk actions" });
  const drawBulk = () => {
    bulk.hidden = selected.size === 0;
    if (!selected.size) return;
    bulk.replaceChildren(h("span", { class: "grow", "aria-live": "polite" }, selected.size + (selected.size === 1 ? " device selected" : " devices selected")),
      h("button", { class: "btn primary", onclick: () => bulkCommand(chosen()) }, "Send command"),
      h("button", { class: "btn", onclick: () => bulkGroup(chosen(), groups) }, "Add to group"),
      h("button", { class: "btn", onclick: () => bulkTag(chosen()) }, "Add tags"),
      h("button", { class: "btn", onclick: () => { selected.clear(); draw(); } }, "Clear selection"));
  };

  // ---- table and pagination ----
  const host = h("div");
  const foot = h("div", { class: "dev-foot" });
  function draw() {
    const pages = Math.max(1, Math.ceil(rows.length / PAGE_SIZE));
    pg = Math.min(pg, pages - 1);
    const slice = rows.slice(pg * PAGE_SIZE, pg * PAGE_SIZE + PAGE_SIZE);
    const all = h("input", { type: "checkbox", "aria-label": "Select all devices on this page" });
    const syncAll = () => {
      const n = slice.filter((d) => selected.has(d.id)).length;
      all.checked = n > 0 && n === slice.length; all.indeterminate = n > 0 && n < slice.length;
    };
    all.addEventListener("change", () => {
      slice.forEach((d) => { if (all.checked) selected.add(d.id); else selected.delete(d.id); });
      host.querySelectorAll(".devsel").forEach((c) => { c.checked = all.checked; c.closest("tr").classList.toggle("selected", all.checked); });
      drawBulk();
    });
    const hasFilters = q.toString() !== "";
    host.replaceChildren(table([
      { label: "Device", render: (d) => h("div", { class: "dev-cell" }, platformTile(d),
        h("div", { class: "dev-cell-text" }, h("div", { class: "primary-cell" }, deviceName(d)), h("div", { class: "secondary-cell" }, d.model || (d.nativeId || "").slice(0, 18)))) },
      { label: "Platform", cls: "hide-sm", render: (d) => h("div", {}, platformTag(d.platform), h("div", { class: "secondary-cell" }, d.osVersion)) },
      { label: "Ownership", cls: "hide-sm", render: (d) => ownershipTag(d.ownership) },
      { label: "Status", cls: "hide-sm", render: (d) => statusTag(d.status) },
      { label: "Compliance", render: (d) => complianceTag(d.compliant) },
      { label: "User", cls: "hide-sm", render: (d) => d.assignee || h("span", { class: "muted" }, "None") },
      { label: "Last seen", cls: "hide-sm", render: (d) => h("span", { title: fmtTime(d.lastSeenAt), class: "nowrap" }, ago(d.lastSeenAt)) },
      { label: h("span", { class: "sr-only" }, "Actions"), cls: "act", render: (d) => rowActions(d) },
    ], slice, {
      label: "Devices",
      onRow: (d) => go("/devices/" + d.id),
      selectable: can("operator") ? {
        header: all,
        cell: (d, tr) => {
          const on = selected.has(d.id);
          tr.classList.toggle("selected", on);
          return h("input", { type: "checkbox", class: "devsel", checked: on, "aria-label": "Select " + deviceName(d), onchange: (e) => {
            if (e.target.checked) selected.add(d.id); else selected.delete(d.id);
            tr.classList.toggle("selected", e.target.checked); syncAll(); drawBulk();
          } });
        },
      } : null,
      empty: emptyState(hasFilters ? "No devices match" : "No devices enrolled yet",
        hasFilters ? "Try other filters, or clear them." : "Enrolled devices appear here with their inventory and compliance.",
        hasFilters ? link("/devices", h("span", { class: "btn" }, "Clear filters")) : can("operator") ? link("/enroll", h("span", { class: "btn primary" }, "Enroll a device")) : null,
        hasFilters ? "search" : "devices"),
    }));
    syncAll();
    drawBulk();
    if (!rows.length) { foot.hidden = true; return; }
    foot.hidden = false;
    const from = pg * PAGE_SIZE + 1, to = Math.min(rows.length, (pg + 1) * PAGE_SIZE);
    const truncated = res.total > devices.length;
    foot.replaceChildren(
      h("span", { class: "grow muted", "aria-live": "polite" }, `Showing ${from} to ${to} of ${rows.length}` + (truncated ? ` (the first ${devices.length} of ${res.total} matches, narrow the filters to see others)` : "")),
      h("div", { class: "row nowrap" },
        h("button", { class: "btn small", disabled: pg === 0, onclick: () => { pg--; draw(); } }, "Previous"),
        h("span", { class: "muted small" }, `Page ${pg + 1} of ${pages}`),
        h("button", { class: "btn small", disabled: pg >= pages - 1, onclick: () => { pg++; draw(); } }, "Next")));
  }
  draw();

  const total = stats ? stats.total : res.total;
  const platformCount = stats ? Object.keys(stats.byPlatform).length : 0;
  return h("div", { class: "page" },
    h("div", { class: "page-head" },
      h("div", { class: "page-head-text" }, h("h1", {}, "Devices"),
        h("p", { class: "sub" }, (total === 1 ? "1 device" : total + " devices") + (platformCount ? " across " + platformCount + (platformCount === 1 ? " platform" : " platforms") : ""))),
      can("operator") ? h("div", { class: "actions" }, link("/enroll", h("span", { class: "btn primary" }, icon("plus"), "Enroll devices"))) : null),
    viewBar,
    bulk,
    h("section", { class: "panel" },
      h("div", { class: "dev-toolbar" },
        h("div", { class: "dev-search" }, icon("search"), search), filterBtn,
        h("span", { class: "grow" }),
        h("button", { type: "button", class: "btn", disabled: !rows.length, onclick: () => exportCsv(rows) }, icon("download"), "Export CSV")),
      filterRow, chipRow, host, foot));
}

// ---------- bulk actions ----------
async function bulkCommand(devs) {
  const common = state.catalogue.filter((c) => devs.every((d) => c.platforms.includes(d.platform) && (d.ownership !== "personal" || c.personal)) && roleOK(c.minRole));
  if (!common.length) { toast("No command is available for every selected device. Select devices of one platform.", "bad"); return; }
  const typeSel = select(common.map((c) => [c.type, commandLabel(c.type)]), common[0].type);
  const paramsHost = h("div");
  const platforms = [...new Set(devs.map((d) => d.platform))];
  const plat = platforms.length === 1 ? platforms[0] : undefined;
  let pf;
  const draw = () => { pf = paramsForm(typeSel.value, plat); paramsHost.replaceChildren(pf); };
  typeSel.addEventListener("change", draw); draw();
  const content = h("div", { class: "stack" }, field("Command", typeSel), paramsHost,
    devs.some((d) => d.ownership === "personal") ? notice("gold", "Personal devices only offer commands limited to the work container.") : null);
  const spec = () => common.find((c) => c.type === typeSel.value);
  const ok = await confirmDialog({ title: `Send a command to ${devs.length} devices`, body: content, confirmLabel: "Send" });
  if (!ok) return;
  if (spec().destructive && !(await confirmDialog({ title: commandLabel(spec().type) + " on " + devs.length + " devices?", body: "This cannot be undone.", confirmLabel: commandLabel(spec().type), danger: true, typeToConfirm: String(devs.length) }))) return;
  let done = 0, failed = 0;
  for (const d of devs) {
    try { await api("POST", `/devices/${d.id}/commands`, { type: typeSel.value, params: pf.get() }); done++; } catch { failed++; }
  }
  toast(`${commandLabel(typeSel.value)} queued for ${done} devices` + (failed ? `, ${failed} refused` : ""), failed ? "bad" : "ok");
}

async function bulkGroup(devs, groups) {
  const statics = groups.filter((g) => g.kind === "static");
  if (!statics.length) { toast("Create a static group first. Smart groups fill themselves from their rules.", "bad"); return; }
  const gsel = select(statics.map((g) => [g.id, g.name]), statics[0].id);
  if (!(await confirmDialog({ title: `Add ${devs.length} devices to a group`, body: field("Static group", gsel), confirmLabel: "Add to group" }))) return;
  for (const d of devs) await api("POST", `/groups/${gsel.value}/devices`, { deviceId: d.id });
  toast(`Added ${devs.length} devices`);
}

async function bulkTag(devs) {
  const t = tagsInput([], "Tags to add");
  if (!(await confirmDialog({ title: `Tag ${devs.length} devices`, body: h("div", { class: "stack" }, t, h("p", { class: "small" }, "Existing tags are kept. Smart groups that use tags update right away.")), confirmLabel: "Add tags" }))) return;
  const add = t.get();
  for (const d of devs) await api("PUT", `/devices/${d.id}/tags`, { tags: [...new Set([...(d.tags || []), ...add])] });
  toast(`Tagged ${devs.length} devices`);
  go(location.pathname + location.search + (location.search ? "&" : "?") + "_=" + Date.now());
}

// ---------- device page ----------
const cmdBadge = (s) => {
  const map = { acknowledged: ["ok", "Done"], error: ["bad", "Failed"], sent: ["warn", "Sent"], not_now: ["warn", "Deferred"], queued: ["", "Queued"], cancelled: ["", "Cancelled"] };
  const [tone, label] = map[s] || ["", s];
  return badge(label, tone);
};

export async function detail(id) {
  const res = await api("GET", "/devices/" + id);
  const d = res.device;
  const name = deviceName(d);
  const active = isActive(d);
  const allowed = can("operator") && active ? allowedCommands(d, roleOK) : [];
  const reload = (tab) => go(location.pathname + "?tab=" + tab + "&_=" + Date.now());

  // ---- header actions ----
  const quickTypes = ["lock", "restart", "refresh", "apply_policy"];
  const quick = quickTypes.map((t) => allowed.find((c) => c.type === t)).filter(Boolean).slice(0, 2);
  const quickButtons = quick.map((spec) => {
    const b = h("button", { type: "button", class: "btn" }, commandLabel(spec.type));
    b.addEventListener("click", () => INSTANT_TYPES.has(spec.type) ? quickSend(d, spec, b) : commandDrawer(d, allowed, { preset: spec.type }));
    return b;
  });
  const moreBtn = h("button", { type: "button", class: "btn", "aria-label": "More actions" }, "More actions", icon("chevronDown"));
  menu(moreBtn, () => [
    allowed.length ? { label: "Send command...", icon: "send", onClick: () => commandDrawer(d, allowed) } : null,
    ...allowed.filter((c) => !quick.includes(c)).map((c) => ({ label: commandLabel(c.type), danger: !!c.destructive, onClick: () => commandDrawer(d, allowed, { preset: c.type }) })),
    allowed.length ? { separator: true } : null,
    { label: "Copy device ID", icon: "copy", onClick: () => copyText(d.id) },
    can("admin") ? { separator: true } : null,
    can("admin") ? { label: "Delete record", icon: "trash", danger: true, onClick: () => removeRecord(d) } : null,
  ]);

  const specLine = [d.model, [platformLabel(d.platform), d.osVersion].filter(Boolean).join(" ")].filter(Boolean).join(", ") + (d.assignee ? ", assigned to " + d.assignee : "");
  const header = h("section", { class: "dev-head" },
    platformTile(d, true),
    h("div", { class: "dev-head-main" },
      h("div", { class: "dev-head-title" }, h("h1", {}, name), ownershipTag(d.ownership), complianceTag(d.compliant), d.status !== "enrolled" ? statusTag(d.status) : null),
      specLine ? h("span", { class: "dev-head-line" }, specLine) : null,
      h("span", { class: "dev-head-line muted" }, "Last check-in " + ago(d.lastSeenAt).toLowerCase() + (d.enrolledAt ? ", enrolled " + fmtTime(d.enrolledAt).replace(/, \d+:\d+.*$/, "") : ""))),
    h("div", { class: "dev-head-actions" }, quickButtons, moreBtn));

  const sendButton = (cls) => h("button", { type: "button", class: "btn " + cls, onclick: () => commandDrawer(d, allowed) }, icon("send"), "Send command");

  const overview = async () => {
    const [hist, groups] = await Promise.all([
      api("GET", `/devices/${d.id}/commands`).catch(() => ({ commands: [] })),
      groupsCache().catch(() => []),
    ]);
    const mine = groups.filter((g) => (res.groups || []).includes(g.id));
    const issues = issuesOf(d);
    const prop = (label, value, mono) => h("div", { class: "prop" }, h("dt", {}, label), h("dd", { class: mono ? "mono" : "" }, value));
    const props = [
      prop("Serial number", d.serial || (d.ownership === "personal" ? "Not collected on personal devices" : "Unknown"), !!d.serial),
      prop("Model", d.model || "Unknown"),
      prop("Operating system", [platformLabel(d.platform), d.osVersion].filter(Boolean).join(" ")),
      prop("User", d.assignee || "None"),
      prop("Groups", mine.length ? h("span", { class: "dev-links" }, mine.map((g) => link("/groups/" + g.id, g.name))) : h("span", { class: "muted" }, "None")),
      prop("Tags", (d.tags || []).length ? h("span", { class: "row tight" }, d.tags.map((t) => badge(t))) : h("span", { class: "muted" }, "None")),
      prop("Enrolled", fmtTime(d.enrolledAt)),
      prop("Last check-in", fmtTime(d.lastSeenAt) + " (" + ago(d.lastSeenAt) + ")"),
      prop("Device ID", h("span", { class: "row" }, h("span", {}, d.id), copyButton(d.id)), true),
      prop("Platform ID", d.nativeId || "None", true),
    ];
    const recent = hist.commands.slice(0, 5);
    return h("div", { class: "dev-grid" },
      h("div", { class: "dev-col" },
        d.ownership === "personal" ? notice("gold", h("b", {}, "Personal device. "), "VaanarSena manages only the work container here. Device-wide actions such as erase, lock and scripts are refused by the server; Retire removes corporate data and management.") : null,
        issues.length ? notice("bad", h("b", {}, issues.length === 1 ? "1 compliance issue" : issues.length + " compliance issues"), h("ul", {}, issues.map((i) => h("li", {}, i)))) : null,
        panel({ title: "Details" }, body(h("dl", { class: "props" }, props)))),
      panel({ title: "Recent commands" },
        recent.length
          ? h("ul", { class: "cmd-list" }, recent.map((c) => h("li", {},
            h("div", { class: "grow" }, h("div", { class: "primary-cell" }, commandLabel(c.type)), h("div", { class: "secondary-cell", title: fmtTime(c.createdAt) }, ago(c.createdAt))),
            cmdBadge(c.status))))
          : emptyState("No commands yet", "Commands you send, and those queued for policy changes, appear here.", null, "send"),
        h("div", { class: "panel-foot" }, link("/devices/" + d.id + "?tab=commands", "All commands"))));
  };

  const commandsTab = async () => {
    const hist = await api("GET", `/devices/${d.id}/commands`);
    return panel({ title: "Commands", sub: allowed.length ? deliveryNote(d.platform) : active ? "Your role cannot send commands to this device." : "This device is no longer managed, so commands are disabled.",
      actions: allowed.length ? [sendButton("primary")] : null },
    table([
      { label: "Command", render: (c) => commandLabel(c.type) },
      { label: "Status", cls: "hide-sm", render: (c) => statusTag(c.status) },
      { label: "Queued", render: (c) => fmtTime(c.createdAt) },
      { label: "Finished", render: (c) => c.completedAt ? fmtTime(c.completedAt) : "" },
      { label: "Result", render: (c) => c.error ? h("span", { class: "small" }, c.error) : "" },
      { label: h("span", { class: "sr-only" }, "Actions"), cls: "act", render: (c) => ["queued", "not_now", "sent"].includes(c.status) && can("operator")
        ? h("button", { class: "btn small", onclick: async () => { await api("POST", `/commands/${c.id}/cancel`); toast("Cancelled"); reload("commands"); } }, "Cancel") : "" },
    ], hist.commands, { label: "Command history", empty: emptyState("No commands yet", "Commands you send, and those VaanarSena queues for policy changes, appear here.", null, "send") }));
  };

  const policyTab = () => panel({ title: "Effective policy", sub: "The merge of every policy and blueprint that applies to this device. Lower priority numbers win." },
    body(Object.keys(res.effectivePolicy || {}).length ? h("pre", { class: "code" }, JSON.stringify(res.effectivePolicy, null, 2)) : h("p", { class: "muted" }, "No policy applies to this device yet. Assign one to a group it belongs to.")));

  const inventoryTab = () => panel({ title: "Inventory", sub: "Reported by the device. Usable in smart group rules as facts.<path>." },
    body(h("pre", { class: "code" }, JSON.stringify(d.facts || {}, null, 2))));

  const groupsTab = async () => {
    const groups = await groupsCache(true);
    const mine = groups.filter((g) => res.groups.includes(g.id));
    const tags = tagsInput(d.tags || [], "Add a tag");
    const statics = groups.filter((g) => g.kind === "static" && !res.groups.includes(g.id));
    const gsel = statics.length ? select(statics.map((g) => [g.id, g.name]), statics[0].id, { "aria-label": "Static group" }) : null;
    return [
      panel({ title: "Groups", sub: "Smart groups follow their rules; static groups are managed by hand." }, table([
        { label: "Group", render: (g) => link("/groups/" + g.id, g.name) },
        { label: "Kind", render: (g) => g.kind === "smart" ? badge("Smart", "ok") : badge("Static") },
        { label: h("span", { class: "sr-only" }, "Actions"), cls: "act", render: (g) => g.kind === "static" && can("operator") ? h("button", { class: "btn small", onclick: async () => { await api("DELETE", `/groups/${g.id}/devices/${d.id}`); toast("Removed from " + g.name); reload("groups"); } }, "Remove") : "" },
      ], mine, { label: "Groups", empty: emptyState("Not in any group", "Add it to a static group below, or write a smart group rule that matches it.", null, "groups") }),
      can("operator") && gsel ? body(h("div", { class: "row" }, gsel, h("button", { class: "btn", onclick: async () => {
        await api("POST", `/groups/${gsel.value}/devices`, { deviceId: d.id }); toast("Added to group"); reload("groups");
      } }, "Add to static group"))) : null),
      can("operator") ? panel({ title: "Tags", sub: "Free-form labels for smart group rules, such as kiosk or sales." }, body(h("div", { class: "stack" }, tags,
        h("div", {}, h("button", { class: "btn primary", onclick: async () => { await api("PUT", `/devices/${d.id}/tags`, { tags: tags.get() }); toast("Tags saved"); } }, "Save tags"))))) : null,
    ];
  };

  const tab = new URLSearchParams(location.search).get("tab") || "overview";
  return h("div", { class: "page" },
    h("div", { class: "crumb" }, link("/devices", "Devices"), " / ", h("span", { "aria-current": "page" }, name)),
    header,
    tabs([
      { id: "overview", label: "Overview", render: overview },
      { id: "commands", label: "Commands", render: commandsTab },
      { id: "policy", label: "Policy", render: policyTab },
      { id: "inventory", label: "Inventory", render: inventoryTab },
      { id: "groups", label: "Groups and tags", render: groupsTab },
    ], tab));
}

async function removeRecord(d) {
  if (!(await confirmDialog({ title: "Delete this device record?", body: "This removes the record and its history from VaanarSena. It does not unenroll the device: retire it first if it is still managed.", confirmLabel: "Delete record", danger: true }))) return;
  await api("DELETE", "/devices/" + d.id);
  toast("Device record deleted");
  go("/devices");
}
