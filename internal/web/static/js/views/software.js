// Software inventory: the fleet-wide view (/software) and the device page tab.
// Everything reported by a device is untrusted and only ever goes through h() / textContent.
import { h, api, page, panel, link, table, tabBar, emptyState, notice, badge, drawer, icon, ago, fmtTime, toast, withBusy,
  skeleton, errorNotice, platformTag, ownershipTag, downloadCsv } from "../core.js";

const KINDS = [["app", "Apps"], ["service", "Services"], ["profile", "Profiles"]];
const KIND_LABEL = Object.fromEntries(KINDS);
const FLEET_PAGE = 50;
const DEVICE_PAGE = 100;
const EXPORT_CAP = 500;

function debounce(fn, ms) {
  let t = null;
  return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); };
}

function searchBox(value, label, placeholder) {
  const input = h("input", { class: "input", type: "search", placeholder, value: value || "", "aria-label": label, autocomplete: "off" });
  return { input, el: h("div", { class: "dev-search" }, icon("search"), input) };
}

function pager(foot, { offset, shown, total, onPage }) {
  if (!total) { foot.hidden = true; return; }
  foot.hidden = false;
  const from = offset + 1, to = offset + shown;
  foot.replaceChildren(
    h("span", { class: "grow muted", "aria-live": "polite" }, `Showing ${from.toLocaleString()} to ${to.toLocaleString()} of ${total.toLocaleString()}`),
    h("div", { class: "row nowrap" },
      h("button", { type: "button", class: "btn small", disabled: offset === 0, onclick: () => onPage(-1) }, "Previous"),
      h("button", { type: "button", class: "btn small", disabled: to >= total, onclick: () => onPage(1) }, "Next")));
}

// ---------- fleet view ----------

function versionChips(it) {
  const vs = (it.versions || []).filter((v) => v.version);
  if (!vs.length) return h("span", { class: "muted" }, "None");
  const multi = (it.versions || []).length > 1;
  const shown = vs.slice(0, 3);
  const more = (it.versions || []).length - shown.length;
  return h("div", { class: "sw-vers" },
    shown.map((v) => h("span", { class: "sw-ver", title: v.version + (multi ? " on " + v.devices + (v.devices === 1 ? " device" : " devices") : "") },
      h("span", { class: "sw-ver-text" }, v.version), multi ? h("span", { class: "sw-ver-n" }, String(v.devices)) : null)),
    more > 0 ? h("span", { class: "sw-more" }, "+" + more + " more") : null);
}

export async function view() {
  const sp = new URLSearchParams(location.search);
  let kind = KIND_LABEL[sp.get("kind")] ? sp.get("kind") : "app";
  let q = sp.get("q") || "";
  let offset = 0;
  let seq = 0;
  let last = null;

  const fetchPage = (k, term, off, limit) =>
    api("GET", `/inventory/software?kind=${k}&q=${encodeURIComponent(term)}&limit=${limit}&offset=${off}`);

  const syncUrl = () => {
    const p = new URLSearchParams();
    if (kind !== "app") p.set("kind", kind);
    if (q) p.set("q", q);
    history.replaceState(null, "", "/software" + (p.toString() ? "?" + p : ""));
  };

  const host = h("div", { class: "sw-host" });
  const foot = h("div", { class: "dev-foot" });
  const exportBtn = h("button", { type: "button", class: "btn", disabled: true }, icon("download"), "Export CSV");
  const sb = searchBox(q, "Search software", "Filter by name or identifier");

  const columns = [
    { label: "Name", render: (it) => h("div", { class: "primary-cell sw-name" }, it.name) },
    { label: "Identifier", cls: "hide-sm", render: (it) => h("span", { class: "mono sw-id" }, it.identifier || "") },
    { label: "Source", cls: "hide-sm", render: (it) => { const src = it.sources && it.sources.length ? it.sources : (it.source ? [it.source] : []); return src.length ? h("span", { class: "sw-sources" }, src.map((x) => badge(x))) : null; } },
    { label: "Devices", cls: "num", render: (it) => it.devices.toLocaleString() },
    { label: "Versions", cls: "hide-sm", render: (it) => versionChips(it) },
  ];

  function draw(res) {
    last = res;
    host.removeAttribute("aria-busy");
    exportBtn.disabled = !res.total;
    if (!res.items.length) {
      host.replaceChildren(q
        ? emptyState("No software matches", `Nothing named or identified like "${q}" in ${KIND_LABEL[kind].toLowerCase()}.`,
          h("button", { type: "button", class: "btn", onclick: () => { sb.input.value = ""; q = ""; offset = 0; load(); } }, "Clear search"), "search")
        : emptyState("Nothing reported yet",
          kind === "profile" ? "Configuration profiles are reported by Apple devices."
            : "Installed software and services arrive with device check-ins.", null, "software"));
      foot.hidden = true;
      return;
    }
    host.replaceChildren(table(columns, res.items, {
      label: KIND_LABEL[kind],
      onRow: (it) => openDrawer(it),
    }));
    pager(foot, { offset, shown: res.items.length, total: res.total, onPage: (dir) => { offset = Math.max(0, offset + dir * FLEET_PAGE); load(); } });
  }

  async function load() {
    const my = ++seq;
    syncUrl();
    host.setAttribute("aria-busy", "true");
    try {
      const res = await fetchPage(kind, q, offset, FLEET_PAGE);
      if (my === seq) draw(res);
    } catch (e) {
      if (my === seq) { host.removeAttribute("aria-busy"); host.replaceChildren(h("div", { class: "sw-pad" }, errorNotice(e))); foot.hidden = true; }
    }
  }

  function openDrawer(it) {
    const subParts = [it.identifier && it.identifier !== it.name ? it.identifier : null, (it.sources && it.sources.length ? it.sources : [it.source]).filter(Boolean).join(", ")].filter(Boolean);
    const versions = (it.versions || []).filter((v) => v.version);
    let version = "";
    let dOffset = 0;
    let dseq = 0;
    const list = h("div", { class: "sw-devs" });
    const more = h("div", { class: "sw-more-row" });
    const verSel = versions.length > 1
      ? h("select", { class: "select", "aria-label": "Filter by version", onchange: (e) => { version = e.target.value; dOffset = 0; loadDevices(false); } },
        h("option", { value: "" }, "All versions (" + it.devices + ")"),
        versions.map((v) => h("option", { value: v.version }, v.version + " (" + v.devices + ")")))
      : null;

    const dr = drawer({
      title: it.name, sub: subParts.join(" - ") || null,
      body: [h("p", { class: "muted small sw-drawer-count", "aria-live": "polite" }, ""), verSel, list, more],
    });
    const countEl = dr.body.querySelector(".sw-drawer-count");

    async function loadDevices(append) {
      const my = ++dseq;
      const qs = new URLSearchParams({ kind, name: it.name, limit: String(FLEET_PAGE), offset: String(dOffset) });
      if (version) qs.set("version", version);
      if (!append) { list.replaceChildren(skeleton(3)); more.replaceChildren(); }
      try {
        const res = await api("GET", "/inventory/software/devices?" + qs);
        if (my !== dseq) return;
        if (!append) list.replaceChildren();
        for (const d of res.items) {
          const a = link("/devices/" + d.deviceId + "?tab=software", h("span", { class: "primary-cell" }, d.deviceName || "Unnamed device"));
          a.className = "sw-dev-link";
          a.addEventListener("click", (e) => { if (!(e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0)) dr.close(); });
          list.append(h("div", { class: "sw-dev" },
            h("div", { class: "grow" }, a, h("div", { class: "row tight sw-dev-meta" }, platformTag(d.platform), ownershipTag(d.ownership))),
            d.version ? h("span", { class: "sw-ver sw-ver-plain", title: d.version }, h("span", { class: "sw-ver-text" }, d.version)) : null));
        }
        if (!res.items.length && !append) list.append(emptyState("No devices", "No device reports this item.", null, "devices"));
        const have = dOffset + res.items.length;
        countEl.textContent = res.total === 1 ? "1 device" : res.total.toLocaleString() + " devices";
        more.replaceChildren(...(have < res.total
          ? [h("button", { type: "button", class: "btn small", onclick: (e) => withBusy(e.currentTarget, async () => { dOffset = have; await loadDevices(true); }) },
            "Show more (" + (res.total - have).toLocaleString() + " left)")] : []));
      } catch (e) { if (my === dseq) list.replaceChildren(errorNotice(e)); }
    }
    loadDevices(false);
  }

  exportBtn.addEventListener("click", () => withBusy(exportBtn, async () => {
    try {
      const res = await fetchPage(kind, q, 0, EXPORT_CAP);
      downloadCsv("software-" + kind + ".csv", ["Name", "Identifier", "Source", "Devices", "Versions"],
        res.items.map((it) => [it.name, it.identifier, (it.sources || [it.source]).join("; "), it.devices, (it.versions || []).map((v) => v.version + " (" + v.devices + ")").join("; ")]));
      if (res.total > res.items.length) toast("Exported the first " + res.items.length + " of " + res.total.toLocaleString() + ". Narrow the search to export the rest.");
    } catch (e) { toast(e.message || "Export failed", "bad"); }
  }));

  const kindBar = tabBar({
    label: "Software kinds", value: kind, items: KINDS.map(([id, label]) => ({ id, label })),
    onChange: (id) => { kind = id; offset = 0; load(); },
  });
  sb.input.addEventListener("input", debounce(() => { const v = sb.input.value.trim(); if (v === q) return; q = v; offset = 0; load(); }, 250));
  sb.input.addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); q = sb.input.value.trim(); offset = 0; load(); } });

  const first = await fetchPage(kind, q, 0, FLEET_PAGE);
  draw(first);
  return page({ title: "Software", sub: "What is installed and running across the fleet, grouped by name. Select a row to see which devices have it." },
    kindBar,
    h("section", { class: "panel" },
      h("div", { class: "dev-toolbar" }, sb.el, h("span", { class: "grow" }), exportBtn),
      host, foot));
}

// ---------- device tab ----------

const SERVICE_TONE = { running: "ok", failed: "bad" };

function stateBadge(it) {
  const s = it.state || "unknown";
  const sub = it.details && it.details.sub;
  return h("span", { class: "sw-state" }, badge(s.charAt(0).toUpperCase() + s.slice(1), SERVICE_TONE[s] || ""),
    sub && sub !== s ? h("span", { class: "muted small" }, sub) : null);
}

function enabledText(it) {
  const e = it.details && it.details.enabled;
  return e ? h("span", { class: e === "enabled" || e === "static" ? "" : "muted" }, e) : h("span", { class: "muted" }, "Unknown");
}

function columnsFor(kind) {
  const name = (it) => h("div", {}, h("div", { class: "primary-cell sw-name" }, it.name),
    it.identifier && it.identifier !== it.name ? h("div", { class: "secondary-cell mono sw-id" }, it.identifier) : null);
  const managed = (it) => (it.managed ? badge("Managed", "brand") : null);
  if (kind === "service") {
    return [
      { label: "Service", render: (it) => h("div", {}, h("div", { class: "primary-cell sw-name" }, it.name),
        it.details && it.details.description ? h("div", { class: "secondary-cell" }, it.details.description) : (it.identifier && it.identifier !== it.name ? h("div", { class: "secondary-cell mono sw-id" }, it.identifier) : null)) },
      { label: "State", render: stateBadge },
      { label: "Startup", cls: "hide-sm", render: enabledText },
      { label: "Source", cls: "hide-sm", render: (it) => (it.source ? badge(it.source) : null) },
      { label: "Managed", cls: "hide-sm", render: managed },
    ];
  }
  return [
    { label: "Name", render: name },
    { label: "Version", render: (it) => (it.version ? h("span", { class: "mono" }, it.version) : h("span", { class: "muted" }, "Not reported")) },
    { label: "Publisher", cls: "hide-sm", render: (it) => it.publisher || "" },
    { label: "Source", cls: "hide-sm", render: (it) => (it.source ? badge(it.source) : null) },
    { label: "Managed", render: managed },
  ];
}

// deviceSoftware(device, refreshSpec): the Software tab. refreshSpec is the catalogue entry for the
// refresh command when this user may send it to this device (role, personal and managed rules already
// applied by the device page), otherwise undefined and the button is not shown.
export async function deviceSoftware(d, refreshSpec) {
  const base = `/devices/${d.id}/inventory`;
  let kind = "app";
  let q = "";
  let offset = 0;
  let seq = 0;
  let meta = null;

  const get = (k, term, off, limit) => api("GET", `${base}?kind=${k}&q=${encodeURIComponent(term)}&limit=${limit}&offset=${off}`);

  let res = await get(kind, q, 0, DEVICE_PAGE);
  const kinds = KINDS.filter(([k]) => res.supported && res.supported[k]);
  if (!kinds.length) {
    return panel({ title: "Software" }, emptyState("Software is not collected for this device",
      res.note || "Installed software is not reported for this platform.", null, "software"));
  }
  if (!kinds.some(([k]) => k === kind)) { kind = kinds[0][0]; res = await get(kind, q, 0, DEVICE_PAGE); }
  meta = res;

  const host = h("div", { class: "sw-host" });
  const foot = h("div", { class: "dev-foot" });
  const collected = h("span", { class: "muted small sw-collected" });
  const exportBtn = h("button", { type: "button", class: "btn", disabled: true }, icon("download"), "Export CSV");
  const sb = searchBox("", "Search installed software", "Filter by name, identifier or publisher");
  const bar = tabBar({
    label: "Software kinds", value: kind,
    items: kinds.map(([id, label]) => ({ id, label, count: (meta.counts || {})[id] })),
    onChange: (id) => { kind = id; offset = 0; load(); },
  });

  const refreshBtn = refreshSpec ? h("button", { type: "button", class: "btn" }, icon("refresh"), "Refresh inventory") : null;
  if (refreshBtn) {
    refreshBtn.addEventListener("click", () => withBusy(refreshBtn, async () => {
      try {
        await api("POST", `/devices/${d.id}/commands`, { type: refreshSpec.type, params: {} });
        toast("Refresh inventory queued. New software appears after the next check-in.");
      } catch (e) { toast(e.message || "The command was refused", "bad"); }
    }));
  }

  function drawMeta() {
    collected.textContent = meta.collectedAt ? "Collected " + ago(meta.collectedAt).toLowerCase() : "Not collected yet";
    if (meta.collectedAt) collected.title = fmtTime(meta.collectedAt); else collected.removeAttribute("title");
  }

  function draw() {
    host.removeAttribute("aria-busy");
    exportBtn.disabled = !res.total;
    drawMeta();
    if (!res.items.length) {
      foot.hidden = true;
      if (q) {
        host.replaceChildren(emptyState("No matches", `Nothing in ${KIND_LABEL[kind].toLowerCase()} matches "${q}".`,
          h("button", { type: "button", class: "btn", onclick: () => { sb.input.value = ""; q = ""; offset = 0; load(); } }, "Clear search"), "search"));
      } else if (!meta.collectedAt) {
        host.replaceChildren(emptyState("Not collected yet", "Inventory has not been collected yet. It arrives with the next check-in.", null, "clock"));
      } else {
        host.replaceChildren(emptyState("Nothing reported", `This device reported no ${KIND_LABEL[kind].toLowerCase()}.`, null, "software"));
      }
      return;
    }
    host.replaceChildren(table(columnsFor(kind), res.items, { label: KIND_LABEL[kind] + " installed on " + (d.name || "this device"), dense: true }));
    pager(foot, { offset, shown: res.items.length, total: res.total, onPage: (dir) => { offset = Math.max(0, offset + dir * DEVICE_PAGE); load(); } });
  }

  async function load() {
    const my = ++seq;
    host.setAttribute("aria-busy", "true");
    try {
      const r = await get(kind, q, offset, DEVICE_PAGE);
      if (my !== seq) return;
      res = r; meta = r;
      draw();
    } catch (e) {
      if (my === seq) { host.removeAttribute("aria-busy"); host.replaceChildren(h("div", { class: "sw-pad" }, errorNotice(e))); foot.hidden = true; }
    }
  }

  exportBtn.addEventListener("click", () => withBusy(exportBtn, async () => {
    try {
      const r = await get(kind, q, 0, 1000);
      const head = kind === "service"
        ? ["Name", "Unit", "State", "Substate", "Startup", "Description", "Source"]
        : ["Name", "Identifier", "Version", "Publisher", "Source", "Managed"];
      const rows = r.items.map((it) => kind === "service"
        ? [it.name, it.identifier, it.state, (it.details || {}).sub, (it.details || {}).enabled, (it.details || {}).description, it.source]
        : [it.name, it.identifier, it.version, it.publisher, it.source, it.managed ? "Yes" : "No"]);
      downloadCsv((d.name || "device") + "-" + kind + ".csv", head, rows);
      if (r.total > r.items.length) toast("Exported the first " + r.items.length + " of " + r.total.toLocaleString() + ". Narrow the search to export the rest.");
    } catch (e) { toast(e.message || "Export failed", "bad"); }
  }));
  sb.input.addEventListener("input", debounce(() => { const v = sb.input.value.trim(); if (v === q) return; q = v; offset = 0; load(); }, 250));
  sb.input.addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); q = sb.input.value.trim(); offset = 0; load(); } });

  draw();
  return panel({ title: "Software", sub: "Installed apps, services and profiles this device has reported.", actions: [collected, refreshBtn] },
    meta.note ? h("div", { class: "sw-pad" }, notice(d.ownership === "personal" ? "gold" : "info", meta.note)) : null,
    h("div", { class: "sw-subtabs" }, bar),
    h("div", { class: "dev-toolbar sw-toolbar" }, sb.el, h("span", { class: "grow" }), exportBtn),
    host, foot);
}
