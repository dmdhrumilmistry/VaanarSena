// Unsaved-changes guard: editing a policy, then pressing the browser's Back
// button, must ask before leaving; Cancel stays, Leave page goes back.
const { chromium } = require("playwright-core");
const { BASE, EMAIL, PASSWORD, CHANNEL } = require("./config");
(async () => {
  const b = await chromium.launch({ channel: CHANNEL, headless: true });
  const p = await (await b.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1400, height: 900 } })).newPage();
  const errs = []; p.on("pageerror", (e) => errs.push(e.message));
  await p.goto(BASE + "/"); await p.fill("#login-email", EMAIL); await p.fill("#login-pw", PASSWORD);
  await p.click("button[type=submit]"); await p.waitForSelector(".topbar");
  const id = await p.evaluate(async () => (await (await fetch("/api/v1/policies", { headers: { "X-Requested-With": "t" } })).json()).policies[0].id);
  await p.goto(BASE + "/devices"); await p.waitForSelector(".topbar");
  await p.evaluate((id) => { history.pushState(null, "", "/policies/" + id); window.dispatchEvent(new Event("vs:navigate")); }, id);
  await p.waitForSelector(".savebar", { state: "attached" });
  const name = p.locator("input").first(); await name.fill((await name.inputValue()) + " x");
  await p.waitForTimeout(200);
  await p.goBack(); await p.waitForTimeout(400);
  const dlg = await p.locator("dialog[open]").count();
  console.log("dialog on back:", dlg, "url:", p.url());
  await p.locator("dialog[open] button", { hasText: "Cancel" }).click(); await p.waitForTimeout(200);
  console.log("after cancel url:", p.url(), "still editing:", await p.locator(".savebar:not([hidden])").count());
  await p.goBack(); await p.waitForTimeout(400);
  await p.locator("dialog[open] button", { hasText: "Leave page" }).click(); await p.waitForTimeout(700);
  console.log("after leave url:", p.url());
  console.log("errors:", errs.join(" | ") || "none");
  await b.close();
})();
