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
| GET | `/auth/me` | [auditor] current user |
| POST | `/auth/password` | [auditor] `{current, new}` |
| GET, POST | `/api-tokens` | [auditor] list or create (`{name, expiresInDays}`); the token is returned once |
| DELETE | `/api-tokens/{id}` | [auditor] |

## Devices and commands

| Method | Path | |
|---|---|---|
| GET | `/devices?platform=&ownership=&status=&group=&tag=&q=&limit=&offset=` | [auditor] |
| GET | `/devices/{id}` | [auditor] device, groups, effective policy |
| PATCH | `/devices/{id}` | [operator] `{name, assignee}`; `ownership` needs admin |
| DELETE | `/devices/{id}` | [admin] deletes the record only; retire the device first |
| GET | `/devices/{id}/commands` | [auditor] |
| POST | `/devices/{id}/commands` | [operator] `{type, params}`; returns 202 with the queued command |
| POST | `/commands/{id}/cancel` | [operator] |
| GET | `/commands/catalogue` | [auditor] command types, roles, platforms, BYOD eligibility |
| GET | `/devices/{id}/inventory?kind=app\|service\|profile&q=&limit=&offset=` | [auditor] installed apps, services or profiles, with `counts`, `supported` and a BYOD `note` |
| GET | `/inventory/software?kind=&q=&limit=&offset=` | [auditor] software across the fleet with device counts and versions |
| GET | `/inventory/software/devices?kind=&identifier=&name=&version=` | [auditor] devices that have one piece of software |

Command `params`: `message`, `phone`, `pin` (macOS, 6 digits), `appId`, `url`,
`hash` and `version` (Windows MSI), `script` (Linux), `preserveDataPlan`.

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

## Administration

| Method | Path | |
|---|---|---|
| GET, POST | `/users` | [admin] |
| PATCH, DELETE | `/users/{id}` | [admin] `{name, role, disabled, password}` |
| GET | `/audit?before=&limit=` | [auditor] |
| GET | `/stats` | [auditor] includes `software: {apps, devicesReporting}` |
| GET | `/info` | [auditor] version, enabled platforms, CA fingerprint |

Unauthenticated: `GET /healthz`, `GET /readyz`, `GET /mdm/ca.pem`.
