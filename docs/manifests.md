# Declarative configuration: manifests, groups, blueprints and payloads

Everything you can configure in the console can also be described in YAML or
JSON **manifests** and deployed without the UI:

- `vsctl apply -f config/` from a laptop or a CI pipeline,
- `POST /api/v1/apply` from any tool,
- a directory the server reconciles on its own (`VS_MANIFEST_DIR`), for GitOps.

All three use the same code path as the console, so a manifest and a click
produce identical state. Working examples are in
[`examples/manifests`](https://github.com/dmdhrumilmistry/VaanarSena/tree/main/examples/manifests); CI keeps them valid.

## Resource format

```yaml
apiVersion: vaanarsena.io/v1
kind: Group | Policy | Blueprint
metadata:
  name: unique-name          # identity: apply matches existing resources by name
  description: optional
spec: {...}
```

Separate several resources with `---`, or use JSON (one object, an array, or
`{"items": [...]}`). Unknown fields are rejected everywhere, so a typo fails
loudly instead of silently disabling a control.

## Groups

### Static groups

Membership is managed by hand (console, API, `vsctl`) or listed in the
manifest:

```yaml
kind: Group
metadata: {name: kiosks}
spec:
  kind: static
  members:                  # omit to leave membership to the console
    serials: [F9FXK2Q1XX, R58N123ABC]
    ids: [3f2c...]
```

### Smart (dynamic) groups

Membership is computed from rules and refreshed every minute, immediately
when a device enrolls, and when its tags change. Devices that stop matching
leave the group, and the policies and blueprints targeting it stop applying.

```yaml
kind: Group
metadata: {name: at-risk}
spec:
  kind: smart
  rules:
    match: any                # any = OR, all = AND
    conditions:
      - {field: compliant, op: eq, value: false}
      - {field: lastSeenDays, op: gt, value: 7}
    rules:                    # nested groups of conditions, up to 5 deep
      - match: all
        conditions:
          - {field: platform, op: eq, value: linux}
          - {field: facts.linux.diskEncrypted, op: eq, value: false}
```

| Fields | |
|---|---|
| `platform`, `ownership`, `status`, `name`, `serial`, `model`, `osVersion`, `assignee` | Device attributes |
| `compliant` | `true`, `false`, or missing when unknown |
| `tags` | Labels set with `vsctl tag` or the console |
| `enrolledDays`, `lastSeenDays` | Whole days since enrollment and last check-in |
| `facts.<path>` | Any inventory value by dotted path, e.g. `facts.apple.IsSupervised`, `facts.linux.diskEncrypted`, `facts.android.securityPatchLevel`, `facts.chromeos.orgUnitPath`. Windows inventory keys are OMA-URIs containing dots, so use the top-level `osVersion`, `model` and `compliant` fields for Windows instead. |

| Operators | |
|---|---|
| `eq`, `ne` | Case-insensitive; numbers and booleans compare by value |
| `in`, `not_in` | Value is a list |
| `contains`, `not_contains` | Substring, or element of a list such as `tags` |
| `starts_with`, `ends_with`, `matches` | `matches` is a Go regular expression |
| `gt`, `gte`, `lt`, `lte` | Numeric |
| `version_lt`, `version_lte`, `version_gt`, `version_gte` | Dotted versions compared numerically (`17.2 < 17.10`) |
| `exists`, `not_exists` | Field present or not |

A missing field matches only negative operators (`ne`, `not_in`,
`not_contains`, `not_exists`).

Try rules before saving them:

```bash
vsctl preview -f rules.yaml        # prints the matching devices
```

Smart group membership cannot be edited by hand; change the rules instead.

## Policies

```yaml
kind: Policy
metadata: {name: baseline-security}
spec:
  priority: 50                 # lower wins when policies overlap (default 100)
  groups: [all-corporate]
  document:
    passcode: {required: true, minLength: 8, complex: true}
    encryption: {required: true}
    restrictions: {camera: true, usbStorage: false}
    wifi: [{ssid: corp, security: WPA2, password: secret, autoJoin: true}]
    osUpdates: {autoInstall: true, deferDays: 7}
    apps: [{id: com.slack, platform: android, install: required}]
    custom: {...}              # see below
```

### Custom payloads

`document.custom` deploys raw, platform-native settings that the neutral
model does not cover. They are applied after the neutral translation, so they
win on conflict.

```yaml
custom:
  scope: corporate            # default; "all" also targets personal devices
  apple:
    - payload:                # one payload dictionary, as in a profile
        PayloadType: com.apple.security.firewall
        EnableFirewall: true
    - mobileconfig: PD94bWwg...   # a whole .mobileconfig, base64 (signed or not)
  windows:
    - {locUri: ./Device/Vendor/MSFT/Policy/Config/Experience/AllowCortana, format: int, data: "0"}
    - {locUri: ./Vendor/MSFT/Something, op: Exec, format: xml, data: "<xml/>"}
  android:
    shareLocationDisabled: true   # raw Android Management API Policy fields
```

- **Apple**: payloads are added to the VaanarSena policy profile, which the
  server signs. A `.mobileconfig` from Apple Configurator, iMazing or
  ProfileCreator is unpacked (`base64 -w0 file.mobileconfig`). Profiles
  containing an MDM payload are refused.
- **Windows**: any CSP node as an OMA-URI. `op` is `Replace` (default), `Add`,
  `Exec` or `Delete`; `format` is `int`, `chr`, `bool`, `xml`, `b64` or `node`.
- **Android**: keys are merged into the device's AMAPI policy. Fields
  VaanarSena manages (`statusReportingSettings`, `name`, `version`) are refused.

Because a raw payload can do anything the platform allows, custom payloads
reach **personal devices only when `scope: all`** is set. Even then Apple User
Enrollment and Android work profiles limit what takes effect.

## Blueprints

A blueprint bundles configuration and onboarding for the groups it targets:

```yaml
kind: Blueprint
metadata: {name: standard-laptop}
spec:
  priority: 30
  groups: [all-corporate]
  policies: [baseline-security, custom-payloads]   # reuse named policies
  policy:                                          # plus an inline policy
    wifi: [{ssid: corp-wifi, security: WPA2, password: change-me}]
  onEnroll:                                        # onboarding steps
    - {type: refresh}
    - {type: install_app, params: {url: https://apps.example.com/agent.plist}}
    - {type: run_script, params: {script: "apt-get install -y osquery"}}
```

- **Effective configuration** for a device is the merge of every policy
  assigned to it or its groups, the policies its blueprints reference, and
  those blueprints' inline policies, ordered by priority (lower wins).
- **Onboarding steps run once per device**, when it first falls in scope:
  at enrollment, or later when a smart group picks it up. Editing a blueprint
  does not re-run steps on devices already onboarded.
- Steps are ordinary commands from the [command catalogue](api.md). `wipe`
  and `retire` are refused as onboarding steps, and the **BYOD guard still
  applies**: a step not allowed on a personal device is skipped and recorded
  in the audit log as `command.skipped`.

## Applying

```bash
export VS_SERVER=https://mdm.example.com
export VS_TOKEN=vsat_...           # Account > API tokens (admin role)

vsctl diff  -f config/             # what would change
vsctl apply -f config/             # apply; prints created/updated/unchanged
vsctl apply -f config/ --prune     # also delete what this owner applied before
vsctl export > fleet.yaml          # current state as manifests
```

- **All or nothing validation.** The whole set is checked (shape, values,
  references between resources, such as policies a blueprint names) before
  anything is written. Errors list every problem.
- **Idempotent.** Re-applying the same files reports everything `unchanged`
  and touches no device.
- **Ownership and prune.** Each apply carries an owner (`--owner`, default
  `manifest`). Resources record their owner, and `--prune` deletes only
  resources of *that* owner missing from the files. Console-made resources
  are never pruned. Two pipelines with different owners never delete each
  other's resources. Editing a manifest-owned resource in the console makes
  the console its owner; the next apply from that source takes it back.
- **Side effects.** Affected devices get a fresh policy push, and newly
  in-scope devices run blueprint onboarding.
- **Export** includes secrets such as Wi-Fi passphrases, so it requires the
  admin role. Policies pinned to single devices are not expressible in
  manifests and are left out.

Over plain HTTP:

```bash
curl -X POST "$VS_SERVER/api/v1/apply?dryRun=true&owner=ci" \
  -H "Authorization: Bearer $VS_TOKEN" -H 'Content-Type: application/yaml' \
  --data-binary @config/fleet.yaml
```

## GitOps

Mount a directory of manifests (a Git checkout, or a Kubernetes ConfigMap)
and let the server reconcile it:

| Variable | Default | |
|---|---|---|
| `VS_MANIFEST_DIR` | | Directory of `*.yaml`, `*.yml`, `*.json` files, read recursively |
| `VS_MANIFEST_INTERVAL` | `5m` | How often to re-read it; unchanged content is skipped |
| `VS_MANIFEST_OWNER` | `gitops` | Owner label for apply and prune |
| `VS_MANIFEST_PRUNE` | `false` | Delete owned resources removed from the directory |

A broken file stops the whole apply (and is logged); the last good state stays
in place. With the Helm chart, put manifests under the `manifests:` value.

A CI pipeline works just as well:

```yaml
# .github/workflows/mdm.yml
on: {push: {branches: [main]}, pull_request: {}}
jobs:
  mdm:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - run: curl -fsSL -o vsctl https://github.com/dmdhrumilmistry/VaanarSena/releases/latest/download/vsctl-linux-amd64 && chmod +x vsctl
      - run: ./vsctl diff -f mdm/ --owner ci                        # on pull requests
      - if: github.ref == 'refs/heads/main'
        run: ./vsctl apply -f mdm/ --owner ci --prune
    env:
      VS_SERVER: https://mdm.example.com
      VS_TOKEN: ${{ secrets.VAANARSENA_TOKEN }}
```
