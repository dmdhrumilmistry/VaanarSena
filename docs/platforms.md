# Platform setup

Each platform is independent: configure only the ones you need. The console's
dashboard shows which are enabled.

## Apple (iOS, iPadOS, macOS)

Apple devices are woken through APNs, which needs an **MDM push certificate**.

1. Generate a key and CSR: `openssl req -new -newkey rsa:2048 -nodes -keyout apns.key -out apns.csr -subj "/CN=VaanarSena"`.
2. Have the CSR signed for MDM by a vendor certificate (Apple Developer
   Enterprise Program, "MDM CSR" capability), then upload the signed request
   at <https://identity.apple.com>. Download the push certificate and convert
   it: `openssl x509 -inform DER -in MDM_*.cer -out apns.crt`.
3. Set `VS_APNS_CERT_FILE` and `VS_APNS_KEY_FILE`.

The certificate lasts one year. **Renew it with the same Apple ID**: a new
certificate with a different topic orphans every enrolled device. The server
logs a warning 30 days before expiry.

**Corporate enrollment**: create an `apple` / `corporate` token, open the
profile URL in Safari on the device and install the profile.

**BYOD (User Enrollment)**: create an `apple` / `personal` token with the
user's **Managed Apple ID** (from Apple Business Manager) as assignee. Apple
then creates a separate managed APFS volume; the organisation cannot see
personal data, read hardware identifiers or erase the device, and the server
refuses those commands anyway.

Not yet supported: Automated Device Enrollment (ABM/DEP), Apps and Books (VPP)
licensing, Declarative Device Management. `install_app` works with an
enterprise app manifest URL.

## Windows 10 / 11

Works with no extra configuration. Windows uses the OnPremise enrollment flow:
the user's email plus the enrollment token as password.

1. Create a `windows` token with the user's email as assignee.
2. On the PC: **Settings > Accounts > Access work or school > Enroll only in
   device management**, or open the `ms-device-enrollment:` link from the
   console.
3. Enter the email and server address shown, then the token when asked for a
   password.

For auto-discovery by email domain, publish
`enterpriseenrollment.<your-domain>` as a CNAME to the server.

Windows checks in every 15 minutes (on the DMClient schedule set at
enrollment) and at sign-in. Push wake-ups need WNS and are not used.

The management endpoint authenticates the device by TLS client certificate.
Behind an ingress, the ingress must request the client certificate and pass
it on; see [deployment.md](deployment.md#client-certificates).

Personal devices receive only `DeviceLock` (sign-in) policies. BitLocker,
camera, USB and update policies are corporate only.

## Android

Uses the **Android Management API** (AMAPI).

1. In a Google Cloud project, enable the Android Management API and create a
   service account with the *Android Management User* role. Download a JSON key.
2. Create an enterprise (one-time signup flow, see
   <https://developers.google.com/android/management/quickstart>) and note its
   name, `enterprises/LC0...`.
3. Set `VS_GOOGLE_CREDENTIALS_FILE`, `VS_GOOGLE_PROJECT_ID` and `VS_ANDROID_ENTERPRISE`.

**Corporate (fully managed)**: create an `android` / `corporate` token. On a
factory-reset device, tap the welcome screen six times and scan the QR code.

**BYOD (work profile)**: create an `android` / `personal` token and open the
enrollment link on the phone. Android isolates the work profile; `retire`
deletes it and leaves personal data alone.

Devices are synced every 5 minutes. Apps are managed declaratively through
the policy `apps` list (`platform: android`).

## ChromeOS

ChromeOS devices are enrolled into your Google Workspace or Chrome Enterprise
domain on the device itself. VaanarSena imports them and drives commands.

1. Use the same service account, enable the Admin SDK API, and grant it
   domain-wide delegation for
   `https://www.googleapis.com/auth/admin.directory.device.chromeos`.
2. Set `VS_GOOGLE_CREDENTIALS_FILE` and `VS_GOOGLE_ADMIN_SUBJECT` (a Workspace admin).

Supported commands: refresh, restart, lock (disable), wipe (remote powerwash)
and retire (deprovision). Device policy stays in the Google Admin console.

## Linux

The agent supports systemd-based distributions with apt, dnf, zypper or pacman.

```bash
sudo vaanarsena-agent enroll --server https://mdm.example.com --token <TOKEN>
# With a private TLS CA:  --server-ca /path/to/ca.pem
sudo systemctl enable --now vaanarsena-agent
```

The agent polls every 60 seconds over mutual TLS. Corporate machines support
lock, restart, shutdown, OS update, package install/remove and `run_script`
(admin only; the full script is written to the audit log). It enforces USB
storage blocking and reports compliance for disk encryption (LUKS).

Personal machines run in **inventory-only** mode: the agent sends basic facts
(no serial numbers or hardware identifiers), evaluates compliance, enforces
nothing, and refuses every command except refresh and retire, even if the
server were to send one.
