-- Outbound webhooks: signed HTTP notifications of device state changes, for
-- conditional access (block a non-compliant device at the identity provider,
-- VPN or proxy) and other automation.
CREATE TABLE webhooks (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL UNIQUE,
    url        TEXT NOT NULL,
    -- Signing secret, encrypted with VS_SECRET_KEY.
    secret     BYTEA NOT NULL,
    events     TEXT[] NOT NULL,
    enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per event. The payload is a snapshot taken when the event happened,
-- so retries send what was true then. device_id has no foreign key: events
-- outlive deleted devices.
CREATE TABLE webhook_events (
    id         BIGSERIAL PRIMARY KEY,
    event      TEXT NOT NULL,
    device_id  UUID,
    payload    JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX webhook_events_created_idx ON webhook_events (created_at);

-- One row per (webhook, event). A sender claims a pending delivery by setting
-- leased_until, so a replica that dies mid-send leaves it to be retried
-- rather than lost, and a manual retry cannot race a send in flight.
-- device_id is copied from the event so per-device ordering is a cheap check.
CREATE TABLE webhook_deliveries (
    id              BIGSERIAL PRIMARY KEY,
    webhook_id      UUID NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
    event_id        BIGINT NOT NULL REFERENCES webhook_events(id) ON DELETE CASCADE,
    device_id       UUID,
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'delivered', 'failed')),
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    leased_until    TIMESTAMPTZ,
    response_code   INTEGER,
    last_error      TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at    TIMESTAMPTZ,
    UNIQUE (webhook_id, event_id)
);
CREATE INDEX webhook_deliveries_due_idx ON webhook_deliveries (next_attempt_at) WHERE status = 'pending';
CREATE INDEX webhook_deliveries_webhook_idx ON webhook_deliveries (webhook_id, id DESC);
-- Pruning and the cascade from webhook_events look deliveries up by event.
CREATE INDEX webhook_deliveries_event_idx ON webhook_deliveries (event_id);
-- Per-device ordering: is there an older pending delivery for this device?
CREATE INDEX webhook_deliveries_device_pending_idx ON webhook_deliveries (webhook_id, device_id, event_id)
    WHERE status = 'pending';

-- Device events are raised here, in the database, so every writer (each
-- platform driver, the API, manifests) produces them and none can forget to.
-- Events are recorded only when an enabled webhook subscribes to them.
CREATE FUNCTION device_webhook_events() RETURNS trigger AS $$
DECLARE
    evs     TEXT[] := '{}';
    ev      TEXT;
    eid     BIGINT;
    payload JSONB;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM webhooks WHERE enabled) THEN
        RETURN NEW;
    END IF;
    IF TG_OP = 'INSERT' THEN
        IF NEW.status = 'enrolled' THEN
            evs := evs || 'device.enrolled'::text;
        END IF;
    ELSE
        IF NEW.status IS DISTINCT FROM OLD.status AND NEW.status IN ('enrolled', 'retired', 'wiped') THEN
            evs := evs || ('device.' || NEW.status);
        END IF;
        IF NEW.compliant IS DISTINCT FROM OLD.compliant THEN
            evs := evs || 'device.compliance_changed'::text;
        END IF;
        IF NEW.ownership IS DISTINCT FROM OLD.ownership THEN
            evs := evs || 'device.ownership_changed'::text;
        END IF;
    END IF;
    FOREACH ev IN ARRAY evs LOOP
        CONTINUE WHEN NOT EXISTS (SELECT 1 FROM webhooks WHERE enabled AND ev = ANY(events));
        payload := jsonb_build_object(
            'device', jsonb_build_object(
                'id', NEW.id, 'platform', NEW.platform, 'ownership', NEW.ownership, 'status', NEW.status,
                -- BYOD: hardware identifiers never leave the server for a
                -- personal device, whatever is stored.
                'name', NEW.name, 'serial', CASE WHEN NEW.ownership = 'personal' THEN '' ELSE NEW.serial END,
                'model', NEW.model, 'osVersion', NEW.os_version,
                'assignee', NEW.assignee, 'compliant', NEW.compliant,
                'complianceIssues', COALESCE(NEW.facts -> 'compliance' -> 'issues', '[]'::jsonb),
                'lastSeenAt', NEW.last_seen_at));
        IF TG_OP = 'UPDATE' THEN
            payload := payload || jsonb_build_object('previous', jsonb_build_object(
                'status', OLD.status, 'ownership', OLD.ownership, 'compliant', OLD.compliant));
        END IF;
        INSERT INTO webhook_events (event, device_id, payload) VALUES (ev, NEW.id, payload) RETURNING id INTO eid;
        INSERT INTO webhook_deliveries (webhook_id, event_id, device_id)
            SELECT id, eid, NEW.id FROM webhooks WHERE enabled AND ev = ANY(events);
    END LOOP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Devices are updated on every check-in; the WHEN clause keeps those updates
-- from calling the function at all unless an event-worthy column changed.
CREATE TRIGGER devices_webhook_events_insert AFTER INSERT ON devices
    FOR EACH ROW WHEN (NEW.status = 'enrolled') EXECUTE FUNCTION device_webhook_events();
CREATE TRIGGER devices_webhook_events_update AFTER UPDATE OF status, compliant, ownership ON devices
    FOR EACH ROW WHEN (OLD.status IS DISTINCT FROM NEW.status OR OLD.compliant IS DISTINCT FROM NEW.compliant
        OR OLD.ownership IS DISTINCT FROM NEW.ownership)
    EXECUTE FUNCTION device_webhook_events();
