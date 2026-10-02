package store

import (
	"context"
	"encoding/json"
	"time"
)

const userCols = `id, email, name, password_hash, role, disabled, created_at, last_login_at`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.Disabled, &u.CreatedAt, &u.LastLoginAt)
	return &u, notFound(err)
}

// CreateUser inserts a user.
func (s *Store) CreateUser(ctx context.Context, email, name, passwordHash, role string) (*User, error) {
	return scanUser(s.DB.QueryRow(ctx, `INSERT INTO users (email, name, password_hash, role)
		VALUES (lower($1), $2, $3, $4) RETURNING `+userCols, email, name, passwordHash, role))
}

// UserByEmail looks up a user by email, case-insensitively.
func (s *Store) UserByEmail(ctx context.Context, email string) (*User, error) {
	return scanUser(s.DB.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE email = lower($1)`, email))
}

// UserByID looks up a user by ID.
func (s *Store) UserByID(ctx context.Context, id string) (*User, error) {
	return scanUser(s.DB.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

// ListUsers returns all users.
func (s *Store) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+userCols+` FROM users ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdateUser changes a user's name, role and disabled flag.
func (s *Store) UpdateUser(ctx context.Context, id, name, role string, disabled bool) (*User, error) {
	return scanUser(s.DB.QueryRow(ctx, `UPDATE users SET name = $2, role = $3, disabled = $4
		WHERE id = $1 RETURNING `+userCols, id, name, role, disabled))
}

// SetPassword replaces a user's password hash.
func (s *Store) SetPassword(ctx context.Context, id, hash string) error {
	return s.exec1(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, id, hash)
}

// DeleteUser removes a user.
func (s *Store) DeleteUser(ctx context.Context, id string) error {
	return s.exec1(ctx, `DELETE FROM users WHERE id = $1`, id)
}

// TouchLogin records a successful login.
func (s *Store) TouchLogin(ctx context.Context, id string) error {
	_, err := s.DB.Exec(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, id)
	return err
}

// CountAdmins returns the number of enabled admins.
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRow(ctx, `SELECT count(*) FROM users WHERE role = 'admin' AND NOT disabled`).Scan(&n)
	return n, err
}

// CreateAPIToken stores a hashed API token.
func (s *Store) CreateAPIToken(ctx context.Context, userID, name, hash string, expires *time.Time) (*APIToken, error) {
	var t APIToken
	err := s.DB.QueryRow(ctx, `INSERT INTO api_tokens (user_id, name, token_hash, expires_at) VALUES ($1, $2, $3, $4)
		RETURNING id, user_id, name, created_at, expires_at, last_used_at`, userID, name, hash, expires).
		Scan(&t.ID, &t.UserID, &t.Name, &t.CreatedAt, &t.ExpiresAt, &t.LastUsedAt)
	return &t, err
}

// UserByAPIToken resolves a token hash to its (enabled, unexpired) user.
func (s *Store) UserByAPIToken(ctx context.Context, hash string) (*User, error) {
	u, err := scanUser(s.DB.QueryRow(ctx, `UPDATE api_tokens t SET last_used_at = now()
		FROM users u WHERE t.token_hash = $1 AND t.user_id = u.id AND NOT u.disabled
		AND (t.expires_at IS NULL OR t.expires_at > now())
		RETURNING u.id, u.email, u.name, u.password_hash, u.role, u.disabled, u.created_at, u.last_login_at`, hash))
	return u, err
}

// ListAPITokens lists a user's tokens.
func (s *Store) ListAPITokens(ctx context.Context, userID string) ([]*APIToken, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, user_id, name, created_at, expires_at, last_used_at
		FROM api_tokens WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*APIToken
	for rows.Next() {
		var t APIToken
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.CreatedAt, &t.ExpiresAt, &t.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}

// DeleteAPIToken revokes one of a user's tokens.
func (s *Store) DeleteAPIToken(ctx context.Context, userID, id string) error {
	return s.exec1(ctx, `DELETE FROM api_tokens WHERE id = $1 AND user_id = $2`, id, userID)
}

// Audit appends an audit record. Failures are returned so callers can decide;
// the API layer treats audit failure as request failure.
func (s *Store) Audit(ctx context.Context, actor, action, target string, details any, remoteIP string) error {
	b, err := json.Marshal(details)
	if err != nil || details == nil {
		b = []byte("{}")
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO audit_log (actor, action, target, details, remote_ip) VALUES ($1, $2, $3, $4, $5)`,
		actor, action, target, b, remoteIP)
	return err
}

// ListAudit returns audit entries, newest first, before the given ID (0 = latest).
func (s *Store) ListAudit(ctx context.Context, before int64, limit int) ([]*AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.DB.Query(ctx, `SELECT id, at, actor, action, target, details, remote_ip FROM audit_log
		WHERE ($1 = 0 OR id < $1) ORDER BY id DESC LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.At, &e.Actor, &e.Action, &e.Target, &e.Details, &e.RemoteIP); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// GetSetting reads a raw setting value.
func (s *Store) GetSetting(ctx context.Context, key string) (value []byte, encrypted bool, err error) {
	err = s.DB.QueryRow(ctx, `SELECT value, encrypted FROM settings WHERE key = $1`, key).Scan(&value, &encrypted)
	return value, encrypted, notFound(err)
}

// PutSetting upserts a setting.
func (s *Store) PutSetting(ctx context.Context, key string, value []byte, encrypted bool) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO settings (key, value, encrypted) VALUES ($1, $2, $3)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, encrypted = EXCLUDED.encrypted, updated_at = now()`,
		key, value, encrypted)
	return err
}

// DeleteSetting removes a setting; a missing key is not an error.
func (s *Store) DeleteSetting(ctx context.Context, key string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM settings WHERE key = $1`, key)
	return err
}

// InsertSettingIfAbsent stores a setting only if the key is new and reports
// whether it was inserted. Used for first-boot generation races.
func (s *Store) InsertSettingIfAbsent(ctx context.Context, key string, value []byte, encrypted bool) (bool, error) {
	tag, err := s.DB.Exec(ctx, `INSERT INTO settings (key, value, encrypted) VALUES ($1, $2, $3) ON CONFLICT (key) DO NOTHING`,
		key, value, encrypted)
	return tag.RowsAffected() == 1, err
}

func (s *Store) exec1(ctx context.Context, sql string, args ...any) error {
	tag, err := s.DB.Exec(ctx, sql, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
