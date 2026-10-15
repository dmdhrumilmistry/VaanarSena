-- Single sign-on: the OpenID Connect identity ("<issuer>|<sub>") a user is
-- bound to after their first SSO login. Later logins match on it, not on the
-- email address, which the identity provider may let users change.
ALTER TABLE users ADD COLUMN oidc_subject TEXT;
CREATE UNIQUE INDEX users_oidc_subject_idx ON users (oidc_subject) WHERE oidc_subject IS NOT NULL;
