package store

import (
	"context"
	"encoding/json"
	"time"
)

// Webhook delivery statuses.
const (
	DeliveryPending   = "pending"
	DeliveryDelivered = "delivered"
	DeliveryFailed    = "failed"
)

// Webhook is an outbound HTTP endpoint notified of device events.
type Webhook struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Secret    []byte    `json:"-"` // encrypted
	Events    []string  `json:"events"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// LastDelivery summarises the most recent delivery, for the console.
	LastDelivery *WebhookDelivery `json:"lastDelivery,omitempty"`
}

// WebhookDelivery is one attempt series to send one event to one webhook.
type WebhookDelivery struct {
	ID            int64           `json:"id"`
	WebhookID     string          `json:"webhookId"`
	EventID       int64           `json:"eventId"`
	Event         string          `json:"event"`
	DeviceID      *string         `json:"deviceId,omitempty"`
	Status        string          `json:"status"`
	Attempts      int             `json:"attempts"`
	NextAttemptAt time.Time       `json:"nextAttemptAt"`
	ResponseCode  *int            `json:"responseCode,omitempty"`
	LastError     string          `json:"lastError,omitempty"`
	CreatedAt     time.Time       `json:"createdAt"`
	DeliveredAt   *time.Time      `json:"deliveredAt,omitempty"`
	EventAt       time.Time       `json:"eventAt"`
	Payload       json.RawMessage `json:"payload,omitempty"`
}

const webhookCols = `id, name, url, secret, events, enabled, created_at, updated_at`

func scanWebhook(row interface{ Scan(...any) error }) (*Webhook, error) {
	var w Webhook
	err := row.Scan(&w.ID, &w.Name, &w.URL, &w.Secret, &w.Events, &w.Enabled, &w.CreatedAt, &w.UpdatedAt)
	return &w, notFound(err)
}

// CreateWebhook stores a webhook. secret must already be encrypted.
func (s *Store) CreateWebhook(ctx context.Context, w *Webhook) (*Webhook, error) {
	return scanWebhook(s.DB.QueryRow(ctx, `INSERT INTO webhooks (name, url, secret, events, enabled)
		VALUES ($1, $2, $3, $4, $5) RETURNING `+webhookCols, w.Name, w.URL, w.Secret, w.Events, w.Enabled))
}

// UpdateWebhook replaces a webhook's settings, keeping its secret.
func (s *Store) UpdateWebhook(ctx context.Context, w *Webhook) (*Webhook, error) {
	return scanWebhook(s.DB.QueryRow(ctx, `UPDATE webhooks SET name = $2, url = $3, events = $4, enabled = $5, updated_at = now()
		WHERE id = $1 RETURNING `+webhookCols, w.ID, w.Name, w.URL, w.Events, w.Enabled))
}

// SetWebhookSecret replaces the encrypted signing secret.
func (s *Store) SetWebhookSecret(ctx context.Context, id string, secret []byte) error {
	return s.exec1(ctx, `UPDATE webhooks SET secret = $2, updated_at = now() WHERE id = $1`, id, secret)
}

// WebhookByID fetches a webhook.
func (s *Store) WebhookByID(ctx context.Context, id string) (*Webhook, error) {
	return scanWebhook(s.DB.QueryRow(ctx, `SELECT `+webhookCols+` FROM webhooks WHERE id = $1`, id))
}

// ListWebhooks returns every webhook with its latest delivery, in one query.
func (s *Store) ListWebhooks(ctx context.Context) ([]*Webhook, error) {
	rows, err := s.DB.Query(ctx, `SELECT w.id, w.name, w.url, w.secret, w.events, w.enabled, w.created_at, w.updated_at,
			d.id, d.event_id, e.event, e.device_id, d.status, d.attempts, d.next_attempt_at,
			d.response_code, d.last_error, d.created_at, d.delivered_at, e.created_at
		FROM webhooks w
		LEFT JOIN LATERAL (SELECT * FROM webhook_deliveries WHERE webhook_id = w.id ORDER BY id DESC LIMIT 1) d ON true
		LEFT JOIN webhook_events e ON e.id = d.event_id
		ORDER BY w.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Webhook
	for rows.Next() {
		var w Webhook
		var d struct {
			ID, EventID              *int64
			Event, Status, LastError *string
			DeviceID                 *string
			Attempts, Code           *int
			Next, Created, EventAt   *time.Time
			Delivered                *time.Time
		}
		if err := rows.Scan(&w.ID, &w.Name, &w.URL, &w.Secret, &w.Events, &w.Enabled, &w.CreatedAt, &w.UpdatedAt,
			&d.ID, &d.EventID, &d.Event, &d.DeviceID, &d.Status, &d.Attempts, &d.Next,
			&d.Code, &d.LastError, &d.Created, &d.Delivered, &d.EventAt); err != nil {
			return nil, err
		}
		if d.ID != nil {
			w.LastDelivery = &WebhookDelivery{ID: *d.ID, WebhookID: w.ID, EventID: *d.EventID, Event: *d.Event,
				DeviceID: d.DeviceID, Status: *d.Status, Attempts: *d.Attempts, NextAttemptAt: *d.Next,
				ResponseCode: d.Code, LastError: *d.LastError, CreatedAt: *d.Created, DeliveredAt: d.Delivered, EventAt: *d.EventAt}
		}
		out = append(out, &w)
	}
	return out, rows.Err()
}

// DeleteWebhook removes a webhook and its deliveries.
func (s *Store) DeleteWebhook(ctx context.Context, id string) error {
	return s.exec1(ctx, `DELETE FROM webhooks WHERE id = $1`, id)
}

const deliveryCols = `d.id, d.webhook_id, d.event_id, e.event, e.device_id, d.status, d.attempts, d.next_attempt_at,
	d.response_code, d.last_error, d.created_at, d.delivered_at, e.created_at, e.payload`

func scanDelivery(row interface{ Scan(...any) error }) (*WebhookDelivery, error) {
	var d WebhookDelivery
	err := row.Scan(&d.ID, &d.WebhookID, &d.EventID, &d.Event, &d.DeviceID, &d.Status, &d.Attempts, &d.NextAttemptAt,
		&d.ResponseCode, &d.LastError, &d.CreatedAt, &d.DeliveredAt, &d.EventAt, &d.Payload)
	return &d, notFound(err)
}

// ListWebhookDeliveries returns a webhook's deliveries, newest first, before
// the given delivery ID (0 for the newest).
func (s *Store) ListWebhookDeliveries(ctx context.Context, webhookID string, before int64, limit int) ([]*WebhookDelivery, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.DB.Query(ctx, `SELECT `+deliveryCols+` FROM webhook_deliveries d JOIN webhook_events e ON e.id = d.event_id
		WHERE d.webhook_id = $1 AND ($2 = 0 OR d.id < $2) ORDER BY d.id DESC LIMIT $3`, webhookID, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*WebhookDelivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// AddWebhookEvent records an event and queues it for one webhook only (used
// for test pings, which must not reach other endpoints).
func (s *Store) AddWebhookEvent(ctx context.Context, webhookID, event string, payload any) (*WebhookDelivery, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var id int64
	err = s.DB.QueryRow(ctx, `WITH ev AS (INSERT INTO webhook_events (event, payload) VALUES ($2, $3) RETURNING id)
		INSERT INTO webhook_deliveries (webhook_id, event_id) SELECT $1, id FROM ev RETURNING id`, webhookID, event, b).Scan(&id)
	if err != nil {
		return nil, notFound(err)
	}
	return scanDelivery(s.DB.QueryRow(ctx, `SELECT `+deliveryCols+` FROM webhook_deliveries d
		JOIN webhook_events e ON e.id = d.event_id WHERE d.id = $1`, id))
}

// FailPendingDeliveries marks a webhook's pending deliveries failed, so a
// webhook enabled again later does not replay a stale backlog. They can still
// be retried by hand.
func (s *Store) FailPendingDeliveries(ctx context.Context, webhookID, reason string) error {
	_, err := s.DB.Exec(ctx, `UPDATE webhook_deliveries SET status = 'failed', last_error = $2, leased_until = NULL
		WHERE webhook_id = $1 AND status = 'pending'`, webhookID, reason)
	return err
}

// RetryWebhookDelivery makes a pending or failed delivery due now with a fresh
// attempt budget. It returns ErrNotFound for an unknown delivery, one already
// delivered, or one being sent right now (so two senders never race).
func (s *Store) RetryWebhookDelivery(ctx context.Context, webhookID string, id int64) error {
	return s.exec1(ctx, `UPDATE webhook_deliveries SET status = 'pending', next_attempt_at = now(), attempts = 0, last_error = ''
		WHERE id = $1 AND webhook_id = $2 AND status <> 'delivered' AND (leased_until IS NULL OR leased_until < now())`, id, webhookID)
}

// DueDelivery is a claimed delivery with what is needed to send it.
type DueDelivery struct {
	WebhookDelivery
	URL    string
	Secret []byte // encrypted
}

// ClaimDueDeliveries leases up to limit due deliveries for lease, so other
// replicas skip them while they are sent. Each claim counts as an attempt.
//
// Events for one device reach one webhook in order: a delivery is not due
// while an older one for the same device is still pending and waiting (backing
// off, or leased by another sender). Older ones due now are claimed in the same
// batch, oldest first, and the dispatcher sends them in that order.
func (s *Store) ClaimDueDeliveries(ctx context.Context, limit int, lease time.Duration) ([]*DueDelivery, error) {
	rows, err := s.DB.Query(ctx, `WITH due AS (
			SELECT d.id FROM webhook_deliveries d JOIN webhooks w ON w.id = d.webhook_id
			WHERE d.status = 'pending' AND d.next_attempt_at <= now()
				AND (d.leased_until IS NULL OR d.leased_until < now()) AND w.enabled
				AND NOT EXISTS (SELECT 1 FROM webhook_deliveries o
					WHERE o.webhook_id = d.webhook_id AND o.device_id = d.device_id AND o.status = 'pending'
						AND o.event_id < d.event_id
						AND (o.next_attempt_at > now() OR o.leased_until >= now()))
			ORDER BY d.event_id LIMIT $1 FOR UPDATE OF d SKIP LOCKED),
		claimed AS (
			UPDATE webhook_deliveries d SET attempts = d.attempts + 1, leased_until = now() + $2 * interval '1 second'
			FROM due WHERE d.id = due.id RETURNING d.*)
		SELECT d.id, d.webhook_id, d.event_id, e.event, e.device_id, d.status, d.attempts, d.next_attempt_at,
			d.response_code, d.last_error, d.created_at, d.delivered_at, e.created_at, e.payload, w.url, w.secret
		FROM claimed d JOIN webhook_events e ON e.id = d.event_id JOIN webhooks w ON w.id = d.webhook_id`,
		limit, int(lease.Seconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*DueDelivery
	for rows.Next() {
		var d DueDelivery
		if err := rows.Scan(&d.ID, &d.WebhookID, &d.EventID, &d.Event, &d.DeviceID, &d.Status, &d.Attempts, &d.NextAttemptAt,
			&d.ResponseCode, &d.LastError, &d.CreatedAt, &d.DeliveredAt, &d.EventAt, &d.Payload, &d.URL, &d.Secret); err != nil {
			return nil, err
		}
		out = append(out, &d)
	}
	return out, rows.Err()
}

// FinishDelivery records the outcome of an attempt and ends the lease. A zero
// retryAt with a failure marks the delivery failed for good.
func (s *Store) FinishDelivery(ctx context.Context, id int64, code *int, errMsg string, retryAt time.Time) error {
	switch {
	case errMsg == "":
		return s.exec1(ctx, `UPDATE webhook_deliveries SET status = 'delivered', response_code = $2, last_error = '',
			delivered_at = now(), leased_until = NULL WHERE id = $1`, id, code)
	case retryAt.IsZero():
		return s.exec1(ctx, `UPDATE webhook_deliveries SET status = 'failed', response_code = $2, last_error = $3,
			leased_until = NULL WHERE id = $1`, id, code, errMsg)
	default:
		return s.exec1(ctx, `UPDATE webhook_deliveries SET response_code = $2, last_error = $3, next_attempt_at = $4,
			leased_until = NULL WHERE id = $1`, id, code, errMsg, retryAt)
	}
}

// ReleaseDelivery hands back a claimed delivery that was not attempted (an
// older event for the same device failed first), without using an attempt.
func (s *Store) ReleaseDelivery(ctx context.Context, id int64) error {
	return s.exec1(ctx, `UPDATE webhook_deliveries SET attempts = greatest(attempts - 1, 0), leased_until = NULL WHERE id = $1`, id)
}

// PruneWebhookEvents deletes events older than the cutoff that have nothing
// left to deliver.
func (s *Store) PruneWebhookEvents(ctx context.Context, cutoff time.Time) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM webhook_events e WHERE e.created_at < $1
		AND NOT EXISTS (SELECT 1 FROM webhook_deliveries d WHERE d.event_id = e.id AND d.status = 'pending')`, cutoff)
	return err
}
