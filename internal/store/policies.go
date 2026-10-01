package store

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
)

// --- groups ---

// CreateGroup inserts a group.
func (s *Store) CreateGroup(ctx context.Context, name, desc string) (*Group, error) {
	var g Group
	err := s.DB.QueryRow(ctx, `INSERT INTO groups (name, description) VALUES ($1, $2) RETURNING id, name, description, created_at`,
		name, desc).Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt)
	return &g, err
}

// ListGroups returns groups with member counts.
func (s *Store) ListGroups(ctx context.Context) ([]*Group, error) {
	rows, err := s.DB.Query(ctx, `SELECT g.id, g.name, g.description, g.created_at, count(m.device_id)
		FROM groups g LEFT JOIN group_members m ON m.group_id = g.id GROUP BY g.id ORDER BY g.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt, &g.DeviceCount); err != nil {
			return nil, err
		}
		out = append(out, &g)
	}
	return out, rows.Err()
}

// DeleteGroup removes a group.
func (s *Store) DeleteGroup(ctx context.Context, id string) error {
	return s.exec1(ctx, `DELETE FROM groups WHERE id = $1`, id)
}

// --- policies ---

const policyCols = `p.id, p.name, p.description, p.priority, p.document, p.version, p.created_at, p.updated_at,
	COALESCE(array_agg(a.group_id) FILTER (WHERE a.group_id IS NOT NULL), '{}')::text[],
	COALESCE(array_agg(a.device_id) FILTER (WHERE a.device_id IS NOT NULL), '{}')::text[]`

func scanPolicy(row interface{ Scan(...any) error }) (*Policy, error) {
	var p Policy
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.Priority, &p.Document, &p.Version, &p.CreatedAt, &p.UpdatedAt,
		&p.GroupIDs, &p.DeviceIDs)
	return &p, notFound(err)
}

// SavePolicy creates or updates a policy and replaces its assignments.
func (s *Store) SavePolicy(ctx context.Context, p *Policy) (*Policy, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if p.ID == "" {
		err = tx.QueryRow(ctx, `INSERT INTO policies (name, description, priority, document) VALUES ($1, $2, $3, $4) RETURNING id`,
			p.Name, p.Description, p.Priority, p.Document).Scan(&p.ID)
	} else {
		var tag interface{ RowsAffected() int64 }
		tag, err = tx.Exec(ctx, `UPDATE policies SET name = $2, description = $3, priority = $4, document = $5,
			version = version + 1, updated_at = now() WHERE id = $1`, p.ID, p.Name, p.Description, p.Priority, p.Document)
		if err == nil && tag.RowsAffected() == 0 {
			return nil, ErrNotFound
		}
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM policy_assignments WHERE policy_id = $1`, p.ID); err != nil {
		return nil, err
	}
	for _, g := range p.GroupIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO policy_assignments (policy_id, group_id) VALUES ($1, $2)`, p.ID, g); err != nil {
			return nil, err
		}
	}
	for _, d := range p.DeviceIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO policy_assignments (policy_id, device_id) VALUES ($1, $2)`, p.ID, d); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.PolicyByID(ctx, p.ID)
}

// PolicyByID fetches a policy with its assignments.
func (s *Store) PolicyByID(ctx context.Context, id string) (*Policy, error) {
	return scanPolicy(s.DB.QueryRow(ctx, `SELECT `+policyCols+` FROM policies p
		LEFT JOIN policy_assignments a ON a.policy_id = p.id WHERE p.id = $1 GROUP BY p.id`, id))
}

// ListPolicies returns all policies.
func (s *Store) ListPolicies(ctx context.Context) ([]*Policy, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+policyCols+` FROM policies p
		LEFT JOIN policy_assignments a ON a.policy_id = p.id GROUP BY p.id ORDER BY p.priority, p.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Policy
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeletePolicy removes a policy.
func (s *Store) DeletePolicy(ctx context.Context, id string) error {
	return s.exec1(ctx, `DELETE FROM policies WHERE id = $1`, id)
}

// EffectivePolicies returns the documents that apply to a device, ordered so
// that later entries take precedence (highest priority number first, so the
// lowest number, the most important, is merged last).
func (s *Store) EffectivePolicies(ctx context.Context, deviceID string) ([]*Policy, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+policyCols+` FROM policies p
		JOIN policy_assignments a ON a.policy_id = p.id
		WHERE a.device_id = $1 OR a.group_id IN (SELECT group_id FROM group_members WHERE device_id = $1)
		GROUP BY p.id ORDER BY p.priority DESC, p.name`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Policy
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DevicesForPolicy lists devices a policy currently applies to.
func (s *Store) DevicesForPolicy(ctx context.Context, policyID string) ([]string, error) {
	rows, err := s.DB.Query(ctx, `SELECT DISTINCT d.id::text FROM devices d
		JOIN policy_assignments a ON a.policy_id = $1
		WHERE d.status = 'enrolled' AND (a.device_id = d.id OR a.group_id IN (SELECT group_id FROM group_members WHERE device_id = d.id))`,
		policyID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// --- commands ---

const cmdCols = `id, device_id, type, params, status, native_request, native_response, error, created_by, created_at, sent_at, completed_at`

func scanCmd(row interface{ Scan(...any) error }) (*Command, error) {
	var c Command
	err := row.Scan(&c.ID, &c.DeviceID, &c.Type, &c.Params, &c.Status, &c.NativeRequest, &c.NativeResponse, &c.Error,
		&c.CreatedBy, &c.CreatedAt, &c.SentAt, &c.CompletedAt)
	return &c, notFound(err)
}

// EnqueueCommand adds a command to a device's queue.
func (s *Store) EnqueueCommand(ctx context.Context, deviceID, typ string, params json.RawMessage, createdBy *string) (*Command, error) {
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	return scanCmd(s.DB.QueryRow(ctx, `INSERT INTO commands (device_id, type, params, created_by) VALUES ($1, $2, $3, $4) RETURNING `+cmdCols,
		deviceID, typ, params, createdBy))
}

// CommandByID fetches a command.
func (s *Store) CommandByID(ctx context.Context, id string) (*Command, error) {
	return scanCmd(s.DB.QueryRow(ctx, `SELECT `+cmdCols+` FROM commands WHERE id = $1`, id))
}

// NextCommand returns the oldest command the device should process next. At
// the start of a session (the device reported Idle) deferred and unanswered
// commands are retried too; mid-session only fresh ones are, so a NotNow
// reply does not loop. It does not change state.
func (s *Store) NextCommand(ctx context.Context, deviceID string, sessionStart bool) (*Command, error) {
	statuses := []string{CmdQueued}
	if sessionStart {
		statuses = []string{CmdQueued, CmdNotNow, CmdSent}
	}
	return scanCmd(s.DB.QueryRow(ctx, `SELECT `+cmdCols+` FROM commands
		WHERE device_id = $1 AND status = ANY($2) ORDER BY created_at LIMIT 1`, deviceID, statuses))
}

// PendingCommands returns all queued commands for a device and marks them
// sent, for protocols that batch (OMA-DM, the Linux agent).
func (s *Store) PendingCommands(ctx context.Context, deviceID string, limit int) ([]*Command, error) {
	rows, err := s.DB.Query(ctx, `UPDATE commands SET status = 'sent', sent_at = now()
		WHERE id IN (SELECT id FROM commands WHERE device_id = $1 AND status IN ('queued', 'not_now')
		             ORDER BY created_at LIMIT $2 FOR UPDATE SKIP LOCKED)
		RETURNING `+cmdCols, deviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Command
	for rows.Next() {
		c, err := scanCmd(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SentCommands lists a device's commands awaiting a response.
func (s *Store) SentCommands(ctx context.Context, deviceID string) ([]*Command, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+cmdCols+` FROM commands WHERE device_id = $1 AND status = 'sent' ORDER BY created_at`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Command
	for rows.Next() {
		c, err := scanCmd(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RequeueSent returns unanswered commands to the queue.
func (s *Store) RequeueSent(ctx context.Context, deviceID string) error {
	_, err := s.DB.Exec(ctx, `UPDATE commands SET status = 'queued' WHERE device_id = $1 AND status = 'sent'`, deviceID)
	return err
}

// MarkCommandSent records the native request that was delivered.
func (s *Store) MarkCommandSent(ctx context.Context, id, native string) error {
	return s.exec1(ctx, `UPDATE commands SET status = 'sent', sent_at = now(), native_request = $2 WHERE id = $1`, id, native)
}

// CompleteCommand records a device's response to a command.
func (s *Store) CompleteCommand(ctx context.Context, id, deviceID, status, response, errMsg string) error {
	q := `UPDATE commands SET status = $3, native_response = $4, error = $5,
		completed_at = CASE WHEN $3 IN ('acknowledged', 'error') THEN now() ELSE NULL END
		WHERE id = $1 AND device_id = $2`
	return s.exec1(ctx, q, id, deviceID, status, response, errMsg)
}

// CancelCommand cancels a command that has not completed.
func (s *Store) CancelCommand(ctx context.Context, id string) error {
	return s.exec1(ctx, `UPDATE commands SET status = 'cancelled', completed_at = now()
		WHERE id = $1 AND status IN ('queued', 'not_now', 'sent')`, id)
}

// ListCommands returns a device's commands, newest first.
func (s *Store) ListCommands(ctx context.Context, deviceID string, limit int) ([]*Command, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.DB.Query(ctx, `SELECT `+cmdCols+` FROM commands WHERE device_id = $1 ORDER BY created_at DESC LIMIT $2`, deviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Command
	for rows.Next() {
		c, err := scanCmd(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CancelPendingCommands cancels everything still queued for a device (used when
// a device is retired or wiped).
func (s *Store) CancelPendingCommands(ctx context.Context, deviceID string) error {
	_, err := s.DB.Exec(ctx, `UPDATE commands SET status = 'cancelled', completed_at = now()
		WHERE device_id = $1 AND status IN ('queued', 'not_now')`, deviceID)
	return err
}
