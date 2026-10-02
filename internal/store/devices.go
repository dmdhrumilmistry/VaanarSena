package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const deviceCols = `id, platform, ownership, status, name, serial, model, os_version, assignee, native_id,
	platform_ids, facts, compliant, tags, cert_serial, enrollment_token_id, enrolled_at, last_seen_at, created_at, updated_at`

func scanDevice(row interface{ Scan(...any) error }) (*Device, error) {
	var d Device
	err := row.Scan(&d.ID, &d.Platform, &d.Ownership, &d.Status, &d.Name, &d.Serial, &d.Model, &d.OSVersion,
		&d.Assignee, &d.NativeID, &d.PlatformIDs, &d.Facts, &d.Compliant, &d.Tags, &d.CertSerial, &d.EnrollmentTokenID,
		&d.EnrolledAt, &d.LastSeenAt, &d.CreatedAt, &d.UpdatedAt)
	return &d, notFound(err)
}

// DeviceFilter narrows ListDevices.
type DeviceFilter struct {
	Platform  string
	Ownership string
	Status    string
	GroupID   string
	Tag       string
	Search    string
	Limit     int
	Offset    int
}

// ListDevices returns devices matching the filter and the total match count.
func (s *Store) ListDevices(ctx context.Context, f DeviceFilter) ([]*Device, int, error) {
	var where []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.Platform != "" {
		add("platform = $%d", f.Platform)
	}
	if f.Ownership != "" {
		add("ownership = $%d", f.Ownership)
	}
	if f.Status != "" {
		add("status = $%d", f.Status)
	}
	if f.GroupID != "" {
		add("id IN (SELECT device_id FROM group_members WHERE group_id = $%d)", f.GroupID)
	}
	if f.Tag != "" {
		add("$%d = ANY(tags)", f.Tag)
	}
	if f.Search != "" {
		add("(name ILIKE $%[1]d OR serial ILIKE $%[1]d OR assignee ILIKE $%[1]d OR model ILIKE $%[1]d)", "%"+f.Search+"%")
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM devices`+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := s.DB.Query(ctx, fmt.Sprintf(`SELECT %s FROM devices%s ORDER BY last_seen_at DESC NULLS LAST, created_at DESC LIMIT $%d OFFSET $%d`,
		deviceCols, clause, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, d)
	}
	return out, total, rows.Err()
}

// DeviceByID fetches a device.
func (s *Store) DeviceByID(ctx context.Context, id string) (*Device, error) {
	return scanDevice(s.DB.QueryRow(ctx, `SELECT `+deviceCols+` FROM devices WHERE id = $1`, id))
}

// DeviceByNativeID fetches a device by its platform identity. platforms lists
// the acceptable platforms (Apple identities are shared by ios/ipados/macos).
func (s *Store) DeviceByNativeID(ctx context.Context, nativeID string, platforms ...string) (*Device, error) {
	return scanDevice(s.DB.QueryRow(ctx, `SELECT `+deviceCols+` FROM devices WHERE native_id = $1 AND platform = ANY($2)`,
		nativeID, platforms))
}

// DeviceByCertSerial resolves a device from its identity certificate serial.
func (s *Store) DeviceByCertSerial(ctx context.Context, serial string) (*Device, error) {
	return scanDevice(s.DB.QueryRow(ctx, `SELECT `+deviceCols+` FROM devices WHERE cert_serial = $1 AND status <> 'wiped'`, serial))
}

// UpsertDevice creates a device or updates the identity fields of an existing
// one with the same (platform, native_id). Re-enrolment of a known device
// resets it to the given status.
func (s *Store) UpsertDevice(ctx context.Context, d *Device) (*Device, error) {
	if len(d.PlatformIDs) == 0 {
		d.PlatformIDs = json.RawMessage("{}")
	}
	if len(d.Facts) == 0 {
		d.Facts = json.RawMessage("{}")
	}
	return scanDevice(s.DB.QueryRow(ctx, `INSERT INTO devices
		(platform, ownership, status, name, serial, model, os_version, assignee, native_id, platform_ids, facts,
		 cert_serial, enrollment_token_id, enrolled_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, now())
		ON CONFLICT (platform, native_id) DO UPDATE SET
			ownership = EXCLUDED.ownership, status = EXCLUDED.status,
			name = COALESCE(NULLIF(EXCLUDED.name, ''), devices.name),
			serial = COALESCE(NULLIF(EXCLUDED.serial, ''), devices.serial),
			model = COALESCE(NULLIF(EXCLUDED.model, ''), devices.model),
			os_version = COALESCE(NULLIF(EXCLUDED.os_version, ''), devices.os_version),
			assignee = COALESCE(NULLIF(EXCLUDED.assignee, ''), devices.assignee),
			platform_ids = devices.platform_ids || EXCLUDED.platform_ids,
			facts = devices.facts || EXCLUDED.facts,
			cert_serial = COALESCE(NULLIF(EXCLUDED.cert_serial, ''), devices.cert_serial),
			enrollment_token_id = COALESCE(EXCLUDED.enrollment_token_id, devices.enrollment_token_id),
			enrolled_at = COALESCE(EXCLUDED.enrolled_at, devices.enrolled_at),
			last_seen_at = now(), updated_at = now()
		RETURNING `+deviceCols,
		d.Platform, d.Ownership, d.Status, d.Name, d.Serial, d.Model, d.OSVersion, d.Assignee, d.NativeID,
		d.PlatformIDs, d.Facts, d.CertSerial, d.EnrollmentTokenID, d.EnrolledAt))
}

// DevicePatch holds optional device field updates.
type DevicePatch struct {
	Status      *string
	Name        *string
	Serial      *string
	Model       *string
	OSVersion   *string
	Assignee    *string
	Ownership   *string
	PlatformIDs map[string]any // merged into platform_ids
	Facts       map[string]any // merged into facts
	Compliant   *bool
	Tags        *[]string
	CertSerial  *string
	Seen        bool
	Enrolled    bool
}

// PatchDevice applies a partial update.
func (s *Store) PatchDevice(ctx context.Context, id string, p DevicePatch) (*Device, error) {
	sets := []string{"updated_at = now()"}
	args := []any{id}
	set := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	merge := func(col string, m map[string]any) error {
		b, err := json.Marshal(m)
		if err != nil {
			return err
		}
		args = append(args, b)
		sets = append(sets, fmt.Sprintf("%[1]s = %[1]s || $%[2]d::jsonb", col, len(args)))
		return nil
	}
	if p.Status != nil {
		set("status", *p.Status)
	}
	if p.Name != nil {
		set("name", *p.Name)
	}
	if p.Serial != nil {
		set("serial", *p.Serial)
	}
	if p.Model != nil {
		set("model", *p.Model)
	}
	if p.OSVersion != nil {
		set("os_version", *p.OSVersion)
	}
	if p.Assignee != nil {
		set("assignee", *p.Assignee)
	}
	if p.Ownership != nil {
		set("ownership", *p.Ownership)
	}
	if p.Compliant != nil {
		set("compliant", *p.Compliant)
	}
	if p.CertSerial != nil {
		set("cert_serial", *p.CertSerial)
	}
	if p.Tags != nil {
		set("tags", *p.Tags)
	}
	if p.PlatformIDs != nil {
		if err := merge("platform_ids", p.PlatformIDs); err != nil {
			return nil, err
		}
	}
	if p.Facts != nil {
		if err := merge("facts", p.Facts); err != nil {
			return nil, err
		}
	}
	if p.Seen {
		sets = append(sets, "last_seen_at = now()")
	}
	if p.Enrolled {
		sets = append(sets, "enrolled_at = COALESCE(enrolled_at, now())")
	}
	return scanDevice(s.DB.QueryRow(ctx, `UPDATE devices SET `+strings.Join(sets, ", ")+` WHERE id = $1 RETURNING `+deviceCols, args...))
}

// DeleteDevice removes a device record and its queue.
func (s *Store) DeleteDevice(ctx context.Context, id string) error {
	return s.exec1(ctx, `DELETE FROM devices WHERE id = $1`, id)
}

// DeviceStats summarises the fleet for the dashboard.
type DeviceStats struct {
	Total        int            `json:"total"`
	ByPlatform   map[string]int `json:"byPlatform"`
	ByOwnership  map[string]int `json:"byOwnership"`
	ByStatus     map[string]int `json:"byStatus"`
	NonCompliant int            `json:"nonCompliant"`
	StaleDays7   int            `json:"stale7d"`
	Software     SoftwareStats  `json:"software"`
}

// Stats computes fleet statistics.
func (s *Store) Stats(ctx context.Context) (*DeviceStats, error) {
	st := &DeviceStats{ByPlatform: map[string]int{}, ByOwnership: map[string]int{}, ByStatus: map[string]int{}}
	rows, err := s.DB.Query(ctx, `SELECT platform, ownership, status, count(*) FROM devices GROUP BY 1, 2, 3`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p, o, stt string
		var n int
		if err := rows.Scan(&p, &o, &stt, &n); err != nil {
			rows.Close()
			return nil, err
		}
		st.Total += n
		st.ByPlatform[p] += n
		st.ByOwnership[o] += n
		st.ByStatus[stt] += n
	}
	rows.Close()
	err = s.DB.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE compliant = false AND status = 'enrolled'),
		count(*) FILTER (WHERE status = 'enrolled' AND (last_seen_at IS NULL OR last_seen_at < now() - interval '7 days'))
		FROM devices`).Scan(&st.NonCompliant, &st.StaleDays7)
	if err != nil {
		return nil, err
	}
	st.Software, err = s.softwareStats(ctx)
	return st, err
}

// --- enrollment tokens ---

const etCols = `id, platform, ownership, group_id, assignee, max_uses, uses, expires_at, created_by, created_at, revoked, extra`

func scanET(row interface{ Scan(...any) error }) (*EnrollmentToken, error) {
	var t EnrollmentToken
	err := row.Scan(&t.ID, &t.Platform, &t.Ownership, &t.GroupID, &t.Assignee, &t.MaxUses, &t.Uses, &t.ExpiresAt,
		&t.CreatedBy, &t.CreatedAt, &t.Revoked, &t.Extra)
	return &t, notFound(err)
}

// CreateEnrollmentToken stores a hashed enrollment token.
func (s *Store) CreateEnrollmentToken(ctx context.Context, t *EnrollmentToken, hash string) (*EnrollmentToken, error) {
	if len(t.Extra) == 0 {
		t.Extra = json.RawMessage("{}")
	}
	return scanET(s.DB.QueryRow(ctx, `INSERT INTO enrollment_tokens
		(token_hash, platform, ownership, group_id, assignee, max_uses, expires_at, created_by, extra)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING `+etCols,
		hash, t.Platform, t.Ownership, t.GroupID, t.Assignee, t.MaxUses, t.ExpiresAt, t.CreatedBy, t.Extra))
}

// SetEnrollmentTokenExtra replaces the platform artefacts of a token.
func (s *Store) SetEnrollmentTokenExtra(ctx context.Context, id string, extra json.RawMessage) error {
	return s.exec1(ctx, `UPDATE enrollment_tokens SET extra = $2 WHERE id = $1`, id, extra)
}

// EnrollmentTokenByHash looks up a token by its hash.
func (s *Store) EnrollmentTokenByHash(ctx context.Context, hash string) (*EnrollmentToken, error) {
	return scanET(s.DB.QueryRow(ctx, `SELECT `+etCols+` FROM enrollment_tokens WHERE token_hash = $1`, hash))
}

// EnrollmentTokenByID looks up a token by ID.
func (s *Store) EnrollmentTokenByID(ctx context.Context, id string) (*EnrollmentToken, error) {
	return scanET(s.DB.QueryRow(ctx, `SELECT `+etCols+` FROM enrollment_tokens WHERE id = $1`, id))
}

// ConsumeEnrollmentToken atomically validates and uses a token. It returns
// ErrNotFound if the token is unknown, expired, revoked or exhausted.
func (s *Store) ConsumeEnrollmentToken(ctx context.Context, hash, platform string) (*EnrollmentToken, error) {
	return scanET(s.DB.QueryRow(ctx, `UPDATE enrollment_tokens SET uses = uses + 1
		WHERE token_hash = $1 AND platform = $2 AND NOT revoked AND uses < max_uses AND expires_at > now()
		RETURNING `+etCols, hash, platform))
}

// ListEnrollmentTokens returns tokens, newest first.
func (s *Store) ListEnrollmentTokens(ctx context.Context) ([]*EnrollmentToken, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+etCols+` FROM enrollment_tokens ORDER BY created_at DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*EnrollmentToken
	for rows.Next() {
		t, err := scanET(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeEnrollmentToken disables a token.
func (s *Store) RevokeEnrollmentToken(ctx context.Context, id string) error {
	return s.exec1(ctx, `UPDATE enrollment_tokens SET revoked = true WHERE id = $1`, id)
}

// DeviceGroups lists group IDs a device belongs to.
func (s *Store) DeviceGroups(ctx context.Context, deviceID string) ([]string, error) {
	rows, err := s.DB.Query(ctx, `SELECT group_id FROM group_members WHERE device_id = $1`, deviceID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// StaleDevices lists enrolled devices not seen since the cutoff.
func (s *Store) StaleDevices(ctx context.Context, cutoff time.Time) ([]*Device, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+deviceCols+` FROM devices WHERE status = 'enrolled' AND last_seen_at < $1`, cutoff)
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
