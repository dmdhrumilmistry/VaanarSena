// Settings > Webhooks: signed notifications of device events for conditional
// access and other automation.
import { h, api, go, table, badge, ago, fmtTime, emptyState, toast, confirmDialog, errorNotice, notice, copyLine, drawer, menu, icon, withBusy } from "../core.js";
import { input, field, check, toggle } from "../forms.js";

const DOCS = "https://dmdhrumilmistry.github.io/VaanarSena/webhooks/";

const reload = () => go("/settings/webhooks?_=" + Date.now());

const EVENT_LABELS = {
  "device.compliance_changed": "Compliance changed", "device.enrolled": "Enrolled", "device.retired": "Retired",
  "device.wiped": "Erased", "device.ownership_changed": "Ownership changed", "webhook.test": "Test",
};
const eventLabel = (e) => EVENT_LABELS[e] || e;

function deliveryBadge(d) {
  if (!d) return h("span", { class: "muted small" }, "Nothing sent yet");
  if (d.status === "delivered") return badge("Delivered", "ok");
  if (d.status === "failed") return badge("Failed", "bad");
  return badge(d.attempts ? "Retrying" : "Queued", "warn");
}

// The one-time view of a signing secret, after creation or rotation.
function secretNotice(secret) {
  return h("div", { class: "secret token-new" }, h("b", {}, "Copy the signing secret now. It is not shown again."), copyLine(secret),
    h("p", { class: "help" }, "Your receiver uses it to check the X-VaanarSena-Signature header of every request."));
}

function editDrawer(events, hook) {
  const name = input(hook ? hook.name : "", { placeholder: "Identity provider" });
  const url = input(hook ? hook.url : "", { type: "url", placeholder: "https://idp.example.com/hooks/vaanarsena" });
  const chosen = new Set(hook ? hook.events : ["device.compliance_changed"]);
  const boxes = events.map((e) => {
    const c = check(chosen.has(e.type), h("span", {}, h("span", {}, eventLabel(e.type)), h("span", { class: "help wh-event-help" }, e.description)));
    c.dataset.event = e.type;
    return c;
  });
  const enabled = toggle(hook ? hook.enabled : true, "Send events");
  const out = h("div");
  const save = h("button", { class: "btn primary", type: "button" }, hook ? "Save changes" : "Add webhook");
  const cancel = h("button", { class: "btn", type: "button" }, "Cancel");
  const form = h("div", { class: "form" },
    field("Name", name), field("URL", url, "VaanarSena POSTs JSON here. Redirects are not followed."),
    h("fieldset", { class: "wh-events" }, h("legend", {}, "Events"), boxes),
    enabled, out);
  const d = drawer({ title: hook ? "Edit webhook" : "Add a webhook", sub: "Requests are signed with HMAC-SHA256 and retried with backoff for about a day and a half.",
    body: form, footer: [cancel, save] });
  cancel.addEventListener("click", () => d.close());
  save.addEventListener("click", () => withBusy(save, async () => {
    out.replaceChildren();
    const body = { name: name.get(), url: url.get(), events: boxes.filter((b) => b.get()).map((b) => b.dataset.event), enabled: enabled.get() };
    try {
      if (hook) { await api("PUT", "/webhooks/" + hook.id, body); toast("Webhook saved"); d.close(); reload(); return; }
      const r = await api("POST", "/webhooks", body);
      // Show the secret once; the list reloads when the drawer closes.
      d.body.replaceChildren(h("div", { class: "form" }, notice("ok", "Webhook added: " + r.webhook.name), secretNotice(r.secret)));
      const done = h("button", { class: "btn primary", type: "button", onclick: () => { d.close(); reload(); } }, "Done");
      d.footer.replaceChildren(done);
      done.focus();
    } catch (e) { out.replaceChildren(errorNotice(e)); }
  }));
}

function deliveriesDrawer(hook) {
  const host = h("div");
  const more = h("button", { class: "btn", type: "button" }, "Load older deliveries");
  const moreWrap = h("div", { class: "wh-more" }, more);
  let rows = [], before = 0;
  const retry = (dl, btn) => withBusy(btn, async () => {
    try { await api("POST", `/webhooks/${hook.id}/deliveries/${dl.id}/retry`); toast("Delivery queued again"); rows = []; before = 0; await load(); }
    catch (e) { toast(e.message, "bad"); }
  });
  // A stacked list rather than a table: errors are long and the drawer is narrow.
  const item = (dl) => {
    const detail = [
      dl.attempts ? (dl.attempts === 1 ? "1 attempt" : dl.attempts + " attempts") : null,
      dl.responseCode ? "HTTP " + dl.responseCode : null,
      dl.status === "pending" && dl.attempts ? "next try " + fmtTime(dl.nextAttemptAt) : null,
      dl.status === "delivered" && dl.deliveredAt ? "delivered " + fmtTime(dl.deliveredAt) : null,
    ].filter(Boolean).join(", ");
    let retryBtn = null;
    if (dl.status !== "delivered") {
      retryBtn = h("button", { class: "btn small", type: "button" }, "Retry now");
      retryBtn.addEventListener("click", () => retry(dl, retryBtn));
    }
    return h("li", { class: "wh-dl" },
      h("div", { class: "wh-dl-head" },
        h("div", { class: "grow" }, h("span", { class: "primary-cell" }, eventLabel(dl.event)), " ",
          h("span", { class: "secondary-cell", title: fmtTime(dl.eventAt) }, ago(dl.eventAt))),
        deliveryBadge(dl), retryBtn),
      detail ? h("div", { class: "secondary-cell" }, detail) : null,
      dl.lastError ? h("div", { class: "small wh-error" }, dl.lastError) : null);
  };
  const draw = () => host.replaceChildren(rows.length ? h("ul", { class: "wh-dl-list", "aria-label": "Deliveries" }, rows.map(item))
    : emptyState("No deliveries yet", "Events this webhook subscribes to appear here as they happen. Send a test to try it.", null, "send"));
  const load = async () => {
    const r = await api("GET", `/webhooks/${hook.id}/deliveries?limit=50` + (before ? "&before=" + before : ""));
    rows = rows.concat(r.deliveries);
    if (r.deliveries.length) before = r.deliveries[r.deliveries.length - 1].id;
    moreWrap.hidden = r.deliveries.length < 50;
    draw();
  };
  more.addEventListener("click", () => withBusy(more, load));
  const d = drawer({ title: "Deliveries", sub: hook.name + " - " + hook.url, body: h("div", { class: "stack" }, host, moreWrap),
    footer: [h("button", { class: "btn", type: "button", onclick: () => d.close() }, "Close")] });
  load().catch((e) => host.replaceChildren(errorNotice(e)));
}

async function sendTest(hook) {
  try { await api("POST", `/webhooks/${hook.id}/test`); toast("Test event queued. It is sent within a few seconds."); }
  catch (e) { toast(e.message, "bad"); }
}

async function rotate(hook) {
  if (!(await confirmDialog({ title: "Rotate the signing secret for " + hook.name + "?", danger: true, confirmLabel: "Rotate secret",
    body: "Requests are signed with the new secret right away. Update the receiver at the same time, or it rejects them." }))) return;
  try {
    const r = await api("POST", `/webhooks/${hook.id}/rotate-secret`);
    const d = drawer({ title: "New signing secret", sub: hook.name, body: secretNotice(r.secret),
      footer: [h("button", { class: "btn primary", type: "button", onclick: () => d.close() }, "Done")] });
  } catch (e) { toast(e.message, "bad"); }
}

async function remove(hook) {
  if (!(await confirmDialog({ title: "Delete " + hook.name + "?", body: "Its delivery history is deleted too. Events stop being sent.", confirmLabel: "Delete webhook", danger: true }))) return;
  try { await api("DELETE", "/webhooks/" + hook.id); toast("Webhook deleted"); reload(); } catch (e) { toast(e.message, "bad"); }
}

export async function webhooksTab() {
  const [res, ev] = await Promise.all([api("GET", "/webhooks"), api("GET", "/webhooks/events")]);
  const addButton = () => h("button", { class: "btn primary", type: "button", onclick: () => editDrawer(ev.events) }, icon("plus"), "Add webhook");
  const actions = (hook) => {
    const t = h("button", { type: "button", class: "iconbtn", "aria-label": "Actions for " + hook.name }, icon("more"));
    menu(t, [
      { label: "Edit", icon: "edit", onClick: () => editDrawer(ev.events, hook) },
      { label: "View deliveries", icon: "inbox", onClick: () => deliveriesDrawer(hook) },
      { label: "Send test event", icon: "send", disabled: !hook.enabled, onClick: () => sendTest(hook) },
      { separator: true },
      { label: "Rotate signing secret", icon: "refresh", onClick: () => rotate(hook) },
      { label: "Delete", icon: "trash", danger: true, onClick: () => remove(hook) },
    ]);
    return t;
  };
  return h("section", { class: "panel" },
    h("div", { class: "panel-head" },
      h("div", {}, h("h2", {}, "Webhooks"),
        h("p", {}, "Notify your identity provider, VPN or SIEM when a device's compliance, enrollment or ownership changes, for example to block non-compliant devices. ",
          h("a", { class: "link", href: DOCS, target: "_blank", rel: "noopener" }, "How to verify requests"), ".")),
      res.webhooks.length ? h("div", { class: "row" }, addButton()) : null),
    table([
      { label: "Webhook", render: (w) => h("div", {}, h("div", { class: "primary-cell" }, w.name), h("div", { class: "secondary-cell wh-url" }, w.url)) },
      { label: "Events", cls: "hide-sm", render: (w) => h("span", { class: "row tight wh-badges" }, w.events.map((e) => badge(eventLabel(e)))) },
      { label: "State", render: (w) => w.enabled ? badge("On", "ok") : badge("Off") },
      { label: "Last delivery", render: (w) => h("div", {}, deliveryBadge(w.lastDelivery),
        w.lastDelivery ? h("div", { class: "secondary-cell", title: fmtTime(w.lastDelivery.createdAt) }, eventLabel(w.lastDelivery.event) + ", " + ago(w.lastDelivery.createdAt)) : null) },
      { label: h("span", { class: "sr-only" }, "Actions"), cls: "col-actions", render: actions },
    ], res.webhooks, { label: "Webhooks", onRow: (w) => deliveriesDrawer(w),
      empty: emptyState("No webhooks yet", "Add one to send device events to another system.", addButton(), "send") }));
}
