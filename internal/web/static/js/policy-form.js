// Policy document editor: a form for every part of the neutral policy model,
// including custom platform payloads, with a JSON view kept in sync.
import { h, icon } from "./core.js";
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

function section(title, desc, managed, buildBody, { open = false } = {}) {
  const state = h("span", { class: "state" });
  const det = h("details", { class: "cfg" },
    h("summary", {}, h("div", {}, h("div", { class: "t" }, title), h("div", { class: "d" }, desc)), state, icon("chevron", "chev")));
  const bodyEl = h("div", { class: "body" });
  det.append(bodyEl);
  const refresh = () => {
    const m = managed();
    state.replaceChildren(m ? h("span", { class: "badge ok" }, "Managed") : h("span", { class: "badge" }, "Not managed"));
  };
  buildBody(bodyEl, refresh);
  det.open = open || !!managed(); // after buildBody: managed() reads its controls
  det.addEventListener("input", refresh);
  det.addEventListener("click", () => setTimeout(refresh));
  refresh();
  return det;
}

function passcodeSection(p) {
  let on;
  const min = input(p?.minLength ?? 8, { type: "number", min: 0, max: 64, class: "short" });
  const complex = check(p?.complex, "Require letters and numbers (not a simple PIN)");
  const inactivity = input(p?.maxInactivityMinutes ?? "", { type: "number", min: 0, max: 1440, class: "short", placeholder: "minutes" });
  const attempts = input(p?.maxFailedAttempts ?? "", { type: "number", min: 2, max: 16, class: "short" });
  const expiry = input(p?.expiryDays ?? "", { type: "number", min: 0, class: "short", placeholder: "days" });
  const history = input(p?.historyLength ?? "", { type: "number", min: 0, class: "short" });
  const fields = h("div", { class: "form" },
    field("Minimum length", min, null, { inline: true }),
    field("Complexity", complex, null, { inline: true }),
    field("Lock after inactivity", inactivity, "Minutes before the screen locks.", { inline: true }),
    field("Failed attempts before wipe", attempts, "2 to 16. Leave empty to never wipe. On personal devices only the work profile is affected.", { inline: true }),
    field("Expires after", expiry, "Days until the passcode must change. Leave empty for never.", { inline: true }),
    field("Remember previous", history, "Passcodes that cannot be reused.", { inline: true }));
  const el = section("Passcode", "Require a passcode to unlock the device or work profile.", () => on.get(), (b) => {
    on = toggle(p?.required, "Require a passcode", (v) => { fields.hidden = !v; });
    fields.hidden = !p?.required;
    b.append(on, fields);
  });
  el.get = () => on.get() ? prune({ required: true, minLength: min.get(), complex: complex.get() || undefined,
    maxInactivityMinutes: inactivity.get(), maxFailedAttempts: attempts.get(), expiryDays: expiry.get(), historyLength: history.get() }) : undefined;
  return el;
}

function encryptionSection(e) {
  let on;
  const el = section("Encryption", "Require storage encryption: FileVault, BitLocker, Android encryption or LUKS.", () => on.get(), (b) => {
    on = toggle(e?.required, "Require encrypted storage");
    b.append(on, h("div", { class: "help muted small" }, "Linux devices report whether a LUKS volume is present; the agent cannot encrypt a disk after installation."));
  });
  el.get = () => (on.get() ? { required: true } : undefined);
  return el;
}

function restrictionsSection(r) {
  const ctrls = {};
  const el = section("Restrictions", "Allow or block device features. Anything left unmanaged keeps the user's choice.",
    () => Object.values(ctrls).some((c) => c.get() !== undefined), (b) => {
      RESTRICTIONS.forEach(([key, label, help]) => {
        ctrls[key] = tristate(r?.[key]);
        b.append(h("div", { class: "restriction" }, h("div", {}, h("div", {}, label), help ? h("div", { class: "help" }, help) : null), ctrls[key]));
      });
    });
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
  let rep;
  const el = section("Wi-Fi", "Networks the device joins. Passphrases are shown only to admins.", () => rep && rep.get().length > 0, (b) => {
    rep = repeater(list, wifiRow, { addLabel: "Add network", empty: "No managed networks." });
    b.append(rep);
  });
  el.get = () => rep.get();
  return el;
}

function updatesSection(u) {
  let on;
  const auto = check(u?.autoInstall, "Install updates automatically");
  const defer = input(u?.deferDays ?? "", { type: "number", min: 0, max: 90, class: "short", placeholder: "days" });
  const inner = h("div", { class: "form" }, field("Automatic install", auto, null, { inline: true }),
    field("Delay updates by", defer, "0 to 90 days. Windows quality updates are capped at 30.", { inline: true }));
  const el = section("OS updates", "How and when operating system updates install.", () => on.get(), (b) => {
    on = toggle(!!u, "Manage OS updates", (v) => { inner.hidden = !v; });
    inner.hidden = !u;
    b.append(on, inner);
  });
  el.get = () => (on.get() ? { autoInstall: !!auto.get(), deferDays: defer.get() || undefined } : undefined);
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
  let rep;
  const el = section("Apps", "Apps to install, offer or block. Android apps come from managed Google Play.", () => rep && rep.get().length > 0, (b) => {
    rep = repeater(list, appRow, { addLabel: "Add app", empty: "No managed apps." });
    b.append(rep);
  });
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

function customSection(c) {
  let scope, apple, win, android;
  const managed = () => !!(apple && (apple.get().length || win.get().length || safe(() => android.get())));
  const el = section("Custom payloads", "Raw platform settings for anything the options above do not cover.", managed, (b) => {
    scope = segmented([["corporate", "Corporate devices only"], ["all", "All devices, including personal"]], c?.scope || "corporate");
    apple = repeater(c?.apple, applePayloadRow, { addLabel: "Add Apple payload", empty: "No Apple payloads." });
    win = repeater(c?.windows, windowsNodeRow, { addLabel: "Add OMA-URI setting", empty: "No Windows settings.", newItem: () => ({ format: "int" }) });
    android = jsonEditor(c?.android, { rows: 6, label: "Android policy fields" });
    b.append(
      field("Applies to", scope, "Raw payloads can do anything the platform allows, so they reach personal devices only if you choose so. Apple User Enrollment and Android work profiles still limit what takes effect."),
      h("div", { class: "stack" }, h("div", { class: "row" }, icon("apple"), h("b", {}, "Apple")), apple),
      h("div", { class: "stack" }, h("div", { class: "row" }, icon("windows"), h("b", {}, "Windows")), win),
      h("div", { class: "stack" }, h("div", { class: "row" }, icon("android"), h("b", {}, "Android")),
        field("Android Management API fields", android, "Merged over the generated policy, e.g. {\"statusBarDisabled\": true}. statusReportingSettings is managed by VaanarSena.")));
  });
  el.get = () => {
    const out = prune({ apple: apple.get(), windows: win.get(), android: android.get() });
    if (!out) return undefined;
    if (scope.get() === "all") out.scope = "all";
    return out;
  };
  return el;
}

function safe(fn) { try { return fn(); } catch { return undefined; } }

// policyEditor(doc) -> element with get() returning the document.
export function policyEditor(doc = {}) {
  let current = doc || {};
  const formHost = h("div");
  const json = jsonEditor(current, { rows: 22, label: "Policy document" });
  const err = h("div");
  let sections = [];
  const buildForm = (d) => {
    sections = [
      ["passcode", passcodeSection(d.passcode)],
      ["encryption", encryptionSection(d.encryption)],
      ["restrictions", restrictionsSection(d.restrictions)],
      ["wifi", wifiSection(d.wifi)],
      ["osUpdates", updatesSection(d.osUpdates)],
      ["apps", appsSection(d.apps)],
      ["custom", customSection(d.custom)],
    ];
    formHost.replaceChildren(...sections.map(([, s]) => s));
  };
  const fromForm = () => prune(Object.fromEntries(sections.map(([k, s]) => [k, s.get()]))) || {};
  buildForm(current);

  let mode = "form";
  const jsonHost = h("div", { hidden: true }, h("p", { class: "muted small" }, "The same document the API and manifests use. Unknown fields are rejected when you save."), json);
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
  const el = h("div", { class: "stack" }, h("div", { class: "row spread" }, h("div", { class: "muted small" }, "Settings each platform does not support are skipped on that platform."), sw), err, formHost, jsonHost);
  el.get = () => (mode === "json" ? json.get() || {} : fromForm());
  return el;
}
