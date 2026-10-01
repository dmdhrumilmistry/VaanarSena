CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email         TEXT NOT NULL UNIQUE,
    name          TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('admin', 'operator', 'auditor')),
    disabled      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at TIMESTAMPTZ
);

CREATE TABLE api_tokens (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ
);

CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      BYTEA NOT NULL,
    encrypted  BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE groups (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE enrollment_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash  TEXT NOT NULL UNIQUE,
    platform    TEXT NOT NULL CHECK (platform IN ('apple', 'windows', 'android', 'linux')),
    ownership   TEXT NOT NULL CHECK (ownership IN ('corporate', 'personal')),
    group_id    UUID REFERENCES groups(id) ON DELETE SET NULL,
    assignee    TEXT NOT NULL DEFAULT '',
    max_uses    INTEGER NOT NULL DEFAULT 1,
    uses        INTEGER NOT NULL DEFAULT 0,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked     BOOLEAN NOT NULL DEFAULT FALSE,
    -- Platform artefacts, e.g. the AMAPI enrollment token value and QR code.
    extra       JSONB NOT NULL DEFAULT '{}'
);

CREATE TABLE devices (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    platform      TEXT NOT NULL CHECK (platform IN ('ios', 'ipados', 'macos', 'windows', 'android', 'chromeos', 'linux')),
    ownership     TEXT NOT NULL CHECK (ownership IN ('corporate', 'personal')),
    status        TEXT NOT NULL DEFAULT 'enrolling' CHECK (status IN ('enrolling', 'enrolled', 'retired', 'wiped')),
    name          TEXT NOT NULL DEFAULT '',
    serial        TEXT NOT NULL DEFAULT '',
    model         TEXT NOT NULL DEFAULT '',
    os_version    TEXT NOT NULL DEFAULT '',
    assignee      TEXT NOT NULL DEFAULT '',
    -- Platform identity, unique per platform. Apple UDID / EnrollmentID,
    -- Windows DeviceID, AMAPI device resource name, ChromeOS deviceId, agent ID.
    native_id     TEXT NOT NULL,
    platform_ids  JSONB NOT NULL DEFAULT '{}',
    facts         JSONB NOT NULL DEFAULT '{}',
    compliant     BOOLEAN,
    cert_serial   TEXT NOT NULL DEFAULT '',
    enrollment_token_id UUID REFERENCES enrollment_tokens(id) ON DELETE SET NULL,
    enrolled_at   TIMESTAMPTZ,
    last_seen_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (platform, native_id)
);
CREATE INDEX devices_cert_serial_idx ON devices (cert_serial) WHERE cert_serial <> '';

-- Identity certificates issued during enrollment whose device has not checked
-- in yet. Binds the certificate to the enrollment token's ownership and
-- assignee, so ownership is decided by the server, never claimed by the device.
CREATE TABLE pending_identities (
    cert_serial         TEXT PRIMARY KEY,
    enrollment_token_id UUID NOT NULL REFERENCES enrollment_tokens(id) ON DELETE CASCADE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE group_members (
    group_id  UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, device_id)
);

CREATE TABLE policies (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    priority    INTEGER NOT NULL DEFAULT 100,
    document    JSONB NOT NULL,
    version     INTEGER NOT NULL DEFAULT 1,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE policy_assignments (
    policy_id UUID NOT NULL REFERENCES policies(id) ON DELETE CASCADE,
    group_id  UUID REFERENCES groups(id) ON DELETE CASCADE,
    device_id UUID REFERENCES devices(id) ON DELETE CASCADE,
    CHECK ((group_id IS NULL) <> (device_id IS NULL))
);
CREATE UNIQUE INDEX policy_assignments_group_idx ON policy_assignments (policy_id, group_id) WHERE group_id IS NOT NULL;
CREATE UNIQUE INDEX policy_assignments_device_idx ON policy_assignments (policy_id, device_id) WHERE device_id IS NOT NULL;

CREATE TABLE commands (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id    UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    type         TEXT NOT NULL,
    params       JSONB NOT NULL DEFAULT '{}',
    status       TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'sent', 'acknowledged', 'error', 'not_now', 'cancelled')),
    -- The native request actually sent, for troubleshooting.
    native_request  TEXT NOT NULL DEFAULT '',
    native_response TEXT NOT NULL DEFAULT '',
    error        TEXT NOT NULL DEFAULT '',
    created_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at      TIMESTAMPTZ,
    completed_at TIMESTAMPTZ
);
CREATE INDEX commands_pending_idx ON commands (device_id, created_at) WHERE status IN ('queued', 'not_now', 'sent');

CREATE TABLE audit_log (
    id         BIGSERIAL PRIMARY KEY,
    at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor      TEXT NOT NULL,
    action     TEXT NOT NULL,
    target     TEXT NOT NULL DEFAULT '',
    details    JSONB NOT NULL DEFAULT '{}',
    remote_ip  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX audit_log_at_idx ON audit_log (at DESC);

-- The audit log is append only: refuse UPDATE and DELETE at the database.
CREATE FUNCTION audit_log_immutable() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_log is append only';
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER audit_log_no_update BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_immutable();
