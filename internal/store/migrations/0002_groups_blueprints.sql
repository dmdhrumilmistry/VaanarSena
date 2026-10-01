-- Static and smart (dynamic) groups. Smart group membership is computed from
-- rules; static membership is managed by hand or by apply.
ALTER TABLE groups ADD COLUMN kind TEXT NOT NULL DEFAULT 'static' CHECK (kind IN ('static', 'smart'));
ALTER TABLE groups ADD COLUMN rules JSONB;
ALTER TABLE groups ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- Who put a device in a group: an admin ('manual') or the rule engine ('smart').
ALTER TABLE group_members ADD COLUMN source TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'smart'));
ALTER TABLE group_members ADD COLUMN added_at TIMESTAMPTZ NOT NULL DEFAULT now();
CREATE INDEX group_members_device_idx ON group_members (device_id);

-- Free-form labels usable in smart group rules.
ALTER TABLE devices ADD COLUMN tags TEXT[] NOT NULL DEFAULT '{}';

-- A blueprint bundles policies, an inline policy document (including custom
-- payloads) and onboarding commands, and is assigned to groups.
CREATE TABLE blueprints (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    priority    INTEGER NOT NULL DEFAULT 100,
    -- {"policies": [names], "policy": {...}, "onEnroll": [{type, params}]}
    spec        JSONB NOT NULL,
    version     INTEGER NOT NULL DEFAULT 1,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE blueprint_assignments (
    blueprint_id UUID NOT NULL REFERENCES blueprints(id) ON DELETE CASCADE,
    group_id     UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    PRIMARY KEY (blueprint_id, group_id)
);

-- Resources created or last written by a manifest apply, so prune can remove
-- only what the manifests own and never touch console-made objects.
ALTER TABLE policies ADD COLUMN managed_by TEXT NOT NULL DEFAULT '';
ALTER TABLE groups ADD COLUMN managed_by TEXT NOT NULL DEFAULT '';
ALTER TABLE blueprints ADD COLUMN managed_by TEXT NOT NULL DEFAULT '';

-- Onboarding steps of a blueprint run once per device; editing the blueprint
-- later does not re-run them on devices already onboarded.
-- The primary key makes claiming a run atomic across replicas.
CREATE TABLE blueprint_runs (
    blueprint_id UUID NOT NULL REFERENCES blueprints(id) ON DELETE CASCADE,
    device_id    UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    version      INTEGER NOT NULL,
    ran_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (blueprint_id, device_id)
);
