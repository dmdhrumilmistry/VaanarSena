import { h, api, state, page, panel, body, link, table, platformTag, ownershipTag, complianceTag, statusTag, ago, fmtTime,
  can, emptyState, go, toast, confirmDialog, errorNotice, notice, tabs, icon, groupsCache, copyButton } from "../core.js";
import { select, input, tagsInput, field } from "../forms.js";
import { paramsForm, allowedCommands, commandLabel } from "../commands.js";

const RANK = { auditor: 1, operator: 2, admin: 3 };
const roleOK = (r) => RANK[state.me.role] >= RANK[r];

export async function list() {
  const q = new URLSearchParams(location.search);
  const [res, groups] = await Promise.all([api("GET", "/devices?limit=500&" + q.toString()), groupsCache()]);
  const devices = res.devices;
  const selected = new Set();

  const search = h("input", { class: "input", type: "search", placeholder: "Search name, serial, user or model", value: q.get("q") || "", "aria-label": "Search devices" });
  const sel = (name, label, opts) => {
    const s = select([["", label], ...opts], q.get(name) || "", { "aria-label": label });
    s.dataset.name = name; return s;
  };
  const filters = [
    sel("platform", "All platforms", [["ios", "iOS"], ["ipados", "iPadOS"], ["macos", "macOS"], ["windows", "Windows"], ["android", "Android"], ["chromeos", "ChromeOS"], ["linux", "Linux"]]),
    sel("ownership", "Any ownership", [["corporate", "Corporate"], ["personal", "Personal"]]),
    sel("status", "Any status", [["enrolled", "Enrolled"], ["enrolling", "Enrolling"], ["retired", "Retired"], ["wiped", "Wiped"]]),
    sel("group", "Any group", groups.map((g) => [g.id, g.name])),
  ];
  const apply = () => {
    const p = new URLSearchParams();
    if (search.value.trim()) p.set("q", search.value.trim());
    for (const f of filters) if (f.value) p.set(f.dataset.name, f.value);
    go("/devices" + (p.toString() ? "?" + p : ""));
  };
  filters.forEach((f) => f.addEventListener("change", apply));
  search.addEventListener("keydown", (e) => { if (e.key === "Enter") apply(); });

  const bulk = h("div", { class: "bulkbar", hidden: true });
  const drawBulk = () => {
    bulk.hidden = selected.size === 0;
    if (!selected.size) return;
    bulk.replaceChildren(h("span", {}, selected.size + " selected"),
      h("button", { class: "btn primary", onclick: () => bulkCommand(devices.filter((d) => selected.has(d.id))) }, "Send command"),
      h("button", { class: "btn", onclick: () => bulkGroup(devices.filter((d) => selected.has(d.id)), groups) }, "Add to group"),
      h("button", { class: "btn", onclick: () => bulkTag(devices.filter((d) => selected.has(d.id))) }, "Add tags"),
      h("button", { class: "btn", onclick: () => { selected.clear(); document.querySelectorAll(".devsel").forEach((c) => { c.checked = false; c.closest("tr").classList.remove("selected"); }); drawBulk(); } }, "Clear"));
  };
  const all = h("input", { type: "checkbox", "aria-label": "Select all devices", onchange: () => {
    document.querySelectorAll(".devsel").forEach((c) => { c.checked = all.checked; c.dispatchEvent(new Event("change")); });
  } });

  const tbl = table([
    { label: "Device", render: (d) => h("div", {}, h("div", { class: "primary-cell" }, d.name || d.serial || "Unnamed device"), h("div", { class: "secondary-cell" }, d.model || d.nativeId.slice(0, 18))) },
    { label: "Platform", render: (d) => h("div", {}, platformTag(d.platform), h("div", { class: "secondary-cell" }, d.osVersion)) },
    { label: "Ownership", render: (d) => ownershipTag(d.ownership) },
    { label: "Status", render: (d) => statusTag(d.status) },
    { label: "Compliance", render: (d) => complianceTag(d.compliant) },
    { label: "User", render: (d) => d.assignee || h("span", { class: "muted" }, "None") },
    { label: "Last seen", render: (d) => ago(d.lastSeenAt) },
  ], devices, {
    onRow: (d) => go("/devices/" + d.id),
    selectable: can("operator") ? {
      header: all,
      cell: (d, tr) => h("input", { type: "checkbox", class: "devsel", "aria-label": "Select " + (d.name || d.id), onchange: (e) => {
        if (e.target.checked) selected.add(d.id); else selected.delete(d.id);
        tr.classList.toggle("selected", e.target.checked); drawBulk();
      } }),
    } : null,
    empty: emptyState(q.toString() ? "No devices match" : "No devices enrolled yet",
      q.toString() ? "Try other filters, or clear them." : "Enrolled devices appear here with their inventory and compliance.",
      q.toString() ? link("/devices", h("span", { class: "btn" }, "Clear filters")) : can("operator") ? link("/enroll", h("span", { class: "btn primary" }, "Enroll a device")) : null),
  });

  return page({ title: "Devices", sub: res.total === 1 ? "1 device" : res.total + " devices",
    actions: can("operator") ? [link("/enroll", h("span", { class: "btn primary" }, icon("plus"), "Enroll devices"))] : [] },
    h("div", { class: "filters" }, h("div", { class: "search" }, search), ...filters),
    bulk, h("section", { class: "panel" }, tbl));
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
export async function detail(id) {
  const res = await api("GET", "/devices/" + id);
  const d = res.device;
  const name = d.name || d.serial || "Unnamed device";
  const allowed = can("operator") ? allowedCommands(d, roleOK) : [];

  const overview = () => {
    const props = [
      ["Platform", h("span", {}, platformTag(d.platform), " ", d.osVersion)], ["Model", d.model || "Unknown"],
      ["Serial number", d.serial || (d.ownership === "personal" ? "Not collected on personal devices" : "Unknown")],
      ["User", d.assignee || "None"], ["Status", statusTag(d.status)], ["Compliance", complianceTag(d.compliant)],
      ["Enrolled", fmtTime(d.enrolledAt)], ["Last check-in", fmtTime(d.lastSeenAt) + " (" + ago(d.lastSeenAt) + ")"],
      ["Device ID", h("span", { class: "row" }, h("span", { class: "mono" }, d.id), copyButton(d.id))],
      ["Platform ID", h("span", { class: "mono" }, d.nativeId)],
    ];
    const issues = (((d.facts || {}).compliance || {}).issues) || [];
    return [
      d.ownership === "personal" ? notice("gold", h("b", {}, "Personal device. "), "VaanarSena manages only the work container here. Device-wide actions such as erase, lock and scripts are refused by the server; Retire removes corporate data and management.") : null,
      issues.length ? notice("bad", h("b", {}, "Compliance issues"), h("ul", {}, issues.map((i) => h("li", {}, i)))) : null,
      panel({}, body(h("dl", { class: "kv" }, props.flatMap(([k, v]) => [h("dt", {}, k), h("dd", {}, v)])))),
    ];
  };

  const commandsTab = async () => {
    const hist = await api("GET", `/devices/${d.id}/commands`);
    const out = [];
    if (allowed.length && d.status !== "retired" && d.status !== "wiped") out.push(commandPanel(d, allowed));
    out.push(panel({ title: "History" }, table([
      { label: "Command", render: (c) => commandLabel(c.type) },
      { label: "Status", render: (c) => statusTag(c.status) },
      { label: "Queued", render: (c) => fmtTime(c.createdAt) },
      { label: "Finished", render: (c) => c.completedAt ? fmtTime(c.completedAt) : "" },
      { label: "Result", render: (c) => c.error ? h("span", { class: "small" }, c.error) : "" },
      { label: "", render: (c) => ["queued", "not_now", "sent"].includes(c.status) && can("operator")
        ? h("button", { class: "btn small", onclick: async () => { await api("POST", `/commands/${c.id}/cancel`); toast("Cancelled"); go(location.pathname + "?tab=commands&_=" + Date.now()); } }, "Cancel") : "" },
    ], hist.commands, { empty: emptyState("No commands yet", "Commands you send, and those VaanarSena queues for policy changes, appear here.") })));
    return out;
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
        { label: "Kind", render: (g) => g.kind === "smart" ? h("span", { class: "badge ok" }, "Smart") : h("span", { class: "badge" }, "Static") },
        { label: "", render: (g) => g.kind === "static" && can("operator") ? h("button", { class: "btn small", onclick: async () => { await api("DELETE", `/groups/${g.id}/devices/${d.id}`); toast("Removed from " + g.name); go(location.pathname + "?tab=groups&_=" + Date.now()); } }, "Remove") : "" },
      ], mine, { empty: emptyState("Not in any group", "Add it to a static group below, or write a smart group rule that matches it.") }),
      can("operator") && gsel ? body(h("div", { class: "row" }, gsel, h("button", { class: "btn", onclick: async () => {
        await api("POST", `/groups/${gsel.value}/devices`, { deviceId: d.id }); toast("Added to group"); go(location.pathname + "?tab=groups&_=" + Date.now());
      } }, "Add to static group"))) : null),
      can("operator") ? panel({ title: "Tags", sub: "Free-form labels for smart group rules, such as kiosk or sales." }, body(h("div", { class: "stack" }, tags,
        h("div", {}, h("button", { class: "btn primary", onclick: async () => { await api("PUT", `/devices/${d.id}/tags`, { tags: tags.get() }); toast("Tags saved"); } }, "Save tags"))))) : null,
    ];
  };

  const tab = new URLSearchParams(location.search).get("tab") || "overview";
  return page({ title: name, crumb: link("/devices", "Devices"),
    sub: h("span", { class: "row" }, platformTag(d.platform), ownershipTag(d.ownership), statusTag(d.status)),
    actions: can("admin") ? [h("button", { class: "btn quiet", onclick: () => removeRecord(d) }, "Delete record")] : [] },
    tabs([
      { id: "overview", label: "Overview", render: overview },
      { id: "commands", label: "Commands", render: commandsTab },
      { id: "policy", label: "Policy", render: policyTab },
      { id: "inventory", label: "Inventory", render: inventoryTab },
      { id: "groups", label: "Groups and tags", render: groupsTab },
    ], tab));
}

function commandPanel(d, allowed) {
  const typeSel = select(allowed.map((c) => [c.type, commandLabel(c.type) + (c.destructive ? " (destructive)" : "")]), allowed[0].type, { "aria-label": "Command" });
  const host = h("div");
  const out = h("div");
  let pf;
  const desc = h("div", { class: "help" });
  const draw = () => { pf = paramsForm(typeSel.value, d.platform); host.replaceChildren(pf); desc.textContent = allowed.find((c) => c.type === typeSel.value).description; };
  typeSel.addEventListener("change", draw); draw();
  const send = h("button", { class: "btn primary", onclick: async () => {
    const spec = allowed.find((c) => c.type === typeSel.value);
    if (spec.destructive && !(await confirmDialog({ title: commandLabel(spec.type) + "?", body: spec.description + ". This cannot be undone.", confirmLabel: commandLabel(spec.type), danger: true, typeToConfirm: d.name || d.id.slice(0, 8) }))) return;
    try {
      await api("POST", `/devices/${d.id}/commands`, { type: typeSel.value, params: pf.get() });
      toast(commandLabel(typeSel.value) + " queued");
      go(location.pathname + "?tab=commands&_=" + Date.now());
    } catch (e) { out.replaceChildren(errorNotice(e)); }
  } }, "Send command");
  return panel({ title: "Send a command", sub: d.platform === "windows" ? "Windows devices pick up commands at their next check-in, within 15 minutes." : d.platform === "linux" ? "The agent picks up commands within a minute." : "Delivered right away when the device is online." },
    body(h("div", { class: "form" }, field("Command", typeSel, null), desc, host, out, h("div", {}, send))));
}

async function removeRecord(d) {
  if (!(await confirmDialog({ title: "Delete this device record?", body: "This removes the record and its history from VaanarSena. It does not unenroll the device: retire it first if it is still managed.", confirmLabel: "Delete record", danger: true }))) return;
  await api("DELETE", "/devices/" + d.id);
  toast("Device record deleted");
  go("/devices");
}
