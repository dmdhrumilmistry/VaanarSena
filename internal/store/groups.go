package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

const groupCols = `g.id, g.name, g.description, g.kind, g.rules, g.managed_by, g.created_at, g.updated_at,
	(SELECT count(*) FROM group_members m WHERE m.group_id = g.id)`

func scanGroup(row interface{ Scan(...any) error }) (*Group, error) {
	var g Group
	var rules []byte
	err := row.Scan(&g.ID, &g.Name, &g.Description, &g.Kind, &rules, &g.ManagedBy, &g.CreatedAt, &g.UpdatedAt, &g.DeviceCount)
	if len(rules) > 0 {
		g.Rules = rules
	}
	return &g, notFound(err)
}

// ErrSmartGroup is returned when manual membership changes target a smart group.
var ErrSmartGroup = errors.New("membership of a smart group is computed from its rules and cannot be edited")

// SaveGroup creates (ID empty) or updates a group. Changing a smart group to
// static keeps its current members as manual ones; changing static to smart
// drops manual members, since rules now decide.
func (s *Store) SaveGroup(ctx context.Context, g *Group) (*Group, error) {
	var rules any
	if g.Kind == GroupSmart {
		rules = []byte(g.Rules)
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if g.ID == "" {
		err = tx.QueryRow(ctx, `INSERT INTO groups (name, description, kind, rules, managed_by) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			g.Name, g.Description, g.Kind, rules, g.ManagedBy).Scan(&g.ID)
		if err != nil {
			return nil, err
		}
	} else {
		var oldKind string
		if err := tx.QueryRow(ctx, `SELECT kind FROM groups WHERE id = $1 FOR UPDATE`, g.ID).Scan(&oldKind); err != nil {
			return nil, notFound(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE groups SET name = $2, description = $3, kind = $4, rules = $5, managed_by = $6, updated_at = now() WHERE id = $1`,
			g.ID, g.Name, g.Description, g.Kind, rules, g.ManagedBy); err != nil {
			return nil, err
		}
		switch {
		case oldKind == GroupSmart && g.Kind == GroupStatic:
			_, err = tx.Exec(ctx, `UPDATE group_members SET source = 'manual' WHERE group_id = $1`, g.ID)
		case oldKind == GroupStatic && g.Kind == GroupSmart:
			_, err = tx.Exec(ctx, `DELETE FROM group_members WHERE group_id = $1`, g.ID)
		}
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GroupByID(ctx, g.ID)
}

// GroupByID fetches a group.
func (s *Store) GroupByID(ctx context.Context, id string) (*Group, error) {
	return scanGroup(s.DB.QueryRow(ctx, `SELECT `+groupCols+` FROM groups g WHERE g.id = $1`, id))
}

// GroupByName fetches a group by its unique name.
func (s *Store) GroupByName(ctx context.Context, name string) (*Group, error) {
	return scanGroup(s.DB.QueryRow(ctx, `SELECT `+groupCols+` FROM groups g WHERE g.name = $1`, name))
}

// ListGroups returns all groups with member counts. kind filters when set.
func (s *Store) ListGroups(ctx context.Context, kind ...string) ([]*Group, error) {
	q := `SELECT ` + groupCols + ` FROM groups g`
	var args []any
	if len(kind) > 0 && kind[0] != "" {
		q += ` WHERE g.kind = $1`
		args = append(args, kind[0])
	}
	rows, err := s.DB.Query(ctx, q+` ORDER BY g.name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Group
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// DeleteGroup removes a group and returns the devices that were in it.
func (s *Store) DeleteGroup(ctx context.Context, id string) ([]string, error) {
	members, err := s.GroupMembers(ctx, id)
	if err != nil {
		return nil, err
	}
	return members, s.exec1(ctx, `DELETE FROM groups WHERE id = $1`, id)
}

// GroupMembers lists device IDs in a group.
func (s *Store) GroupMembers(ctx context.Context, groupID string) ([]string, error) {
	rows, err := s.DB.Query(ctx, `SELECT device_id::text FROM group_members WHERE group_id = $1`, groupID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// AddDeviceToGroup adds manual membership to a static group.
func (s *Store) AddDeviceToGroup(ctx context.Context, groupID, deviceID string) error {
	tag, err := s.DB.Exec(ctx, `INSERT INTO group_members (group_id, device_id, source)
		SELECT id, $2, 'manual' FROM groups WHERE id = $1 AND kind = 'static' ON CONFLICT DO NOTHING`, groupID, deviceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		g, err := s.GroupByID(ctx, groupID)
		if err != nil {
			return err
		}
		if g.Kind == GroupSmart {
			return ErrSmartGroup
		}
	}
	return nil
}

// RemoveDeviceFromGroup removes manual membership from a static group.
func (s *Store) RemoveDeviceFromGroup(ctx context.Context, groupID, deviceID string) error {
	g, err := s.GroupByID(ctx, groupID)
	if err != nil {
		return err
	}
	if g.Kind == GroupSmart {
		return ErrSmartGroup
	}
	return s.exec1(ctx, `DELETE FROM group_members WHERE group_id = $1 AND device_id = $2`, groupID, deviceID)
}

// SetStaticMembers replaces a static group's members (used by apply).
func (s *Store) SetStaticMembers(ctx context.Context, groupID string, deviceIDs []string) (added, removed []string, err error) {
	return s.syncMembers(ctx, groupID, deviceIDs, "manual")
}

// SetSmartMembers replaces a smart group's computed members and reports the
// devices whose membership changed.
func (s *Store) SetSmartMembers(ctx context.Context, groupID string, deviceIDs []string) (added, removed []string, err error) {
	return s.syncMembers(ctx, groupID, deviceIDs, "smart")
}

func (s *Store) syncMembers(ctx context.Context, groupID string, want []string, source string) (added, removed []string, err error) {
	if want == nil {
		want = []string{}
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	rows, err := tx.Query(ctx, `DELETE FROM group_members WHERE group_id = $1 AND NOT (device_id::text = ANY($2)) RETURNING device_id::text`, groupID, want)
	if err != nil {
		return nil, nil, err
	}
	if removed, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return nil, nil, err
	}
	rows, err = tx.Query(ctx, `INSERT INTO group_members (group_id, device_id, source)
		SELECT $1, d.id, $3 FROM devices d WHERE d.id::text = ANY($2)
		ON CONFLICT DO NOTHING RETURNING device_id::text`, groupID, want, source)
	if err != nil {
		return nil, nil, err
	}
	if added, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return nil, nil, err
	}
	return added, removed, tx.Commit(ctx)
}

// AllDevices streams every non-wiped device, for smart group evaluation.
func (s *Store) AllDevices(ctx context.Context) ([]*Device, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+deviceCols+` FROM devices WHERE status <> 'wiped'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// --- blueprints ---

const blueprintCols = `b.id, b.name, b.description, b.priority, b.spec, b.version, b.managed_by, b.created_at, b.updated_at,
	COALESCE((SELECT array_agg(a.group_id::text) FROM blueprint_assignments a WHERE a.blueprint_id = b.id), '{}')`

func scanBlueprint(row interface{ Scan(...any) error }) (*Blueprint, error) {
	var b Blueprint
	err := row.Scan(&b.ID, &b.Name, &b.Description, &b.Priority, &b.Spec, &b.Version, &b.ManagedBy, &b.CreatedAt, &b.UpdatedAt, &b.GroupIDs)
	return &b, notFound(err)
}

// SaveBlueprint creates or updates a blueprint and replaces its group targets.
func (s *Store) SaveBlueprint(ctx context.Context, b *Blueprint) (*Blueprint, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if b.ID == "" {
		err = tx.QueryRow(ctx, `INSERT INTO blueprints (name, description, priority, spec, managed_by) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			b.Name, b.Description, b.Priority, b.Spec, b.ManagedBy).Scan(&b.ID)
	} else {
		var tag interface{ RowsAffected() int64 }
		tag, err = tx.Exec(ctx, `UPDATE blueprints SET name = $2, description = $3, priority = $4, spec = $5, managed_by = $6,
			version = version + 1, updated_at = now() WHERE id = $1`, b.ID, b.Name, b.Description, b.Priority, b.Spec, b.ManagedBy)
		if err == nil && tag.RowsAffected() == 0 {
			return nil, ErrNotFound
		}
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM blueprint_assignments WHERE blueprint_id = $1`, b.ID); err != nil {
		return nil, err
	}
	for _, g := range b.GroupIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO blueprint_assignments (blueprint_id, group_id) VALUES ($1, $2)`, b.ID, g); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.BlueprintByID(ctx, b.ID)
}

// BlueprintByID fetches a blueprint.
func (s *Store) BlueprintByID(ctx context.Context, id string) (*Blueprint, error) {
	return scanBlueprint(s.DB.QueryRow(ctx, `SELECT `+blueprintCols+` FROM blueprints b WHERE b.id = $1`, id))
}

// BlueprintByName fetches a blueprint by name.
func (s *Store) BlueprintByName(ctx context.Context, name string) (*Blueprint, error) {
	return scanBlueprint(s.DB.QueryRow(ctx, `SELECT `+blueprintCols+` FROM blueprints b WHERE b.name = $1`, name))
}

// ListBlueprints returns all blueprints.
func (s *Store) ListBlueprints(ctx context.Context) ([]*Blueprint, error) {
	return s.queryBlueprints(ctx, `SELECT `+blueprintCols+` FROM blueprints b ORDER BY b.priority, b.name`)
}

// DeviceBlueprints returns the blueprints targeting a device's groups, least
// important first (so later entries win when merged).
func (s *Store) DeviceBlueprints(ctx context.Context, deviceID string) ([]*Blueprint, error) {
	return s.queryBlueprints(ctx, `SELECT `+blueprintCols+` FROM blueprints b
		WHERE b.id IN (SELECT a.blueprint_id FROM blueprint_assignments a
		               JOIN group_members m ON m.group_id = a.group_id WHERE m.device_id = $1)
		ORDER BY b.priority DESC, b.name`, deviceID)
}

func (s *Store) queryBlueprints(ctx context.Context, q string, args ...any) ([]*Blueprint, error) {
	rows, err := s.DB.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Blueprint
	for rows.Next() {
		b, err := scanBlueprint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// DeleteBlueprint removes a blueprint.
func (s *Store) DeleteBlueprint(ctx context.Context, id string) error {
	return s.exec1(ctx, `DELETE FROM blueprints WHERE id = $1`, id)
}

// DevicesForBlueprint lists enrolled devices a blueprint targets.
func (s *Store) DevicesForBlueprint(ctx context.Context, id string) ([]string, error) {
	rows, err := s.DB.Query(ctx, `SELECT DISTINCT m.device_id::text FROM blueprint_assignments a
		JOIN group_members m ON m.group_id = a.group_id
		JOIN devices d ON d.id = m.device_id AND d.status = 'enrolled'
		WHERE a.blueprint_id = $1`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// PolicyByName fetches a policy by name.
func (s *Store) PolicyByName(ctx context.Context, name string) (*Policy, error) {
	return scanPolicy(s.DB.QueryRow(ctx, `SELECT `+policyCols+` FROM policies p
		LEFT JOIN policy_assignments a ON a.policy_id = p.id WHERE p.name = $1 GROUP BY p.id`, name))
}

// DevicesInGroups lists enrolled devices in any of the groups.
func (s *Store) DevicesInGroups(ctx context.Context, groupIDs []string) ([]string, error) {
	rows, err := s.DB.Query(ctx, `SELECT DISTINCT m.device_id::text FROM group_members m
		JOIN devices d ON d.id = m.device_id AND d.status = 'enrolled' WHERE m.group_id::text = ANY($1)`, groupIDs)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// ClaimBlueprintRun records that a blueprint's onboarding ran on a device and
// reports whether this caller won the claim (false: it already ran).
func (s *Store) ClaimBlueprintRun(ctx context.Context, blueprintID, deviceID string, version int) (bool, error) {
	tag, err := s.DB.Exec(ctx, `INSERT INTO blueprint_runs (blueprint_id, device_id, version) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING`, blueprintID, deviceID, version)
	return tag.RowsAffected() == 1, err
}

// SetSmartMembership adds or removes one device from a smart group and
// reports whether membership changed.
func (s *Store) SetSmartMembership(ctx context.Context, groupID, deviceID string, member bool) (bool, error) {
	var sql string
	if member {
		sql = `INSERT INTO group_members (group_id, device_id, source) VALUES ($1, $2, 'smart') ON CONFLICT DO NOTHING`
	} else {
		sql = `DELETE FROM group_members WHERE group_id = $1 AND device_id = $2`
	}
	tag, err := s.DB.Exec(ctx, sql, groupID, deviceID)
	return tag.RowsAffected() == 1, err
}

// DeviceIDsBySerial resolves serial numbers to device IDs (unknown serials
// are skipped).
func (s *Store) DeviceIDsBySerial(ctx context.Context, serials []string) ([]string, error) {
	rows, err := s.DB.Query(ctx, `SELECT id::text FROM devices WHERE serial <> '' AND serial = ANY($1)`, serials)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
