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
| GET | `/devices?platform=&ownership=&status=&group=&q=&limit=&offset=` | [auditor] |
| GET | `/devices/{id}` | [auditor] device, groups, effective policy |
| PATCH | `/devices/{id}` | [operator] `{name, assignee}`; `ownership` needs admin |
| DELETE | `/devices/{id}` | [admin] deletes the record only; retire the device first |
| GET | `/devices/{id}/commands` | [auditor] |
| POST | `/devices/{id}/commands` | [operator] `{type, params}`; returns 202 with the queued command |
| POST | `/commands/{id}/cancel` | [operator] |
| GET | `/commands/catalogue` | [auditor] command types, roles, platforms, BYOD eligibility |

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

## Policies and groups

| Method | Path | |
|---|---|---|
| GET | `/policies` | [auditor] Wi-Fi passwords masked |
| POST | `/policies` | [admin] `{name, description, priority, document, groupIds, deviceIds}` |
| GET, PUT, DELETE | `/policies/{id}` | [auditor] / [admin] |
| GET | `/groups` | [auditor] |
| POST | `/groups` | [admin] `{name, description}` |
| DELETE | `/groups/{id}` | [admin] |
| POST | `/groups/{id}/devices` | [operator] `{deviceId}` |
| DELETE | `/groups/{id}/devices/{deviceId}` | [operator] |

Saving a policy, or changing group membership, queues `apply_policy` for every
affected device. See [ARCHITECTURE.md](ARCHITECTURE.md#unified-policy-model)
for the document format.

## Administration

| Method | Path | |
|---|---|---|
| GET, POST | `/users` | [admin] |
| PATCH, DELETE | `/users/{id}` | [admin] `{name, role, disabled, password}` |
| GET | `/audit?before=&limit=` | [auditor] |
| GET | `/stats` | [auditor] |
| GET | `/info` | [auditor] version, enabled platforms, CA fingerprint |

Unauthenticated: `GET /healthz`, `GET /readyz`, `GET /mdm/ca.pem`.
