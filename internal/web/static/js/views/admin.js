import { h, api, state, page, panel, body, table, fmtTime, ago, emptyState, toast, confirmDialog, errorNotice, notice, copyLine } from "../core.js";
import { input, select, field } from "../forms.js";

const ROLES = [["auditor", "Auditor: read only"], ["operator", "Operator: devices and enrollment"], ["admin", "Admin: everything"]];
const roleName = (r) => r.charAt(0).toUpperCase() + r.slice(1);

export async function users() {
  const res = await api("GET", "/users");
  const email = input("", { type: "email", placeholder: "person@example.com" });
  const name = input("", { placeholder: "Full name" });
  const pw = input("", { type: "password", placeholder: "At least 12 characters", autocomplete: "new-password" });
  const role = select(ROLES, "operator");
  const err = h("div");
  const add = h("button", { class: "btn primary", onclick: async () => {
    err.replaceChildren();
    try { await api("POST", "/users", { email: email.get(), name: name.get(), password: pw.get(), role: role.get() }); toast("User added"); location.reload(); }
    catch (e) { err.replaceChildren(errorNotice(e)); }
  } }, "Add user");
  const changeRole = async (u) => {
    const r = select(ROLES, u.role);
    if (!(await confirmDialog({ title: "Change role for " + u.email, body: field("Role", r), confirmLabel: "Change role" }))) return;
    try { await api("PATCH", "/users/" + u.id, { role: r.get() }); toast("Role changed"); location.reload(); } catch (e) { toast(e.message, "bad"); }
  };
  const resetPw = async (u) => {
    const p = input("", { type: "password", placeholder: "At least 12 characters", autocomplete: "new-password" });
    if (!(await confirmDialog({ title: "Set a new password for " + u.email, body: field("New password", p, "Share it with them securely; they can change it under Account."), confirmLabel: "Set password" }))) return;
    try { await api("PATCH", "/users/" + u.id, { password: p.get() }); toast("Password set"); } catch (e) { toast(e.message, "bad"); }
  };
  return page({ title: "Users", sub: "People who can sign in to the console. Every change they make is in the audit log." },
    panel({ title: "Add a user" }, body(h("div", { class: "form" },
      h("div", { class: "grid2" }, field("Email", email), field("Name", name), field("Initial password", pw), field("Role", role)),
      err, h("div", {}, add)))),
    h("section", { class: "panel block" }, table([
      { label: "User", render: (u) => h("div", {}, h("div", { class: "primary-cell" }, u.name || u.email), u.name ? h("div", { class: "secondary-cell" }, u.email) : null) },
      { label: "Role", render: (u) => roleName(u.role) },
      { label: "State", render: (u) => h("span", { class: "status" }, h("span", { class: "dot " + (u.disabled ? "" : "ok") }), u.disabled ? "Disabled" : "Active") },
      { label: "Last sign-in", render: (u) => ago(u.lastLoginAt) },
      { label: "", render: (u) => u.id === state.me.id ? h("span", { class: "muted small" }, "You") : h("div", { class: "row" },
        h("button", { class: "btn small", onclick: () => changeRole(u) }, "Role"),
        h("button", { class: "btn small", onclick: () => resetPw(u) }, "Password"),
        h("button", { class: "btn small", onclick: async () => {
          try { await api("PATCH", "/users/" + u.id, { disabled: !u.disabled }); toast(u.disabled ? "User enabled" : "User disabled"); location.reload(); } catch (e) { toast(e.message, "bad"); }
        } }, u.disabled ? "Enable" : "Disable")) },
    ], res.users)));
}

export async function audit() {
  const q = input("", { type: "search", placeholder: "Filter by action, actor or target" });
  const host = h("div");
  let entries = [];
  let before = 0;
  const more = h("button", { class: "btn" }, "Load older entries");
  const draw = () => {
    const f = q.get().toLowerCase();
    const rows = f ? entries.filter((e) => (e.action + " " + e.actor + " " + e.target + " " + JSON.stringify(e.details)).toLowerCase().includes(f)) : entries;
    host.replaceChildren(table([
      { label: "When", render: (e) => h("span", { title: fmtTime(e.at) }, ago(e.at)) },
      { label: "Who", render: (e) => e.actor },
      { label: "Action", render: (e) => h("span", { class: /denied|failed|skipped/.test(e.action) ? "badge bad" : "badge" }, e.action) },
      { label: "Target", render: (e) => h("span", { class: "mono" }, e.target) },
      { label: "Details", render: (e) => Object.keys(e.details || {}).length ? h("span", { class: "mono" }, JSON.stringify(e.details)) : "" },
      { label: "From", render: (e) => e.remoteIp },
    ], rows, { empty: emptyState("No matching entries", "Try a shorter filter.") }));
  };
  const load = async () => {
    const res = await api("GET", "/audit?limit=200" + (before ? "&before=" + before : ""));
    entries = entries.concat(res.entries);
    if (res.entries.length) before = res.entries[res.entries.length - 1].id;
    more.hidden = res.entries.length < 200;
    draw();
  };
  q.addEventListener("input", draw);
  more.addEventListener("click", load);
  await load();
  return page({ title: "Audit log", sub: "Every change, sign-in and refused command. Entries cannot be edited or deleted, even in the database." },
    h("div", { style: { maxWidth: "420px", marginBottom: "14px" } }, q),
    h("section", { class: "panel" }, host), h("div", { class: "block" }, more));
}

export async function account() {
  const toks = await api("GET", "/api-tokens");
  const cur = input("", { type: "password", autocomplete: "current-password" });
  const nw = input("", { type: "password", autocomplete: "new-password", placeholder: "At least 12 characters" });
  const pwOut = h("div");
  const tname = input("", { placeholder: "terraform, ci-pipeline" });
  const days = select([["30", "30 days"], ["90", "90 days"], ["365", "1 year"], ["0", "Never"]], "90");
  const tokOut = h("div");
  return page({ title: "Account", sub: state.me.email + ", " + roleName(state.me.role) },
    panel({ title: "Password" }, body(h("div", { class: "form" },
      h("div", { class: "grid2" }, field("Current password", cur), field("New password", nw)), pwOut,
      h("div", {}, h("button", { class: "btn primary", onclick: async () => {
        try { await api("POST", "/auth/password", { current: cur.get(), new: nw.get() }); pwOut.replaceChildren(notice("ok", "Password changed.")); cur.value = nw.value = ""; }
        catch (e) { pwOut.replaceChildren(errorNotice(e)); }
      } }, "Change password"))))),
    panel({ title: "API tokens", sub: "For vsctl, CI and other automation. A token acts with your role. Send it as Authorization: Bearer <token>." },
      body(h("div", { class: "form" }, h("div", { class: "grid2" }, field("Name", tname), field("Expires", days)), tokOut,
        h("div", {}, h("button", { class: "btn primary", onclick: async () => {
          try {
            const r = await api("POST", "/api-tokens", { name: tname.get(), expiresInDays: Number(days.value) });
            tokOut.replaceChildren(h("div", { class: "secret stack" }, h("b", {}, "Copy this token now. It is not shown again."), copyLine(r.token)));
          } catch (e) { tokOut.replaceChildren(errorNotice(e)); }
        } }, "Create token")))),
      table([
        { label: "Name", render: (t) => t.name },
        { label: "Created", render: (t) => fmtTime(t.createdAt) },
        { label: "Expires", render: (t) => t.expiresAt ? fmtTime(t.expiresAt) : "Never" },
        { label: "Last used", render: (t) => ago(t.lastUsedAt) },
        { label: "", render: (t) => h("button", { class: "btn small", onclick: async (e) => {
          if (!(await confirmDialog({ title: `Revoke "${t.name}"?`, body: "Anything using it stops working.", confirmLabel: "Revoke", danger: true }))) return;
          await api("DELETE", "/api-tokens/" + t.id); e.target.closest("tr").remove(); toast("Token revoked");
        } }, "Revoke") },
      ], toks.tokens, { empty: h("div", { class: "empty" }, "No tokens yet.") })));
}
