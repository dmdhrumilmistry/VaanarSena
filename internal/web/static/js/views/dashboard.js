import { h, api, state, page, panel, body, link, table, platformTag, ownershipTag, complianceTag, ago, can, emptyState, go } from "../core.js";

const plural = (n, one, many) => n + " " + (n === 1 ? one : many);

export async function view() {
  const [s, attentionRes, plat] = await Promise.all([
    api("GET", "/stats"),
    api("GET", "/devices?status=enrolled&limit=500"),
    can("admin") ? api("GET", "/platforms").catch(() => null) : Promise.resolve(null),
  ]);
  const enrolled = s.byStatus.enrolled || 0;
  const now = Date.now();
  const stale = (d) => !d.lastSeenAt || now - new Date(d.lastSeenAt).getTime() > 7 * 864e5;
  const attention = attentionRes.devices.filter((d) => d.compliant === false || stale(d));
  const nonCompliant = s.nonCompliant || 0;
  const staleN = s.stale7d || 0;
  const healthy = Math.max(0, enrolled - nonCompliant - staleN);
  const enrolling = s.byStatus.enrolling || 0;

  const readiness = s.total === 0
    ? h("div", { class: "readiness" },
        h("h2", {}, "No devices yet. Enroll the first one to start."),
        h("p", { class: "muted" }, "Create an enrollment link for Apple, Windows, Android or Linux, then open it on the device."),
        can("operator") ? h("div", { class: "row" }, link("/enroll", h("span", { class: "btn primary" }, "Enroll a device"))) : null)
    : h("div", { class: "readiness" },
        h("h2", {}, attention.length
          ? `${plural(enrolled, "device is", "devices are")} under management, and ${plural(attention.length, "needs", "need")} attention.`
          : `${plural(enrolled, "device is", "devices are")} under management, and all are in good standing.`),
        bar([[healthy, "ok"], [nonCompliant, "bad"], [staleN, "warn"], [enrolling, "idle"]]),
        h("div", { class: "legend" },
          h("span", {}, h("i", { class: "dot ok" }), h("b", {}, healthy), "in good standing"),
          h("span", {}, h("i", { class: "dot bad" }), h("b", {}, nonCompliant), "not compliant"),
          h("span", {}, h("i", { class: "dot warn" }), h("b", {}, staleN), "silent for 7 days"),
          h("span", {}, h("i", { class: "dot" }), h("b", {}, enrolling), "still enrolling"),
          h("span", {}, h("i", { class: "dot gold" }), h("b", {}, s.byOwnership.personal || 0), "personal (BYOD)")));

  const byPlatform = Object.entries(s.byPlatform).sort((a, b) => b[1] - a[1]);
  const max = Math.max(1, ...byPlatform.map(([, n]) => n));
  const composition = byPlatform.length
    ? body(...byPlatform.map(([p, n]) => {
        const m = h("span"); m.style.width = (100 * n / max) + "%";
        return h("div", { class: "meter-row" }, platformTag(p), h("div", { class: "meter" }, m), h("div", { class: "num" }, n));
      }))
    : body(h("p", { class: "muted" }, "Devices appear here by platform once enrolled."));

  const attentionTable = table([
    { label: "Device", render: (d) => h("div", {}, h("div", { class: "primary-cell" }, d.name || d.serial || "Unnamed device"), h("div", { class: "secondary-cell" }, d.assignee || "No user")) },
    { label: "Platform", render: (d) => platformTag(d.platform) },
    { label: "Why", render: (d) => d.compliant === false ? complianceTag(false) : h("span", { class: "status" }, h("span", { class: "dot warn" }), "Last seen " + ago(d.lastSeenAt)) },
    { label: "Ownership", render: (d) => ownershipTag(d.ownership) },
  ], attention.slice(0, 8), { onRow: (d) => go("/devices/" + d.id), empty: emptyState("Nothing needs attention", "Every enrolled device is compliant and checked in this week.") });

  const platforms = plat ? platformReadiness(plat.platforms) : null;

  return page({ title: "Overview", sub: state.info.org },
    h("section", { class: "panel" }, body(readiness)),
    h("div", { class: "grid2 block" },
      panel({ title: "Fleet by platform" }, composition),
      panel({ title: "Needs attention", actions: attention.length > 8 ? [link("/devices", "See all devices")] : null }, attentionTable)),
    platforms);
}

function bar(parts) {
  const total = parts.reduce((a, [n]) => a + n, 0) || 1;
  const el = h("div", { class: "bar", role: "img", "aria-label": "Fleet readiness" });
  for (const [n, cls] of parts) {
    if (!n) continue;
    const s = h("span", { class: cls }); s.style.width = (100 * n / total) + "%"; el.append(s);
  }
  return el;
}

function platformReadiness(p) {
  const rows = [
    ["apple", "Apple (iOS, iPadOS, macOS)", p.apple],
    ["windows", "Windows", p.windows],
    ["android", "Android", p.android],
    ["chromeos", "ChromeOS", p.chromeos],
    ["linux", "Linux", p.linux],
  ];
  const missing = rows.filter(([, , st]) => !st.configured);
  const names = { apple: "Apple", windows: "Windows", android: "Android", chromeos: "ChromeOS", linux: "Linux" };
  const list = missing.map(([k]) => names[k]);
  const sub = !missing.length ? "Every platform is ready to enroll devices."
    : `${list.join(", ")} ${list.length === 1 ? "needs" : "need"} setup before devices can enroll.`;
  return panel({ title: "Platforms", sub,
    actions: [link("/settings/platforms", h("span", { class: "btn small" }, "Manage platforms"))] },
    body(h("div", { class: "legend" }, rows.map(([k, label, st]) => h("span", {}, h("i", { class: "dot " + (st.configured ? "ok" : st.error ? "bad" : "") }), label)))));
}
