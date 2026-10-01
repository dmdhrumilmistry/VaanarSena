# Security model

## What VaanarSena protects

- **The fleet from unauthorised control.** Only authenticated admins with the
  right role can queue commands, and every command is audited.
- **Personal devices from overreach.** BYOD owners can rely on the server
  refusing anything outside the corporate container.
- **Secrets at rest.** The CA key, APNs unlock tokens and other device secrets
  are encrypted with AES-256-GCM under a key derived from `VS_SECRET_KEY`.

## Device identity

| Platform | Proof of identity |
|---|---|
| Apple | Detached CMS signature (`Mdm-Signature`) by an identity certificate the server issued at enrollment, checked against the server CA, and bound to the device's UDID or EnrollmentID |
| Windows | TLS client certificate signed from the device's CSR at enrollment, bound to its DeviceID |
| Linux | TLS client certificate signed from the agent's CSR, bound to its machine ID |
| Android, ChromeOS | Google's APIs; the server authenticates to Google with a service account |

Subjects of issued certificates are set by the server, never copied from the
CSR.

## Ownership and the BYOD guard

Ownership comes from the **enrollment token** an admin created, never from
the device. Changing it later requires the `admin` role and is audited.

On personal devices the server refuses `wipe`, `lock`, `restart`, `shutdown`,
`clear_passcode`, lost mode, `locate`, `os_update` and `run_script`. It allows
`refresh`, `apply_policy`, managed app install/remove and `retire`. The check
is in `internal/command` and runs on every enqueue, so a client bug cannot get
around it. The platforms enforce the same boundary on their side:

- Apple User Enrollment cannot erase the device or read hardware identifiers.
- Android work profiles scope commands and policy to the profile; deleting a
  personal device removes only the work profile.
- Windows personal devices receive only sign-in (`DeviceLock`) policies.
- The Linux agent runs in inventory-only mode and refuses commands locally too.

Inventory collected from personal devices excludes serial numbers, IMEI and
hardware addresses.

## Admin authentication

- Passwords: bcrypt (cost 12), at least 12 characters. Ten failed logins per
  email and IP within 15 minutes trigger a lockout.
- Sessions: HS256 JWTs in an `HttpOnly`, `SameSite=Strict` cookie, re-checked
  against the database on every request, so disabling a user takes effect
  immediately. Cookie-authenticated writes also need `X-Requested-With`.
- API tokens: 256-bit random, stored as SHA-256 hashes, optionally expiring.
- Roles: `auditor` (read only), `operator` (non-destructive commands,
  enrollment), `admin` (wipe, scripts, policy, users). The last admin cannot
  be demoted, disabled or deleted.

## Audit log

Every mutation writes to `audit_log` before it takes effect; if the write
fails, the request fails. A database trigger rejects `UPDATE` and `DELETE` on
the table. `run_script` records the full script body.

## Hardening checklist

- Serve only HTTPS (`VS_PUBLIC_URL=https://...`); cookies are then `Secure`.
- Set `VS_TRUST_PROXY=true` only behind a proxy that overwrites
  `X-Forwarded-For`, and `VS_CLIENT_CERT_HEADER` only behind one that
  overwrites the certificate header.
- Store `VS_SECRET_KEY`, the APNs key and the Google service account in a
  secret manager.
- Use API tokens with an expiry for automation.
- Monitor the audit log for `command.denied`, `auth.login_failed` and `*.enroll_denied`.

## Reporting vulnerabilities

See [SECURITY.md](../SECURITY.md).
