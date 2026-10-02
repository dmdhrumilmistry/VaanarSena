import { h, api, state, page, panel, body, link, table, fmtTime, can, emptyState, toast, errorNotice, notice, icon, groupsCache, copyLine, badge, withBusy } from "../core.js";
import { input, select, field } from "../forms.js";

const PLATFORMS = [
  { id: "apple", label: "Apple", icon: "apple", desc: "iPhone, iPad and Mac, through Apple's MDM protocol." },
  { id: "windows", label: "Windows", icon: "windows", desc: "Windows 10 and 11 Pro, Enterprise and Education." },
  { id: "android", label: "Android", icon: "android", desc: "Fully managed phones or a work profile on personal ones." },
  { id: "linux", label: "Linux", icon: "linux", desc: "Desktops and servers with the VaanarSena agent." },
];

const OWNERSHIP = {
  corporate: { label: "Company-owned", icon: "shield", desc: "Full management: policies, apps, lock, restart and erase.",
    points: ["Policies and apps", "Lock, restart and erase", "Hardware identifiers"] },
  personal: { label: "Personal (BYOD)", icon: "account", desc: "Work data only. VaanarSena cannot see personal data, collect hardware IDs, lock or erase the device.",
    points: ["Separate work area", "No lock or erase", "No access to personal data"] },
};

const INSTRUCTIONS = {
  apple: { corporate: "Open the link in Safari on the device, then install the downloaded profile from Settings.",
    personal: "Open the link in Safari on the device. iOS creates a separate work volume tied to the user's Managed Apple ID." },
  windows: { corporate: "On the PC, open Settings > Accounts > Access work or school > Enroll only in device management. Sign in with the email, then use the token as the password.",
    personal: "On the PC, open Settings > Accounts > Access work or school > Enroll only in device management. Only sign-in policies apply to personal PCs." },
  android: { corporate: "Factory reset the device, tap the welcome screen six times, then scan the QR code.",
    personal: "Open the enrollment link on the phone. Android creates a separate work profile." },
  linux: { corporate: "Run this on the machine as root, then start the agent with systemctl enable --now vaanarsena-agent.",
    personal: "Run this on the machine as root. On personal machines the agent only reports inventory." },
};

function step(n, title, sub, ...content) {
  return h("section", { class: "panel step" },
    h("div", { class: "step-head" }, h("span", { class: "step-num", "aria-hidden": "true" }, String(n)),
      h("div", {}, h("h2", {}, h("span", { class: "sr-only" }, "Step " + n + ": "), title), sub ? h("p", {}, sub) : null)),
    h("div", { class: "step-body" }, ...content));
}

export async function view() {
  const [toks, groups] = await Promise.all([api("GET", "/enrollment-tokens"), groupsCache()]);
  const enabled = { apple: state.info.platforms.ios, windows: state.info.platforms.windows, android: state.info.platforms.android, linux: state.info.platforms.linux };
  let platform = PLATFORMS.find((p) => enabled[p.id])?.id || "windows";
  let ownership = "corporate";

  const platGrid = h("div", { class: "plat-grid", role: "radiogroup", "aria-label": "Platform" });
  const ownGrid = h("div", { class: "own-grid", role: "radiogroup", "aria-label": "Ownership" });
  const assignee = input("", { placeholder: "person@example.com" });
  const assigneeHelp = h("div", { class: "help" });
  const groupSel = select([["", "No group"], ...groups.filter((g) => g.kind === "static").map((g) => [g.id, g.name])], "");
  const uses = input(1, { type: "number", min: 1, max: 10000, class: "short" });
  const hours = select([["24", "1 day"], ["72", "3 days"], ["168", "1 week"], ["720", "30 days"], ["2160", "90 days"]], "72");
  const result = h("div");
  const err = h("div");

  const drawChoices = () => {
    platGrid.replaceChildren(...PLATFORMS.map((p) => {
      const on = enabled[p.id];
      const b = h("button", { type: "button", class: "choice plat-card", role: "radio", "aria-checked": String(platform === p.id), "aria-pressed": String(platform === p.id),
        onclick: () => { if (on) { platform = p.id; drawChoices(); } } },
      h("span", { class: "tile" }, icon(p.icon)),
      h("span", { class: "name" }, p.label),
      h("span", { class: "desc" }, p.desc));
      if (!on) b.setAttribute("aria-disabled", "true");
      return h("div", { class: "plat-opt" }, b,
        on ? null : h("div", { class: "plat-setup" }, can("admin") ? link("/settings/platforms", "Set up " + p.label) : "Not set up yet. Ask an admin to set it up."));
    }));
    ownGrid.replaceChildren(...Object.entries(OWNERSHIP).map(([k, o]) => h("button", { type: "button", class: "choice plat-card " + (k === "personal" ? "personal" : ""), role: "radio",
      "aria-checked": String(ownership === k), "aria-pressed": String(ownership === k), onclick: () => { ownership = k; drawChoices(); } },
    h("span", { class: "tile" }, icon(o.icon)),
    h("span", { class: "name" }, o.label),
    h("span", { class: "desc" }, o.desc),
    h("ul", { class: "opts" }, o.points.map((t) => h("li", {}, icon("check"), t))))));
    assigneeHelp.textContent = platform === "apple" && ownership === "personal"
      ? "Required: the user's Managed Apple ID from Apple Business Manager."
      : platform === "windows" ? "The user signs in with this email; leave empty to allow any." : "Optional. Shown on the device record.";
  };
  drawChoices();

  const create = h("button", { class: "btn primary", onclick: () => withBusy(create, async () => {
    err.replaceChildren();
    try {
      const r = await api("POST", "/enrollment-tokens", { platform, ownership, assignee: assignee.get(), groupId: groupSel.value || null, maxUses: uses.get() || 1, expiresInHours: Number(hours.value) });
      result.replaceChildren(resultPanel(r));
      result.scrollIntoView({ behavior: "smooth", block: "start" });
    } catch (e) { err.replaceChildren(errorNotice(e)); }
  }) }, "Create enrollment");

  return page({ title: "Enroll devices", sub: "Create a single-use or shared enrollment, then open it on the devices." },
    step(1, "Choose a platform", null, platGrid),
    step(2, "Choose who owns the device", "Decided here, never by the device. It sets what VaanarSena is allowed to do.", ownGrid),
    step(3, "Set the details", null, h("div", { class: "form details-form" },
      field("User", assignee, assigneeHelp, { inline: true }),
      field("Add to group", groupSel, "Smart groups pick devices up on their own.", { inline: true }),
      field("Devices allowed", uses, "How many devices may enroll with this link.", { inline: true }),
      field("Expires after", hours, null, { inline: true }),
      err, h("div", {}, create))),
    result,
    panel({ title: "Recent enrollments", sub: "Track and revoke the enrollments you created." }, tokenTable(toks.tokens)));
}

function resultPanel(r) {
  const t = r.meta, ins = r.instructions;
  const rows = [];
  if (ins.profileUrl) rows.push(field("Enrollment link", copyLine(ins.profileUrl)));
  if (ins.enrollUrl && t.platform === "windows") rows.push(field("Start enrollment on the PC", copyLine(ins.enrollUrl)), field("Server address", copyLine(ins.server)), field("Password", copyLine(ins.password)));
  if (ins.enrollUrl && t.platform === "android") rows.push(field("Enrollment link", copyLine(ins.enrollUrl)));
  if (ins.command) rows.push(field("Command", copyLine(ins.command)));
  const label = PLATFORMS.find((p) => p.id === t.platform).label;
  return panel({ title: "Ready to enroll", sub: label + ", " + (t.ownership === "personal" ? "personal" : "company-owned") + ", expires " + fmtTime(t.expiresAt),
    actions: [badge(t.ownership === "personal" ? "Personal" : "Company-owned", t.ownership === "personal" ? "personal" : "ok")] },
  body(h("div", { class: "ready" },
    h("div", {},
      notice("gold", "This token is shown once. Copy what you need now."),
      h("div", { class: "ready-fields" },
        h("p", {}, INSTRUCTIONS[t.platform][t.ownership]),
        rows)),
    ins.qrCode ? h("div", { class: "ready-qr" }, h("img", { class: "qr", alt: "Enrollment QR code", src: `/api/v1/enrollment-tokens/${t.id}/qr.png` }),
      h("div", { class: "help" }, "Scan with the device camera or the setup screen.")) : null)));
}

function tokenTable(tokens) {
  const stateOf = (t) => t.revoked ? "Revoked" : new Date(t.expiresAt) < new Date() ? "Expired" : t.uses >= t.maxUses ? "Used up" : "Active";
  const stateBadge = (s) => badge(s, s === "Active" ? "ok" : s === "Revoked" ? "bad" : "");
  return table([
    { label: "Platform", render: (t) => { const p = PLATFORMS.find((x) => x.id === t.platform); return h("span", { class: "plat" }, icon(p ? p.icon : "devices"), p ? p.label : t.platform); } },
    { label: "Ownership", render: (t) => t.ownership === "personal" ? badge("Personal", "personal") : badge("Corporate") },
    { label: "User", render: (t) => t.assignee || h("span", { class: "muted" }, "Anyone") },
    { label: "Used", cls: "num", render: (t) => `${t.uses} of ${t.maxUses}` },
    { label: "Expires", render: (t) => fmtTime(t.expiresAt) },
    { label: "State", render: (t) => { const c = h("span", { class: "state-cell" }, stateBadge(stateOf(t))); t.__state = c; return c; } },
    { label: "", cls: "col-actions", render: (t) => stateOf(t) === "Active" ? h("button", { class: "btn small", onclick: async (e) => {
      await api("DELETE", "/enrollment-tokens/" + t.id); toast("Enrollment revoked"); t.__state.replaceChildren(stateBadge("Revoked")); e.target.remove();
    } }, "Revoke") : "" },
  ], tokens, { empty: emptyState("No enrollments yet", "Enrollments you create appear here so you can track and revoke them.", null, "enroll"), label: "Recent enrollments" });
}
