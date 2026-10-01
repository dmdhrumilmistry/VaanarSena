# Hosting VaanarSena

VaanarSena is one stateless Go server plus PostgreSQL. Pick the shape that
fits; all of them run the same image, `ghcr.io/dmdhrumilmistry/vaanarsena`.

| Option | Good for | TLS | Client certificates (Windows, Linux) |
|---|---|---|---|
| [Single VM with Caddy](#option-1-single-vm-with-caddy-recommended-to-start) | Up to a few thousand devices, simplest operations | Let's Encrypt, automatic | Caddy forwards them; no setup |
| [Kubernetes with Helm](#option-2-kubernetes-with-helm) | HA, existing clusters | cert-manager | ingress-nginx forwards them; the chart configures it |
| [Server terminates TLS](#option-3-server-terminates-tls) | Behind an L4 load balancer or TLS passthrough | Your certificate | Native |
| [Local evaluation](#local-evaluation) | Trying the console and the Linux agent | none (HTTP) | Not available over HTTP |

## Requirements for every option

- **A public DNS name** (e.g. `mdm.example.com`) reachable from wherever your
  devices are, with a **publicly trusted TLS certificate**. Apple and Windows
  refuse private or self-signed server certificates.
- **Outbound HTTPS** from the server to `api.push.apple.com:443` (Apple),
  `androidmanagement.googleapis.com` and `admin.googleapis.com` (Android,
  ChromeOS), if you use those platforms.
- **`VS_SECRET_KEY`**, generated once with `openssl rand -hex 32` and stored
  in your secret manager. It encrypts the CA key and device secrets; losing it
  forces every device to re-enroll.
- **Optional DNS for Windows auto-discovery**: a CNAME
  `enterpriseenrollment.<your-email-domain>` pointing at the server, so users
  can enroll by typing only their email address.

### Sizing

| Devices | Server | PostgreSQL |
|---|---|---|
| up to 1,000 | 1 vCPU, 512 MiB | 1 vCPU, 1 GiB, 10 GiB disk |
| up to 10,000 | 2 replicas x 2 vCPU, 1 GiB | 2 vCPU, 4 GiB, 50 GiB disk |
| more | scale replicas horizontally | managed PostgreSQL with backups |

Load is dominated by check-ins: Linux agents every 60 seconds, Windows every
15 minutes, Apple on push. Smart groups are re-evaluated every minute over
all devices, which is cheap up to tens of thousands of devices.

## Option 1: single VM with Caddy (recommended to start)

[`deploy/caddy`](../deploy/caddy) runs Caddy, VaanarSena and PostgreSQL with
Docker Compose. Caddy obtains and renews a Let's Encrypt certificate and
forwards device client certificates, so every platform works with no extra
configuration.

```bash
# On a VM with Docker (Ubuntu 24.04, 2 vCPU, 2 GiB is plenty to start):
git clone https://github.com/dmdhrumilmistry/VaanarSena.git
cd VaanarSena/deploy/caddy
cp .env.example .env
$EDITOR .env          # VS_DOMAIN, ACME_EMAIL, POSTGRES_PASSWORD, VS_SECRET_KEY, admin
cp ../../examples/manifests/*.yaml manifests/   # optional: starter groups and policies
docker compose up -d
```

1. Point an A/AAAA record for `VS_DOMAIN` at the VM.
2. Open inbound TCP 80 and 443 (80 is needed for the ACME HTTP challenge).
3. Open `https://VS_DOMAIN` and sign in with the bootstrap admin.

Manifests in `deploy/caddy/manifests` are applied on boot and every five
minutes (owner `gitops`). Keep that directory in Git and pull on change, or
point `VS_MANIFEST_DIR` at a checkout.

**Why the header is safe here:** only Caddy publishes ports; VaanarSena has
none, so the only way to reach it is through Caddy, which overwrites
`X-Client-Cert` on every request. PostgreSQL is on an internal-only network.

**Backups:**

```bash
docker compose exec -T postgres pg_dump -U vaanarsena vaanarsena | gzip > vaanarsena-$(date +%F).sql.gz
```

Store dumps off the VM, together with `.env` (it holds `VS_SECRET_KEY`).

**Upgrades:** `docker compose pull && docker compose up -d`. Migrations run at
start-up.

## Option 2: Kubernetes with Helm

```bash
helm repo add dmdhrumilmistry https://dmdhrumilmistry.github.io/helm-charts
helm install vaanarsena dmdhrumilmistry/vaanarsena \
  --namespace vaanarsena --create-namespace \
  --set publicHost=mdm.example.com \
  --set ingress.annotations."cert-manager\.io/cluster-issuer"=letsencrypt
```

Prerequisites: ingress-nginx (for client certificate forwarding) and
cert-manager (or your own TLS Secret). The chart generates the secret key,
admin password and device CA, keeps them on upgrade and uninstall, runs
PostgreSQL (or uses yours), applies a NetworkPolicy so only the ingress
controller reaches the server, and can carry GitOps manifests in its
`manifests:` value. See the [chart README](https://github.com/dmdhrumilmistry/helm-charts/tree/main/vaanarsena).

For HA: `replicaCount: 2`, `podDisruptionBudget.enabled: true`, and an external
managed PostgreSQL via `externalDatabase.url`.

Managed clusters:

- **GKE / EKS / AKS**: the simplest path is ingress-nginx behind a TCP
  (`LoadBalancer`) Service, with DNS pointing at its address.
- **Cloud HTTP(S) load balancers** work if they request client certificates
  and forward the leaf certificate in a header, as URL-encoded PEM or base64
  DER. Set `VS_CLIENT_CERT_HEADER` to that header; for example AWS ALB mTLS in
  passthrough mode sends `X-Amzn-Mtls-Clientcert`. The load balancer must
  overwrite that header and be the only path to the pods. If yours cannot do
  this, run it in TCP mode in front of ingress-nginx. Apple devices sign
  their requests and work behind any load balancer.

## Option 3: server terminates TLS

Behind an L4 load balancer (AWS NLB, HAProxy in TCP mode) or directly on the
internet:

```bash
VS_TLS_CERT_FILE=/etc/vaanarsena/tls.crt \
VS_TLS_KEY_FILE=/etc/vaanarsena/tls.key \
VS_LISTEN_ADDR=:443 \
VS_PUBLIC_URL=https://mdm.example.com \
vaanarsena serve
```

The server asks for (but does not require) client certificates issued by its
CA. Renew the certificate with your usual tooling (certbot, cert-manager) and
restart the process.

## Local evaluation

```bash
cp deploy/.env.example deploy/.env
docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d
```

Serves plain HTTP on `localhost:8080`: enough to explore the console, the API,
manifests and smart groups. Real devices need HTTPS; for a local HTTPS stack
use Option 1 with `VS_DOMAIN=localhost` (Caddy then issues a certificate from
its own local CA), as described in [testing.md](testing.md).

## Hardening checklist

- Rotate the bootstrap admin password after first login; create named
  accounts and give automation API tokens with an expiry.
- Restrict the console by network if you can (VPN, IP allow list on `/` and
  `/api/`); device endpoints (`/mdm/`, `/EnrollmentServer/`,
  `/ManagementServer/`, `/agent/`) must stay reachable by devices.
- Monitor the audit log (`GET /api/v1/audit`) for `command.denied`,
  `auth.login_failed`, `*.enroll_denied` and `manifest.apply`.
- See [security.md](security.md) for the threat model.
