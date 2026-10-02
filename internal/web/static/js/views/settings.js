import { h, api, state, page, panel, body, fmtTime, toast, confirmDialog, errorNotice, notice, icon, copyLine, tabs, go, link } from "../core.js";
import { input, field, filePicker, fileToText, fileToBase64, textarea } from "../forms.js";

const sourceNote = (st) => st.source === "environment"
  ? notice("gold", "Set by environment variables on the server. Change it there, or remove them to manage it here.") : null;

const statusLine = (st, okText) => st.configured
  ? h("span", { class: "status" }, h("span", { class: "dot ok" }), okText)
  : st.error ? h("span", { class: "status" }, h("span", { class: "dot bad" }), "Not working: " + st.error)
  : h("span", { class: "status" }, h("span", { class: "dot" }), "Not set up");

async function reload(tab) { go("/settings/" + tab + "?_=" + Date.now()); }

function remove(label, path, what) {
  return h("button", { class: "btn quiet", onclick: async () => {
    if (!(await confirmDialog({ title: "Remove " + label + "?", body: what, confirmLabel: "Remove", danger: true }))) return;
    try { await api("DELETE", path); toast(label + " removed"); reload("platforms"); } catch (e) { toast(e.message, "bad"); }
  } }, "Remove");
}

function applePanel(st) {
  const env = st.source === "environment";
  const out = h("div");
  const csrOut = h("div");
  let certData = null, certDER = false, keyPEM = "";
  const certName = h("span", { class: "muted small" }, "No file chosen");
  const keyName = h("span", { class: "muted small" }, "Using the key generated on the server");
  const genCSR = h("button", { class: "btn", disabled: env, onclick: async () => {
    try {
      const r = await api("POST", "/platforms/apple/csr");
      const blob = URL.createObjectURL(new Blob([r.csr], { type: "application/pkcs10" }));
      csrOut.replaceChildren(h("div", { class: "stack" }, notice("ok", "Signing request created. Its private key stays on this server."),
        h("a", { class: "btn small", href: blob, download: "vaanarsena-push.csr" }, icon("download"), "Download vaanarsena-push.csr")));
    } catch (e) { csrOut.replaceChildren(errorNotice(e)); }
  } }, st.details.csrPending ? "Create a new signing request" : "Create signing request");
  const certPick = filePicker(".pem,.cer,.crt", "Choose certificate", async (f) => {
    const text = await fileToText(f);
    if (text.includes("-----BEGIN")) { certData = text; certDER = false; } else { certData = await fileToBase64(f); certDER = true; }
    certName.textContent = f.name;
  });
  const keyPick = filePicker(".pem,.key", "Use my own private key", async (f) => { keyPEM = await fileToText(f); keyName.textContent = f.name; });
  const upload = h("button", { class: "btn primary", disabled: env, onclick: async () => {
    out.replaceChildren();
    if (!certData) { out.replaceChildren(errorNotice(new Error("Choose the certificate Apple issued first."))); return; }
    try { await api("PUT", "/platforms/apple", { certificate: certData, der: certDER, privateKey: keyPEM }); toast("Apple push certificate saved. Apple enrollment is on."); reload("platforms"); }
    catch (e) { out.replaceChildren(errorNotice(e)); }
  } }, "Save certificate");
  const steps = h("ol", { class: "stack" },
    h("li", {}, h("b", {}, "Create a signing request. "), "VaanarSena generates the key and keeps it.", h("div", { class: "row", style: { marginTop: "8px" } }, genCSR), csrOut),
    h("li", {}, h("b", {}, "Have it signed and upload it to Apple. "), "An MDM vendor certificate signs the request, then upload it at ",
      h("a", { class: "link", href: "https://identity.apple.com", target: "_blank", rel: "noopener" }, "identity.apple.com"), " with your organization's Apple ID and download the push certificate."),
    h("li", {}, h("b", {}, "Upload the certificate here. "), "A .pem or Apple's .cer file both work.",
      h("div", { class: "row", style: { marginTop: "8px" } }, certPick, certName), h("div", { class: "row", style: { marginTop: "6px" } }, keyPick, keyName), out,
      h("div", { style: { marginTop: "8px" } }, upload)));
  return panel({ title: "Apple", sub: "iPhone, iPad and Mac. Needs an Apple MDM push certificate, renewed yearly with the same Apple ID.",
    actions: st.configured && !env ? [remove("Apple push certificate", "/platforms/apple", "Enrolled Apple devices stop receiving commands until a certificate with the same topic is added again.")] : [] },
    body(h("div", { class: "stack" }, statusLine(st, "Ready. Push topic " + (st.details.topic || "")), st.details.expires ? h("div", { class: "muted small" }, "Certificate expires " + fmtTime(st.details.expires)) : null,
      sourceNote(st), env ? null : steps)));
}

function googlePanel(st) {
  const env = st.source === "environment";
  const creds = textarea("", { rows: 6, placeholder: '{"type": "service_account", ...}', "aria-label": "Service account key JSON" });
  const project = input(st.details.projectId || "", { placeholder: "Taken from the key if left empty" });
  const pick = filePicker(".json,application/json", "Choose key file", async (f) => { creds.value = await fileToText(f); });
  const out = h("div");
  const save = h("button", { class: "btn primary", disabled: env, onclick: async () => {
    out.replaceChildren();
    try { await api("PUT", "/platforms/google", { credentials: creds.value.trim(), projectId: project.get() }); toast("Google service account saved"); reload("platforms"); }
    catch (e) { out.replaceChildren(errorNotice(e)); }
  } }, st.configured ? "Replace key" : "Save key");
  return panel({ title: "Google service account", sub: "Used for Android and ChromeOS. Create it in a Google Cloud project with the Android Management API (and Admin SDK for ChromeOS) enabled.",
    actions: st.configured && !env ? [remove("Google service account", "/platforms/google", "Android and ChromeOS management stop until a key is added again.")] : [] },
    body(h("div", { class: "stack" },
      statusLine(st, "Ready. " + (st.details.serviceAccount || "")),
      st.details.projectId ? h("div", { class: "muted small" }, "Project " + st.details.projectId) : null,
      sourceNote(st),
      env ? null : h("div", { class: "form" },
        field("Service account key", h("div", { class: "stack" }, h("div", {}, pick), creds), "Stored encrypted. It is never shown again after saving."),
        field("Cloud project ID", project), out, h("div", {}, save)))));
}

function androidPanel(st, google, flash) {
  const env = st.source === "environment";
  const out = h("div");
  const ent = input("", { placeholder: "enterprises/LC01abcdef" });
  const connect = h("button", { class: "btn primary", disabled: env || !google.configured, onclick: async () => {
    out.replaceChildren();
    try { const r = await api("POST", "/platforms/android/signup"); location.assign(r.signupUrl); }
    catch (e) { out.replaceChildren(errorNotice(e)); }
  } }, "Connect Android Enterprise");
  const bind = h("button", { class: "btn", disabled: env || !google.configured, onclick: async () => {
    out.replaceChildren();
    try { await api("PUT", "/platforms/android", { enterprise: ent.get() }); toast("Android Enterprise connected"); reload("platforms"); }
    catch (e) { out.replaceChildren(errorNotice(e)); }
  } }, "Use this enterprise");
  return panel({ title: "Android", sub: "Fully managed phones and work profiles, through Google's Android Management API.",
    actions: st.configured && !env ? [remove("Android Enterprise", "/platforms/android", "VaanarSena stops managing Android devices. They stay enrolled with Google until you delete the enterprise there.")] : [] },
    body(h("div", { class: "stack" },
      flash,
      statusLine(st, "Ready. " + (st.details.enterprise || "")),
      sourceNote(st),
      !google.configured && !env ? notice("warn", "Add the Google service account above first.") : null,
      env || st.configured ? null : h("div", { class: "stack" },
        h("p", {}, "Connecting opens Google's sign-up, where you name the enterprise and accept the terms with a Google account. Google then sends you back here."),
        st.details.signupPending ? h("p", { class: "muted small" }, "A sign-up was started and not finished. Connecting again starts a fresh one.") : null,
        h("div", {}, connect),
        h("hr", { class: "sep" }),
        field("Already have an enterprise?", h("div", { class: "row" }, h("div", { style: { flex: "1 1 260px" } }, ent), bind), "Its name from the Android Management API, for example from an earlier setup."),
        out))));
}

function chromePanel(st, google) {
  const env = st.source === "environment";
  const subject = input("", { type: "email", placeholder: "admin@example.com" });
  const customer = input(st.details.customerId || "", { placeholder: "my_customer" });
  const out = h("div");
  const save = h("button", { class: "btn primary", disabled: env || !google.configured, onclick: async () => {
    out.replaceChildren();
    try { await api("PUT", "/platforms/chromeos", { adminSubject: subject.get(), customerId: customer.get() }); toast("ChromeOS connected"); reload("platforms"); }
    catch (e) { out.replaceChildren(errorNotice(e)); }
  } }, "Connect ChromeOS");
  return panel({ title: "ChromeOS", sub: "Imports Chromebooks enrolled in your Google Workspace and sends them commands.",
    actions: st.configured && !env ? [remove("ChromeOS", "/platforms/chromeos", "Chromebooks stop syncing.")] : [] },
    body(h("div", { class: "stack" }, statusLine(st, "Ready. Syncing every 15 minutes."), sourceNote(st),
      !google.configured && !env ? notice("warn", "Add the Google service account above first.") : null,
      env || st.configured ? null : h("div", { class: "form" },
        h("p", { class: "muted" }, "Grant the service account domain-wide delegation for the scope admin.directory.device.chromeos in the Google Admin console, then enter an admin it may act as."),
        h("div", { class: "grid2" }, field("Workspace admin", subject), field("Customer ID", customer, "Leave as my_customer unless you manage several.")), out, h("div", {}, save)))));
}

async function platformsTab() {
  const res = await api("GET", "/platforms");
  const p = res.platforms;
  const q = new URLSearchParams(location.search);
  const flash = q.get("android") === "connected" ? notice("ok", "Android Enterprise connected. You can enroll Android devices now.")
    : q.get("android") === "error" ? notice("bad", "Android Enterprise was not connected: " + (q.get("message") || "unknown error")) : null;
  const host = location.host;
  return [
    applePanel(p.apple),
    googlePanel(p.google),
    androidPanel(p.android, p.google, flash),
    chromePanel(p.chromeos, p.google),
    panel({ title: "Windows", sub: "Built in. Windows 10 and 11 enroll with the user's email and an enrollment token." },
      body(h("div", { class: "stack" }, statusLine(p.windows, "Ready"),
        field("Discovery address", copyLine(p.windows.details.discoveryUrl)),
        h("p", { class: "muted small" }, "To let users enroll by typing only their email, create a DNS record enterpriseenrollment.<your-email-domain> pointing to " + host + ".")))),
    panel({ title: "Linux", sub: "Built in. Install the agent and enroll with a token." },
      body(h("div", { class: "stack" }, statusLine(p.linux, "Ready"),
        h("p", {}, "Agent binaries for amd64 and arm64 are on the ",
          h("a", { class: "link", href: "https://github.com/dmdhrumilmistry/VaanarSena/releases/latest", target: "_blank", rel: "noopener" }, "releases page"), "."),
        copyLine("sudo vaanarsena-agent enroll --server " + res.publicUrl + " --token <TOKEN>")))),
  ];
}

async function serverTab() {
  const i = state.info;
  const caUrl = "/mdm/ca.pem";
  return panel({ title: "Server" }, body(h("dl", { class: "kv" },
    h("dt", {}, "Version"), h("dd", {}, i.version),
    h("dt", {}, "Organization"), h("dd", {}, i.org),
    h("dt", {}, "Public address"), h("dd", { class: "mono" }, i.publicUrl),
    h("dt", {}, "Device CA fingerprint"), h("dd", { class: "mono" }, i.caFingerprint),
    h("dt", {}, "Device CA"), h("dd", {}, h("a", { class: "btn small", href: caUrl, download: "vaanarsena-ca.pem" }, icon("download"), "Download")),
    h("dt", {}, "Documentation"), h("dd", {}, h("a", { class: "link", href: "https://dmdhrumilmistry.github.io/VaanarSena/", target: "_blank", rel: "noopener" }, "dmdhrumilmistry.github.io/VaanarSena")))));
}

export async function view(tab) {
  return page({ title: "Settings", sub: "Platform connections and server details. Credentials are stored encrypted with the server key." },
    tabs([{ id: "platforms", label: "Platforms", render: platformsTab }, { id: "server", label: "Server", render: serverTab }], tab));
}
