// Policy document editor: section cards for every part of the neutral policy model,
// including custom platform payloads, with a JSON view kept in sync. Also exports
// saveBar(), the sticky unsaved-changes bar shared by the policy, group and blueprint editors.
import { h, icon, menu, drawer, confirmDialog, errorNotice, withBusy, go, state } from "./core.js";
import { field, input, select, toggle, tristate, repeater, jsonEditor, fileToBase64, filePicker, segmented, prune, check } from "./forms.js";

const RESTRICTIONS = [
  ["camera", "Camera", "Taking photos and video."],
  ["screenCapture", "Screenshots and screen recording", "On personal devices this applies to work apps only."],
  ["usbStorage", "USB storage", "Copying files to and from removable drives."],
  ["bluetooth", "Bluetooth", ""],
  ["appInstalls", "Installing apps", "Users installing apps themselves."],
  ["factoryReset", "Factory reset from settings", "Users erasing the device themselves."],
  ["developerMode", "Developer mode", "Developer options and debugging."],
];

// A section card: title and description on the left, an optional switch and extra
// controls on the right, then the body.
function card(key, title, desc, ...right) {
  const body = h("div", { class: "pcard-body" });
  const el = h("section", { class: "pcard", id: "sec-" + key },
    h("div", { class: "pcard-head" },
      h("div", { class: "grow" }, h("h2", { class: "t" }, title), h("p", { class: "d" }, desc)), ...right), body);
  el.body = body;
  el.key = key;
  el.label = title;
  return el;
}

// A card whose header switch turns the whole section on. With collapse the body is
// hidden while the switch is off.
function switchCard(key, title, desc, swLabel, aria, initial, collapse, buildBody) {
  let el;
  const sw = toggle(initial, swLabel, (v) => { if (collapse) el.body.hidden = !v; });
  sw.input.setAttribute("aria-label", aria);
  el = card(key, title, desc, sw);
  buildBody(el.body);
  el.body.hidden = (collapse && !initial) || !el.body.childNodes.length;
  el.isOn = () => sw.get();
  return el;
}

function passcodeSection(p) {
  const min = input(p?.minLength ?? 8, { type: "number", min: 0, max: 64 });
  const complex = check(p?.complex, "Require letters and numbers (not a simple PIN)");
  const inactivity = input(p?.maxInactivityMinutes ?? "", { type: "number", min: 0, max: 1440, placeholder: "minutes" });
  const attempts = input(p?.maxFailedAttempts ?? "", { type: "number", min: 2, max: 16 });
  const expiry = input(p?.expiryDays ?? "", { type: "number", min: 0, placeholder: "days" });
  const history = input(p?.historyLength ?? "", { type: "number", min: 0 });
  const el = switchCard("passcode", "Passcode", "Require a passcode to unlock the device or work profile.", "Required", "Require a passcode", !!p?.required, true, (b) => {
    b.append(h("div", { class: "pgrid" },
      field("Minimum length", min, "Characters, 0 to 64."),
      field("Complexity", complex),
      field("Lock after inactivity", inactivity, "Minutes before the screen locks."),
      field("Failed attempts before wipe", attempts, "2 to 16. Leave empty to never wipe. On personal devices only the work profile is affected."),
      field("Expires after", expiry, "Days until the passcode must change. Leave empty for never."),
      field("Remember previous", history, "Passcodes that cannot be reused.")));
  });
  el.get = () => el.isOn() ? prune({ required: true, minLength: min.get(), complex: complex.get() || undefined,
    maxInactivityMinutes: inactivity.get(), maxFailedAttempts: attempts.get(), expiryDays: expiry.get(), historyLength: history.get() }) : undefined;
  return el;
}

function encryptionSection(e) {
  const el = switchCard("encryption", "Encryption", "Require storage encryption: FileVault, BitLocker, Android encryption or LUKS.", "Required", "Require encrypted storage", !!e?.required, false, (b) => {
    b.append(h("p", { class: "help" }, "Linux devices report whether a LUKS volume is present; the agent cannot encrypt a disk after installation."));
  });
  el.get = () => (el.isOn() ? { required: true } : undefined);
  return el;
}

function restrictionsSection(r) {
  const ctrls = {};
  const count = h("span", { class: "badge" });
  const el = card("restrictions", "Restrictions", "Allow or block device features. Anything left unmanaged keeps the user's choice.", count);
  el.body.classList.add("flush");
  RESTRICTIONS.forEach(([key, label, help]) => {
    ctrls[key] = tristate(r?.[key]);
    ctrls[key].setAttribute("aria-label", label);
    el.body.append(h("div", { class: "restriction" }, h("div", {}, h("div", { class: "r-label" }, label), help ? h("div", { class: "help" }, help) : null), ctrls[key]));
  });
  const managed = () => Object.values(ctrls).filter((c) => c.get() !== undefined).length;
  const refresh = () => { const n = managed(); count.textContent = n ? n + " managed" : "None managed"; count.className = "badge" + (n ? " ok" : ""); };
  el.addEventListener("click", () => setTimeout(refresh));
  refresh();
  el.isOn = () => managed() > 0;
  el.get = () => {
    const out = {};
    for (const [k, c] of Object.entries(ctrls)) if (c.get() !== undefined) out[k] = c.get();
    return Object.keys(out).length ? out : undefined;
  };
  return el;
}

function wifiRow(w = {}) {
  const ssid = input(w.ssid || "", { placeholder: "Network name (SSID)" });
  const sec = select([["WPA2", "WPA2 Personal"], ["WPA3", "WPA3 Personal"], ["WEP", "WEP"], ["NONE", "Open"]], w.security || "WPA2");
  const pw = input(w.password || "", { type: "password", placeholder: "Passphrase", autocomplete: "new-password" });
  const hidden = check(w.hidden, "Hidden network");
  const auto = check(w.autoJoin ?? true, "Join automatically");
  sec.addEventListener("change", () => { pw.hidden = sec.value === "NONE"; });
  pw.hidden = sec.value === "NONE";
  const el = h("div", { class: "grid2" }, field("Network name", ssid), field("Security", sec), field("Passphrase", pw, w.password === "********" ? "Saved passphrase is kept unless you type a new one." : null), h("div", { class: "row" }, hidden, auto));
  el.get = () => (ssid.get() ? prune({ ssid: ssid.get(), security: sec.get(), password: sec.get() === "NONE" ? undefined : pw.get(), hidden: hidden.get() || undefined, autoJoin: auto.get() || undefined }) : null);
  return el;
}


function wifiSection(list) {
  const rep = repeater(list, wifiRow, { addLabel: "Add network", empty: "No managed networks." });
  const el = card("wifi", "Wi-Fi", "Networks the device joins. Passphrases are shown only to admins.");
  el.body.append(rep);
  el.isOn = () => rep.get().length > 0;
  el.get = () => rep.get();
  return el;
}

function updatesSection(u) {
  const auto = check(u?.autoInstall, "Install updates automatically");
  const defer = input(u?.deferDays ?? "", { type: "number", min: 0, max: 90, placeholder: "days" });
  const el = switchCard("osUpdates", "OS updates", "How and when operating system updates install.", "Managed", "Manage OS updates", !!u, true, (b) => {
    b.append(h("div", { class: "pgrid" }, field("Automatic install", auto), field("Delay updates by", defer, "0 to 90 days. Windows quality updates are capped at 30.")));
  });
  el.get = () => (el.isOn() ? { autoInstall: !!auto.get(), deferDays: defer.get() || undefined } : undefined);
  return el;
}

function appRow(a = {}) {
  const platform = select([["android", "Android"], ["apple", "Apple"], ["windows", "Windows"], ["linux", "Linux"]], a.platform || "android");
  const id = input(a.id || "", { placeholder: "Package, bundle ID or ProductCode" });
  const install = select([["required", "Install automatically"], ["available", "Available to install"], ["blocked", "Blocked"]], a.install || "required");
  const url = input(a.url || "", { placeholder: "Manifest or package URL (optional)" });
  const el = h("div", { class: "grid2" }, field("Platform", platform), field("App", id), field("Install", install), field("URL", url));
  el.get = () => (id.get() ? prune({ platform: platform.get(), id: id.get(), install: install.get(), url: url.get() }) : null);
  return el;
}


function appsSection(list) {
  const rep = repeater(list, appRow, { addLabel: "Add app", empty: "No managed apps." });
  const el = card("apps", "Apps", "Apps to install, offer or block. Android apps come from managed Google Play.");
  el.body.append(rep);
  el.isOn = () => rep.get().length > 0;
  el.get = () => rep.get();
  return el;
}

// ---- custom payloads ----
function applePayloadRow(p = {}) {
  const isFile = !!p.mobileconfig;
  let mode = isFile ? "file" : "payload";
  const type = input(p.payload?.PayloadType || "", { placeholder: "com.apple.security.firewall" });
  const rest = { ...(p.payload || {}) };
  delete rest.PayloadType;
  const keys = jsonEditor(Object.keys(rest).length ? rest : { EnableFirewall: true }, { rows: 6, label: "Payload keys" });
  let b64 = p.mobileconfig || "";
  const fname = h("span", { class: "muted small" }, b64 ? "Profile attached (" + Math.round(b64.length * 0.75 / 1024) + " KB)" : "No file chosen");
  const picker = filePicker(".mobileconfig,application/x-apple-aspen-config", "Choose .mobileconfig", async (f) => {
    b64 = await fileToBase64(f); fname.textContent = f.name + " (" + Math.round(f.size / 1024) + " KB)";
  });
  const payloadBox = h("div", { class: "stack" }, field("Payload type", type, "The PayloadType from Apple's Device Management documentation."), field("Keys", keys, "Everything except the PayloadType, as JSON."));
  const fileBox = h("div", { class: "stack" }, h("div", { class: "row" }, picker, fname),
    h("div", { class: "help muted small" }, "A profile exported from Apple Configurator, iMazing or ProfileCreator. Signed or unsigned; its payloads are merged into the VaanarSena policy profile, which the server signs."));
  const sw = segmented([["payload", "Single payload"], ["file", "Upload .mobileconfig"]], mode, (v) => { mode = v; payloadBox.hidden = v !== "payload"; fileBox.hidden = v !== "file"; });
  payloadBox.hidden = mode !== "payload"; fileBox.hidden = mode !== "file";
  const el = h("div", { class: "stack" }, sw, payloadBox, fileBox);
  el.get = () => {
    if (mode === "file") return b64 ? { mobileconfig: b64 } : null;
    if (!type.get()) return null;
    return { payload: { PayloadType: type.get(), ...(keys.get() || {}) } };
  };
  return el;
}

function windowsNodeRow(n = {}) {
  const uri = input(n.locUri || "", { placeholder: "./Device/Vendor/MSFT/Policy/Config/Area/Setting" });
  const op = select([["Replace", "Replace"], ["Add", "Add"], ["Exec", "Exec"], ["Delete", "Delete"]], n.op || "Replace");
  const fmt = select([["int", "Integer"], ["chr", "String"], ["bool", "Boolean"], ["xml", "XML"], ["b64", "Base64"], ["node", "Node"]], n.format || "int");
  const data = input(n.data || "", { placeholder: "Value" });
  const el = h("div", { class: "form" }, field("OMA-URI", uri), h("div", { class: "grid3" }, field("Operation", op), field("Format", fmt), field("Value", data)));
  el.get = () => (uri.get() ? prune({ locUri: uri.get(), op: op.get() === "Replace" ? undefined : op.get(), format: fmt.get(), data: data.get() }) : null);
  return el;
}


const PAYLOAD_KINDS = {
  apple: { label: "Apple", icon: "apple", menu: "Apple payload or profile" },
  windows: { label: "Windows", icon: "windows", menu: "Windows OMA-URI setting" },
  android: { label: "Android", icon: "android", menu: "Android policy fields" },
};

function describePayload(kind, d) {
  if (kind === "apple") {
    if (d.mobileconfig) return { title: "Configuration profile", sub: "Uploaded .mobileconfig, " + Math.round(d.mobileconfig.length * 0.75 / 1024) + " KB" };
    const type = (d.payload && d.payload.PayloadType) || "";
    const keys = Object.keys(d.payload || {}).filter((k) => k !== "PayloadType").length;
    return { title: type.split(".").pop() || "Apple payload", sub: type + (keys ? " - " + keys + (keys === 1 ? " key" : " keys") : "") };
  }
  if (kind === "windows") {
    return { title: String(d.locUri || "").split("/").filter(Boolean).pop() || "Windows setting", sub: d.locUri + (d.data !== undefined && d.data !== "" ? " = " + d.data : "") };
  }
  return { title: "Android policy fields", sub: Object.keys(d || {}).join(", ") };
}

function customSection(c) {
  const items = [];
  (c?.apple || []).forEach((d) => items.push({ kind: "apple", data: d }));
  (c?.windows || []).forEach((d) => items.push({ kind: "windows", data: d }));
  if (c?.android && Object.keys(c.android).length) items.push({ kind: "android", data: c.android });
  const scope = segmented([["corporate", "Corporate devices only"], ["all", "All devices, including personal"]], c?.scope || "corporate");
  const listHost = h("div", { class: "payload-list" });

  const addBtn = h("button", { type: "button", class: "btn small" }, icon("plus"), "Add payload", icon("chevronDown"));
  const el = card("custom", "Custom payloads", "Raw platform settings for anything the options above do not cover.", addBtn);
  const changed = () => el.dispatchEvent(new Event("change", { bubbles: true }));

  function openEditor(kind, idx) {
    const existing = idx >= 0 ? items[idx].data : undefined;
    const jed = kind === "android" ? jsonEditor(existing, { rows: 12, label: "Android policy fields" }) : null;
    const ed = kind === "apple" ? applePayloadRow(existing || {})
      : kind === "windows" ? windowsNodeRow(existing || { format: "int" })
        : field("Android Management API fields", jed,
          "Merged over the generated policy, e.g. {\"statusBarDisabled\": true}. statusReportingSettings is managed by VaanarSena.");
    const msg = h("div");
    const cancel = h("button", { type: "button", class: "btn", onclick: () => dr.close() }, "Cancel");
    const done = h("button", { type: "button", class: "btn primary", onclick: () => {
      msg.replaceChildren();
      let v;
      try { v = kind === "android" ? jed.get() : ed.get(); } catch (e) { msg.replaceChildren(errorNotice(e)); return; }
      if (kind === "android" && (v === undefined || !Object.keys(v).length)) {
        if (idx >= 0) items.splice(idx, 1);
      } else {
        if (!v) { msg.replaceChildren(errorNotice(new Error(kind === "apple" ? "Enter a payload type or choose a .mobileconfig file." : "Enter the OMA-URI."))); return; }
        if (idx >= 0) items[idx].data = v; else items.push({ kind, data: v });
      }
      dr.close(); draw(); changed();
    } }, idx >= 0 ? "Save payload" : "Add payload");
    const dr = drawer({ title: (idx >= 0 ? "Edit " : "Add ") + PAYLOAD_KINDS[kind].label + " payload",
      sub: kind === "apple" ? "A single payload or a whole configuration profile." : kind === "windows" ? "One setting for the Windows policy CSP." : "Fields merged into the Android policy.",
      body: [ed, msg], footer: [cancel, done] });
  }

  function draw() {
    listHost.replaceChildren(...(items.length ? items.map((it, i) => {
      const k = PAYLOAD_KINDS[it.kind];
      const d = describePayload(it.kind, it.data);
      return h("div", { class: "payload" },
        h("span", { class: "payload-tile", title: k.label }, icon(k.icon)),
        h("div", { class: "payload-text" }, h("div", { class: "payload-title" }, d.title), h("div", { class: "payload-sub" }, d.sub)),
        h("button", { type: "button", class: "btn small ghost", "aria-label": "Edit " + k.label + " payload " + d.title, onclick: () => openEditor(it.kind, i) }, "Edit"),
        h("button", { type: "button", class: "iconbtn", "aria-label": "Remove " + k.label + " payload " + d.title, title: "Remove", onclick: () => { items.splice(i, 1); draw(); changed(); } }, icon("trash")));
    }) : [h("div", { class: "payload-empty" }, "No custom payloads yet. Use Add payload to create one.")]));
  }
  menu(addBtn, () => Object.entries(PAYLOAD_KINDS).map(([kind, k]) => ({
    label: kind === "android" && items.some((i) => i.kind === "android") ? "Edit " + k.menu : k.menu, icon: k.icon,
    onClick: () => openEditor(kind, kind === "android" ? items.findIndex((i) => i.kind === "android") : -1),
  })));
  draw();
  el.body.append(
    field("Applies to", scope, "Raw payloads can do anything the platform allows, so they reach personal devices only if you choose so. Apple User Enrollment and Android work profiles still limit what takes effect."),
    listHost);
  el.isOn = () => items.length > 0;
  el.get = () => {
    const out = prune({
      apple: items.filter((i) => i.kind === "apple").map((i) => i.data),
      windows: items.filter((i) => i.kind === "windows").map((i) => i.data),
      android: (items.find((i) => i.kind === "android") || {}).data,
    });
    if (!out) return undefined;
    if (scope.get() === "all") out.scope = "all";
    return out;
  };
  return el;
}

// policyEditor(doc) -> element with get() returning the document and sections() returning
// the current section cards ({ id, key, label, isOn() }) for navigation.
export function policyEditor(doc = {}) {
  let current = doc || {};
  const formHost = h("div", { class: "pcards" });
  const json = jsonEditor(current, { rows: 22, label: "Policy document" });
  const err = h("div");
  let sections = [];
  const buildForm = (d) => {
    sections = [
      passcodeSection(d.passcode),
      encryptionSection(d.encryption),
      restrictionsSection(d.restrictions),
      wifiSection(d.wifi),
      updatesSection(d.osUpdates),
      appsSection(d.apps),
      customSection(d.custom),
    ];
    formHost.replaceChildren(...sections);
  };
  const fromForm = () => prune(Object.fromEntries(sections.map((s) => [s.key, s.get()]))) || {};
  buildForm(current);

  let mode = "form";
  const jsonHost = h("div", { class: "pcard", hidden: true }, h("div", { class: "pcard-head" }, h("div", { class: "grow" }, h("h2", { class: "t" }, "Policy document"),
    h("p", { class: "d" }, "The same document the API and manifests use. Unknown fields are rejected when you save."))), h("div", { class: "pcard-body" }, json));
  const sw = segmented([["form", "Form"], ["json", "JSON"]], "form", (v) => {
    err.replaceChildren();
    try {
      if (v === "json") { json.set(fromForm()); }
      else { current = json.get() || {}; buildForm(current); }
      mode = v;
      formHost.hidden = v !== "form"; jsonHost.hidden = v !== "json";
    } catch (e) {
      err.replaceChildren(h("div", { class: "notice bad" }, icon("alert"), e.message));
      sw.querySelectorAll("button").forEach((b) => b.setAttribute("aria-pressed", String(b.dataset.v === mode)));
    }
  });
  sw.setAttribute("aria-label", "Editor view");
  const el = h("div", { class: "peditor" },
    h("div", { class: "peditor-bar" }, h("div", { class: "help" }, "Settings each platform does not support are skipped on that platform."), sw), err, formHost, jsonHost);
  el.get = () => (mode === "json" ? json.get() || {} : fromForm());
  el.sections = () => (mode === "json" ? [] : sections.map((s) => ({ id: s.id, key: s.key, label: s.label, isOn: s.isOn })));
  return el;
}

// saveBar({ snapshot, isNew, createLabel, saveLabel, onSave, onDiscard }): the sticky bar at
// the bottom of an editor. It shows while the form differs from its saved state (always for
// new items), guards against leaving with unsaved changes, and shows save errors inline.
// snapshot() returns a string describing the current form. Call attach(root) once the page
// element exists. Returns { el, attach, reset, isDirty }.
export function saveBar({ snapshot, isNew = false, createLabel = "Create", saveLabel = "Save and push", onSave, onDiscard }) {
  let baseline = "";
  let dirty = false;
  let root = null;
  const errHost = h("div", { class: "savebar-err", hidden: true });
  const discard = h("button", { type: "button", class: "btn" }, isNew ? "Cancel" : "Discard");
  const save = h("button", { type: "button", class: "btn primary" }, isNew ? createLabel : saveLabel);
  const msg = h("span", { class: "savebar-msg" }, isNew ? "Review the settings, then create it." : "You have unsaved changes.");
  const el = h("div", { class: "savebar", role: "region", "aria-label": "Save changes", hidden: !isNew },
    errHost, h("div", { class: "savebar-row" }, h("span", { class: "dot warn", "aria-hidden": "true" }), msg, h("span", { class: "grow" }), discard, save));

  const snap = () => { try { return snapshot(); } catch (e) { return "!" + e.message; } };
  const check = () => { dirty = snap() !== baseline; el.hidden = !(isNew || dirty); };
  const later = () => setTimeout(check, 0);
  const onClick = (e) => {
    if (!dirty) return;
    const a = e.target.closest && e.target.closest("a[href]");
    if (!a || e.defaultPrevented || e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0 || a.target === "_blank" || a.origin !== location.origin) return;
    if (a.pathname === location.pathname && a.search === location.search) return;
    e.preventDefault(); e.stopPropagation();
    confirmDialog({ title: "Leave without saving?", body: "Your changes to this page are not saved and will be lost.", confirmLabel: "Leave page", danger: true })
      .then((ok) => { if (ok) { dispose(); go(a.pathname + a.search); } });
  };
  const onUnload = (e) => { if (dirty) { e.preventDefault(); e.returnValue = ""; } };
  const guard = {
    isDirty: () => dirty,
    confirmLeave: (path) => confirmDialog({ title: "Leave without saving?", body: "Your changes to this page are not saved and will be lost.", confirmLabel: "Leave page", danger: true })
      .then((ok) => { if (ok) { dispose(); go(path); } }),
    dispose: () => dispose(),
  };
  function dispose() {
    if (state.leaveGuard === guard) state.leaveGuard = null;
    document.removeEventListener("click", onClick, true);
    window.removeEventListener("beforeunload", onUnload);
    if (root) for (const t of ["input", "change", "click"]) root.removeEventListener(t, later);
    dirty = false;
  }
  function reset() { baseline = snap(); dirty = false; el.hidden = !isNew; errHost.hidden = true; errHost.replaceChildren(); }
  function attach(r) {
    root = r;
    baseline = snap();
    for (const t of ["input", "change", "click"]) root.addEventListener(t, later);
    document.addEventListener("click", onClick, true);
    window.addEventListener("beforeunload", onUnload);
    state.leaveGuard = guard;
  }
  save.addEventListener("click", () => withBusy(save, async () => {
    errHost.hidden = true; errHost.replaceChildren();
    try { await onSave(); reset(); } catch (e) { errHost.replaceChildren(errorNotice(e)); errHost.hidden = false; }
  }));
  discard.addEventListener("click", () => { dispose(); onDiscard(); });
  return { el, attach, reset, dispose, isDirty: () => dirty };
}
