# Deployment

## Requirements

- PostgreSQL 14 or newer.
- A public DNS name with a trusted TLS certificate. Apple and Windows devices
  refuse self-signed server certificates.
- `VS_SECRET_KEY`, stored somewhere you will not lose it.

## Docker Compose

```bash
cp deploy/.env.example deploy/.env
docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d
```

The image is `ghcr.io/dmdhrumilmistry/vaanarsena`. It runs as a non-root user
on a distroless base and listens on 8080. The Linux agent binary is included
at `/usr/local/share/vaanarsena/vaanarsena-agent`.

## Kubernetes (Helm)

```bash
helm repo add dmdhrumilmistry https://dmdhrumilmistry.github.io/helm-charts
helm install vaanarsena dmdhrumilmistry/vaanarsena \
  --namespace vaanarsena --create-namespace \
  --set publicHost=mdm.example.com
```

The chart deploys the server and (by default) a PostgreSQL StatefulSet. It
generates the secret key, the admin password and the device CA on first
install and keeps them across upgrades. See the chart README for every value,
including an external database and platform credentials.

## Client certificates

Windows and Linux devices authenticate to the management endpoints with a TLS
client certificate issued by the VaanarSena CA. Apple devices sign each
request instead (`Mdm-Signature`), which survives any proxy.

You have two options:

1. **TLS passthrough or in-process TLS.** Set `VS_TLS_CERT_FILE` and
   `VS_TLS_KEY_FILE`; the server requests client certificates itself.
2. **TLS-terminating proxy.** The proxy must request a client certificate
   (without failing when there is none, since browsers and Apple devices do
   not send one) and forward it in a header. Set `VS_CLIENT_CERT_HEADER` to
   that header. With ingress-nginx:

   ```yaml
   nginx.ingress.kubernetes.io/auth-tls-secret: <namespace>/<ca-secret>   # key ca.crt
   nginx.ingress.kubernetes.io/auth-tls-verify-client: "optional"
   nginx.ingress.kubernetes.io/auth-tls-pass-certificate-to-upstream: "true"
   ```

   and `VS_CLIENT_CERT_HEADER=ssl-client-cert`. The proxy needs the device CA,
   so supply it with `VS_CA_CERT_FILE` / `VS_CA_KEY_FILE` instead of letting
   the server generate one. The Helm chart does all of this for you.

   **Only trust the header if the proxy overwrites it on every request.**
   Otherwise a client can forge a certificate header. ingress-nginx does.

## Backups

Back up PostgreSQL and `VS_SECRET_KEY` (and the CA files, if you supply
them). Losing the CA forces every device to re-enroll.

## Upgrades

Migrations run automatically at start-up under an advisory lock, so several
replicas can start at once.

## Scaling

The server is stateless; run several replicas behind the load balancer.
Android and ChromeOS syncs run in every replica and are idempotent.
