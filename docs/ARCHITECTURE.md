# VaanarSena architecture

VaanarSena is a self-hosted, open source mobile device management (MDM) server
for enterprises that want full control over their fleet without a SaaS vendor
in the middle. It manages corporate-owned and personally owned (BYOD) devices
across Apple, Windows, Android, ChromeOS and Linux from one control plane.

## Goals

- **Complete control.** One binary, one PostgreSQL database, your own PKI. No
  phone-home, no telemetry, no licence server.
- **Native protocols.** Use each platform's own management channel instead of
  a lowest-common-denominator agent wherever the platform offers one.
- **BYOD with privacy by construction.** Ownership is a first-class property of
  a device. Personally owned devices get a restricted command set, enforced on
  the server, so an operator cannot wipe a personal phone by mistake.
- **One policy model.** Admins write a platform-neutral policy once; each
  platform driver translates it to its native format.
- **Auditable.** Every state-changing admin action is written to an
  append-only audit log.

## Platform coverage

| Platform | Channel | Corporate | BYOD | Spec |
|---|---|---|---|---|
| iOS / iPadOS | Apple MDM (check-in + command protocol, APNs push) | Device enrollment | User Enrollment (managed Apple ID, separate APFS volume) | Apple Device Management protocol |
| macOS | Apple MDM | Device enrollment | User Enrollment | Apple Device Management protocol |
| Windows 10/11 | MS-MDE2 enrollment + OMA-DM 1.2 (SyncML) | Full MDM | "Work or school account" MDM-only enrollment | MS-MDE2, MS-MDM, OMA-DM 1.2 |
| Android | Android Management API (Google) | Fully managed device | Work profile | developers.google.com/android/management |
| ChromeOS | Google Admin SDK Directory API (chromeosdevices) | Managed device | n/a (ChromeOS has no BYOD MDM model) | developers.google.com/admin-sdk/directory |
| Linux | VaanarSena agent over HTTPS (mTLS) | Full | Inventory-only mode | this repository |

Android and ChromeOS are managed through Google's APIs because that is the only
supported management path for them: Google retired the Device Admin API, and
ChromeOS devices are enrolled into a Google Workspace / Chrome Enterprise
domain. VaanarSena is the system of record and drives those APIs; it does not
need Google to store your policy history or audit trail.

## Components

```
            +--------------------- vaanarsena (single Go binary) ---------------------+
 admins --> | REST API (/api/v1)  + embedded web console (/)                          |
            |   auth: bcrypt users, JWT sessions, API tokens, RBAC                     |
            |   audit log, unified policy engine, command queue                        |
            |-------------------------------------------------------------------------|
 Apple ---> | /mdm/apple/enroll   /mdm/apple/checkin   /mdm/apple/server   --> APNs    |
 Windows -> | /EnrollmentServer/Discovery.svc  Policy.svc  Enrollment.svc              |
            | /ManagementServer/MDM.svc (OMA-DM SyncML)                                |
 Linux ---> | /agent/v1/enroll  /agent/v1/checkin (mTLS)                              |
            | Android Management API client  -------------------------> Google        |
            | Admin SDK chromeosdevices client -----------------------> Google        |
            +-----------------------------------+-------------------------------------+
                                                |
                                           PostgreSQL
```

### Packages

| Package | Responsibility |
|---|---|
| `cmd/vaanarsena` | Server entry point: `serve`, `migrate`, `create-admin`. |
| `cmd/vaanarsena-agent` | Linux agent: enrolls with a token, reports inventory, runs queued commands. |
| `internal/config` | Environment-driven configuration. |
| `internal/store` | PostgreSQL access and embedded SQL migrations. |
| `internal/pki` | Server CA: generated on first boot, persisted encrypted in the database, signs device identity certificates. |
| `internal/auth` | Users, password hashing, JWT sessions, API tokens, RBAC middleware. |
| `internal/policy` | Platform-neutral policy model and per-platform translators. |
| `internal/command` | Platform-neutral command catalogue and the BYOD guard. |
| `internal/apple` | Enrollment profile generation, check-in and command endpoints, APNs pusher. |
| `internal/windows` | MS-MDE2 discovery/policy/enrollment SOAP services and the OMA-DM endpoint. |
| `internal/android` | Android Management API client: enterprises, policies, enrollment tokens, device commands. |
| `internal/chromeos` | Admin SDK client: device sync and commands. |
| `internal/agent` | Server side of the Linux agent protocol. |
| `internal/groups` | Smart group rule engine: conditions over device fields, tags and inventory facts. |
| `internal/blueprint` | Blueprint spec: referenced policies, inline policy, onboarding steps. |
| `internal/manifest` | Declarative resources (Group, Policy, Blueprint): decode, validate, apply, prune, export, and the GitOps directory loader. |
| `cmd/vsctl` | CLI for apply/diff/export and device helpers. |
| `internal/api` | Admin REST API. |
| `internal/web` | Embedded single-page web console. |

## Data model

- `users`: console/API users with a role (`admin`, `operator`, `auditor`).
- `api_tokens`: hashed long-lived tokens for automation.
- `enrollment_tokens`: one-time or multi-use secrets bound to a platform,
  ownership (`corporate` / `personal`), optional assignee and expiry.
- `devices`: one row per enrolled device: platform, ownership, status,
  serial, model, OS version, last seen, and a `facts` JSONB for platform
  inventory. Platform-specific identity lives in `platform_ids` JSONB (Apple
  UDID/push token/magic, Windows DeviceID, AMAPI device name, ChromeOS
  deviceId, agent certificate serial).
- `policies` and `policy_assignments`: neutral policy documents assigned to
  device groups or individual devices.
- `groups` (`kind` static or smart, `rules` for smart) and `group_members`
  (`source` manual or smart). A reconciler re-evaluates smart groups every
  minute, at enrollment and on tag changes, and pushes policy to devices whose
  membership changed.
- `blueprints`, `blueprint_assignments` (to groups) and `blueprint_runs`
  (onboarding ran once per device; the primary key makes the claim atomic
  across replicas).
- `managed_by` on groups, policies and blueprints records which manifest
  source owns them, scoping prune.
- `devices.tags`: free-form labels usable in smart group rules.

**Effective configuration** for a device = merge of policies assigned to it
or its groups, policies referenced by blueprints targeting its groups, and
those blueprints' inline policies, ordered by priority (lower number wins).
- `commands`: queue of platform-neutral commands with status
  (`queued` -> `sent` -> `acknowledged` / `error` / `not_now`), the native
  payload that was actually sent, and the device's response.
- `audit_log`: actor, action, target, details, time. Append only.
- `pki`: the CA certificate and its key, encrypted with `VS_SECRET_KEY`.

## Security model

- **Transport.** All device channels require TLS. The server can terminate TLS
  itself or run behind an ingress. Device identity is proven by a client
  certificate issued by the VaanarSena CA (Windows, Linux, Apple), or by the
  Apple `Mdm-Signature` header (CMS detached signature by the device identity)
  which survives TLS-terminating proxies.
- **Enrollment** requires an enrollment token. Tokens are stored hashed.
- **Admin auth.** bcrypt password hashes, short-lived HS256 JWTs signed with a
  key derived from `VS_SECRET_KEY`, and hashed API tokens. Roles:
  - `auditor`: read only.
  - `operator`: read, send non-destructive commands, manage enrollment tokens.
  - `admin`: everything, including wipe, policy and user management.
- **BYOD guard.** On personally owned devices the server refuses commands
  that reach outside the managed container: device wipe, device lock (Android
  work profile and Apple User Enrollment do not permit it anyway), location,
  and full app inventory. The allowed action is `retire` (remove management and
  managed data only). This is checked in `internal/command`, not the UI.
- **Secrets at rest.** The CA key and third-party credentials (APNs key, Google
  service account) are encrypted with AES-256-GCM using `VS_SECRET_KEY`.

## Unified policy model

```json
{
  "passcode":   {"required": true, "minLength": 8, "complex": true, "maxInactivityMinutes": 5},
  "encryption": {"required": true},
  "restrictions": {"camera": false, "screenCapture": false, "usbStorage": false, "bluetooth": true},
  "wifi":  [{"ssid": "corp", "security": "WPA2", "password": "..."}],
  "osUpdates": {"autoInstall": true, "deferDays": 7},
  "apps": [{"id": "com.example.app", "platform": "android", "install": "required"}],
  "custom": {"scope": "corporate", "apple": [...], "windows": [...], "android": {...}}
}
```

`custom` carries raw platform payloads (see [manifests.md](manifests.md#custom-payloads)),
applied after the neutral translation and only to corporate devices unless
`scope` is `all`.

Translators:

- Apple: `com.apple.mobiledevice.passwordpolicy`, `com.apple.applicationaccess`,
  `com.apple.wifi.managed` payloads in a configuration profile delivered with
  `InstallProfile`.
- Windows: Policy CSP `./Device/Vendor/MSFT/Policy/Config/DeviceLock/...`,
  `Camera/AllowCamera`, BitLocker CSP, delivered as SyncML `Replace`/`Add`.
- Android: AMAPI `Policy` resource (`passwordPolicies`, `cameraAccess`,
  `screenCaptureDisabled`, `applications`, `openNetworkConfiguration`).
- ChromeOS: reported only (device policy is set in the Google Admin console or
  Chrome Policy API; out of scope for v0.1).
- Linux: delivered to the agent as the neutral JSON; the agent enforces what it
  can (screen lock via `gsettings`, LUKS reporting, USB storage via modprobe
  blacklist) and reports compliance.

## Deployment

- `Dockerfile`: multi-stage, static binary on distroless, non-root.
- `deploy/docker-compose.yml`: server + PostgreSQL for evaluation.
- Helm chart: published in
  [dmdhrumilmistry/helm-charts](https://github.com/dmdhrumilmistry/helm-charts)
  as `vaanarsena`.

## Roadmap (beyond v0.1)

- Apple Automated Device Enrollment (DEP/ABM) token sync and Declarative
  Device Management.
- Apple VPP / Apps and Books.
- Windows Autopilot and Azure AD (Entra) federated enrollment.
- SCEP for certificate renewal on Apple and Windows.
- OIDC SSO for the console.
- Compliance-based conditional access webhooks.
