// Package webhook delivers signed HTTP notifications of device events, so an
// identity provider, VPN or proxy can act on them (conditional access: refuse a
// device that is not compliant) and other systems can follow the fleet.
//
// Events are raised by a database trigger on devices (see migration
// 0004_webhooks.sql) and stored with one delivery per subscribed webhook. The
// Dispatcher claims due deliveries with a lease, so several replicas can run
// it at once, and retries failures with exponential backoff.
//
// Each request is a POST with a JSON body:
//
//	{"id": "42", "type": "device.compliance_changed", "createdAt": "...", "data": {...}}
//
// signed in the X-VaanarSena-Signature header as
//
//	t=<unix seconds>,v1=<hex HMAC-SHA256(secret, "<t>.<body>")>
//
// Receivers should check the signature and reject timestamps that are too old.
// Retries reuse the event id, so receivers can drop duplicates by it.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dmdhrumilmistry/VaanarSena/internal/secrets"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

// Event types.
const (
	DeviceEnrolled         = "device.enrolled"
	DeviceRetired          = "device.retired"
	DeviceWiped            = "device.wiped"
	DeviceComplianceChange = "device.compliance_changed"
	DeviceOwnershipChange  = "device.ownership_changed"
	// Test is sent to one webhook on request, to check the receiver.
	Test = "webhook.test"
)

// EventSpec describes an event a webhook can subscribe to.
type EventSpec struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// Catalogue lists the subscribable events. The trigger in migration 0004
// raises them; keep the two in step.
var Catalogue = []EventSpec{
	{DeviceComplianceChange, "A device became compliant, non-compliant or unknown"},
	{DeviceEnrolled, "A device finished enrollment or re-enrolled"},
	{DeviceRetired, "A device was retired (management removed)"},
	{DeviceWiped, "A device was erased"},
	{DeviceOwnershipChange, "A device was reclassified between company-owned and personal"},
}

// Known reports whether typ is a subscribable event.
func Known(typ string) bool {
	for _, e := range Catalogue {
		if e.Type == typ {
			return true
		}
	}
	return false
}

// ValidateURL accepts absolute http and https URLs without credentials.
// Credentials in a URL end up in logs and the console; use the signature to
// authenticate instead.
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("url is not valid")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return errors.New("url must start with https:// or http://")
	}
	if u.Host == "" {
		return errors.New("url must include a host")
	}
	if u.User != nil {
		return errors.New("url must not contain a user name or password; verify the signature instead")
	}
	if u.Fragment != "" {
		return errors.New("url must not contain a fragment")
	}
	return nil
}

// NewSecret returns a fresh signing secret.
func NewSecret() string { return "whsec_" + secrets.Token(32) }

// Sign returns the X-VaanarSena-Signature header value for body at time t.
func Sign(secret string, t time.Time, body []byte) string {
	ts := strconv.FormatInt(t.Unix(), 10)
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts))
	m.Write([]byte("."))
	m.Write(body)
	return "t=" + ts + ",v1=" + hex.EncodeToString(m.Sum(nil))
}

// Verify checks a signature header against body and secret, rejecting
// timestamps further than tolerance from now. Receivers written in Go can use
// it; it also documents the scheme.
func Verify(secret, header string, body []byte, now time.Time, tolerance time.Duration) error {
	var ts, sig string
	for _, part := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch k {
		case "t":
			ts = v
		case "v1":
			sig = v
		}
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || sig == "" {
		return errors.New("malformed signature header")
	}
	if d := now.Sub(time.Unix(sec, 0)); d > tolerance || d < -tolerance {
		return errors.New("signature timestamp outside tolerance")
	}
	want := Sign(secret, time.Unix(sec, 0), body)
	if !hmac.Equal([]byte(want), []byte("t="+ts+",v1="+sig)) {
		return errors.New("signature mismatch")
	}
	return nil
}

// Retry policy: the first retry after 30 seconds, doubling up to 6 hours,
// for at most MaxAttempts attempts (about a day and a half in total).
const (
	MaxAttempts  = 12
	firstBackoff = 30 * time.Second
	maxBackoff   = 6 * time.Hour
	lease        = 2 * time.Minute
	sendTimeout  = 15 * time.Second
	keepEvents   = 30 * 24 * time.Hour
)

// Backoff returns the wait after the given number of failed attempts.
func Backoff(attempts int) time.Duration {
	d := firstBackoff
	for i := 1; i < attempts && d < maxBackoff; i++ {
		d *= 2
	}
	return min(d, maxBackoff)
}

// Dispatcher sends due deliveries.
type Dispatcher struct {
	Store *store.Store
	Box   *secrets.Box
	Log   *slog.Logger
	// Client sends requests; nil uses a client that does not follow redirects.
	Client *http.Client
	// Now returns the current time; nil uses time.Now.
	Now func() time.Time
}

func (d *Dispatcher) client() *http.Client {
	if d.Client != nil {
		return d.Client
	}
	return &http.Client{
		Timeout: sendTimeout,
		// A redirect would send the signed body somewhere the admin did not
		// configure; treat it as a failure instead.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (d *Dispatcher) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// Run sends due deliveries every interval until ctx ends.
func (d *Dispatcher) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	lastPrune := time.Time{}
	for {
		if _, err := d.Tick(ctx); err != nil && ctx.Err() == nil {
			d.Log.Warn("webhook deliveries", "err", err)
		}
		if time.Since(lastPrune) > time.Hour {
			if err := d.Store.PruneWebhookEvents(ctx, d.now().Add(-keepEvents)); err != nil && ctx.Err() == nil {
				d.Log.Warn("prune webhook events", "err", err)
			}
			lastPrune = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick claims and sends one batch of due deliveries, returning how many it
// attempted.
func (d *Dispatcher) Tick(ctx context.Context) (int, error) {
	due, err := d.Store.ClaimDueDeliveries(ctx, 50, lease)
	if err != nil {
		return 0, err
	}
	// One receiver gets its events one at a time, oldest first, so a fast
	// "compliant" cannot overtake the "non-compliant" before it. Different
	// receivers are served in parallel, so a slow one does not hold up others.
	byHook := map[string][]*store.DueDelivery{}
	var order []string
	for _, dl := range due {
		if _, ok := byHook[dl.WebhookID]; !ok {
			order = append(order, dl.WebhookID)
		}
		byHook[dl.WebhookID] = append(byHook[dl.WebhookID], dl)
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, id := range order {
		batch := byHook[id]
		sort.Slice(batch, func(i, j int) bool { return batch[i].EventID < batch[j].EventID })
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			// Once an event for a device fails, its later events in this
			// batch wait: sending them would let them overtake it.
			blocked := map[string]bool{}
			for _, dl := range batch {
				if dl.DeviceID != nil && blocked[*dl.DeviceID] {
					if err := d.Store.ReleaseDelivery(ctx, dl.ID); err != nil && ctx.Err() == nil {
						d.Log.Warn("release webhook delivery", "delivery", dl.ID, "err", err)
					}
					continue
				}
				if !d.deliver(ctx, dl) && dl.DeviceID != nil {
					blocked[*dl.DeviceID] = true
				}
			}
		}()
	}
	wg.Wait()
	return len(due), nil
}

// Body is the JSON document posted to a webhook.
type Body struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	CreatedAt time.Time       `json:"createdAt"`
	Data      json.RawMessage `json:"data"`
}

// deliver sends one delivery and records the outcome. It reports success.
func (d *Dispatcher) deliver(ctx context.Context, dl *store.DueDelivery) bool {
	code, err := d.send(ctx, dl)
	var codePtr *int
	if code != 0 {
		codePtr = &code
	}
	var retryAt time.Time
	msg := ""
	if err != nil {
		msg = err.Error()
		if dl.Attempts < MaxAttempts {
			retryAt = d.now().Add(Backoff(dl.Attempts))
		}
		d.Log.Info("webhook delivery failed", "webhook", dl.WebhookID, "event", dl.Event, "attempt", dl.Attempts, "err", msg)
	}
	if err := d.Store.FinishDelivery(ctx, dl.ID, codePtr, msg, retryAt); err != nil && ctx.Err() == nil {
		d.Log.Warn("record webhook delivery", "delivery", dl.ID, "err", err)
	}
	return err == nil
}

func (d *Dispatcher) send(ctx context.Context, dl *store.DueDelivery) (int, error) {
	secret, err := d.Box.Open(dl.Secret)
	if err != nil {
		return 0, errors.New("signing secret cannot be decrypted (VS_SECRET_KEY changed?); rotate the secret")
	}
	body, _ := json.Marshal(Body{ID: strconv.FormatInt(dl.EventID, 10), Type: dl.Event, CreatedAt: dl.EventAt.UTC(), Data: dl.Payload})
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, dl.URL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "VaanarSena-Webhook/1")
	req.Header.Set("X-VaanarSena-Event", dl.Event)
	req.Header.Set("X-VaanarSena-Event-Id", strconv.FormatInt(dl.EventID, 10))
	req.Header.Set("X-VaanarSena-Delivery", strconv.FormatInt(dl.ID, 10))
	req.Header.Set("X-VaanarSena-Signature", Sign(string(secret), d.now(), body))
	resp, err := d.client().Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // the URL is shown next to the error already
		}
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, fmt.Errorf("receiver answered HTTP %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}
