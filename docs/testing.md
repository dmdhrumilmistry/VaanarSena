# Testing VaanarSena

Three layers, from seconds to a real fleet.

## 1. Automated tests

```bash
make test        # go vet + unit tests (no dependencies)
make e2e         # end-to-end suite; starts a throwaway PostgreSQL in Docker
```

**Unit tests** cover the parts where a bug is a security or correctness
problem: the BYOD guard and role checks, policy merging and per-platform
translation, custom payloads (including `.mobileconfig` unpacking and the
corporate-only default), the smart group rule engine and version comparison,
manifest decoding and validation (including the shipped examples), the CA and
client certificate handling, Apple `Mdm-Signature` verification and
enrollment profiles, and Windows SOAP/SyncML parsing.

**The end-to-end suite** (`internal/e2e`) runs the real HTTPS server with
mutual TLS against PostgreSQL and drives it like an admin, a GitOps pipeline
and Linux agents would. It checks that:

- a dry-run apply writes nothing, and an invalid manifest is rejected whole;
- applying creates groups, policies and blueprints, and re-applying is a no-op;
- agents enrolled with corporate and personal tokens land in a smart group
  from their reported inventory, and leave it when the inventory changes;
- the effective policy merges blueprint layers in priority order;
- blueprint onboarding runs once on the corporate device and is refused (and
  audited) on the personal one;
- smart group membership cannot be edited by hand, tags update smart groups
  immediately, and rule previews are correct;
- prune removes only resources of the applying owner; export round-trips;
- operators cannot apply or export.

To run it against your own database (it is wiped first):

```bash
VS_TEST_DATABASE_URL=postgres://vs:vs@localhost:5432/vs_test?sslmode=disable \
  go test -count=1 -v ./internal/e2e/
```

CI runs both layers on every push and pull request.

## 2. A local stack with real HTTPS

The Caddy stack works on a laptop: with `VS_DOMAIN=localhost` Caddy issues a
certificate from its own local CA, and everything else behaves like
production, including client certificates through the proxy.

```bash
cd deploy/caddy
cat > .env <<'EOF'
VS_DOMAIN=localhost
ACME_EMAIL=dev@example.com
HTTP_PORT=18080
HTTPS_PORT=18443
VS_PUBLIC_URL=https://localhost:18443
POSTGRES_PASSWORD=dev-postgres
VS_SECRET_KEY=dev-0123456789abcdef0123456789abcdef
VS_BOOTSTRAP_ADMIN_EMAIL=admin@example.com
VS_BOOTSTRAP_ADMIN_PASSWORD=correct-horse-battery
EOF
cp ../../examples/manifests/*.yaml manifests/
docker compose up -d
docker compose cp caddy:/data/caddy/pki/authorities/local/root.crt ./caddy-root.crt
```

Then, from the same machine:

```bash
# Console
open https://localhost:18443          # trust caddy-root.crt, or accept the warning

# Declarative config
export VS_SERVER=https://localhost:18443 VS_CA_FILE=$PWD/caddy-root.crt
export VS_TOKEN=...                    # Account > API tokens
vsctl get groups                       # the example groups, owner gitops
vsctl diff -f manifests/ --owner gitops

# A Linux agent on this machine. Create the token in the console
# (Enroll > linux) or with the API: POST /api/v1/enrollment-tokens
sudo vaanarsena-agent enroll --server https://localhost:18443 \
  --token <TOKEN> --server-ca caddy-root.crt
sudo vaanarsena-agent run
```

Within a minute the device appears, reports inventory and compliance, joins
the matching smart groups (for example `at-risk` if its disk is not
encrypted) and runs the blueprint onboarding steps.

Tear down with `docker compose down -v`.

## 3. Real devices

Real devices need the stack on a public DNS name with a publicly trusted
certificate (see [hosting.md](hosting.md)). Test each platform in a pilot
group before rolling out.

| Platform | Test device | Notes |
|---|---|---|
| Linux | Any VM (Multipass, UTM, a cloud VM) | Fastest end-to-end loop; works with the local stack too |
| Windows | A Windows 10/11 Pro or Enterprise VM (Hyper-V, VirtualBox) | Home edition has no MDM. Enroll with **Settings > Accounts > Access work or school > Enroll only in device management** or the `ms-device-enrollment:` link from the console. Check in now: **Access work or school > (account) > Info > Sync**. Inspect what was applied in `Event Viewer > Applications and Services Logs > Microsoft > Windows > DeviceManagement-Enterprise-Diagnostics-Provider` |
| Android | A physical phone, or an emulator image **with Google Play** | Work profile: open the enrollment link. Fully managed: factory reset, tap the welcome screen 6 times, scan the QR code. Use a test enterprise from the AMAPI quickstart |
| iOS / iPadOS | A physical device | The iOS Simulator cannot enroll in MDM. Corporate: open the profile URL in Safari, then **Settings > Profile Downloaded**. BYOD: needs a Managed Apple ID from Apple Business Manager |
| macOS | A physical Mac or a macOS VM on Apple silicon (UTM, Tart) | Open the profile URL and approve it in **System Settings > Privacy & Security > Profiles** |
| ChromeOS | A ChromeOS device or ChromeOS Flex enrolled in your Workspace domain | Appears after the next sync (15 minutes) |

### Pilot checklist

For each platform, with one corporate and one personal device:

1. Enroll; the device shows `enrolled` with inventory within a few minutes.
2. It joins the expected smart groups; `vsctl get devices --group <id>`.
3. Assign a policy (passcode + one restriction); confirm it on the device and
   that the command shows `acknowledged` in the device page.
4. Add a blueprint with an onboarding step; confirm it runs once.
5. Try a device-wide command (lock or wipe) on the **personal** device; the
   server must refuse it with a BYOD error and audit `command.denied`.
6. Retire both devices; management (and on personal devices, only corporate
   data) is removed and the status becomes `retired`.
7. Review the audit log for the whole session.

### Where to look when something fails

- **Server**: JSON logs on stdout; set `VS_LOG_LEVEL=debug` for detail.
- **Commands**: each command keeps the native request it sent and the
  device's raw response (device page, or `GET /api/v1/devices/{id}/commands`).
- **Apple**: `Console.app` on a Mac (filter `mdmclient`); on iOS, Apple
  Configurator's console.
- **Windows**: the DeviceManagement-Enterprise-Diagnostics-Provider event log
  and `mdmdiagnosticstool.exe -out C:\mdm`.
- **Android**: the Android Device Policy app shows the applied policy and
  non-compliance reasons; AMAPI `devices.get` shows `nonComplianceDetails`.
- **Linux**: `journalctl -u vaanarsena-agent`.
