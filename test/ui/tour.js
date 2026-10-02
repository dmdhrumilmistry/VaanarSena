// Visit every console page, screenshot it, and report JS errors.
const { chromium } = require("playwright-core");
const { BASE, EMAIL, PASSWORD, CHANNEL } = require("./config");
const out = process.argv[2];
const scheme = process.argv[3] || "light";
const width = Number(process.argv[4] || 1440);
const only = process.argv[5] ? process.argv[5].split(",") : null;

const PAGES = [
  ["overview", "/"], ["devices", "/devices"], ["device", "DEVICE"], ["device-commands", "DEVICE?tab=commands"],
  ["groups", "/groups"], ["group-edit", "GROUP"], ["group-new", "/groups/new"], ["policies", "/policies"],
  ["policy-edit", "POLICY"], ["blueprints", "/blueprints"], ["blueprint-edit", "BLUEPRINT"], ["manifests", "/manifests"],
  ["enroll", "/enroll"], ["settings", "/settings/platforms"], ["users", "/users"], ["audit", "/audit"], ["account", "/account"],
  ["software", "/software"], ["device-software", "DEVICE?tab=software"], ["device-software-personal", "PERSONALDEV?tab=software"],
];

(async () => {
  const b = await chromium.launch({ channel: CHANNEL, headless: true });
  const ctx = await b.newContext({ ignoreHTTPSErrors: true, viewport: { width, height: 900 }, colorScheme: scheme });
  const p = await ctx.newPage();
  const errors = [];
  p.on("pageerror", (e) => errors.push("pageerror " + p.url() + ": " + e.message));
  p.on("console", (m) => { if (m.type() === "error" && !/Failed to load resource/.test(m.text())) errors.push("console " + p.url() + ": " + m.text()); });

  await p.goto(BASE + "/");
  await p.waitForSelector("#login-email");
  await p.screenshot({ path: `${out}/${scheme}-${width}-login.png` });
  await p.fill("#login-email", EMAIL);
  await p.fill("#login-pw", PASSWORD);
  await p.click("button[type=submit]");
  await p.waitForSelector(".topbar");

  const ids = await p.evaluate(async () => {
    const j = (u) => fetch("/api/v1" + u).then((r) => r.json());
    const [d, g, pol, bp] = await Promise.all([j("/devices?limit=50"), j("/groups"), j("/policies"), j("/blueprints")]);
    return {
      DEVICE: "/devices/" + (d.devices.find((x) => x.compliant === false) || d.devices[0]).id,
      GROUP: "/groups/" + (g.groups.find((x) => x.name === "at-risk") || g.groups[0]).id,
      POLICY: "/policies/" + (pol.policies.find((x) => x.name === "custom-payloads") || pol.policies[0]).id,
      BLUEPRINT: "/blueprints/" + bp.blueprints[0].id,
      PERSONALDEV: "/devices/" + (d.devices.find((x) => x.ownership === "personal") || d.devices[0]).id,
    };
  });

  for (const [name, path] of PAGES) {
    if (only && !only.includes(name)) continue;
    let url = path;
    for (const [k, v] of Object.entries(ids)) url = url.replace(k, v);
    await p.goto(BASE + url);
    await p.waitForFunction(() => { const m = document.querySelector("main"); return m && !/^Loading/.test(m.innerText.trim()); }, null, { timeout: 8000 }).catch(() => errors.push("timeout " + url));
    await p.waitForTimeout(500);
    // the router turns view exceptions into an error notice; treat those as failures
    const viewErr = await p.evaluate(() => { const n = document.querySelector("main .notice.bad[role=alert]"); return n && !document.querySelector("main .page-head") ? n.innerText : null; });
    if (viewErr) errors.push("view error " + url + ": " + viewErr);
    // open every collapsed config section on editors so the full form shows
    if (name.endsWith("-edit") || name === "group-new") await p.evaluate(() => document.querySelectorAll("details.cfg, details.disclosure").forEach((d) => (d.open = true)));
    await p.screenshot({ path: `${out}/${scheme}-${width}-${name}.png`, fullPage: true });
    console.log("ok " + name);
  }
  await b.close();
  if (errors.length) { console.log("ERRORS:\n" + errors.join("\n")); process.exit(1); }
  console.log("no errors");
})().catch((e) => { console.error("FAILED", e.message); process.exit(1); });
