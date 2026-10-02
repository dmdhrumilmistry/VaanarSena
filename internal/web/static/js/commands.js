// Command catalogue helpers: a parameter form for each command type, shared by
// the device page, bulk actions and blueprint onboarding steps.
import { h, state, api, go, drawer, withBusy, notice, errorNotice, toast, confirmDialog } from "./core.js";
import { field, input, textarea, check, prune, select } from "./forms.js";

export const COMMAND_LABELS = {
  refresh: "Refresh inventory", apply_policy: "Re-apply policy", install_app: "Install app", remove_app: "Remove app",
  lock: "Lock", restart: "Restart", shutdown: "Shut down", clear_passcode: "Clear passcode",
  enable_lost_mode: "Enable lost mode", disable_lost_mode: "Disable lost mode", locate: "Locate",
  os_update: "Install OS updates", run_script: "Run script", retire: "Retire", wipe: "Erase device",
  app_inventory: "Read installed apps", profile_inventory: "Read configuration profiles",
  msi_inventory: "Read installed desktop apps", msi_inventory_detail: "Read desktop app details",
};
export const commandLabel = (t) => COMMAND_LABELS[t] || t;

export function specFor(type) { return state.catalogue.find((c) => c.type === type); }

// Commands a role may send to a device, honoring the BYOD guard the server
// enforces (personal devices only get commands marked personal).
export function allowedCommands(device, rank) {
  return state.catalogue.filter((c) => c.platforms.includes(device.platform) &&
    (device.ownership !== "personal" || c.personal) && rank(c.minRole));
}

// paramsForm(type, platform) -> element with get() returning params.
export function paramsForm(type, platform, initial = {}) {
  const f = [];
  const add = (key, label, ctrl, help) => { f.push([key, ctrl]); return field(label, ctrl, help); };
  const parts = [];
  const mac = platform === "macos" || platform === undefined;
  switch (type) {
    case "lock":
      parts.push(add("message", "Message on the lock screen", input(initial.message, { placeholder: "This device is locked by IT" })));
      parts.push(add("phone", "Phone number to show", input(initial.phone, { placeholder: "+91 ..." })));
      if (mac) parts.push(add("pin", "Six-digit PIN (macOS)", input(initial.pin, { inputmode: "numeric", maxlength: 6, class: "short" }), "Needed to unlock a Mac locked this way."));
      break;
    case "enable_lost_mode":
      parts.push(add("message", "Message", input(initial.message, { placeholder: "This device has been reported lost" })));
      parts.push(add("phone", "Phone number", input(initial.phone)));
      break;
    case "install_app":
    case "remove_app":
      parts.push(add("appId", "App", input(initial.appId, { placeholder: "Bundle ID, package name or MSI ProductCode" })));
      if (type === "install_app") {
        parts.push(add("url", "Download URL", input(initial.url, { placeholder: "https://..." }), "Apple: an enterprise app manifest. Windows: the MSI file. Linux: leave empty to use the package manager."));
        if (platform === "windows" || platform === undefined) {
          parts.push(add("hash", "MSI SHA-256 (Windows)", input(initial.hash)));
          parts.push(add("version", "MSI product version (Windows)", input(initial.version, { placeholder: "1.2.3.0" })));
        }
      }
      break;
    case "run_script":
      parts.push(add("script", "Script", textarea(initial.script || "#!/bin/sh\n", { rows: 8 }), "Runs as root on Linux devices. The full script is recorded in the audit log."));
      break;
    case "wipe":
      if (mac) parts.push(add("pin", "Six-digit PIN (macOS)", input(initial.pin, { inputmode: "numeric", maxlength: 6, class: "short" }), "Needed to unlock a Mac erased this way."));
      if (platform === "ios" || platform === undefined) {
        const keep = check(initial.preserveDataPlan, "Keep the cellular data plan (iOS)");
        f.push(["preserveDataPlan", keep]); parts.push(keep);
      }
      break;
  }
  const el = h("div", { class: "form" }, parts.length ? parts : h("p", { class: "muted small" }, "No options for this command."));
  el.get = () => prune(Object.fromEntries(f.map(([k, c]) => [k, c.get()]))) || {};
  return el;
}

// Command types that take parameters (see paramsForm).
const PARAM_TYPES = new Set(["lock", "enable_lost_mode", "install_app", "remove_app", "run_script", "wipe"]);
export const hasParams = (type) => PARAM_TYPES.has(type);
// Safe commands that can be sent straight from a quick action, with no form and no confirmation.
export const INSTANT_TYPES = new Set(["refresh", "apply_policy"]);

export function deliveryNote(platform) {
  if (platform === "windows") return "Windows devices pick up commands at their next check-in, within 15 minutes.";
  if (platform === "linux") return "The agent picks up commands within a minute.";
  return "Delivered right away when the device is online.";
}

// confirmCommand(d, spec): type-to-confirm step for destructive commands. Resolves true to go ahead.
export function confirmCommand(d, spec) {
  if (!spec.destructive) return Promise.resolve(true);
  return confirmDialog({ title: commandLabel(spec.type) + "?", body: spec.description + ". This cannot be undone.", confirmLabel: commandLabel(spec.type), danger: true, typeToConfirm: d.name || d.id.slice(0, 8) });
}

// On a device page, reload it on the Commands tab; from lists the toast is enough.
function afterSend() { if (/^\/devices\/[^/]+/.test(location.pathname)) go(location.pathname + "?tab=commands&_=" + Date.now()); }

// quickSend(d, spec, btn): sends a parameterless safe command without opening the drawer.
export async function quickSend(d, spec, btn) {
  const run = async () => {
    try { await api("POST", `/devices/${d.id}/commands`, { type: spec.type, params: {} }); toast(commandLabel(spec.type) + " queued"); afterSend(); }
    catch (e) { toast(e.message || "The command was refused", "bad"); }
  };
  if (btn) await withBusy(btn, run); else await run();
}

// commandDrawer(d, allowed, { preset }): the send-command flow in a right-side drawer.
// allowed is the list from allowedCommands(); personal devices only ever see commands
// limited to the work container, and the drawer says so.
export function commandDrawer(d, allowed, { preset } = {}) {
  const start = allowed.find((c) => c.type === preset) || allowed[0];
  const typeSel = select(allowed.map((c) => [c.type, commandLabel(c.type) + (c.destructive ? " (destructive)" : "")]), start.type, { "aria-label": "Command" });
  const desc = h("p", { class: "help" });
  const host = h("div");
  const out = h("div");
  let pf;
  const spec = () => allowed.find((c) => c.type === typeSel.value);
  const draw = () => {
    pf = paramsForm(typeSel.value, d.platform);
    host.replaceChildren(pf);
    desc.textContent = spec().description || "";
    out.replaceChildren();
  };
  typeSel.addEventListener("change", draw);
  draw();
  let dr;
  const send = h("button", { type: "button", class: "btn primary" }, "Send command");
  send.addEventListener("click", () => withBusy(send, async () => {
    if (!(await confirmCommand(d, spec()))) return;
    try {
      await api("POST", `/devices/${d.id}/commands`, { type: typeSel.value, params: pf.get() });
      toast(commandLabel(typeSel.value) + " queued");
      dr.close();
      afterSend();
    } catch (e) { out.replaceChildren(errorNotice(e)); }
  }));
  const cancel = h("button", { type: "button", class: "btn", onclick: () => dr.close() }, "Cancel");
  const body = h("div", { class: "form" },
    d.ownership === "personal" ? notice("gold", h("b", {}, "Personal device. "), "Only commands limited to the work container are offered. Erase, lock and scripts are refused by the server.") : null,
    field("Command", typeSel), desc, host, out,
    notice("info", deliveryNote(d.platform) + " The command and its result are recorded in the audit log."));
  dr = drawer({ title: "Send command", sub: d.name || d.serial || "Unnamed device", body, footer: [cancel, send] });
  return dr;
}
