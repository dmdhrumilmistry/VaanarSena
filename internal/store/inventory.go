package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// Inventory kinds.
const (
	KindApp     = "app"
	KindService = "service"
	KindProfile = "profile"
)

// Limits applied when inventory is stored, so a misbehaving device cannot
// grow the database without bound.
const (
	MaxInventoryItems  = 5000
	maxInvName         = 256
	maxInvIdentifier   = 512
	maxInvVersion      = 128
	maxInvPublisher    = 256
	maxInvLabel        = 32
	maxInvDetailsBytes = 4096
)

// ValidInventoryKind reports whether kind is one of the inventory kinds.
func ValidInventoryKind(kind string) bool {
	return kind == KindApp || kind == KindService || kind == KindProfile
}

// InventoryItem is one application, service or configuration profile.
type InventoryItem struct {
	Name       string          `json:"name"`
	Identifier string          `json:"identifier"`
	Version    string          `json:"version"`
	Publisher  string          `json:"publisher"`
	Source     string          `json:"source"`
	State      string          `json:"state"`
	Managed    bool            `json:"managed"`
	Details    json.RawMessage `json:"details"`
}

// InventorySync records when a kind of inventory was last collected.
type InventorySync struct {
	CollectedAt time.Time `json:"collectedAt"`
	Count       int       `json:"count"`
}

// truncate shortens s to at most n bytes without splitting a rune, and drops
// NUL bytes, which PostgreSQL text cannot hold.
func truncate(s string, n int) string {
	s = strings.ToValidUTF8(strings.ReplaceAll(s, "\x00", ""), "")
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 {
		if r, size := utf8.DecodeLastRuneInString(s); r != utf8.RuneError || size != 1 {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}

func (it *InventoryItem) clean() {
	it.Name = truncate(strings.TrimSpace(it.Name), maxInvName)
	it.Identifier = truncate(strings.TrimSpace(it.Identifier), maxInvIdentifier)
	it.Version = truncate(strings.TrimSpace(it.Version), maxInvVersion)
	it.Publisher = truncate(strings.TrimSpace(it.Publisher), maxInvPublisher)
	it.Source = truncate(it.Source, maxInvLabel)
	it.State = truncate(it.State, maxInvLabel)
	if len(it.Details) == 0 || len(it.Details) > maxInvDetailsBytes || !json.Valid(it.Details) {
		it.Details = json.RawMessage("{}")
	}
}

// ReplaceInventory atomically swaps a device's items of one kind for items
// and records the collection time. Items beyond MaxInventoryItems are dropped
// and items without a name are skipped.
func (s *Store) ReplaceInventory(ctx context.Context, deviceID, kind string, items []InventoryItem) error {
	rows := make([][]any, 0, len(items))
	for i := range items {
		it := items[i]
		it.clean()
		if it.Name == "" {
			continue
		}
		if len(rows) == MaxInventoryItems {
			break
		}
		rows = append(rows, []any{deviceID, kind, it.Name, it.Identifier, it.Version, it.Publisher, it.Source, it.State, it.Managed, []byte(it.Details)})
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `DELETE FROM device_inventory WHERE device_id = $1 AND kind = $2`, deviceID, kind); err != nil {
		return err
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"device_inventory"},
		[]string{"device_id", "kind", "name", "identifier", "version", "publisher", "source", "state", "managed", "details"},
		pgx.CopyFromRows(rows)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO device_inventory_sync (device_id, kind, collected_at, item_count) VALUES ($1, $2, now(), $3)
		ON CONFLICT (device_id, kind) DO UPDATE SET collected_at = now(), item_count = EXCLUDED.item_count`,
		deviceID, kind, len(rows)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeleteInventory removes a device's inventory of the given kinds (all kinds
// if none are given), for example when a device becomes personal.
func (s *Store) DeleteInventory(ctx context.Context, deviceID string, kinds ...string) error {
	if len(kinds) == 0 {
		kinds = []string{KindApp, KindService, KindProfile}
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `DELETE FROM device_inventory WHERE device_id = $1 AND kind = ANY($2)`, deviceID, kinds); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM device_inventory_sync WHERE device_id = $1 AND kind = ANY($2)`, deviceID, kinds); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// likePattern builds a case-insensitive substring pattern, escaping wildcards.
func likePattern(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}

func clampPage(limit, offset, def, max int) (int, int) {
	if limit <= 0 {
		limit = def
	}
	if limit > max {
		limit = max
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// DeviceInventory lists a device's items of one kind, ordered by name. q
// matches name, identifier or publisher case-insensitively. total is the
// number of matches before paging.
func (s *Store) DeviceInventory(ctx context.Context, deviceID, kind, q string, limit, offset int) ([]InventoryItem, int, error) {
	limit, offset = clampPage(limit, offset, 200, 1000)
	pat := likePattern(q)
	const cond = `device_id = $1 AND kind = $2 AND (name ILIKE $3 OR identifier ILIKE $3 OR publisher ILIKE $3)`
	var total int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM device_inventory WHERE `+cond, deviceID, kind, pat).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.DB.Query(ctx, `SELECT name, identifier, version, publisher, source, state, managed, details
		FROM device_inventory WHERE `+cond+` ORDER BY lower(name), identifier, id LIMIT $4 OFFSET $5`,
		deviceID, kind, pat, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []InventoryItem{}
	for rows.Next() {
		var it InventoryItem
		if err := rows.Scan(&it.Name, &it.Identifier, &it.Version, &it.Publisher, &it.Source, &it.State, &it.Managed, &it.Details); err != nil {
			return nil, 0, err
		}
		out = append(out, it)
	}
	return out, total, rows.Err()
}

// InventorySync returns when each kind was last collected for a device.
func (s *Store) InventorySync(ctx context.Context, deviceID string) (map[string]InventorySync, error) {
	rows, err := s.DB.Query(ctx, `SELECT kind, collected_at, item_count FROM device_inventory_sync WHERE device_id = $1`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]InventorySync{}
	for rows.Next() {
		var kind string
		var sy InventorySync
		if err := rows.Scan(&kind, &sy.CollectedAt, &sy.Count); err != nil {
			return nil, err
		}
		out[kind] = sy
	}
	return out, rows.Err()
}

// SoftwareVersion is a version of a piece of software and how many devices
// run it.
type SoftwareVersion struct {
	Version string `json:"version"`
	Devices int    `json:"devices"`
}

// FleetSoftwareRow is one piece of software across the fleet.
// Rows are grouped by name (case-insensitive), so the same software installed
// through different package managers or platforms counts once; Identifier and
// Source are the most common ones and Sources lists them all.
type FleetSoftwareRow struct {
	Name       string            `json:"name"`
	Identifier string            `json:"identifier"`
	Source     string            `json:"source"`
	Sources    []string          `json:"sources"`
	Devices    int               `json:"devices"`
	Versions   []SoftwareVersion `json:"versions"`
}

// fleetDevices limits fleet queries to devices that are still managed and
// whose inventory may be shown: personal Linux and Windows devices never
// contribute, even if rows were stored before the device became personal.
const fleetDevices = `d.status NOT IN ('retired', 'wiped') AND NOT (d.ownership = 'personal' AND d.platform IN ('linux', 'windows'))`

// FleetSoftware groups items of one kind across devices by (name, identifier,
// source), most widespread first. Each row lists up to five versions, most
// common first.
func (s *Store) FleetSoftware(ctx context.Context, kind, q string, limit, offset int) ([]FleetSoftwareRow, int, error) {
	limit, offset = clampPage(limit, offset, 100, 500)
	pat := likePattern(q)
	rows, err := s.DB.Query(ctx, `SELECT min(i.name), mode() WITHIN GROUP (ORDER BY i.identifier), mode() WITHIN GROUP (ORDER BY i.source),
			array_agg(DISTINCT i.source ORDER BY i.source), count(DISTINCT i.device_id) AS n, count(*) OVER ()
		FROM device_inventory i JOIN devices d ON d.id = i.device_id
		WHERE i.kind = $1 AND `+fleetDevices+` AND (i.name ILIKE $2 OR i.identifier ILIKE $2 OR i.publisher ILIKE $2)
		GROUP BY lower(i.name)
		ORDER BY n DESC, lower(min(i.name)) LIMIT $3 OFFSET $4`, kind, pat, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []FleetSoftwareRow{}
	total := 0
	var names []string
	for rows.Next() {
		var r FleetSoftwareRow
		if err := rows.Scan(&r.Name, &r.Identifier, &r.Source, &r.Sources, &r.Devices, &total); err != nil {
			return nil, 0, err
		}
		r.Versions = []SoftwareVersion{}
		out = append(out, r)
		names = append(names, strings.ToLower(r.Name))
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	rows.Close()
	if len(out) == 0 {
		return out, total, nil
	}
	vrows, err := s.DB.Query(ctx, `SELECT lower(i.name), i.version, count(DISTINCT i.device_id) AS n
		FROM device_inventory i JOIN devices d ON d.id = i.device_id
		WHERE i.kind = $1 AND `+fleetDevices+` AND lower(i.name) = ANY($2::text[])
		GROUP BY 1, 2 ORDER BY n DESC, i.version`, kind, names)
	if err != nil {
		return nil, 0, err
	}
	defer vrows.Close()
	index := map[string]int{}
	for i := range out {
		index[names[i]] = i
	}
	for vrows.Next() {
		var name string
		var v SoftwareVersion
		if err := vrows.Scan(&name, &v.Version, &v.Devices); err != nil {
			return nil, 0, err
		}
		if i, ok := index[name]; ok && len(out[i].Versions) < 5 {
			out[i].Versions = append(out[i].Versions, v)
		}
	}
	return out, total, vrows.Err()
}

// SoftwareDevice is a device that has a piece of software.
type SoftwareDevice struct {
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
	Platform   string `json:"platform"`
	Ownership  string `json:"ownership"`
	Version    string `json:"version"`
}

// SoftwareDevices lists the devices that have the item identified by
// identifier and name, with the version each has. An empty version matches
// every version.
func (s *Store) SoftwareDevices(ctx context.Context, kind, identifier, name, version string, limit, offset int) ([]SoftwareDevice, int, error) {
	limit, offset = clampPage(limit, offset, 100, 500)
	// identifier is optional: fleet rows group by name across sources.
	const cond = `i.kind = $1 AND ($2 = '' OR i.identifier = $2) AND lower(i.name) = lower($3) AND ($4 = '' OR i.version = $4) AND ` + fleetDevices
	var total int
	if err := s.DB.QueryRow(ctx, `SELECT count(DISTINCT i.device_id) FROM device_inventory i JOIN devices d ON d.id = i.device_id WHERE `+cond,
		kind, identifier, name, version).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.DB.Query(ctx, `SELECT d.id, d.name, d.platform, d.ownership, min(i.version)
		FROM device_inventory i JOIN devices d ON d.id = i.device_id WHERE `+cond+`
		GROUP BY d.id, d.name, d.platform, d.ownership ORDER BY lower(d.name), d.id LIMIT $5 OFFSET $6`,
		kind, identifier, name, version, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []SoftwareDevice{}
	for rows.Next() {
		var d SoftwareDevice
		if err := rows.Scan(&d.DeviceID, &d.DeviceName, &d.Platform, &d.Ownership, &d.Version); err != nil {
			return nil, 0, err
		}
		out = append(out, d)
	}
	return out, total, rows.Err()
}

// SoftwareStats summarises inventory for the dashboard.
type SoftwareStats struct {
	Apps             int `json:"apps"`
	DevicesReporting int `json:"devicesReporting"`
}

func (s *Store) softwareStats(ctx context.Context) (SoftwareStats, error) {
	var st SoftwareStats
	err := s.DB.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM (SELECT 1 FROM device_inventory i JOIN devices d ON d.id = i.device_id
			WHERE i.kind = 'app' AND `+fleetDevices+` GROUP BY lower(i.name)) g),
		(SELECT count(*) FROM device_inventory_sync y JOIN devices d ON d.id = y.device_id
			WHERE y.kind = 'app' AND `+fleetDevices+`)`).Scan(&st.Apps, &st.DevicesReporting)
	return st, err
}
