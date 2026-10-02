# Getting started

Three ways to run VaanarSena, from a laptop to production.

## On your laptop (five minutes)

You need Docker.

```bash
git clone https://github.com/dmdhrumilmistry/VaanarSena.git
cd VaanarSena
cp deploy/.env.example deploy/.env      # then set the passwords and VS_SECRET_KEY
docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d
```

Open <http://localhost:8080> and sign in with `VS_BOOTSTRAP_ADMIN_EMAIL` and
`VS_BOOTSTRAP_ADMIN_PASSWORD` from `deploy/.env`.

This serves plain HTTP: enough to explore the console, write policies, build
smart groups and try manifests. Real devices need HTTPS; for that on a laptop,
follow the local HTTPS stack in [Testing](testing.md#2-a-local-stack-with-real-https).

## On a server with automatic HTTPS

A VM with Docker, a DNS name pointing at it, and ports 80 and 443 open:

```bash
git clone https://github.com/dmdhrumilmistry/VaanarSena.git
cd VaanarSena/deploy/caddy
cp .env.example .env          # VS_DOMAIN, ACME_EMAIL, passwords, VS_SECRET_KEY
docker compose up -d
```

Caddy obtains a Let's Encrypt certificate and forwards device certificates, so
every platform works. Details, sizing and backups are in [Hosting](hosting.md).

## On Kubernetes

```bash
helm repo add dmdhrumilmistry https://dmdhrumilmistry.github.io/helm-charts
helm install vaanarsena dmdhrumilmistry/vaanarsena \
  --namespace vaanarsena --create-namespace \
  --set publicHost=mdm.example.com
```

The chart works with ingress-nginx (the default) and Traefik, which k3s ships
with:

```bash
helm install vaanarsena dmdhrumilmistry/vaanarsena \
  --namespace vaanarsena --create-namespace \
  --set publicHost=mdm.192.168.1.10.nip.io \
  --set ingress.controller=traefik
```

`nip.io` names resolve to the IP inside them, so this works on a LAN with no
DNS setup. Without a public certificate, point `ingress.tls.secretName` at
your own, or accept the browser warning while testing.

Read the generated admin password with:

```bash
kubectl get secret -n vaanarsena vaanarsena-secrets \
  -o jsonpath='{.data.adminPassword}' | base64 -d; echo
```

## First steps in the console

1. **Change the admin password** under Account, and add named accounts for
   your team under Users.
2. **Connect the platforms you need** under Settings > Platforms. Windows and
   Linux are ready already; see [Platform setup](platforms.md) for Apple,
   Android and ChromeOS.
3. **Write a baseline policy** under Policies, for example a passcode,
   encryption and a few restrictions, and assign it to a group.
4. **Enroll a device** under Enroll devices. A Linux VM is the quickest one
   to try end to end.
5. **Watch it arrive** on the overview: it reports inventory, joins its smart
   groups and receives its policy.

## Back up two things

`VS_SECRET_KEY` (or the Helm chart's `<release>-secrets` Secret) and the
database. Without the key the stored certificate authority cannot be
decrypted and every device must enroll again.
