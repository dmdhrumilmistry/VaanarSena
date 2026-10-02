// Capture documentation screenshots from the seeded local server.
const { chromium } = require("playwright-core");
const { BASE, EMAIL, PASSWORD, CHANNEL } = require("./config");
const out = process.argv[2];
(async () => {
  const b = await chromium.launch({ channel: CHANNEL, headless: true });
  const ctx = await b.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1 });
  const p = await ctx.newPage();
  await p.goto(BASE + "/");
  await p.waitForSelector("#login-email");
  await p.waitForTimeout(1600);
  await p.screenshot({ path: out + "/sign-in.png" });
  await p.fill("#login-email", EMAIL);
  await p.fill("#login-pw", PASSWORD);
  await p.click("button[type=submit]");
  await p.waitForSelector(".rail");
  const ids = await p.evaluate(async () => {
    const j = (u) => fetch("/api/v1" + u).then((r) => r.json());
    const [d, g, pol, bp] = await Promise.all([j("/devices?limit=50"), j("/groups"), j("/policies"), j("/blueprints")]);
    return {
      device: d.devices.find((x) => x.name === "design-lap-07").id,
      group: g.groups.find((x) => x.name === "at-risk").id,
      policy: pol.policies.find((x) => x.name === "custom-payloads").id,
      blueprint: bp.blueprints.find((x) => x.name === "standard-laptop").id,
    };
  });
  const shot = async (name, url, prep) => {
    await p.goto(BASE + url);
    await p.waitForFunction(() => { const m = document.querySelector("main"); return m && !/^Loading/.test(m.innerText.trim()); });
    await p.waitForTimeout(600);
    if (prep) await prep();
    await p.screenshot({ path: `${out}/${name}.png` });
    console.log("saved " + name);
  };
  await shot("overview", "/");
  await shot("devices", "/devices");
  await shot("software", "/software");
  await shot("device", "/devices/" + ids.device);
  await shot("smart-group", "/groups/" + ids.group, async () => { await p.waitForSelector("text=match right now"); await p.locator(".rulegroup").first().scrollIntoViewIfNeeded(); await p.evaluate(() => window.scrollBy(0, -80)); });
  await shot("policy-restrictions", "/policies/" + ids.policy, async () => {
    await p.evaluate(() => { const d = [...document.querySelectorAll("section.pcard")].find((x) => /^Restrictions/.test(x.querySelector("h2,h3").textContent.trim())); d.scrollIntoView(); window.scrollBy(0, -20); });
  });
  await shot("custom-payloads", "/policies/" + ids.policy, async () => {
    await p.evaluate(() => { const d = [...document.querySelectorAll("section.pcard")].find((x) => /^Custom payloads/.test(x.querySelector("h2,h3").textContent.trim())); d.scrollIntoView(); window.scrollBy(0, -20); });
  });
  await shot("blueprint", "/blueprints/" + ids.blueprint);
  await shot("enroll", "/enroll");
  await shot("platforms", "/settings/platforms");
  await shot("manifests", "/manifests");
  await ctx.close();
  const dark = await b.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 900 }, colorScheme: "dark" });
  const q = await dark.newPage();
  await q.goto(BASE + "/");
  await q.fill("#login-email", EMAIL);
  await q.fill("#login-pw", PASSWORD);
  await q.click("button[type=submit]");
  await q.waitForSelector(".rail");
  await q.goto(BASE + "/devices");
  await q.waitForTimeout(800);
  await q.screenshot({ path: out + "/devices-dark.png" });
  await b.close();
})();
