// Command catalogue helpers: a parameter form for each command type, shared by
// the device page, bulk actions and blueprint onboarding steps.
import { h, state } from "./core.js";
import { field, input, textarea, check, prune } from "./forms.js";

export const COMMAND_LABELS = {
  refresh: "Refresh inventory", apply_policy: "Re-apply policy", install_app: "Install app", remove_app: "Remove app",
  lock: "Lock", restart: "Restart", shutdown: "Shut down", clear_passcode: "Clear passcode",
  enable_lost_mode: "Enable lost mode", disable_lost_mode: "Disable lost mode", locate: "Locate",
  os_update: "Install OS updates", run_script: "Run script", retire: "Retire", wipe: "Erase device",
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
