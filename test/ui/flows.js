// Functional checks of the console: drive real flows through the UI and verify
// the results through the API.
const { chromium } = require("playwright-core");
const { BASE, EMAIL, PASSWORD, CHANNEL } = require("./config");
const stamp = Date.now().toString(36);
const fails = [];
const check = (cond, msg) => { if (!cond) fails.push(msg); console.log((cond ? "ok   " : "FAIL ") + msg); };

(async () => {
  const b = await chromium.launch({ channel: CHANNEL, headless: true });
  const p = await (await b.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1400, height: 900 } })).newPage();
  const errors = [];
  p.on("pageerror", (e) => errors.push(e.message));
  const api = (path, opts) => p.evaluate(async ([path, opts]) => {
    const r = await fetch("/api/v1" + path, { headers: { "Content-Type": "application/json", "X-Requested-With": "t" }, ...(opts || {}) });
    return r.json();
  }, [path, opts]);

  await p.goto(BASE + "/");
  await p.fill("#login-email", EMAIL);
  await p.fill("#login-pw", PASSWORD);
  await p.click("button[type=submit]");
  await p.waitForSelector(".topbar");

  // 1. Policy through the form
  await p.goto(BASE + "/policies/new");
  await p.waitForSelector("section.pcard");
  const about = p.locator("section.panel").first();
  await about.locator("input").nth(0).fill("ui-policy-" + stamp);
  await about.locator("input").nth(2).fill("25");
  const sec = (t) => p.locator("section.pcard", { has: p.locator(".t", { hasText: t }) });
  await sec("Passcode").locator(".switch").first().click();
  await sec("Passcode").locator("input[type=number]").first().fill("10");
  await sec("Restrictions").locator(".restriction", { hasText: "Camera" }).getByRole("button", { name: "Block" }).click();
  await sec("Restrictions").locator(".restriction", { hasText: "USB storage" }).getByRole("button", { name: "Allow" }).click();
  await sec("Wi-Fi").getByRole("button", { name: "Add network" }).click();
  await sec("Wi-Fi").locator("input[placeholder='Network name (SSID)']").fill("corp-" + stamp);
  await sec("Wi-Fi").locator("input[type=password]").fill("s3cret-pass");
  const addPay = sec("Custom payloads").getByRole("button", { name: "Add payload" });
  await addPay.scrollIntoViewIfNeeded();
  await p.waitForTimeout(300); // the menu closes on scroll
  await addPay.click();
  await p.getByRole("menuitem", { name: "Windows OMA-URI setting" }).click();
  const drw = p.locator("dialog.drawer");
  await drw.locator("input[placeholder^='./Device']").fill("./Device/Vendor/MSFT/Policy/Config/Experience/AllowCortana");
  await drw.locator("input[placeholder='Value']").fill("0");
  await drw.getByRole("button", { name: "Add payload" }).click();
  check(await sec("Custom payloads").locator(".payload").count() === 1, "custom payload listed after adding");
  // round-trip through the JSON tab
  await p.getByRole("button", { name: "JSON", exact: true }).click();
  const jsonText = await p.locator("textarea:visible").first().inputValue();
  check(jsonText.includes('"minLength": 10') && jsonText.includes("AllowCortana"), "policy form serializes to JSON");
  await p.getByRole("button", { name: "Form", exact: true }).click();
  await p.locator(".picklist label", { hasText: "all-corporate" }).locator("input").check();
  await p.getByRole("button", { name: "Create policy" }).first().click();
  await p.waitForURL(/\/policies\/[0-9a-f-]{36}$/, { timeout: 8000 }).catch(() => {});
  const pols = (await api("/policies")).policies;
  const mine = pols.find((x) => x.name === "ui-policy-" + stamp);
  check(!!mine, "policy saved");
  if (mine) {
    const full = await api("/policies/" + mine.id);
    const d = full.document;
    check(d.passcode && d.passcode.required && d.passcode.minLength === 10, "passcode saved");
    check(d.restrictions && d.restrictions.camera === false && d.restrictions.usbStorage === true && !("bluetooth" in d.restrictions), "restrictions saved (unmanaged left out)");
    check(d.wifi && d.wifi[0].ssid === "corp-" + stamp && d.wifi[0].password === "s3cret-pass", "wifi saved");
    check(d.custom && d.custom.windows[0].data === "0" && d.custom.windows[0].format === "int", "windows custom payload saved");
    check(full.priority === 25 && full.groupIds.length === 1, "priority and assignment saved");
    // reload the editor: the form must reflect the saved document
    await p.goto(BASE + "/policies/" + mine.id);
    await p.waitForSelector("section.pcard");
    check(await sec("Passcode").locator("input[role=switch]").isChecked(), "managed section switch is on after load");
    check((await sec("Restrictions").locator(".restriction", { hasText: "Camera" }).locator("button[aria-pressed=true]").innerText()) === "Block", "restriction state restored");
  }

  // 2. Smart group through the builder
  await p.goto(BASE + "/groups/new");
  await p.waitForSelector(".rulegroup");
  await p.locator("section.panel").first().locator("input").first().fill("ui-group-" + stamp);
  const conds = p.locator(".rulegroup .cond");
  // replace defaults: remove all conditions, add one
  while (await conds.count()) await conds.first().getByRole("button", { name: "Remove condition" }).click();
  await p.getByRole("button", { name: "Add condition", exact: true }).click();
  await conds.first().locator("select").nth(0).selectOption("ownership");
  await conds.first().locator("select").nth(1).selectOption("eq");
  await conds.first().locator("select").nth(2).selectOption("personal");
  await p.getByRole("button", { name: "Refresh preview" }).click();
  await p.waitForSelector("text=device matches right now");
  check(await p.locator("text=1 device matches right now.").count() === 1, "rule preview finds the personal device");
  await p.getByRole("button", { name: "Create group" }).first().click();
  await p.waitForURL(/\/groups\/[0-9a-f-]{36}/, { timeout: 8000 }).catch(() => {});
  const g = (await api("/groups")).groups.find((x) => x.name === "ui-group-" + stamp);
  const r = g && g.rules, c0 = r && r.conditions && r.conditions[0];
  check(g && g.kind === "smart" && r.match === "all" && r.conditions.length === 1 && !r.rules && c0.field === "ownership" && c0.op === "eq" && c0.value === "personal", "smart group rules saved exactly");
  await new Promise((r) => setTimeout(r, 1500));
  const g2 = (await api("/groups")).groups.find((x) => x.name === "ui-group-" + stamp);
  check(g2 && g2.deviceCount === 1, "smart group populated");

  // 3. Blueprint with an onboarding step
  await p.goto(BASE + "/blueprints/new");
  await p.waitForSelector("details.disclosure");
  await p.locator("section.panel").first().locator("input").first().fill("ui-blueprint-" + stamp);
  await p.locator(".picklist").first().locator("label", { hasText: "ui-group-" + stamp }).locator("input").check();
  await p.getByRole("button", { name: "Add step" }).click();
  await p.locator(".repeater .item select").last().selectOption("lock");
  await p.locator(".repeater .item input[placeholder='This device is locked by IT']").fill("Welcome aboard");
  await p.getByRole("button", { name: "Create blueprint" }).first().click();
  await p.waitForURL(/\/blueprints\/[0-9a-f-]{36}/, { timeout: 8000 }).catch(() => {});
  const bp = (await api("/blueprints")).blueprints.find((x) => x.name === "ui-blueprint-" + stamp);
  check(bp && bp.spec.onEnroll && bp.spec.onEnroll[0].type === "lock" && bp.spec.onEnroll[0].params.message === "Welcome aboard", "blueprint step with params saved");
  check(bp && bp.groupIds.length === 1, "blueprint targets saved");

  // 4. Command from the device page; BYOD device hides device-wide commands
  const devs = (await api("/devices?limit=50")).devices;
  const corp = devs.find((d) => d.ownership === "corporate" && d.name === "eng-ws-12");
  const pers = devs.find((d) => d.ownership === "personal");
  await p.goto(BASE + "/devices/" + corp.id + "?tab=commands");
  await p.getByRole("button", { name: "Send command" }).first().click();
  await p.locator("dialog.drawer select[aria-label=Command]").waitFor();
  await p.locator("select[aria-label=Command]").selectOption("lock");
  await p.locator("input[placeholder='This device is locked by IT']").fill("Locked by test");
  await p.locator("dialog.drawer").getByRole("button", { name: "Send command" }).click();
  await p.waitForSelector("text=Lock queued").catch(() => {});
  const cmds = await api("/devices/" + corp.id + "/commands");
  check(cmds.commands.some((c) => c.type === "lock" && c.params.message === "Locked by test"), "lock command queued with message");
  await p.goto(BASE + "/devices/" + pers.id + "?tab=commands");
  await p.getByRole("button", { name: "Send command" }).first().click();
  await p.locator("dialog.drawer select[aria-label=Command]").waitFor();
  const opts = await p.locator("select[aria-label=Command] option").allInnerTexts();
  check(!opts.some((o) => /Lock|Run script|Restart/.test(o)) && opts.some((o) => /Retire/.test(o)), "personal device offers only work-container commands");

  // 5. Enrollment wizard
  await p.goto(BASE + "/enroll");
  await p.getByRole("radio", { name: /Linux/ }).click();
  await p.getByRole("button", { name: "Create enrollment" }).click();
  await p.waitForSelector("text=Ready to enroll");
  check((await p.locator(".copyline code").first().innerText()).startsWith("sudo vaanarsena-agent enroll --server"), "linux enrollment shows the agent command");

  // 6. Settings: Google key validation error is shown inline
  await p.goto(BASE + "/settings/platforms");
  await p.waitForSelector("text=Google service account");
  await p.locator("textarea[aria-label='Service account key JSON']").fill('{"type":"authorized_user"}');
  await p.getByRole("button", { name: "Save key" }).click();
  await p.waitForSelector(".notice.bad");
  check((await p.locator(".notice.bad").first().innerText()).includes("service_account"), "invalid Google key explained");

  // 7. Software inventory: fleet page, drawer, device tab, personal note
  await p.goto(BASE + "/software");
  await p.waitForSelector("table tbody tr");
  check(await p.locator("table tbody tr").count() > 5, "software page lists apps");
  const firstRow = p.locator("table tbody tr").first();
  const firstName = (await firstRow.locator(".primary-cell").innerText()).trim();
  await firstRow.click();
  await p.locator("dialog.drawer .sw-dev").first().waitFor({ timeout: 8000 }).catch(() => {});
  check(await p.locator("dialog.drawer .sw-dev").count() > 0 && (await p.locator("dialog.drawer h2").innerText()).trim() === firstName, "software drawer lists devices for " + firstName);
  check((await p.locator("dialog.drawer .sw-dev-link").first().getAttribute("href")).endsWith("?tab=software"), "drawer device links deep-link to the software tab");
  await p.keyboard.press("Escape");
  await p.locator("input[aria-label='Search software']").fill("zzz-no-such-package");
  await p.waitForSelector("text=No software matches");
  check(true, "software search shows an empty state when nothing matches");
  const sdevs = (await api("/devices?limit=50")).devices;
  const linux = sdevs.find((d) => d.ownership === "corporate" && d.platform === "linux");
  let failedId = null;
  for (const d of sdevs.filter((x) => x.ownership === "corporate")) {
    const inv = await api("/devices/" + d.id + "/inventory?kind=service&limit=1000");
    if ((inv.items || []).some((i) => i.state === "failed")) { failedId = d.id; break; }
  }
  check(!!failedId || !linux, "a seeded device has a failed service");
  if (failedId) {
    await p.goto(BASE + "/devices/" + failedId + "?tab=software");
    await p.getByRole("tab", { name: /Services/ }).click();
    await p.waitForSelector(".badge.bad");
    check(await p.locator("table .badge.bad", { hasText: "Failed" }).count() > 0, "device software tab shows a failed service badge");
    check(await p.locator("table .badge.ok", { hasText: "Running" }).count() > 0, "device software tab shows running services");
  }
  if (pers) {
    await p.goto(BASE + "/devices/" + pers.id + "?tab=software");
    await p.waitForSelector("text=personally owned");
    check((await p.locator(".tabbody").innerText()).includes("personally owned"), "personal device software tab shows the server note");
  }

  check(errors.length === 0, "no page errors" + (errors.length ? ": " + errors.join(" | ") : ""));
  await b.close();
  if (fails.length) process.exit(1);
})().catch((e) => { console.error("CRASH", e.message); process.exit(1); });
