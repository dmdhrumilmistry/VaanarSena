# VaanarSena

Open source, self-hosted device management (MDM) for enterprises that want
complete control of their fleet. One Go binary, one PostgreSQL database,
your own certificate authority. No SaaS, no telemetry, no licence server.

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
- **Auditable.** Every admin action lands in an append-only audit log that the
  database itself refuses to modify.

## Quick start (evaluation)

```bash
cp deploy/.env.example deploy/.env      # set the secrets
docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d
open http://localhost:8080              # sign in with the bootstrap admin
```

Linux and Windows management work out of the box. Apple needs an APNs push
certificate, and Android and ChromeOS need a Google service account. See
[docs/platforms.md](docs/platforms.md).

> Real devices need HTTPS on a public DNS name. Put VaanarSena behind a TLS
> ingress (the Helm chart does this) or set `VS_TLS_CERT_FILE` / `VS_TLS_KEY_FILE`.

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
- [Configuration](docs/configuration.md): every `VS_*` variable
- [Platforms](docs/platforms.md): Apple, Windows, Android, ChromeOS and Linux setup
- [Deployment](docs/deployment.md): Docker, Kubernetes, TLS and client certificates
- [Security](docs/security.md): threat model, BYOD guarantees, hardening
- [API](docs/api.md): REST reference

## Building

```bash
make test        # unit tests
make build       # bin/vaanarsena and bin/vaanarsena-agent
make image       # container image
```

Requires Go 1.26+.

## Status

v0.1 is an early release. The protocol implementations follow the published
specifications (Apple Device Management, MS-MDE2, MS-MDM, Android Management
API, Admin SDK) and are covered by unit tests and an end-to-end Linux agent
flow. Validate each platform against real devices in a pilot before rolling
out widely. See the roadmap in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#roadmap-beyond-v01).

## Contributing

Issues and pull requests are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md).
Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## License

Apache License 2.0. See [LICENSE](LICENSE).
