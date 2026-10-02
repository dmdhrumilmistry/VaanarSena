import { h, api, state, page, fmtTime, toast, confirmDialog, errorNotice, notice, icon, copyLine, go, badge, withBusy, tabBar, skeleton } from "../core.js";
import { input, field, filePicker, fileToText, fileToBase64, textarea } from "../forms.js";

const sourceNote = (st) => st.source === "environment"
  ? notice("gold", "Set by environment variables on the server. Change it there, or remove them to manage it here.") : null;

// Status badge for a platform: Connected, Error or Not configured.
const statusBadge = (st, okLabel = "Connected") => st.configured ? badge(okLabel, "ok") : st.error ? badge("Error", "bad") : badge("Not configured");
const statusError = (st) => !st.configured && st.error ? h("div", { class: "plat-error" }, "Not working: " + st.error) : null;

async function reload(tab) { go("/settings/" + tab + "?_=" + Date.now()); }

function remove(label, path, what) {
  return h("button", { class: "btn small", type: "button", onclick: async () => {
    if (!(await confirmDialog({ title: "Remove " + label + "?", body: what, confirmLabel: "Remove", danger: true }))) return;
    try { await api("DELETE", path); toast(label + " removed"); reload("platforms"); } catch (e) { toast(e.message, "bad"); }
  } }, icon("trash"), "Remove");
}

// card({ icon, title, sub, st, okLabel, actions }, ...content): one platform as a card.
function card({ iconName, title, sub, st, okLabel, actions }, ...content) {
  return h("section", { class: "panel plat-panel" },
    h("div", { class: "panel-head" },
      h("div", { class: "plat-title" + (st.configured ? " on" : "") }, h("span", { class: "tile" }, icon(iconName)),
        h("div", {}, h("h2", {}, title, statusBadge(st, okLabel)), sub ? h("p", {}, sub) : null)),
      actions && actions.length ? h("div", { class: "row" }, actions) : null),
    h("div", { class: "panel-body" }, h("div", { class: "stack" }, ...content)));
}

const meta = (pairs) => h("div", { class: "plat-meta" }, pairs.filter(([, v]) => v).map(([k, v]) => h("span", {}, h("b", {}, k), v)));

function applePanel(st) {
  const env = st.source === "environment";
  const out = h("div");
  const csrOut = h("div");
  let certData = null, certDER = false, keyPEM = "";
  const certName = h("span", { class: "muted small" }, "No file chosen");
  const keyName = h("span", { class: "muted small" }, "Using the key generated on the server");
  const genCSR = h("button", { class: "btn", type: "button", disabled: env, onclick: () => withBusy(genCSR, async () => {
    try {
      const r = await api("POST", "/platforms/apple/csr");
      const blob = URL.createObjectURL(new Blob([r.csr], { type: "application/pkcs10" }));
      csrOut.replaceChildren(h("div", { class: "stack" }, notice("ok", "Signing request created. Its private key stays on this server."),
        h("a", { class: "btn small", href: blob, download: "vaanarsena-push.csr" }, icon("download"), "Download vaanarsena-push.csr")));
    } catch (e) { csrOut.replaceChildren(errorNotice(e)); }
  }) }, st.details.csrPending ? "Create a new signing request" : "Create signing request");
  const certPick = filePicker(".pem,.cer,.crt", "Choose certificate", async (f) => {
    const text = await fileToText(f);
    if (text.includes("-----BEGIN")) { certData = text; certDER = false; } else { certData = await fileToBase64(f); certDER = true; }
    certName.textContent = f.name;
  });
  const keyPick = filePicker(".pem,.key", "Use my own private key", async (f) => { keyPEM = await fileToText(f); keyName.textContent = f.name; });
  const upload = h("button", { class: "btn primary", type: "button", disabled: env, onclick: () => withBusy(upload, async () => {
    out.replaceChildren();
    if (!certData) { out.replaceChildren(errorNotice(new Error("Choose the certificate Apple issued first."))); return; }
    try { await api("PUT", "/platforms/apple", { certificate: certData, der: certDER, privateKey: keyPEM }); toast("Apple push certificate saved. Apple enrollment is on."); reload("platforms"); }
    catch (e) { out.replaceChildren(errorNotice(e)); }
  }) }, "Save certificate");
  const steps = h("ol", { class: "num-steps" },
    h("li", {}, h("div", { class: "step-title" }, "Create a signing request"), h("div", { class: "step-text" }, "VaanarSena generates the key and keeps it."), h("div", { class: "act-row" }, genCSR), csrOut),
    h("li", {}, h("div", { class: "step-title" }, "Have it signed and upload it to Apple"),
      h("div", { class: "step-text" }, "An MDM vendor certificate signs the request, then upload it at ",
        h("a", { class: "link", href: "https://identity.apple.com", target: "_blank", rel: "noopener" }, "identity.apple.com"), " with your organization's Apple ID and download the push certificate.")),
    h("li", {}, h("div", { class: "step-title" }, "Upload the certificate here"), h("div", { class: "step-text" }, "A .pem or Apple's .cer file both work."),
      h("div", { class: "act-row file-line" }, certPick, certName), h("div", { class: "act-row file-line" }, keyPick, keyName), out,
      h("div", { class: "act-row" }, upload)));
  return card({ iconName: "apple", title: "Apple", sub: "iPhone, iPad and Mac. Needs an Apple MDM push certificate, renewed yearly with the same Apple ID.", st,
    actions: st.configured && !env ? [remove("Apple push certificate", "/platforms/apple", "Enrolled Apple devices stop receiving commands until a certificate with the same topic is added again.")] : [] },
  statusError(st),
  st.configured ? meta([["Push topic", st.details.topic || ""], ["Expires", st.details.expires ? fmtTime(st.details.expires) : ""]]) : null,
  sourceNote(st), env ? null : steps);
}

function googlePanel(st) {
  const env = st.source === "environment";
  const creds = textarea("", { rows: 6, placeholder: '{"type": "service_account", ...}', "aria-label": "Service account key JSON" });
  const project = input(st.details.projectId || "", { placeholder: "Taken from the key if left empty" });
  const pick = filePicker(".json,application/json", "Choose key file", async (f) => { creds.value = await fileToText(f); });
  const out = h("div");
  const save = h("button", { class: "btn primary", type: "button", disabled: env, onclick: () => withBusy(save, async () => {
    out.replaceChildren();
    try { await api("PUT", "/platforms/google", { credentials: creds.value.trim(), projectId: project.get() }); toast("Google service account saved"); reload("platforms"); }
    catch (e) { out.replaceChildren(errorNotice(e)); }
  }) }, st.configured ? "Replace key" : "Save key");
  return card({ iconName: "lock", title: "Google service account", sub: "Used for Android and ChromeOS. Create it in a Google Cloud project with the Android Management API (and Admin SDK for ChromeOS) enabled.", st,
    actions: st.configured && !env ? [remove("Google service account", "/platforms/google", "Android and ChromeOS management stop until a key is added again.")] : [] },
  statusError(st),
  st.configured ? meta([["Account", st.details.serviceAccount || ""], ["Project", st.details.projectId || ""]]) : null,
  sourceNote(st),
  env ? null : h("div", { class: "plat-form" },
    field("Service account key", h("div", { class: "stack" }, h("div", {}, pick), creds), "Stored encrypted. It is never shown again after saving."),
    field("Cloud project ID", project), out, h("div", {}, save)));
}

function androidPanel(st, google) {
  const env = st.source === "environment";
  const out = h("div");
  const ent = input("", { placeholder: "enterprises/LC01abcdef" });
  const connect = h("button", { class: "btn primary", type: "button", disabled: env || !google.configured, onclick: () => withBusy(connect, async () => {
    out.replaceChildren();
    try { const r = await api("POST", "/platforms/android/signup"); location.assign(r.signupUrl); }
    catch (e) { out.replaceChildren(errorNotice(e)); }
  }) }, "Connect Android Enterprise");
  const bind = h("button", { class: "btn", type: "button", disabled: env || !google.configured, onclick: () => withBusy(bind, async () => {
    out.replaceChildren();
    try { await api("PUT", "/platforms/android", { enterprise: ent.get() }); toast("Android Enterprise connected"); reload("platforms"); }
    catch (e) { out.replaceChildren(errorNotice(e)); }
  }) }, "Use this enterprise");
  return card({ iconName: "android", title: "Android Enterprise", sub: "Fully managed phones and work profiles, through Google's Android Management API.", st,
    actions: st.configured && !env ? [remove("Android Enterprise", "/platforms/android", "VaanarSena stops managing Android devices. They stay enrolled with Google until you delete the enterprise there.")] : [] },
  statusError(st),
  st.configured ? meta([["Enterprise", st.details.enterprise || ""]]) : null,
  sourceNote(st),
  !google.configured && !env && !st.configured ? notice("warn", "Add the Google service account first.") : null,
  env || st.configured ? null : h("div", { class: "plat-form" },
    h("p", { class: "step-text" }, "Connecting opens Google's sign-up, where you name the enterprise and accept the terms with a Google account. Google then sends you back here."),
    st.details.signupPending ? h("p", { class: "muted small" }, "A sign-up was started and not finished. Connecting again starts a fresh one.") : null,
    h("div", {}, connect),
    h("div", { class: "divider-label" }, "or"),
    field("Already have an enterprise?", h("div", { class: "row fill" }, h("div", {}, ent), bind), "Its name from the Android Management API, for example from an earlier setup."),
    out));
}

function chromePanel(st, google) {
  const env = st.source === "environment";
  const subject = input("", { type: "email", placeholder: "admin@example.com" });
  const customer = input(st.details.customerId || "", { placeholder: "my_customer" });
  const out = h("div");
  const save = h("button", { class: "btn primary", type: "button", disabled: env || !google.configured, onclick: () => withBusy(save, async () => {
    out.replaceChildren();
    try { await api("PUT", "/platforms/chromeos", { adminSubject: subject.get(), customerId: customer.get() }); toast("ChromeOS connected"); reload("platforms"); }
    catch (e) { out.replaceChildren(errorNotice(e)); }
  }) }, "Connect ChromeOS");
  return card({ iconName: "chromeos", title: "ChromeOS", sub: "Imports Chromebooks enrolled in your Google Workspace and sends them commands.", st,
    actions: st.configured && !env ? [remove("ChromeOS", "/platforms/chromeos", "Chromebooks stop syncing.")] : [] },
  statusError(st),
  st.configured ? h("p", { class: "step-text" }, "Syncing every 15 minutes.") : null,
  sourceNote(st),
  !google.configured && !env && !st.configured ? notice("warn", "Add the Google service account first.") : null,
  env || st.configured ? null : h("div", { class: "plat-form" },
    h("p", { class: "step-text" }, "Grant the service account domain-wide delegation for the scope admin.directory.device.chromeos in the Google Admin console, then enter an admin it may act as."),
    h("div", { class: "grid2" }, field("Workspace admin", subject), field("Customer ID", customer, "Leave as my_customer unless you manage several.")), out, h("div", {}, save)));
}

async function platformsTab() {
  const res = await api("GET", "/platforms");
  const p = res.platforms;
  const q = new URLSearchParams(location.search);
  const flash = q.get("android") === "connected" ? notice("ok", "Android Enterprise connected. You can enroll Android devices now.")
    : q.get("android") === "error" ? notice("bad", "Android Enterprise was not connected: " + (q.get("message") || "unknown error")) : null;
  const host = location.host;
  return h("div", { class: "settings-stack" },
    flash,
    applePanel(p.apple),
    googlePanel(p.google),
    androidPanel(p.android, p.google),
    chromePanel(p.chromeos, p.google),
    card({ iconName: "windows", title: "Windows", sub: "Built in. Windows 10 and 11 enroll with the user's email and an enrollment token.", st: p.windows, okLabel: "Built in" },
      statusError(p.windows),
      field("Discovery address", copyLine(p.windows.details.discoveryUrl)),
      h("p", { class: "help" }, "To let users enroll by typing only their email, create a DNS record enterpriseenrollment.<your-email-domain> pointing to " + host + ".")),
    card({ iconName: "linux", title: "Linux", sub: "Built in. Install the agent and enroll with a token.", st: p.linux, okLabel: "Built in" },
      statusError(p.linux),
      h("p", { class: "step-text" }, "Agent binaries for amd64 and arm64 are on the ",
        h("a", { class: "link", href: "https://github.com/dmdhrumilmistry/VaanarSena/releases/latest", target: "_blank", rel: "noopener" }, "releases page"), "."),
      copyLine("sudo vaanarsena-agent enroll --server " + res.publicUrl + " --token <TOKEN>")));
}

async function serverTab() {
  const i = state.info;
  const caUrl = "/mdm/ca.pem";
  return h("section", { class: "panel" }, h("div", { class: "panel-head" }, h("div", {}, h("h2", {}, "Server"), h("p", {}, "Details your devices and integrations use to reach this console."))),
    h("div", { class: "panel-body" }, h("dl", { class: "kv kv-card" },
      h("dt", {}, "Version"), h("dd", {}, h("span", { class: "mono" }, i.version)),
      h("dt", {}, "Organization"), h("dd", {}, i.org),
      h("dt", {}, "Public address"), h("dd", {}, copyLine(i.publicUrl)),
      h("dt", {}, "Device CA fingerprint"), h("dd", {}, copyLine(i.caFingerprint)),
      h("dt", {}, "Device CA"), h("dd", {}, h("a", { class: "btn small", href: caUrl, download: "vaanarsena-ca.pem" }, icon("download"), "Download")),
      h("dt", {}, "Documentation"), h("dd", {}, h("a", { class: "link", href: "https://dmdhrumilmistry.github.io/VaanarSena/", target: "_blank", rel: "noopener" }, "dmdhrumilmistry.github.io/VaanarSena")))));
}

export async function view(tab) {
  const current = tab === "server" ? "server" : "platforms";
  const bar = tabBar({ label: "Settings sections", value: current, items: [{ id: "platforms", label: "Platforms" }, { id: "server", label: "Server" }],
    onChange: (id) => go("/settings/" + id) });
  const host = h("div", { class: "tabbody", role: "tabpanel", "aria-labelledby": bar.id + "-" + current }, skeleton(4));
  (current === "server" ? serverTab() : platformsTab()).then((el) => host.replaceChildren(el), (e) => host.replaceChildren(errorNotice(e)));
  return page({ title: "Settings", sub: "Platform connections and server details. Credentials are stored encrypted with the server key." }, bar, host);
}
