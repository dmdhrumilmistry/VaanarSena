import { h, api, state, page, panel, body, link, table, ownershipTag, fmtTime, can, emptyState, toast, errorNotice, notice, icon, groupsCache, copyLine } from "../core.js";
import { input, select, field } from "../forms.js";

const PLATFORMS = [
  { id: "apple", label: "Apple", icon: "apple", desc: "iPhone, iPad and Mac, through Apple's MDM protocol.", key: "apple" },
  { id: "windows", label: "Windows", icon: "windows", desc: "Windows 10 and 11 Pro, Enterprise and Education.", key: "windows" },
  { id: "android", label: "Android", icon: "android", desc: "Fully managed phones or a work profile on personal ones.", key: "android" },
  { id: "linux", label: "Linux", icon: "linux", desc: "Desktops and servers with the VaanarSena agent.", key: "linux" },
];

const OWNERSHIP = {
  corporate: { label: "Company-owned", desc: "Full management: policies, apps, lock, restart and erase." },
  personal: { label: "Personal (BYOD)", desc: "Work data only. VaanarSena cannot see personal data, collect hardware IDs, lock or erase the device." },
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

export async function view() {
  const [toks, groups] = await Promise.all([api("GET", "/enrollment-tokens"), groupsCache()]);
  const enabled = { apple: state.info.platforms.ios, windows: state.info.platforms.windows, android: state.info.platforms.android, linux: state.info.platforms.linux };
  let platform = PLATFORMS.find((p) => enabled[p.id])?.id || "windows";
  let ownership = "corporate";

  const platGrid = h("div", { class: "choice-grid", role: "radiogroup", "aria-label": "Platform" });
  const ownGrid = h("div", { class: "choice-grid", role: "radiogroup", "aria-label": "Ownership" });
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
      const b = h("button", { type: "button", class: "choice", role: "radio", "aria-checked": String(platform === p.id), "aria-pressed": String(platform === p.id),
        onclick: () => { if (on) { platform = p.id; drawChoices(); } } },
        h("span", { class: "title" }, icon(p.icon), p.label),
        h("span", { class: "desc" }, p.desc),
        on ? null : h("span", { class: "desc" }, can("admin") ? link("/settings/platforms", "Set up " + p.label) : "Not set up yet. Ask an admin to set it up."));
      if (!on) b.setAttribute("aria-disabled", "true");
      return b;
    }));
    ownGrid.replaceChildren(...Object.entries(OWNERSHIP).map(([k, o]) => h("button", { type: "button", class: "choice", role: "radio",
      "aria-checked": String(ownership === k), "aria-pressed": String(ownership === k), onclick: () => { ownership = k; drawChoices(); } },
      h("span", { class: "title" }, o.label), h("span", { class: "desc" }, o.desc))));
    assigneeHelp.textContent = platform === "apple" && ownership === "personal"
      ? "Required: the user's Managed Apple ID from Apple Business Manager."
      : platform === "windows" ? "The user signs in with this email; leave empty to allow any." : "Optional. Shown on the device record.";
  };
  drawChoices();

  const create = h("button", { class: "btn primary", onclick: async () => {
    err.replaceChildren(); create.disabled = true;
    try {
      const r = await api("POST", "/enrollment-tokens", { platform, ownership, assignee: assignee.get(), groupId: groupSel.value || null, maxUses: uses.get() || 1, expiresInHours: Number(hours.value) });
      result.replaceChildren(resultPanel(r));
      result.scrollIntoView({ behavior: "smooth", block: "start" });
    } catch (e) { err.replaceChildren(errorNotice(e)); }
    create.disabled = false;
  } }, "Create enrollment");

  return page({ title: "Enroll devices", sub: "Create a single-use or shared enrollment, then open it on the devices." },
    panel({ title: "Platform" }, body(platGrid)),
    panel({ title: "Ownership", sub: "Decided here, never by the device. It sets what VaanarSena is allowed to do." }, body(ownGrid)),
    panel({ title: "Details" }, body(h("div", { class: "form" },
      field("User", assignee, assigneeHelp, { inline: true }),
      field("Add to group", groupSel, "Smart groups pick devices up on their own.", { inline: true }),
      field("Devices allowed", uses, "How many devices may enroll with this link.", { inline: true }),
      field("Expires after", hours, null, { inline: true }),
      err, h("div", {}, create)))),
    result,
    panel({ title: "Recent enrollments" }, tokenTable(toks.tokens)));
}

function resultPanel(r) {
  const t = r.meta, ins = r.instructions;
  const rows = [];
  if (ins.profileUrl) rows.push(field("Enrollment link", copyLine(ins.profileUrl)));
  if (ins.enrollUrl && t.platform === "windows") rows.push(field("Start enrollment on the PC", copyLine(ins.enrollUrl)), field("Server address", copyLine(ins.server)), field("Password", copyLine(ins.password)));
  if (ins.enrollUrl && t.platform === "android") rows.push(field("Enrollment link", copyLine(ins.enrollUrl)));
  if (ins.command) rows.push(field("Command", copyLine(ins.command)));
  return panel({ title: "Ready to enroll", sub: PLATFORMS.find((p) => p.id === t.platform).label + ", " + (t.ownership === "personal" ? "personal" : "company-owned") + ", expires " + fmtTime(t.expiresAt) },
    body(h("div", { class: "stack" },
      notice("gold", "This token is shown once. Copy what you need now."),
      h("p", {}, INSTRUCTIONS[t.platform][t.ownership]),
      rows,
      ins.qrCode ? h("img", { class: "qr", alt: "Enrollment QR code", src: `/api/v1/enrollment-tokens/${t.id}/qr.png` }) : null)));
}

function tokenTable(tokens) {
  const stateOf = (t) => t.revoked ? "Revoked" : new Date(t.expiresAt) < new Date() ? "Expired" : t.uses >= t.maxUses ? "Used up" : "Active";
  return table([
    { label: "Platform", render: (t) => t.platform.charAt(0).toUpperCase() + t.platform.slice(1) },
    { label: "Ownership", render: (t) => ownershipTag(t.ownership) },
    { label: "User", render: (t) => t.assignee || h("span", { class: "muted" }, "Anyone") },
    { label: "Used", cls: "num", render: (t) => `${t.uses} of ${t.maxUses}` },
    { label: "Expires", render: (t) => fmtTime(t.expiresAt) },
    { label: "State", render: (t) => { const s = stateOf(t); return h("span", { class: "status" }, h("span", { class: "dot " + (s === "Active" ? "ok" : "") }), s); } },
    { label: "", render: (t) => stateOf(t) === "Active" ? h("button", { class: "btn small", onclick: async (e) => {
      await api("DELETE", "/enrollment-tokens/" + t.id); toast("Enrollment revoked"); e.target.closest("tr").querySelector("td:nth-child(6)").textContent = "Revoked"; e.target.remove();
    } }, "Revoke") : "" },
  ], tokens, { empty: emptyState("No enrollments yet", "Enrollments you create appear here so you can track and revoke them.") });
}
