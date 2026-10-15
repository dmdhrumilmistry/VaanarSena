# Webhooks

Webhooks send signed HTTP requests to your systems when something happens to
a device. The main use is **conditional access**: tell your identity provider,
VPN or proxy that a device stopped being compliant, so it can refuse that
device until it is fixed. They also suit SIEM pipelines, ticketing and chat
notifications.

Admins manage them under **Settings > Webhooks** or through the
[API](api.md#webhooks).

## Events

| Event | When |
|---|---|
| `device.compliance_changed` | A device's compliance changed: compliant, not compliant, or unknown |
| `device.enrolled` | A device finished enrollment, re-enrolled, or (ChromeOS) finished provisioning |
| `device.retired` | A device was retired: management and corporate data removed |
| `device.wiped` | A device was erased |
| `device.ownership_changed` | A device was reclassified between company-owned and personal |
| `webhook.test` | You pressed "Send test event"; goes to that webhook only |

Events are raised by the database whenever a device row changes, whichever
platform driver or API call changed it, so none are missed. A change in the
list of compliance issues while the device stays non-compliant does not raise
a new event.

## The request

```http
POST /hooks/vaanarsena HTTP/1.1
Content-Type: application/json
User-Agent: VaanarSena-Webhook/1
X-VaanarSena-Event: device.compliance_changed
X-VaanarSena-Event-Id: 4182
X-VaanarSena-Delivery: 9310
X-VaanarSena-Signature: t=1760545200,v1=5f0c...e91a

{
  "id": "4182",
  "type": "device.compliance_changed",
  "createdAt": "2026-10-15T16:20:00Z",
  "data": {
    "device": {
      "id": "9d704c5d-e5c5-4e22-aaa2-bf450932c3a0",
      "platform": "linux",
      "ownership": "corporate",
      "status": "enrolled",
      "name": "eng-ws-12",
      "serial": "PF3XK2",
      "model": "Lenovo ThinkPad T14",
      "osVersion": "Ubuntu 24.04.1 LTS",
      "assignee": "priya@example.com",
      "compliant": false,
      "complianceIssues": ["disk encryption required but no LUKS volume found"],
      "lastSeenAt": "2026-10-15T16:19:58Z"
    },
    "previous": {"status": "enrolled", "ownership": "corporate", "compliant": true}
  }
}
```

- `data.device` is a snapshot from the moment of the event; a retry sends the
  same snapshot.
- `previous` holds the old status, ownership and compliance (absent for a
  device seen for the first time).
- `serial` is empty for personal devices: VaanarSena does not collect hardware
  identifiers from them, so none can be sent.
- `assignee` is the user the device belongs to, the usual key for conditional
  access.

Answer with any 2xx status within 15 seconds. Redirects are not followed.

## Verify the signature

Each webhook has a signing secret (`whsec_...`), shown once when the webhook is
created and again only when you rotate it. The signature header is

```
X-VaanarSena-Signature: t=<unix seconds>,v1=<hex HMAC-SHA256(secret, "<t>.<raw body>")>
```

To verify: take `t` and `v1`, compute the HMAC over the timestamp, a dot and the
raw request body (before any JSON parsing), compare in constant time, and
reject timestamps more than five minutes from your clock.

```python
import hashlib, hmac, time

def verify(secret: str, header: str, body: bytes, tolerance=300) -> bool:
    parts = dict(p.split("=", 1) for p in header.split(","))
    t, sig = parts.get("t", ""), parts.get("v1", "")
    if not t.isdigit() or abs(time.time() - int(t)) > tolerance:
        return False
    mac = hmac.new(secret.encode(), f"{t}.".encode() + body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(mac, sig)
```

Go receivers can use `webhook.Verify` from
`github.com/dmdhrumilmistry/VaanarSena/internal/webhook` as a reference.

## Delivery and retries

- Deliveries are queued in the database and sent within about five seconds.
  Several server replicas share the work without sending anything twice at
  the same time.
- A failure (network error, timeout or non-2xx answer) is retried after 30
  seconds, then with the wait doubling up to six hours, for 12 attempts in
  all (about a day and a half). After that the delivery is marked failed.
- Retries keep the same `X-VaanarSena-Event-Id`: drop duplicates by it.
- **Events for one device arrive in order.** While an older event for a
  device is failing and backing off, newer events for that device wait behind
  it, so a receiver never applies a stale compliance state last. Event IDs
  increase, so a receiver can also ignore any event older than the last one
  it applied for that device. Events for different devices are independent.
- **View deliveries** shows each attempt's result; **Retry now** sends a
  pending or failed delivery again immediately (not one already delivered, or
  being sent at that moment).
- Disabling a webhook stops new events from being recorded for it and marks
  its pending deliveries failed, so enabling it again does not replay a stale
  backlog. They can still be retried by hand.
- Events with nothing left to send are deleted after 30 days.

## Security notes

- Only admins can see or change webhooks. Creating, editing, rotating,
  testing, retrying and deleting are audited.
- Signing secrets are stored encrypted with `VS_SECRET_KEY`. If that key
  changes, rotate every webhook's secret.
- The URL may not contain a user name or password; authenticate requests with
  the signature instead.
- Webhook URLs are admin-controlled and are called from the server, so they
  can reach internal addresses. That is deliberate (an internal SIEM is a
  common target); restrict outbound traffic at the network if you need to.
