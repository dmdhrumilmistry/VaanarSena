<p align="center">
  <img src="internal/web/static/brand/emblem.svg" width="140" alt="Hanuman leaping with the Dronagiri mountain">
</p>

<h1 align="center">VaanarSena</h1>

<p align="center">
  <a href="https://dmdhrumilmistry.github.io/VaanarSena/"><b>Documentation</b></a> |
  <a href="https://dmdhrumilmistry.github.io/VaanarSena/getting-started/">Getting started</a> |
  <a href="https://github.com/dmdhrumilmistry/VaanarSena/releases/latest">Releases</a>
</p>

Open source, self-hosted device management (MDM) for enterprises that want
complete control of their fleet. One Go binary, one PostgreSQL database,
your own certificate authority. No SaaS, no telemetry, no licence server.

![The VaanarSena console](docs/assets/screens/overview.png)

VaanarSena manages **corporate-owned and personally owned (BYOD)** devices on
**iOS, iPadOS, macOS, Windows, Android, ChromeOS and Linux** from one console
and one REST API.

| Platform | How it is managed | Corporate | BYOD |
|---|---|---|---|
| iOS / iPadOS / macOS | Apple MDM protocol + APNs | Device enrollment | User Enrollment (Managed Apple ID) |
| Windows 10/11 | MS-MDE2 enrollment + OMA-DM (SyncML) | Full MDM | MDM-only work account |
| Android | Android Management API | Fully managed | Work profile |
| ChromeOS | Google Admin SDK | Imported from your domain | n/a |
| Linux | VaanarSena agent (mTLS) | Full | Inventory-only |

## Why

- **Native protocols.** Each platform is driven through its own management
  channel, not a lowest-common-denominator agent.
- **BYOD privacy enforced by the server.** Ownership is decided by the
  enrollment token, never claimed by the device. On personal devices the server
  refuses wipe, lock, restart, location, scripts and hardware inventory;
  `retire` removes only corporate data and management.
  See [docs/security.md](docs/security.md).
- **One policy, every platform.** Write a platform-neutral policy (passcode,
  encryption, restrictions, Wi-Fi, OS updates, apps); each driver translates it
  into configuration profiles, Policy CSP nodes or AMAPI policies.
- **Custom payloads.** When the neutral model is not enough, ship raw Apple
  payloads or a whole `.mobileconfig`, any Windows CSP node (OMA-URI), or raw
  Android Management API fields. Corporate-only unless you opt in.
- **Static and smart groups.** Smart (dynamic) groups compute membership from
  rules over device fields, tags and inventory facts, such as
  "iPhones below iOS 17" or "Linux laptops without disk encryption", and
  update continuously. Configuration follows membership.
- **Blueprints.** Bundle policies, an inline policy and onboarding steps
  (install apps, run scripts, update the OS), target them at groups, and every
  device that falls in scope is onboarded exactly once.
- **Configuration as code.** Groups, policies and blueprints are YAML
  manifests: `vsctl apply`/`diff`/`export`, `POST /api/v1/apply`, or a GitOps
  directory the server reconciles itself. Validated as a whole, idempotent,
  with owner-scoped prune.
- **Auditable.** Every admin action lands in an append-only audit log that the
  database itself refuses to modify.

## Configuration as code

```yaml
# fleet.yaml
apiVersion: vaanarsena.io/v1
kind: Group
metadata: {name: ios-needs-update}
spec:
  kind: smart
  rules:
    match: all
    conditions:
      - {field: platform, op: in, value: [ios, ipados]}
      - {field: osVersion, op: version_lt, value: "17.0"}
---
apiVersion: vaanarsena.io/v1
kind: Blueprint
metadata: {name: ios-update-push}
spec:
  groups: [ios-needs-update]
  policy:
    passcode: {required: true, minLength: 6}
  onEnroll:
    - {type: os_update}
```

```bash
export VS_SERVER=https://mdm.example.com VS_TOKEN=vsat_...
vsctl diff  -f fleet.yaml      # what would change
vsctl apply -f fleet.yaml      # created/updated/unchanged per resource
```

More in [docs/manifests.md](docs/manifests.md) and
[examples/manifests](examples/manifests).

## Quick start (evaluation)

```bash
cp deploy/.env.example deploy/.env      # set the secrets
docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d
open http://localhost:8080              # sign in with the bootstrap admin
```

Linux and Windows management work out of the box. Apple needs an APNs push
certificate, and Android and ChromeOS need a Google service account. See
[docs/platforms.md](docs/platforms.md).

> Real devices need HTTPS on a public DNS name. For production on a single
> VM, [deploy/caddy](deploy/caddy) adds automatic Let's Encrypt TLS with
> client certificate forwarding. See [docs/hosting.md](docs/hosting.md).

## Kubernetes

```bash
helm repo add dmdhrumilmistry https://dmdhrumilmistry.github.io/helm-charts
helm install vaanarsena dmdhrumilmistry/vaanarsena \
  --namespace vaanarsena --create-namespace \
  --set publicHost=mdm.example.com
```

See [docs/deployment.md](docs/deployment.md).

## Enrolling a Linux machine

```bash
# Console: Enroll > platform linux > Create enrollment token
sudo install -m 0755 vaanarsena-agent /usr/local/bin/
sudo vaanarsena-agent enroll --server https://mdm.example.com --token <TOKEN>
sudo install -m 0644 packaging/linux/vaanarsena-agent.service /etc/systemd/system/
sudo systemctl enable --now vaanarsena-agent
```

## Documentation

- [Architecture](docs/ARCHITECTURE.md): components, data model, protocols
- [Hosting](docs/hosting.md): single VM with Caddy, Kubernetes, sizing, DNS, backups
- [Manifests](docs/manifests.md): custom payloads, static and smart groups, blueprints, vsctl, GitOps
- [Testing](docs/testing.md): automated suites, a local HTTPS stack, piloting real devices
- [Configuration](docs/configuration.md): every `VS_*` variable
- [Platforms](docs/platforms.md): Apple, Windows, Android, ChromeOS and Linux setup
- [Deployment](docs/deployment.md): Docker, Kubernetes, TLS and client certificates
- [Security](docs/security.md): threat model, BYOD guarantees, hardening
- [API](docs/api.md): REST reference

## Building

```bash
make test        # vet + unit tests
make e2e         # end-to-end suite against a throwaway PostgreSQL (Docker)
make build       # bin/vaanarsena, bin/vaanarsena-agent and bin/vsctl
make image       # container image
```

Requires Go 1.26+.

## Status

Early release. The protocol implementations follow the published
specifications (Apple Device Management, MS-MDE2, MS-MDM, Android Management
API, Admin SDK) and are covered by unit tests and an end-to-end suite that
runs the HTTPS server with mutual TLS against PostgreSQL in CI. Validate each platform against real devices in a pilot before rolling
out widely. See the roadmap in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#roadmap-beyond-v01).

## Contributing

Issues and pull requests are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md).
Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## License

Apache License 2.0. See [LICENSE](LICENSE).
