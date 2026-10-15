# REST API

Base path `/api/v1`. Authenticate with `Authorization: Bearer <token>`, using
an API token (`vsat_...`) or the `token` returned by login. Bodies are JSON;
unknown fields are rejected.

Minimum role in brackets.

## Auth

| Method | Path | |
|---|---|---|
| POST | `/auth/login` | `{email, password}` -> `{user, token}` |
| POST | `/auth/logout` | clears the session cookie |
| GET | `/auth/providers` | unauthenticated: `{password: true, oidc: {enabled, name}}` for the sign-in page |
| GET | `/auth/oidc/start?next=` | unauthenticated: redirects to the identity provider; `next` is a path on this site. See [sso.md](sso.md) |
| GET | `/auth/oidc/callback` | unauthenticated: the provider's redirect target; sets the session cookie and redirects to `next`, or to `/login?sso_error=<code>` |
| GET | `/auth/me` | [auditor] current user; `sso` and `hasPassword` tell how they sign in |
| POST | `/auth/password` | [auditor] `{current, new}` |
| GET, POST | `/api-tokens` | [auditor] list or create (`{name, expiresInDays}`); the token is returned once |
| DELETE | `/api-tokens/{id}` | [auditor] |

## Devices and commands

| Method | Path | |
|---|---|---|
| GET | `/devices?platform=&ownership=&status=&group=&tag=&compliant=&q=&limit=&offset=` | [auditor] `compliant` is `true`, `false` or `unknown` (never evaluated) |
| GET | `/devices/{id}` | [auditor] device, groups, effective policy |
| PATCH | `/devices/{id}` | [operator] `{name, assignee}`; `ownership` needs admin. Changing ownership or assignee re-evaluates smart groups and re-pushes policy. Making a device personal removes its serial and inventory and cancels pending commands not allowed on personal devices |
| DELETE | `/devices/{id}` | [admin] deletes the record only; retire the device first |
| GET | `/devices/{id}/commands?limit=` | [auditor] newest first, up to 500 |
| POST | `/devices/{id}/commands` | [operator] `{type, params}`; returns 202 with the queued command, 403 if not permitted, 409 if the device's ownership changed meanwhile |
| POST | `/commands/{id}/cancel` | [operator] |
| GET | `/commands/catalogue` | [auditor] command types, roles, platforms, BYOD eligibility |
| GET | `/devices/{id}/inventory?kind=app\|service\|profile&q=&limit=&offset=` | [auditor] installed apps, services or profiles, with `counts`, `supported` and a BYOD `note` |
| GET | `/inventory/software?kind=&q=&limit=&offset=` | [auditor] software across the fleet with device counts and versions |
| GET | `/inventory/software/devices?kind=&identifier=&name=&version=` | [auditor] devices that have one piece of software |

Command `params`: `message`, `phone`, `pin` (macOS, 6 digits), `appId`, `url`,
`hash` and `version` (Windows MSI), `script` (Linux), `preserveDataPlan`.
`message` and `phone` are shown on Apple devices only.

Parameters are checked against the target platform when the command is sent,
and a command that cannot be delivered is refused with 403:

| Command | Requires |
|---|---|
| `install_app`, `remove_app` (any platform) | `appId` or `url` |
| `install_app` on Windows | `appId` (MSI product code), `url`, `hash` and `version` |
| `remove_app` on Windows | `appId` (MSI product code) |
| `remove_app` on Apple | `appId` (bundle ID) |
| `install_app`, `remove_app` on Linux | `appId`, a package name |
| `run_script` | `script` |

```bash
curl -X POST https://mdm.example.com/api/v1/devices/$ID/commands \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"type":"lock","params":{"message":"Please return to IT"}}'
```

## Enrollment

| Method | Path | |
|---|---|---|
| GET | `/enrollment-tokens` | [operator] |
| POST | `/enrollment-tokens` | [operator] `{platform, ownership, assignee, groupId, maxUses, expiresInHours}` -> `{token, meta, instructions}` |
| DELETE | `/enrollment-tokens/{id}` | [operator] revoke |
| GET | `/enrollment-tokens/{id}/qr.png` | [operator] Android provisioning QR |

## Policies, groups and blueprints

| Method | Path | |
|---|---|---|
| GET | `/policies` | [auditor] Wi-Fi passwords masked |
| POST | `/policies` | [admin] `{name, description, priority, document, groupIds, deviceIds}` |
| GET, PUT, DELETE | `/policies/{id}` | [auditor] / [admin] |
| GET | `/groups?kind=static\|smart` | [auditor] |
| POST | `/groups` | [admin] `{name, description, kind, rules}` |
| GET, PUT, DELETE | `/groups/{id}` | [auditor] / [admin]; GET includes member IDs |
| POST | `/groups/{id}/devices` | [operator] `{deviceId}`; static groups only (409 for smart) |
| DELETE | `/groups/{id}/devices/{deviceId}` | [operator] static groups only |
| POST | `/groups/preview` | [auditor] body: a rule set; returns matching devices without saving |
| GET | `/groups/schema` | [auditor] rule fields and operators |
| PUT | `/devices/{id}/tags` | [operator] `{tags: [...]}`; re-evaluates smart groups immediately |
| GET | `/blueprints` | [auditor] |
| POST | `/blueprints` | [admin] `{name, description, priority, groupIds, spec}` |
| GET, PUT, DELETE | `/blueprints/{id}` | [auditor] / [admin] |

Saving a policy or blueprint, or a membership change, queues `apply_policy` for
every affected device, and devices newly in scope of a blueprint run its
onboarding steps. Formats are in [manifests.md](manifests.md).

## Declarative apply

| Method | Path | |
|---|---|---|
| POST | `/apply?dryRun=&prune=&owner=` | [admin] YAML or JSON manifest; returns `{dryRun, changes: [{kind, name, action}]}`. 400 with `problems` if invalid (nothing written) |
| GET | `/export?format=yaml\|json` | [admin] every group, policy and blueprint as manifests, secrets included |

`vsctl` wraps these; see [manifests.md](manifests.md).

## Webhooks

All [admin]. See [webhooks.md](webhooks.md) for events, the request format and
signature verification.

| Method | Path | |
|---|---|---|
| GET | `/webhooks` | every webhook with its `lastDelivery` |
| GET | `/webhooks/events` | subscribable events with descriptions |
| POST | `/webhooks` | `{name, url, events, enabled}` -> `{webhook, secret}`; the signing secret is returned only here. 400 with `problems` if invalid |
| GET, PUT, DELETE | `/webhooks/{id}` | PUT takes the same body as POST and keeps the secret |
| POST | `/webhooks/{id}/rotate-secret` | -> `{secret}`; requests are signed with it from now on |
| POST | `/webhooks/{id}/test` | queues a `webhook.test` event for this webhook only; 202 with the delivery |
| GET | `/webhooks/{id}/deliveries?before=&limit=` | newest first, with status, attempts, response code, last error and the event payload |
| POST | `/webhooks/{id}/deliveries/{deliveryId}/retry` | send a pending or failed delivery again now; 409 if it was delivered or is being sent |

## Platforms

All [admin], except the Android sign-up callback. Credentials set here are
stored encrypted; those set by environment variables cannot be changed here.

| Method | Path | |
|---|---|---|
| GET | `/platforms` | state of Apple, Google, Android, ChromeOS, Windows and Linux, with the public URL |
| POST | `/platforms/apple/csr` | -> `{csr}`: a push certificate signing request; its key stays on the server |
| PUT, DELETE | `/platforms/apple` | PUT `{certificate, der, privateKey, topic}`: the push certificate from Apple (PEM, or base64 DER with `der: true`); `privateKey` is optional when the CSR came from the server, `topic` when it can be read from the certificate |
| PUT, DELETE | `/platforms/google` | PUT `{credentials, projectId}`: a service account key (JSON) |
| POST | `/platforms/android/signup` | -> `{signupUrl}`: starts Android Enterprise sign-up |
| GET | `/platforms/android/callback` | unauthenticated: Google's redirect after sign-up |
| PUT, DELETE | `/platforms/android` | PUT `{enterprise}`: use an existing enterprise |
| PUT, DELETE | `/platforms/chromeos` | PUT `{adminSubject, customerId}` |

## Administration

| Method | Path | |
|---|---|---|
| GET, POST | `/users` | [admin] |
| PATCH, DELETE | `/users/{id}` | [admin] `{name, role, disabled, password}` |
| GET | `/audit?before=&limit=` | [auditor] |
| GET | `/stats` | [auditor] includes `software: {apps, devicesReporting}` |
| GET | `/info` | [auditor] version, enabled platforms, CA fingerprint |

Unauthenticated: `GET /healthz`, `GET /readyz`, `GET /mdm/ca.pem`.
