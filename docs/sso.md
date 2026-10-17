# Single sign-on

People can sign in to the console with your identity provider (Entra ID,
Okta, Google Workspace, Keycloak, Authentik or any other OpenID Connect
provider). By default password sign-in keeps working alongside it; you can
turn it off for everyone except named break-glass admins (see
[Turn off password sign-in](#turn-off-password-sign-in)).

## How it works

VaanarSena uses the OpenID Connect authorization code flow with PKCE:

1. "Sign in with ..." sends the browser to the provider with a fresh `state`,
   `nonce` and PKCE challenge. They are kept in a short-lived, encrypted,
   `HttpOnly` cookie, never in the URL or the database.
2. The provider sends the browser back to
   `<VS_PUBLIC_URL>/api/v1/auth/oidc/callback`. VaanarSena exchanges the code
   for an ID token and verifies it locally: signature against the provider's
   published keys (RS256, PS256 or ES256 families), issuer, audience (and
   `azp` when there are several audiences), expiry and nonce.
3. The verified identity is mapped to a VaanarSena user (below) and the
   browser gets the same session cookie a password sign-in gives.

Every sign-in, and every refused one with the reason, is in the audit log
(`auth.login` and `auth.login_failed` with `"method": "oidc"`).

## Set it up

1. Register an application (web, confidential client) with your provider.
   Redirect URI: `<VS_PUBLIC_URL>/api/v1/auth/oidc/callback`.
2. Make sure the ID token carries `email`, and `groups` if you want roles to
   come from groups. Many providers need a setting for the groups claim; the
   claim name is configurable with `VS_OIDC_GROUPS_CLAIM`.
3. Set the variables in [configuration.md](configuration.md#single-sign-on-openid-connect)
   and restart:

```bash
VS_OIDC_ISSUER=https://login.microsoftonline.com/<tenant-id>/v2.0
VS_OIDC_CLIENT_ID=<application-id>
VS_OIDC_CLIENT_SECRET=<secret>
VS_OIDC_NAME="Microsoft"
VS_OIDC_ADMIN_GROUPS=<object-id-of-the-mdm-admins-group>
VS_OIDC_OPERATOR_GROUPS=<object-id-of-the-helpdesk-group>
VS_OIDC_AUDITOR_GROUPS=<object-id-of-the-security-group>
VS_OIDC_ALLOWED_DOMAINS=example.com
```

`VS_OIDC_ISSUER` must match the `issuer` the provider publishes at
`<issuer>/.well-known/openid-configuration` exactly, including any trailing
slash.

## Who gets in, and with which role

There are two modes.

**Roles from groups** (any `VS_OIDC_*_GROUPS` is set). Groups are
authoritative:

- A person in a mapped group gets the highest role their groups grant, at
  every sign-in, so removing them from the group in the provider takes effect
  at their next sign-in.
- A person in no mapped group is refused.
- Accounts are created on first sign-in.
- The last enabled admin is never demoted by a group change; the refusal is
  noted in the audit log entry for that sign-in.

**Existing users only** (no group variables). Only people who already have a
VaanarSena account, matched by email, can sign in, and they keep the role an
admin gave them under Users. SSO never creates accounts in this mode.

In both modes:

- The first SSO sign-in links the account to the provider's identity
  (issuer and subject). Later sign-ins match on that, not on the email, so a
  person who changes their email at the provider keeps their account, and a
  different identity that claims the same email is refused.
- An email is only used to link to an existing account, or to create one,
  when the ID token says `email_verified: true`. Otherwise anyone able to set
  their own email address at the provider could take over the account that
  owns it. Some providers never send the claim (Entra ID, for one); for those,
  either ask an admin to have each person sign in once with a verified
  address, or set `VS_OIDC_TRUST_UNVERIFIED_EMAIL=true` only if users cannot
  choose their own email at that provider.
- Disabled users are refused.
- `VS_OIDC_ALLOWED_DOMAINS`, when set, limits SSO to those email domains.

Accounts created by SSO have no password. An admin can set one under Users
(for emergency access); the Users page shows how each person signs in.

## Google

Google is an OpenID Connect provider. Create the client in a Cloud project
owned by your organization's Google account, not a personal Gmail account:
whoever owns the project controls sign-in to VaanarSena. The OAuth client
can only be created in the console (`gcloud` cannot create web clients).

1. In the [Google Cloud console](https://console.cloud.google.com), pick the
   project, then **Google Auth Platform**:
    - **Branding**: app name (for example "VaanarSena") and a support email.
    - **Audience**: *Internal* if everyone signs in with your Google Workspace
      domain. Otherwise *External*; while it is in *Testing*, only the
      accounts listed under **Test users** can sign in, so add them or choose
      **Publish app** (VaanarSena asks only for `openid email profile`, which
      needs no Google verification).
    - **Clients > Create client**: type *Web application*, authorized
      redirect URI `https://<your host>/api/v1/auth/oidc/callback`.
2. Copy the client ID and secret into the server's configuration:

```bash
VS_OIDC_ISSUER=https://accounts.google.com
VS_OIDC_CLIENT_ID=<client id>.apps.googleusercontent.com
VS_OIDC_CLIENT_SECRET=<client secret>
VS_OIDC_NAME=Google
VS_OIDC_ALLOWED_DOMAINS=example.com      # optional: only your Workspace domain
```

Google does not put group membership in its ID tokens, so leave the
`VS_OIDC_*_GROUPS` variables unset: create each person under Users with their
role, and Google signs them in ("existing users only" above). Google marks
addresses it has verified with `email_verified`, so linking works without
`VS_OIDC_TRUST_UNVERIFIED_EMAIL`.

## Turn off password sign-in

```bash
VS_PASSWORD_LOGIN=false
VS_PASSWORD_LOGIN_ALLOW=breakglass-admin@example.com   # comma-separated
```

- Only the listed accounts can still sign in with a password; keep at least
  one admin there with a strong password, for when the identity provider is
  down or misconfigured. The sign-in page hides the password form behind an
  "Administrator sign-in with a password" link.
- Everyone else gets "Password sign-in is turned off for this account" (HTTP
  403), audited as `auth.login_failed`. That answer is given only after the
  password has been checked, so it does not reveal which emails are allowed.
- API tokens are not affected: automation and `vsctl` keep working.
- The server refuses to start with `VS_PASSWORD_LOGIN=false` and neither
  single sign-on nor an allowed account, which would lock everyone out.

## When sign-in fails

The sign-in page explains the reason, and the audit log has the detail:

| Shown as | Cause |
|---|---|
| not in a group that grants access | groups are mapped and none of the person's groups is |
| no console account | no group mapping and no existing user with that email |
| could not be reached | discovery or the token endpoint failed; check the issuer URL and network |
| could not be verified | the ID token failed a check (signature, audience, expiry, nonce); see the server log |
| took too long | more than ten minutes passed, or sign-in started in another tab |
| has not verified your email | the ID token lacks `email_verified: true` on a first sign-in; see above |
| already linked to a different sign-in | the email belongs to a user linked to another identity |
