# Configuration

VaanarSena is configured with environment variables.

## Core

| Variable | Default | Description |
|---|---|---|
| `VS_SECRET_KEY` | (required) | At least 32 characters. Encrypts the CA key and device secrets at rest and signs console sessions. **Back it up**: without it the stored CA cannot be decrypted and every device must re-enroll. Generate with `openssl rand -hex 32`. |
| `VS_DATABASE_URL` | `postgres://vaanarsena:vaanarsena@localhost:5432/vaanarsena?sslmode=disable` | PostgreSQL 14+ connection string. |
| `VS_PUBLIC_URL` | `http://localhost:8080` | External base URL devices use. Must be HTTPS in production. |
| `VS_LISTEN_ADDR` | `:8080` | Listen address. |
| `VS_ORG_NAME` | `VaanarSena` | Shown in profiles, certificates and the console. |
| `VS_TLS_CERT_FILE` / `VS_TLS_KEY_FILE` | | Serve TLS in-process. The server then requests (but does not require) client certificates issued by its CA. |
| `VS_CLIENT_CERT_HEADER` | | Behind a TLS-terminating proxy, the header carrying the URL-encoded PEM client certificate (for example `ssl-client-cert` with ingress-nginx). Only set this if the proxy overwrites the header on every request. |
| `VS_TRUST_PROXY` | `false` | Honour `X-Forwarded-For` for audit IPs and login rate limiting. |
| `VS_SESSION_TTL` | `12h` | Console session lifetime. |
| `VS_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. Logs are JSON on stdout. |
| `VS_BOOTSTRAP_ADMIN_EMAIL` / `VS_BOOTSTRAP_ADMIN_PASSWORD` | | Create the first admin when no users exist. Ignored afterwards. |

## Apple

| Variable | Description |
|---|---|
| `VS_APNS_CERT_FILE` / `VS_APNS_KEY_FILE` | MDM push certificate and key (PEM). |
| `VS_APNS_TOPIC` | Push topic. Read from the certificate's UID when empty. |

## Google (Android and ChromeOS)

| Variable | Description |
|---|---|
| `VS_GOOGLE_CREDENTIALS_FILE` | Service account key (JSON). |
| `VS_GOOGLE_PROJECT_ID` | Cloud project that owns the Android enterprise. |
| `VS_ANDROID_ENTERPRISE` | AMAPI enterprise, for example `enterprises/LC01abcdef`. Enables Android. |
| `VS_GOOGLE_ADMIN_SUBJECT` | Workspace admin impersonated through domain-wide delegation. Enables ChromeOS. |
| `VS_GOOGLE_CUSTOMER_ID` | Workspace customer ID (default `my_customer`). |

## CLI

```
vaanarsena serve                     run the server (applies migrations first)
vaanarsena migrate                   apply migrations and exit
vaanarsena create-admin -email E     create an admin (password from VS_ADMIN_PASSWORD or stdin)
vaanarsena version
```
