// Sign-in page with password sign-in restricted to break-glass accounts.
// Needs the dev server started in that mode:
//   VS_PASSWORD_LOGIN=false VS_PASSWORD_LOGIN_ALLOW=admin@example.com \
//   VS_OIDC_ISSUER=https://accounts.google.com VS_OIDC_CLIENT_ID=test VS_OIDC_NAME=Google test/dev/start.sh
//   node signin-restricted.js <screenshot dir>
const { chromium } = require("playwright-core");
const { BASE, EMAIL, PASSWORD, CHANNEL } = require("./config");
const out = process.argv[2] || ".";
const fails = [];
const check = (c, m) => { console.log((c ? "ok   " : "FAIL ") + m); if (!c) fails.push(m); };
(async () => {
  const b = await chromium.launch({ channel: CHANNEL, headless: true });
  for (const [scheme, width] of [["light", 1440], ["dark", 390]]) {
    const p = await (await b.newContext({ ignoreHTTPSErrors: true, viewport: { width, height: 900 }, colorScheme: scheme })).newPage();
    const errors = [];
    p.on("pageerror", (e) => errors.push(e.message));
    await p.goto(BASE + "/");
    await p.waitForSelector(".sso-btn");
    await p.waitForTimeout(300);
    await p.screenshot({ path: `${out}/restricted-${scheme}-${width}.png` });
    check(await p.locator("form").isHidden(), `${scheme}: password form hidden`);
    check(!/\b(null|undefined)\b/.test(await p.locator(".login .form-wrap").innerText()), `${scheme}: no stray null/undefined text`);
    check(await p.locator(".divider-label").count() === 0, `${scheme}: no "or use a password" divider`);
    check((await p.locator(".sso-btn").innerText()).includes("Sign in with Google"), `${scheme}: Google button`);
    const reveal = p.getByRole("button", { name: "Administrator sign-in with a password" });
    check(await reveal.isVisible(), `${scheme}: break-glass link visible`);
    await reveal.click();
    check(await p.locator("form").isVisible(), `${scheme}: form shown after the link`);
    check(await p.evaluate(() => document.activeElement && document.activeElement.id === "login-email"), `${scheme}: focus moves to email`);
    await p.screenshot({ path: `${out}/restricted-${scheme}-${width}-revealed.png` });
    if (scheme === "light") {
      await p.fill("#login-email", EMAIL);
      await p.fill("#login-pw", PASSWORD);
      await p.click("button[type=submit]");
      await p.waitForSelector(".topbar", { timeout: 8000 }).catch(() => {});
      check(await p.locator(".topbar").count() === 1, "break-glass admin signs in with a password");
    }
    check(errors.length === 0, `${scheme}: no page errors ${errors.join(" | ")}`);
  }
  // A non-allowed user with the right password is refused by the server.
  const api = await (await b.newContext({ ignoreHTTPSErrors: true })).newPage();
  await api.goto(BASE + "/");
  const r = await api.evaluate(async () => {
    const x = await fetch("/api/v1/auth/login", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ email: "nobody@example.com", password: "whatever-password" }) });
    return x.status;
  });
  check(r === 401, "unknown user still gets the generic 401");
  await b.close();
  process.exit(fails.length ? 1 : 0);
})().catch((e) => { console.error("CRASH", e.message); process.exit(1); });
