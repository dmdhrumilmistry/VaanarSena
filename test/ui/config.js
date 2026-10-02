// Shared settings for the browser tests. Defaults match test/dev/start.sh.
//   VS_UI_BASE      console URL            (https://localhost:18443)
//   VS_UI_EMAIL     admin email            (admin@example.com)
//   VS_UI_PASSWORD  admin password         (the dev server's bootstrap password)
//   VS_UI_CHANNEL   installed browser      (msedge on Windows, chrome elsewhere)
module.exports = {
  BASE: process.env.VS_UI_BASE || "https://localhost:18443",
  EMAIL: process.env.VS_UI_EMAIL || "admin@example.com",
  PASSWORD: process.env.VS_UI_PASSWORD || "correct-horse-battery",
  CHANNEL: process.env.VS_UI_CHANNEL || (process.platform === "win32" ? "msedge" : "chrome"),
};
