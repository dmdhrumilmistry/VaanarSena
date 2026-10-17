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

Changing a device's ownership re-evaluates its smart groups and re-pushes its
policy at once, so a device made personal stops receiving company-owned-only
configuration (and the Linux agent removes what it had applied). Making a
device personal is one transaction that also removes the serial number and
platform inventory collected while it was corporate, and cancels pending
commands not permitted on personal devices. Every command is queued only if
the device still has the ownership it was authorised against, so a command
authorised a moment before the change cannot slip in after it.

Inventory collected from personal devices excludes serial numbers, IMEI and
hardware addresses, so webhook events about personal devices carry none
either. Software inventory is limited the same way, on the
server: personal Linux and Windows devices report no apps or services (anything
a modified client sends is discarded), Apple devices report managed apps only,
and Android reports only the work profile. Changing a device to personal
deletes the software already stored for it.

## Admin authentication

- Passwords: bcrypt (cost 12), at least 12 characters. Ten failed logins per
  email and IP within 15 minutes trigger a lockout.
- Sessions: HS256 JWTs in an `HttpOnly`, `SameSite=Strict` cookie, re-checked
  against the database on every request, so disabling a user takes effect
  immediately. Cookie-authenticated writes also need `X-Requested-With`.
- API tokens: 256-bit random, stored as SHA-256 hashes, optionally expiring.
- Roles: `auditor` (read only), `operator` (non-destructive commands,
  enrollment), `admin` (wipe, scripts, policy, users, webhooks). The last
  admin cannot be demoted, disabled or deleted, by hand or by an SSO group
  mapping.
- Single sign-on (optional, [sso.md](sso.md)): OpenID Connect authorization
  code flow with PKCE. `state`, `nonce` and the PKCE verifier live in an
  encrypted, `HttpOnly`, ten-minute cookie. The ID token's signature, issuer,
  audience, `azp`, expiry and nonce are verified locally against the
  provider's published keys. Users are bound to the provider's issuer and
  subject after their first sign-in; an unverified email cannot link to an
  existing account. The post-sign-in redirect only goes to paths on this
  site.

## Webhooks

[Webhooks](webhooks.md) are admin-only. Each request is signed with
HMAC-SHA256 over a timestamp and the body, with a per-webhook secret stored
encrypted with `VS_SECRET_KEY` and shown only at creation or rotation.
Redirects are not followed, and URLs may not embed credentials. Webhook URLs
are called from the server and may point at internal addresses; restrict
outbound traffic at the network if that matters in your environment.

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
- With SSO, map provider groups to roles so access follows your directory,
  and keep one admin with a password for emergencies.
- With SSO, turn off password sign-in (`VS_PASSWORD_LOGIN=false`) and keep
  one break-glass admin with a strong password in `VS_PASSWORD_LOGIN_ALLOW`.
- Monitor the audit log for `command.denied`, `auth.login_failed` and `*.enroll_denied`.

## Reporting vulnerabilities

See [SECURITY.md](https://github.com/dmdhrumilmistry/VaanarSena/blob/main/SECURITY.md).
