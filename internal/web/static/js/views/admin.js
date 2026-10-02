import { h, api, state, page, panel, body, table, fmtTime, ago, emptyState, toast, confirmDialog, errorNotice, notice, copyLine, drawer, menu, icon, badge, initials, chip, withBusy } from "../core.js";
import { input, select, field } from "../forms.js";

const ROLES = [["auditor", "Auditor: read only"], ["operator", "Operator: devices and enrollment"], ["admin", "Admin: everything"]];
const roleName = (r) => r.charAt(0).toUpperCase() + r.slice(1);
const roleBadge = (r) => h("span", { class: "badge role-" + r }, roleName(r));

export async function users() {
  const res = await api("GET", "/users");
  const search = input("", { type: "search", placeholder: "Search by name or email", "aria-label": "Search users" });
  const roleFilter = select([["", "All roles"], ...ROLES.map(([v]) => [v, roleName(v)])], "", { "aria-label": "Filter by role" });
  const count = h("span", { class: "count" });
  const host = h("div");

  const openAdd = () => {
    const email = input("", { type: "email", placeholder: "person@example.com", autocomplete: "off" });
    const name = input("", { placeholder: "Full name" });
    const pw = input("", { type: "password", placeholder: "At least 12 characters", autocomplete: "new-password" });
    const role = select(ROLES, "operator");
    const err = h("div");
    const add = h("button", { class: "btn primary", type: "button", onclick: () => withBusy(add, async () => {
      err.replaceChildren();
      try { await api("POST", "/users", { email: email.get(), name: name.get(), password: pw.get(), role: role.get() }); toast("User added"); location.reload(); }
      catch (e) { err.replaceChildren(errorNotice(e)); }
    }) }, "Add user");
    const d = drawer({ title: "Add a user", sub: "They sign in with this email and password, and can change the password under Account.",
      body: h("div", { class: "form" }, field("Email", email), field("Name", name), field("Initial password", pw, "Share it with them securely."), field("Role", role, "Roles limit what this person can do in the console."), err),
      footer: [h("button", { class: "btn", type: "button", onclick: () => d.close() }, "Cancel"), add] });
  };

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
  const toggleDisabled = async (u) => {
    try { await api("PATCH", "/users/" + u.id, { disabled: !u.disabled }); toast(u.disabled ? "User enabled" : "User disabled"); location.reload(); } catch (e) { toast(e.message, "bad"); }
  };

  const rowActions = (u) => {
    if (u.id === state.me.id) return h("span", { class: "muted small" }, "You");
    const trigger = h("button", { type: "button", class: "iconbtn", "aria-label": "Actions for " + (u.name || u.email) }, icon("more"));
    menu(trigger, [
      { label: "Change role", icon: "edit", onClick: () => changeRole(u) },
      { label: "Set password", icon: "lock", onClick: () => resetPw(u) },
      { separator: true },
      { label: u.disabled ? "Enable user" : "Disable user", icon: u.disabled ? "check" : "x", danger: !u.disabled, onClick: () => toggleDisabled(u) },
    ]);
    return trigger;
  };

  const draw = () => {
    const f = search.get().toLowerCase();
    const rf = roleFilter.value;
    const rows = res.users.filter((u) => (!rf || u.role === rf) && (!f || ((u.name || "") + " " + u.email).toLowerCase().includes(f)));
    count.textContent = rows.length === res.users.length ? res.users.length + (res.users.length === 1 ? " user" : " users") : rows.length + " of " + res.users.length;
    host.replaceChildren(table([
      { label: "User", render: (u) => h("div", { class: "userline" }, h("span", { class: "avatar", "aria-hidden": "true" }, initials(u.name || u.email)),
        h("div", {}, h("div", { class: "primary-cell" }, u.name || u.email, u.id === state.me.id ? h("span", { class: "you" }, badge("You")) : null), u.name ? h("div", { class: "secondary-cell" }, u.email) : null)) },
      { label: "Role", render: (u) => roleBadge(u.role) },
      { label: "State", render: (u) => u.disabled ? badge("Disabled", "bad") : badge("Active", "ok") },
      { label: "Last sign-in", render: (u) => h("span", { title: u.lastLoginAt ? fmtTime(u.lastLoginAt) : "" }, ago(u.lastLoginAt)) },
      { label: "", cls: "col-actions", render: rowActions },
    ], rows, { label: "Users", empty: emptyState("No matching users", "Try a different search or role.", null, "users") }));
  };
  search.addEventListener("input", draw);
  roleFilter.addEventListener("change", draw);
  draw();

  return page({ title: "Users", sub: "People who can sign in to the console. Every change they make is in the audit log.",
    actions: [h("button", { class: "btn primary", type: "button", onclick: openAdd }, icon("plus"), "Add user")] },
  h("section", { class: "panel" }, h("div", { class: "toolbar" }, h("div", { class: "search" }, search), roleFilter, count), host));
}

export async function audit() {
  const q = input("", { type: "search", placeholder: "Search action, actor, target or details", "aria-label": "Search the audit log" });
  const kind = select([["", "All actions"], ["problems", "Denied or failed"]], "", { "aria-label": "Filter by action" });
  const actorSel = select([["", "All actors"]], "", { "aria-label": "Filter by actor" });
  const host = h("div");
  const chips = h("div", { class: "chip-row" });
  const count = h("span", { class: "count" });
  let entries = [];
  let before = 0;
  const more = h("button", { class: "btn", type: "button" }, "Load older entries");
  const moreWrap = h("div", { class: "audit-more" }, more);
  const isProblem = (e) => /denied|failed|skipped/.test(e.action);
  const syncActors = () => {
    const cur = actorSel.value;
    const actors = [...new Set(entries.map((e) => e.actor))].sort();
    actorSel.replaceChildren(h("option", { value: "" }, "All actors"), ...actors.map((a) => h("option", { value: a, selected: a === cur }, a)));
  };
  const draw = () => {
    const f = q.get().toLowerCase();
    const rows = entries.filter((e) => (!kind.value || isProblem(e)) && (!actorSel.value || e.actor === actorSel.value)
      && (!f || (e.action + " " + e.actor + " " + e.target + " " + JSON.stringify(e.details)).toLowerCase().includes(f)));
    count.textContent = "Showing " + rows.length + " of " + entries.length + " loaded";
    chips.replaceChildren(...[
      kind.value ? chip("Denied or failed", () => { kind.value = ""; draw(); }) : null,
      actorSel.value ? chip("Actor: " + actorSel.value, () => { actorSel.value = ""; draw(); }) : null,
      f ? chip("Search: " + q.get(), () => { q.value = ""; draw(); }) : null].filter(Boolean));
    chips.hidden = !chips.childNodes.length;
    host.replaceChildren(table([
      { label: "When", render: (e) => h("div", { class: "audit-when" }, h("div", {}, fmtTime(e.at)), h("div", { class: "secondary-cell" }, ago(e.at))) },
      { label: "Who", render: (e) => e.actor },
      { label: "Action", render: (e) => badge(e.action, isProblem(e) ? "bad" : "") },
      { label: "Target", render: (e) => e.target ? h("span", { class: "audit-target", title: e.target }, e.target) : "" },
      { label: "Details", render: (e) => { const t = Object.keys(e.details || {}).length ? JSON.stringify(e.details) : ""; return t ? h("span", { class: "audit-details", title: t }, t) : ""; } },
      { label: "From", render: (e) => h("span", { class: "mono" }, e.remoteIp) },
    ], rows, { dense: true, label: "Audit log", empty: emptyState("No matching entries", "Try a shorter search or clear the filters.", null, "audit") }));
  };
  const load = async () => {
    await withBusy(more, async () => {
      const res = await api("GET", "/audit?limit=200" + (before ? "&before=" + before : ""));
      entries = entries.concat(res.entries);
      if (res.entries.length) before = res.entries[res.entries.length - 1].id;
      moreWrap.hidden = res.entries.length < 200;
      syncActors();
      draw();
    });
  };
  q.addEventListener("input", draw);
  kind.addEventListener("change", draw);
  actorSel.addEventListener("change", draw);
  more.addEventListener("click", load);
  await load();
  return page({ title: "Audit log", sub: "Every change, sign-in and refused command. Entries cannot be edited or deleted, even in the database." },
    h("section", { class: "panel" },
      h("div", { class: "toolbar" }, h("div", { class: "search" }, q), kind, actorSel, count),
      chips, host, moreWrap));
}

export async function account() {
  const toks = await api("GET", "/api-tokens");
  const me = state.me;
  const cur = input("", { type: "password", autocomplete: "current-password" });
  const nw = input("", { type: "password", autocomplete: "new-password", placeholder: "At least 12 characters" });
  const pwOut = h("div");
  const tname = input("", { placeholder: "terraform, ci-pipeline" });
  const days = select([["30", "30 days"], ["90", "90 days"], ["365", "1 year"], ["0", "Never"]], "90");
  const tokOut = h("div");
  const pwBtn = h("button", { class: "btn primary", type: "button", onclick: () => withBusy(pwBtn, async () => {
    try { await api("POST", "/auth/password", { current: cur.get(), new: nw.get() }); pwOut.replaceChildren(notice("ok", "Password changed.")); cur.value = nw.value = ""; }
    catch (e) { pwOut.replaceChildren(errorNotice(e)); }
  }) }, "Change password");
  const tokBtn = h("button", { class: "btn primary", type: "button", onclick: () => withBusy(tokBtn, async () => {
    try {
      const r = await api("POST", "/api-tokens", { name: tname.get(), expiresInDays: Number(days.value) });
      tokOut.replaceChildren(h("div", { class: "secret token-new" }, h("b", {}, "Copy this token now. It is not shown again."), copyLine(r.token)));
    } catch (e) { tokOut.replaceChildren(errorNotice(e)); }
  }) }, "Create token");
  return page({ title: "Account", sub: "Your profile, password and API tokens." },
    h("div", { class: "account-grid" },
      panel({}, body(h("div", { class: "profile" },
        h("span", { class: "avatar", "aria-hidden": "true" }, initials(me.name || me.email)),
        h("div", {}, h("h2", {}, me.name || me.email), me.name ? h("div", { class: "email" }, me.email) : null),
        h("div", { class: "profile-kv" },
          h("div", {}, h("span", {}, "Role"), roleBadge(me.role)),
          h("div", {}, h("span", {}, "Email"), h("span", {}, me.email)))))),
      h("div", { class: "account-main" },
        panel({ title: "Password", sub: "Use at least 12 characters." }, body(h("div", { class: "form form-narrow" },
          field("Current password", cur), field("New password", nw), pwOut, h("div", {}, pwBtn)))),
        panel({ title: "API tokens", sub: "For vsctl, CI and other automation. A token acts with your role. Send it as Authorization: Bearer <token>." },
          body(h("div", { class: "form form-narrow" }, field("Name", tname), field("Expires", days), tokOut, h("div", {}, tokBtn))),
          table([
            { label: "Name", render: (t) => h("span", { class: "primary-cell" }, t.name) },
            { label: "Created", render: (t) => fmtTime(t.createdAt) },
            { label: "Expires", render: (t) => t.expiresAt ? fmtTime(t.expiresAt) : "Never" },
            { label: "Last used", render: (t) => ago(t.lastUsedAt) },
            { label: "", cls: "col-actions", render: (t) => h("button", { class: "btn small", onclick: async (e) => {
              if (!(await confirmDialog({ title: `Revoke "${t.name}"?`, body: "Anything using it stops working.", confirmLabel: "Revoke", danger: true }))) return;
              await api("DELETE", "/api-tokens/" + t.id); e.target.closest("tr").remove(); toast("Token revoked");
            } }, "Revoke") },
          ], toks.tokens, { label: "API tokens", empty: emptyState("No tokens yet", "Create one above to use the API or vsctl.", null, "lock") })))));
}
