package e2e

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/dmdhrumilmistry/VaanarSena/internal/chromeos"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

// fakeAdminSDK stands in for the Admin SDK Directory API's chromeosdevices
// collection, recording the device commands it was asked to issue.
type fakeAdminSDK struct {
	srv     *httptest.Server
	mu      sync.Mutex
	devices []map[string]any
	issued  []string // "<deviceId> <commandType>"
}

func newFakeAdminSDK(t *testing.T) *fakeAdminSDK {
	f := &fakeAdminSDK{}
	const base = "/admin/directory/v1/customer/my_customer/devices/chromeos"
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"access_token": "fake-token", "token_type": "Bearer", "expires_in": 3600})
	})
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		reply(w, map[string]any{"chromeosdevices": f.devices})
	})
	// Device paths end in "{id}:issueCommand", which ServeMux wildcards cannot
	// express, so they are matched by prefix.
	mux.HandleFunc("POST "+base+"/", func(w http.ResponseWriter, r *http.Request) {
		id, verb, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, base+"/"), ":")
		if !ok || verb != "issueCommand" {
			http.NotFound(w, r)
			return
		}
		var body struct{ CommandType string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.issued = append(f.issued, id+" "+body.CommandType)
		f.mu.Unlock()
		reply(w, map[string]any{"commandId": "1"})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAdminSDK) issuedCommands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.issued...)
}

// TestChromeOSOnboardingAndQueue checks that imported ChromeOS devices are
// bootstrapped like enrolled ones (smart groups, blueprint onboarding) and that
// commands whose wake-up was missed are delivered by the periodic drain.
func TestChromeOSOnboardingAndQueue(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	admin := newFakeAdminSDK(t)
	google := &fakeGoogle{key: e.google.key, srv: admin.srv}

	e.call("POST", "/api/v1/apply", `
apiVersion: vaanarsena.io/v1
kind: Group
metadata: {name: chromebooks}
spec:
  kind: smart
  rules:
    match: all
    conditions:
      - {field: platform, op: eq, value: chromeos}
---
apiVersion: vaanarsena.io/v1
kind: Blueprint
metadata: {name: chromebook-onboarding}
spec:
  groups: [chromebooks]
  onEnroll:
    - {type: restart}
`, 200, nil)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	drv, err := chromeos.NewFromJSON(ctx, e.st, []byte(google.credentials()), "admin@e2e.test", "my_customer",
		admin.srv.URL+"/admin/directory/v1/", log)
	if err != nil {
		t.Fatal(err)
	}
	drv.Svc = e.svc
	e.svc.Register(drv)

	admin.devices = []map[string]any{
		{"deviceId": "cros-1", "serialNumber": "SN1", "status": "ACTIVE", "model": "Chromebook"},
		{"deviceId": "cros-2", "serialNumber": "SN2", "status": "PRE_PROVISIONED"},
	}
	if err := drv.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if got := admin.issuedCommands(); len(got) != 1 || got[0] != "cros-1 REBOOT" {
		t.Fatalf("issued = %v, want the onboarding restart on the active device only", got)
	}
	d1, err := e.st.DeviceByNativeID(ctx, "cros-1", store.PlatformChromeOS)
	if err != nil {
		t.Fatal(err)
	}
	groups, _ := e.st.DeviceGroups(ctx, d1.ID)
	if len(groups) != 1 {
		t.Errorf("imported device not placed in its smart group: %v", groups)
	}

	// Onboarding runs once: a second sync of the same device does nothing.
	if err := drv.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if got := admin.issuedCommands(); len(got) != 1 {
		t.Fatalf("onboarding repeated on re-sync: %v", got)
	}

	// Provisioning finishing later counts as enrollment.
	admin.devices[1]["status"] = "ACTIVE"
	if err := drv.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if got := admin.issuedCommands(); len(got) != 2 || got[1] != "cros-2 REBOOT" {
		t.Fatalf("issued = %v, want onboarding once provisioning finished", got)
	}

	// A command queued without a wake-up (the driver was down) is delivered by
	// the drain that runs after each periodic sync.
	if _, err := e.st.EnqueueCommand(ctx, d1.ID, "restart", nil, nil); err != nil {
		t.Fatal(err)
	}
	drv.Drain(ctx)
	if got := admin.issuedCommands(); len(got) != 3 || got[2] != "cros-1 REBOOT" {
		t.Fatalf("issued = %v, want the queued restart delivered by the drain", got)
	}
	cmds, _ := e.st.ListCommands(ctx, d1.ID, 10)
	for _, c := range cmds {
		if c.Status == store.CmdQueued {
			t.Errorf("command %s still queued after drain", c.Type)
		}
	}
}
