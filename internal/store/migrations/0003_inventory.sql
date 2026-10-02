-- Software inventory: applications, services and configuration profiles
-- reported by devices. Each (device, kind) is replaced as a whole on every
-- report, so there is no per-row history.
CREATE TABLE device_inventory (
    id         BIGSERIAL PRIMARY KEY,
    device_id  UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('app', 'service', 'profile')),
    name       TEXT NOT NULL,
    -- Package name, bundle ID, unit name or PayloadIdentifier.
    identifier TEXT NOT NULL DEFAULT '',
    version    TEXT NOT NULL DEFAULT '',
    publisher  TEXT NOT NULL DEFAULT '',
    -- dpkg rpm pacman apk flatpak snap systemd apple msi android
    source     TEXT NOT NULL DEFAULT '',
    -- Services: running stopped failed. Apps: installed installing.
    state      TEXT NOT NULL DEFAULT '',
    -- Installed by VaanarSena or the MDM rather than by the user.
    managed    BOOLEAN NOT NULL DEFAULT FALSE,
    details    JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX device_inventory_device_kind_idx ON device_inventory (device_id, kind);
CREATE INDEX device_inventory_kind_name_idx ON device_inventory (kind, lower(name));

CREATE TABLE device_inventory_sync (
    device_id    UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    kind         TEXT NOT NULL,
    collected_at TIMESTAMPTZ NOT NULL,
    item_count   INTEGER NOT NULL,
    PRIMARY KEY (device_id, kind)
);
