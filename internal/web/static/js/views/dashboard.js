import { h, api, state, page, panel, body, link, table, platformTag, platformLabel, ago, fmtTime, can, emptyState, go, kpi, icon } from "../core.js";
import { needsAttention, attentionIssue, rowActions, platformTile } from "./devices.js";

const plural = (n, one, many) => n + " " + (n === 1 ? one : many);

// Platforms are grouped the way the artboard does: iOS and iPadOS share a row.
const GROUPS = [["Windows", ["windows"]], ["macOS", ["macos"]], ["iOS and iPadOS", ["ios", "ipados"]], ["Android", ["android"]], ["ChromeOS", ["chromeos"]], ["Linux", ["linux"]]];

export async function view() {
  const [s, attentionRes, plat, audit, topApps] = await Promise.all([
    api("GET", "/stats"),
    api("GET", "/devices?status=enrolled&limit=500"),
    can("admin") ? api("GET", "/platforms").catch(() => null) : Promise.resolve(null),
    api("GET", "/audit?limit=5").catch(() => null),
    api("GET", "/inventory/software?kind=app&limit=6").catch(() => null),
  ]);
  const enrolled = s.byStatus.enrolled || 0;
  const attention = attentionRes.devices.filter(needsAttention);
  const nonCompliant = s.nonCompliant || 0;
  const staleN = s.stale7d || 0;
  // The list is capped at 500 devices; beyond that fall back to the fleet-wide counts.
  const attentionN = attentionRes.total > attentionRes.devices.length ? nonCompliant + staleN : attention.length;
  const compliantPct = enrolled ? Math.round(1000 * Math.max(0, enrolled - nonCompliant) / enrolled) / 10 : null;
  const enrolling = s.byStatus.enrolling || 0;
  const personal = s.byOwnership.personal || 0;

  const actions = can("operator") ? [link("/enroll", h("span", { class: "btn primary" }, icon("plus"), "Enroll devices"))] : [];

  if (s.total === 0) {
    return page({ title: "Overview", sub: "Fleet health across every platform.", actions },
      panel({}, emptyState("No devices yet", "Create an enrollment link for Apple, Windows, Android or Linux, then open it on the device.",
        can("operator") ? link("/enroll", h("span", { class: "btn primary" }, "Enroll a device")) : null, "devices")),
      platformReadiness(plat));
  }

  const kpis = h("div", { class: "grid4 kpi-row" },
    kpi({ label: "Managed devices", value: enrolled.toLocaleString(), href: "/devices", delta: enrolling ? plural(enrolling, "device", "devices") + " still enrolling" : "All enrollments complete", tone: enrolling ? "info" : "ok" }),
    kpi({ label: "Compliant", value: compliantPct === null ? "None" : compliantPct + "%", href: "/devices?view=attention",
      delta: nonCompliant ? plural(nonCompliant, "device", "devices") + " not compliant" : "No compliance issues", tone: nonCompliant ? "bad" : "ok" }),
    kpi({ label: "Need attention", value: attentionN.toLocaleString(), href: "/devices?view=attention",
      delta: attentionN ? `${nonCompliant} not compliant, ${staleN} silent for 7 days` : "Everything is in good standing", tone: attentionN ? "warn" : "ok" }),
    kpi({ label: "Personal (BYOD)", value: personal.toLocaleString(), href: "/devices?ownership=personal", delta: "Work data only", tone: "personal" }));

  const attentionPanel = panel({ title: "Needs attention", sub: "Not compliant, or silent for more than 7 days",
    actions: attentionN > 8 ? [link("/devices?view=attention", "View all " + attentionN)] : null },
  table([
    { label: "Device", render: (d) => h("div", { class: "dev-cell" }, platformTile(d),
      h("div", { class: "dev-cell-text" }, h("div", { class: "primary-cell" }, d.name || d.serial || "Unnamed device"), h("div", { class: "secondary-cell" }, d.assignee || "No user"))) },
    { label: "Platform", cls: "hide-sm", render: (d) => h("div", {}, platformTag(d.platform), h("div", { class: "secondary-cell" }, d.osVersion)) },
    { label: "Issue", render: (d) => attentionIssue(d) },
    { label: "Last seen", cls: "hide-sm", render: (d) => h("span", { class: "nowrap", title: fmtTime(d.lastSeenAt) }, ago(d.lastSeenAt)) },
    { label: h("span", { class: "sr-only" }, "Actions"), cls: "act", render: (d) => rowActions(d) },
  ], attention.slice(0, 8), { label: "Devices that need attention", onRow: (d) => go("/devices/" + d.id),
    empty: emptyState("Nothing needs attention", "Every enrolled device is compliant and checked in this week.", null, "check") }));

  const counts = Object.entries(s.byPlatform);
  const rowsByGroup = GROUPS.map(([label, keys]) => [label, keys.reduce((a, k) => a + (s.byPlatform[k] || 0), 0)]);
  const known = new Set(GROUPS.flatMap(([, k]) => k));
  for (const [p, n] of counts) if (!known.has(p)) rowsByGroup.push([platformLabel(p), n]);
  const shown = rowsByGroup.filter(([, n]) => n > 0).sort((a, b) => b[1] - a[1]);
  const max = Math.max(1, ...shown.map(([, n]) => n));
  const composition = panel({ title: "Fleet by platform" }, body(shown.length
    ? h("ul", { class: "plat-bars" }, shown.map(([label, n]) => {
      const m = h("span", { class: "plat-fill" }); m.style.width = (100 * n / max) + "%";
      return h("li", {}, h("div", { class: "plat-bar-top" }, h("span", {}, label), h("span", { class: "num" }, n.toLocaleString())), h("div", { class: "meter", "aria-hidden": "true" }, m));
    }))
    : h("p", { class: "muted" }, "Devices appear here by platform once enrolled.")));

  const entries = audit && audit.entries ? audit.entries : null;
  const activity = entries ? panel({ title: "Recent activity", actions: [link("/audit", "Audit log")] },
    entries.length
      ? h("ul", { class: "activity" }, entries.map((e) => h("li", {},
        h("span", { class: "activity-dot " + (/denied|failed|skipped/.test(e.action) ? "bad" : "") }),
        h("div", { class: "grow" }, h("div", { class: "primary-cell" }, e.action.replace(/[._]/g, " ")), h("div", { class: "secondary-cell", title: fmtTime(e.at) }, [e.actor, ago(e.at)].filter(Boolean).join(", "))))))
      : emptyState("No activity yet", "Changes and sign-ins appear here.", null, "audit")) : null;

  return page({ title: "Overview", sub: "Fleet health across every platform.", actions },
    kpis,
    h("div", { class: "overview-grid" }, attentionPanel, h("div", { class: "overview-side" }, composition, softwarePanel(s.software, topApps), activity)),
    plat ? platformReadiness(plat) : null);
}

// Distinct apps, devices reporting and the six most widespread apps. Hidden when the data is missing.
function softwarePanel(sw, top) {
  if (!sw || !top || !Array.isArray(top.items)) return null;
  const items = top.items;
  const max = Math.max(1, ...items.map((i) => i.devices));
  return panel({ title: "Software", actions: [link("/software", "View all")] },
    body(h("div", { class: "sw-stats" },
      h("div", {}, h("span", { class: "sw-stat-n" }, (sw.apps || 0).toLocaleString()), h("span", { class: "sw-stat-l" }, "distinct apps")),
      h("div", {}, h("span", { class: "sw-stat-n" }, (sw.devicesReporting || 0).toLocaleString()), h("span", { class: "sw-stat-l" }, "devices reporting"))),
    items.length
      ? [h("h3", { class: "sw-top-h" }, "Most widespread apps"),
        h("ul", { class: "plat-bars" }, items.map((it) => {
          const m = h("span", { class: "plat-fill" }); m.style.width = (100 * it.devices / max) + "%";
          return h("li", {}, h("div", { class: "plat-bar-top" },
            link("/software?q=" + encodeURIComponent(it.name), h("span", { class: "sw-top-name" }, it.name)),
            h("span", { class: "num" }, it.devices.toLocaleString())),
          h("div", { class: "meter", "aria-hidden": "true" }, m));
        }))]
      : h("p", { class: "muted" }, "Installed apps appear here once devices check in.")));
}

function platformReadiness(plat) {
  if (!plat) return null;
  const p = plat.platforms;
  const rows = [["Apple (iOS, iPadOS, macOS)", p.apple], ["Windows", p.windows], ["Android", p.android], ["ChromeOS", p.chromeos], ["Linux", p.linux]];
  const missing = rows.filter(([, st]) => !st.configured);
  const configured = rows.length - missing.length;
  const names = missing.map(([label]) => label.replace(/ \(.*\)/, ""));
  const sub = !missing.length ? "Every platform is ready to enroll devices."
    : `${names.join(", ")} ${names.length === 1 ? "needs" : "need"} setup before devices can enroll.`;
  return panel({ title: "Platforms", sub, actions: [link("/settings/platforms", h("span", { class: "btn small" }, "Manage platforms"))] },
    body(h("ul", { class: "plat-status" }, rows.map(([label, st]) => h("li", {},
      h("span", { class: "dot " + (st.configured ? "ok" : st.error ? "bad" : "") }), h("span", {}, label),
      h("span", { class: "muted small" }, st.configured ? "Connected" : st.error ? "Error" : "Not configured"))),
    ),
    h("p", { class: "help plat-count" }, `${configured} of ${rows.length} platforms connected`)));
}
