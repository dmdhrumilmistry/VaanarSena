package e2e

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dmdhrumilmistry/VaanarSena/internal/agent"
	"github.com/dmdhrumilmistry/VaanarSena/internal/auth"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
	"github.com/dmdhrumilmistry/VaanarSena/internal/webhook"
)

// receiver records webhook requests and verifies their signatures.
type receiver struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	secret string
	status int
	got    []webhook.Body
	bad    int
}

func newReceiver(t *testing.T) *receiver {
	rc := &receiver{t: t, status: http.StatusNoContent}
	rc.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rc.mu.Lock()
		defer rc.mu.Unlock()
		if err := webhook.Verify(rc.secret, r.Header.Get("X-VaanarSena-Signature"), body, time.Now(), 5*time.Minute); err != nil {
			rc.bad++
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		var b webhook.Body
		_ = json.Unmarshal(body, &b)
		if r.Header.Get("X-VaanarSena-Event") != b.Type || r.Header.Get("X-VaanarSena-Event-Id") != b.ID {
			rc.t.Errorf("headers do not match the body: %v", r.Header)
		}
		if rc.status < 300 {
			rc.got = append(rc.got, b)
		}
		w.WriteHeader(rc.status)
	}))
	t.Cleanup(rc.srv.Close)
	return rc
}

func (rc *receiver) events() []webhook.Body {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return append([]webhook.Body{}, rc.got...)
}

type eventData struct {
	Device struct {
		ID               string
		Ownership        string
		Serial           string
		Compliant        *bool
		ComplianceIssues []string
	}
	Previous *struct{ Compliant *bool }
}

func TestComplianceWebhooks(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	rc := newReceiver(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	disp := &webhook.Dispatcher{Store: e.st, Box: e.box, Log: log}

	// Invalid webhooks are refused with every problem listed.
	var bad struct{ Problems []string }
	e.call("POST", "/api/v1/webhooks", `{"name":"","url":"ftp://x","events":["nope"]}`, 400, &bad)
	if len(bad.Problems) != 3 {
		t.Errorf("problems = %v", bad.Problems)
	}

	var created struct {
		Webhook store.Webhook
		Secret  string
	}
	e.call("POST", "/api/v1/webhooks", `{"name":"idp","url":"`+rc.srv.URL+`/hook","events":["device.compliance_changed","device.enrolled"]}`, 201, &created)
	if created.Secret == "" || !created.Webhook.Enabled {
		t.Fatalf("created = %+v", created)
	}
	rc.secret = created.Secret
	raw := e.call("GET", "/api/v1/webhooks/"+created.Webhook.ID, "", 200, nil)
	if strings.Contains(string(raw), created.Secret) {
		t.Fatal("secret returned after creation")
	}

	// Enrolling a device and reporting it non-compliant raises two events.
	a := e.enrollAgent("personal", "hook-1")
	a.checkinWith(agent.CheckinRequest{Facts: map[string]any{"hostname": "h"},
		Compliance: &agent.Compliance{Compliant: false, Issues: []string{"disk encryption required"}}})
	// Unchanged compliance raises nothing more.
	a.checkinWith(agent.CheckinRequest{Facts: map[string]any{"hostname": "h"},
		Compliance: &agent.Compliance{Compliant: false, Issues: []string{"disk encryption required"}}})
	if n, err := disp.Tick(ctx); err != nil || n != 2 {
		t.Fatalf("tick sent %d (err %v), want 2", n, err)
	}
	got := rc.events()
	if len(got) != 2 || got[0].Type != webhook.DeviceEnrolled || got[1].Type != webhook.DeviceComplianceChange {
		t.Fatalf("events = %+v", got)
	}
	var data eventData
	if err := json.Unmarshal(got[1].Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Device.ID != a.id || data.Device.Compliant == nil || *data.Device.Compliant ||
		len(data.Device.ComplianceIssues) != 1 || data.Previous == nil || data.Previous.Compliant != nil {
		t.Errorf("compliance event data = %+v", data)
	}
	// BYOD: no hardware identifier leaves the server for a personal device.
	if data.Device.Ownership != "personal" || data.Device.Serial != "" {
		t.Errorf("personal device event carries a serial: %+v", data.Device)
	}

	// A failing receiver is retried with backoff, and can be retried by hand.
	rc.mu.Lock()
	rc.status = http.StatusServiceUnavailable
	rc.mu.Unlock()
	a.checkinWith(agent.CheckinRequest{Facts: map[string]any{"hostname": "h"}, Compliance: &agent.Compliance{Compliant: true}})
	if n, _ := disp.Tick(ctx); n != 1 {
		t.Fatalf("tick sent %d, want 1", n)
	}
	var dl struct{ Deliveries []store.WebhookDelivery }
	e.call("GET", "/api/v1/webhooks/"+created.Webhook.ID+"/deliveries", "", 200, &dl)
	failed := dl.Deliveries[0]
	if failed.Status != store.DeliveryPending || failed.Attempts != 1 || failed.ResponseCode == nil || *failed.ResponseCode != 503 ||
		!failed.NextAttemptAt.After(time.Now()) {
		t.Fatalf("failed delivery = %+v", failed)
	}
	if n, _ := disp.Tick(ctx); n != 0 {
		t.Fatal("delivery retried before its backoff")
	}
	rc.mu.Lock()
	rc.status = http.StatusOK
	rc.mu.Unlock()
	e.call("POST", "/api/v1/webhooks/"+created.Webhook.ID+"/deliveries/"+strconv.FormatInt(failed.ID, 10)+"/retry", "", 204, nil)
	if n, _ := disp.Tick(ctx); n != 1 {
		t.Fatal("manual retry not sent")
	}
	if last := rc.events(); len(last) != 3 || last[2].Type != webhook.DeviceComplianceChange || last[2].ID == got[1].ID {
		t.Fatalf("events after retry = %+v", last)
	}

	// The test ping reaches only this webhook.
	e.call("POST", "/api/v1/webhooks/"+created.Webhook.ID+"/test", "", 202, nil)
	disp.Tick(ctx)
	if last := rc.events(); last[len(last)-1].Type != webhook.Test {
		t.Fatalf("test event not delivered: %+v", last)
	}

	// A rotated secret signs from then on; the old one no longer verifies.
	var rot struct{ Secret string }
	e.call("POST", "/api/v1/webhooks/"+created.Webhook.ID+"/rotate-secret", "", 200, &rot)
	e.call("POST", "/api/v1/webhooks/"+created.Webhook.ID+"/test", "", 202, nil)
	disp.Tick(ctx)
	rc.mu.Lock()
	rejected := rc.bad
	rc.secret = rot.Secret
	rc.mu.Unlock()
	if rejected != 1 {
		t.Fatalf("receiver rejected %d requests, want 1 signed with the new secret", rejected)
	}

	// Disabled webhooks record no events.
	e.call("PUT", "/api/v1/webhooks/"+created.Webhook.ID, `{"name":"idp","url":"`+rc.srv.URL+`/hook","events":["device.compliance_changed"],"enabled":false}`, 200, nil)
	a.checkinWith(agent.CheckinRequest{Facts: map[string]any{"hostname": "h"}, Compliance: &agent.Compliance{Compliant: false}})
	var n int
	if err := e.st.DB.QueryRow(ctx, `SELECT count(*) FROM webhook_events WHERE event = 'device.compliance_changed'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("compliance events = %d (err %v), want 2: none while disabled", n, err)
	}
	var audit struct{ Entries []store.AuditEntry }
	e.call("GET", "/api/v1/audit?limit=50", "", 200, &audit)
	actions := map[string]bool{}
	for _, en := range audit.Entries {
		actions[en.Action] = true
	}
	for _, want := range []string{"webhook.create", "webhook.update", "webhook.rotate_secret", "webhook.test", "webhook.retry"} {
		if !actions[want] {
			t.Errorf("%s not audited", want)
		}
	}

	// Webhooks are for admins only.
	hash, _ := auth.HashPassword("operator-password-1")
	if _, err := e.st.CreateUser(ctx, "op@e2e.test", "", hash, store.RoleOperator); err != nil {
		t.Fatal(err)
	}
	e.token = ""
	var login struct{ Token string }
	e.call("POST", "/api/v1/auth/login", `{"email":"op@e2e.test","password":"operator-password-1"}`, 200, &login)
	e.token = login.Token
	e.call("GET", "/api/v1/webhooks", "", 403, nil)
	e.call("POST", "/api/v1/webhooks/"+created.Webhook.ID+"/rotate-secret", "", 403, nil)
}

// TestReclassifiedDeviceLeaksNoIdentifiers pins the BYOD rules for a device
// made personal after enrolling as corporate: its serial is removed, commands
// not allowed on personal devices that are still queued are cancelled, and
// webhook events carry no serial.
func TestReclassifiedDeviceLeaksNoIdentifiers(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	rc := newReceiver(t)
	disp := &webhook.Dispatcher{Store: e.st, Box: e.box, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	var created struct {
		Webhook store.Webhook
		Secret  string
	}
	e.call("POST", "/api/v1/webhooks", `{"name":"idp","url":"`+rc.srv.URL+`","events":["device.ownership_changed"]}`, 201, &created)
	rc.secret = created.Secret

	a := e.enrollAgent("corporate", "reclass-serial")
	a.checkin(map[string]any{"hostname": "r", "serial": "SN-CORP-1"}, nil)
	var queued store.Command
	e.call("POST", "/api/v1/devices/"+a.id+"/commands", `{"type":"run_script","params":{"script":"id"}}`, 202, &queued)

	var d store.Device
	e.call("PATCH", "/api/v1/devices/"+a.id, `{"ownership":"personal"}`, 200, &d)
	if d.Serial != "" || strings.Contains(string(d.Facts), "SN-CORP-1") {
		t.Fatalf("serial kept after reclassification: %q %s", d.Serial, d.Facts)
	}
	c, err := e.st.CommandByID(ctx, queued.ID)
	if err != nil || c.Status != store.CmdCancelled {
		t.Fatalf("queued run_script = %+v (err %v), want cancelled", c, err)
	}
	if cr := a.checkin(map[string]any{"hostname": "r"}, nil); types(cr.Commands)["run_script"] {
		t.Fatal("corporate-only command delivered to the device made personal")
	}
	disp.Tick(ctx)
	got := rc.events()
	if len(got) != 1 || got[0].Type != webhook.DeviceOwnershipChange {
		t.Fatalf("events = %+v", got)
	}
	var data eventData
	_ = json.Unmarshal(got[0].Data, &data)
	if data.Device.Ownership != "personal" || data.Device.Serial != "" {
		t.Fatalf("ownership event leaks a serial: %+v", data.Device)
	}
}

// TestWebhookEventsStayInOrder: a failing older event for a device holds back
// newer ones for that device, so a receiver never applies a stale state last.
func TestWebhookEventsStayInOrder(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	rc := newReceiver(t)
	disp := &webhook.Dispatcher{Store: e.st, Box: e.box, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	var created struct {
		Webhook store.Webhook
		Secret  string
	}
	e.call("POST", "/api/v1/webhooks", `{"name":"idp","url":"`+rc.srv.URL+`","events":["device.compliance_changed"]}`, 201, &created)
	rc.secret = created.Secret
	a := e.enrollAgent("corporate", "order-1")
	report := func(ok bool) {
		a.checkinWith(agent.CheckinRequest{Facts: map[string]any{"hostname": "o"}, Compliance: &agent.Compliance{Compliant: ok}})
	}

	rc.mu.Lock()
	rc.status = http.StatusServiceUnavailable
	rc.mu.Unlock()
	report(false) // A
	report(true)  // B
	disp.Tick(ctx)
	var dl struct{ Deliveries []store.WebhookDelivery }
	e.call("GET", "/api/v1/webhooks/"+created.Webhook.ID+"/deliveries", "", 200, &dl)
	if len(dl.Deliveries) != 2 {
		t.Fatalf("deliveries = %+v", dl.Deliveries)
	}
	newer, older := dl.Deliveries[0], dl.Deliveries[1]
	if older.Attempts != 1 || newer.Attempts != 0 {
		t.Fatalf("attempts older %d newer %d: the newer event must wait for the failing older one", older.Attempts, newer.Attempts)
	}
	// While the older one backs off, the newer one is not due either.
	if n, _ := disp.Tick(ctx); n != 0 {
		t.Fatalf("tick sent %d while the older event backs off", n)
	}
	rc.mu.Lock()
	rc.status = http.StatusOK
	rc.mu.Unlock()
	e.call("POST", "/api/v1/webhooks/"+created.Webhook.ID+"/deliveries/"+strconv.FormatInt(older.ID, 10)+"/retry", "", 204, nil)
	disp.Tick(ctx)
	got := rc.events()
	if len(got) != 2 || got[0].ID != strconv.FormatInt(older.EventID, 10) || got[1].ID != strconv.FormatInt(newer.EventID, 10) {
		t.Fatalf("events = %+v, want older then newer", got)
	}

	// Disabling fails what is still pending, so enabling later replays nothing.
	rc.mu.Lock()
	rc.status = http.StatusServiceUnavailable
	rc.mu.Unlock()
	report(false)
	disp.Tick(ctx)
	e.call("PUT", "/api/v1/webhooks/"+created.Webhook.ID, `{"name":"idp","url":"`+rc.srv.URL+`","events":["device.compliance_changed"],"enabled":false}`, 200, nil)
	e.call("GET", "/api/v1/webhooks/"+created.Webhook.ID+"/deliveries", "", 200, &dl)
	if dl.Deliveries[0].Status != store.DeliveryFailed {
		t.Fatalf("pending delivery after disable = %+v", dl.Deliveries[0])
	}
	// A delivered event cannot be retried.
	e.call("POST", "/api/v1/webhooks/"+created.Webhook.ID+"/deliveries/"+strconv.FormatInt(newer.ID, 10)+"/retry", "", 409, nil)
}
