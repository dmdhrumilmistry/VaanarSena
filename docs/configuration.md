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
| `VS_CA_CERT_FILE` / `VS_CA_KEY_FILE` | | Use an operator-supplied device CA (PEM) instead of the generated one, e.g. so an ingress can verify device certificates. |
| `VS_WEB_DIR` | | Development only: serve the console from this directory on disk instead of the files built into the binary. |

## Single sign-on (OpenID Connect)

Set `VS_OIDC_ISSUER` to let people sign in to the console with your identity
provider. Password sign-in keeps working. See [sso.md](sso.md).

| Variable | Default | Description |
|---|---|---|
| `VS_OIDC_ISSUER` | | Issuer URL, for example `https://login.microsoftonline.com/<tenant>/v2.0`. Must be `https://` (`http://` only for localhost). Enables SSO. |
| `VS_OIDC_CLIENT_ID` | | Client (application) ID. Required with the issuer. |
| `VS_OIDC_CLIENT_SECRET` | | Client secret. Leave empty for a public client. |
| `VS_OIDC_SCOPES` | `openid email profile` | Space-separated scopes. |
| `VS_OIDC_NAME` | `single sign-on` | Label on the sign-in button: "Sign in with ...". |
| `VS_OIDC_GROUPS_CLAIM` | `groups` | ID token claim that lists the user's groups. |
| `VS_OIDC_ADMIN_GROUPS` / `VS_OIDC_OPERATOR_GROUPS` / `VS_OIDC_AUDITOR_GROUPS` | | Comma-separated groups that grant each role. When any is set, groups decide the role at every sign-in and accounts are created on first sign-in. |
| `VS_OIDC_ALLOWED_DOMAINS` | | Comma-separated email domains allowed to use SSO. |
| `VS_PASSWORD_LOGIN` | `true` | `false` turns off console sign-in with a password, except for the accounts in `VS_PASSWORD_LOGIN_ALLOW`. API tokens keep working. The server refuses to start if this would leave no way to sign in. |
| `VS_PASSWORD_LOGIN_ALLOW` | | Comma-separated break-glass emails that may still sign in with a password when `VS_PASSWORD_LOGIN=false`, for when the identity provider is unavailable. |
| `VS_OIDC_TRUST_UNVERIFIED_EMAIL` | `false` | Link and create accounts from emails the provider does not mark `email_verified: true`. Only for providers that never send the claim and do not let users choose their address. See [sso.md](sso.md). |

The redirect URI to register with the provider is
`<VS_PUBLIC_URL>/api/v1/auth/oidc/callback`.

## Declarative configuration (GitOps)

| Variable | Default | Description |
|---|---|---|
| `VS_MANIFEST_DIR` | | Directory of manifests (`*.yaml`, `*.yml`, `*.json`, recursive) applied on start and periodically. See [manifests.md](manifests.md). |
| `VS_MANIFEST_INTERVAL` | `5m` | Re-read interval. Unchanged content is skipped. |
| `VS_MANIFEST_OWNER` | `gitops` | Owner label recorded on applied resources and used to scope prune. |
| `VS_MANIFEST_PRUNE` | `false` | Delete resources of this owner that were removed from the directory. |

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

## Linux agent

| Variable | Description |
|---|---|
| `VS_AGENT_ALLOW_HTTP` | Set to `1` to let `vaanarsena-agent enroll` use an `http://` server. Local testing only. |

## vsctl

| Variable | Description |
|---|---|
| `VS_SERVER` | Server base URL. Required. |
| `VS_TOKEN` | API token (`vsat_...`), created under Account in the console. Required. |
| `VS_CA_FILE` | PEM file to trust for the server's TLS certificate (private PKI). Optional. |

## CLI

```
vaanarsena serve                     run the server (applies migrations first)
vaanarsena migrate                   apply migrations and exit
vaanarsena create-admin -email E     create an admin (password from VS_ADMIN_PASSWORD or stdin)
vaanarsena version
```
