package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dmdhrumilmistry/VaanarSena/internal/agent"
	"github.com/dmdhrumilmistry/VaanarSena/internal/auth"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

type inventoryResp struct {
	Kind        string
	Items       []store.InventoryItem
	Total       int
	CollectedAt *time.Time
	Counts      map[string]int
	Supported   map[string]bool
	Note        string
}

type softwareResp struct {
	Items []struct {
		Name       string
		Identifier string
		Source     string
		Sources    []string
		Devices    int
		Versions   []struct {
			Version string
			Devices int
		}
	}
	Total int
}

type softwareDevicesResp struct {
	Items []struct {
		DeviceID   string
		DeviceName string
		Platform   string
		Ownership  string
		Version    string
	}
	Total int
}

func app(name, version string) agent.InventoryItem {
	return agent.InventoryItem{Name: name, Identifier: name, Version: version, Publisher: "Ubuntu Developers", Source: "dpkg"}
}

func service(name, state, enabled string) agent.InventoryItem {
	return agent.InventoryItem{Name: name, Identifier: name + ".service", Source: "systemd", State: state,
		Details: map[string]string{"enabled": enabled, "description": name + " daemon"}}
}

// checkinInventory sends a check-in carrying inventory.
func (a *fakeAgent) checkinInventory(inv *agent.Inventory) agent.CheckinResponse {
	a.e.t.Helper()
	return a.checkinWith(agent.CheckinRequest{Facts: map[string]any{"hostname": a.id}, Inventory: inv})
}

// storedRows counts inventory rows in the database, bypassing every API-level
// rule, so BYOD tests prove the data was never stored.
func (e *env) storedRows(deviceID string) int {
	e.t.Helper()
	var n int
	err := e.st.DB.QueryRow(context.Background(),
		`SELECT (SELECT count(*) FROM device_inventory WHERE device_id = $1) + (SELECT count(*) FROM device_inventory_sync WHERE device_id = $1)`,
		deviceID).Scan(&n)
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestLinuxInventory(t *testing.T) {
	e := setup(t)
	a := e.enrollAgent("corporate", "mid-inv-a")
	b := e.enrollAgent("corporate", "mid-inv-b")

	a.checkinInventory(&agent.Inventory{
		Apps:     []agent.InventoryItem{app("openssl", "3.0.13"), app("curl", "8.5.0"), app("git", "2.43.0")},
		Services: []agent.InventoryItem{service("ssh", "running", "enabled"), service("nginx", "failed", "enabled")},
		Hash:     "h1",
	})
	b.checkinInventory(&agent.Inventory{
		Apps: []agent.InventoryItem{app("curl", "8.9.1"), app("git", "2.43.0"), app("firefox", "131.0")},
	})

	var inv inventoryResp
	e.call("GET", "/api/v1/devices/"+a.id+"/inventory", "", 200, &inv)
	if inv.Kind != "app" || inv.Total != 3 || len(inv.Items) != 3 || inv.CollectedAt == nil {
		t.Fatalf("app inventory: %+v", inv)
	}
	if inv.Items[0].Name != "curl" || inv.Items[1].Name != "git" || inv.Items[2].Name != "openssl" {
		t.Errorf("items not ordered by name: %+v", inv.Items)
	}
	if it := inv.Items[0]; it.Version != "8.5.0" || it.Source != "dpkg" || it.State != "installed" || it.Publisher != "Ubuntu Developers" {
		t.Errorf("curl: %+v", it)
	}
	if inv.Counts["app"] != 3 || inv.Counts["service"] != 2 || inv.Counts["profile"] != 0 {
		t.Errorf("counts: %v", inv.Counts)
	}
	if !inv.Supported["app"] || !inv.Supported["service"] || inv.Supported["profile"] {
		t.Errorf("supported: %v", inv.Supported)
	}

	e.call("GET", "/api/v1/devices/"+a.id+"/inventory?kind=service", "", 200, &inv)
	if inv.Kind != "service" || inv.Total != 2 || inv.Items[0].Name != "nginx" || inv.Items[0].State != "failed" || inv.Items[1].State != "running" {
		t.Errorf("services: %+v", inv)
	}
	if string(inv.Items[1].Details) == "" || inv.Items[1].Identifier != "ssh.service" {
		t.Errorf("service details: %+v", inv.Items[1])
	}

	// Search matches name, identifier and publisher, case-insensitively; paging reports the total.
	e.call("GET", "/api/v1/devices/"+a.id+"/inventory?q=OPENSSL", "", 200, &inv)
	if inv.Total != 1 || inv.Items[0].Name != "openssl" {
		t.Errorf("search: %+v", inv)
	}
	e.call("GET", "/api/v1/devices/"+a.id+"/inventory?q=ubuntu&limit=2&offset=1", "", 200, &inv)
	if inv.Total != 3 || len(inv.Items) != 2 || inv.Items[0].Name != "git" {
		t.Errorf("paging: %+v", inv)
	}
	e.call("GET", "/api/v1/devices/"+a.id+"/inventory?kind=bogus", "", 400, nil)

	// A device that never reported has no collection time.
	c := e.enrollAgent("corporate", "mid-inv-c")
	e.call("GET", "/api/v1/devices/"+c.id+"/inventory", "", 200, &inv)
	if inv.Total != 0 || inv.CollectedAt != nil || len(inv.Items) != 0 || !inv.Supported["app"] {
		t.Errorf("never collected: %+v", inv)
	}

	// Fleet view: curl is on two devices in two versions, git on two in one
	// version, firefox on one device.
	var sw softwareResp
	e.call("GET", "/api/v1/inventory/software", "", 200, &sw)
	if sw.Total != 4 || len(sw.Items) != 4 {
		t.Fatalf("fleet software: %+v", sw)
	}
	if sw.Items[0].Name != "curl" && sw.Items[0].Name != "git" {
		t.Errorf("most widespread first: %+v", sw.Items)
	}
	byName := map[string]int{}
	for i, it := range sw.Items {
		byName[it.Name] = i
	}
	curl := sw.Items[byName["curl"]]
	if curl.Devices != 2 || len(curl.Versions) != 2 || curl.Versions[0].Devices != 1 {
		t.Errorf("curl: %+v", curl)
	}
	if sw.Items[byName["firefox"]].Devices != 1 || sw.Items[byName["git"]].Devices != 2 {
		t.Errorf("counts: %+v", sw.Items)
	}
	e.call("GET", "/api/v1/inventory/software?q=fire", "", 200, &sw)
	if sw.Total != 1 || sw.Items[0].Name != "firefox" {
		t.Errorf("fleet search: %+v", sw)
	}
	e.call("GET", "/api/v1/inventory/software?kind=service", "", 200, &sw)
	if sw.Total != 2 {
		t.Errorf("fleet services: %+v", sw)
	}
	e.call("GET", "/api/v1/inventory/software?kind=bogus", "", 400, nil)

	var sd softwareDevicesResp
	e.call("GET", "/api/v1/inventory/software/devices?identifier=curl&name=curl", "", 200, &sd)
	if sd.Total != 2 || len(sd.Items) != 2 {
		t.Fatalf("software devices: %+v", sd)
	}
	e.call("GET", "/api/v1/inventory/software/devices?identifier=curl&name=curl&version=8.9.1", "", 200, &sd)
	if sd.Total != 1 || sd.Items[0].DeviceID != b.id || sd.Items[0].Version != "8.9.1" || sd.Items[0].Platform != "linux" || sd.Items[0].Ownership != "corporate" {
		t.Errorf("software devices by version: %+v", sd)
	}
	e.call("GET", "/api/v1/inventory/software/devices?identifier=curl", "", 400, nil) // name is required

	// The overview statistics gain a software summary and keep existing fields.
	var st struct {
		Total    int
		Software struct{ Apps, DevicesReporting int }
	}
	e.call("GET", "/api/v1/stats", "", 200, &st)
	if st.Total != 3 || st.Software.Apps != 4 || st.Software.DevicesReporting != 2 {
		t.Errorf("stats: %+v", st)
	}

	// Each report replaces the previous one, per kind: apps are untouched by a
	// services-only report, and removed packages disappear.
	a.checkinInventory(&agent.Inventory{Apps: []agent.InventoryItem{app("curl", "8.6.0")}})
	a.checkinInventory(&agent.Inventory{Services: []agent.InventoryItem{service("cron", "running", "enabled")}})
	e.call("GET", "/api/v1/devices/"+a.id+"/inventory", "", 200, &inv)
	if inv.Total != 1 || inv.Items[0].Version != "8.6.0" {
		t.Errorf("replace apps: %+v", inv)
	}
	e.call("GET", "/api/v1/devices/"+a.id+"/inventory?kind=service", "", 200, &inv)
	if inv.Total != 1 || inv.Items[0].Name != "cron" {
		t.Errorf("replace services: %+v", inv)
	}

	// A check-in without inventory changes nothing.
	a.checkin(map[string]any{"hostname": "a"}, nil)
	e.call("GET", "/api/v1/devices/"+a.id+"/inventory", "", 200, &inv)
	if inv.Total != 1 {
		t.Errorf("inventory lost on a plain check-in: %+v", inv)
	}

	// Retired devices drop out of the fleet view.
	e.call("POST", "/api/v1/devices/"+b.id+"/commands", `{"type":"retire"}`, 202, nil)
	b.checkin(map[string]any{"hostname": "b"}, ackAll(b.checkin(map[string]any{"hostname": "b"}, nil).Commands))
	e.call("GET", "/api/v1/inventory/software?q=firefox", "", 200, &sw)
	if sw.Total != 0 {
		t.Errorf("retired device still in fleet software: %+v", sw)
	}
}

func TestInventoryIsBounded(t *testing.T) {
	e := setup(t)
	a := e.enrollAgent("corporate", "mid-inv-big")
	var apps []agent.InventoryItem
	for i := 0; i < store.MaxInventoryItems+100; i++ {
		apps = append(apps, app(fmt.Sprintf("pkg-%05d", i), "1.0"))
	}
	long := make([]byte, 1000)
	for i := range long {
		long[i] = 'x'
	}
	apps[0] = agent.InventoryItem{Name: string(long), Identifier: string(long), Version: string(long), Publisher: string(long), Source: "dpkg"}
	a.checkinInventory(&agent.Inventory{Apps: apps})
	var inv inventoryResp
	e.call("GET", "/api/v1/devices/"+a.id+"/inventory?limit=5000", "", 200, &inv)
	if inv.Total != store.MaxInventoryItems || len(inv.Items) > 1000 {
		t.Fatalf("total=%d returned=%d", inv.Total, len(inv.Items))
	}
	var n int
	if err := e.st.DB.QueryRow(context.Background(),
		`SELECT count(*) FROM device_inventory WHERE device_id = $1 AND (length(name) > 256 OR length(identifier) > 512 OR length(version) > 128 OR length(publisher) > 256)`,
		a.id).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d rows exceed the string limits (err=%v)", n, err)
	}
}

// The BYOD rule: a personally owned Linux machine never has software or
// services stored, even when a modified agent sends them.
func TestPersonalLinuxInventoryIsDropped(t *testing.T) {
	e := setup(t)
	personal := e.enrollAgent("personal", "mid-inv-byod")
	corporate := e.enrollAgent("corporate", "mid-inv-corp")

	cr := personal.checkinInventory(&agent.Inventory{
		Apps:     []agent.InventoryItem{app("openssl", "3.0.13"), app("steam", "1.0.0.79")},
		Services: []agent.InventoryItem{service("ssh", "running", "enabled")},
		Hash:     "x",
	})
	if !cr.Personal {
		t.Fatal("server did not mark the agent personal")
	}
	corporate.checkinInventory(&agent.Inventory{Apps: []agent.InventoryItem{app("openssl", "3.0.13")}})

	// Nothing was stored, checked in the database itself.
	if n := e.storedRows(personal.id); n != 0 {
		t.Fatalf("%d inventory rows stored for a personal device", n)
	}

	for _, kind := range []string{"app", "service", "profile"} {
		var inv inventoryResp
		e.call("GET", "/api/v1/devices/"+personal.id+"/inventory?kind="+kind, "", 200, &inv)
		if inv.Total != 0 || len(inv.Items) != 0 || inv.CollectedAt != nil {
			t.Errorf("%s: personal device returned inventory: %+v", kind, inv)
		}
		if inv.Supported["app"] || inv.Supported["service"] || inv.Supported["profile"] {
			t.Errorf("%s: supported = %v on a personal Linux device", kind, inv.Supported)
		}
		if inv.Note == "" {
			t.Errorf("%s: no note explaining that nothing is collected", kind)
		}
	}

	// Only the corporate device counts in the fleet view and the statistics.
	var sw softwareResp
	e.call("GET", "/api/v1/inventory/software", "", 200, &sw)
	if sw.Total != 1 || sw.Items[0].Name != "openssl" || sw.Items[0].Devices != 1 {
		t.Errorf("fleet software includes the personal device: %+v", sw)
	}
	var sd softwareDevicesResp
	e.call("GET", "/api/v1/inventory/software/devices?identifier=openssl&name=openssl", "", 200, &sd)
	if sd.Total != 1 || sd.Items[0].DeviceID != corporate.id {
		t.Errorf("software devices include the personal device: %+v", sd)
	}
	var st struct {
		Software struct{ Apps, DevicesReporting int }
	}
	e.call("GET", "/api/v1/stats", "", 200, &st)
	if st.Software.DevicesReporting != 1 {
		t.Errorf("stats count the personal device: %+v", st)
	}
}

// Reclassifying a corporate device as personal removes what was collected.
func TestReclassifiedDeviceInventoryIsPurged(t *testing.T) {
	e := setup(t)
	a := e.enrollAgent("corporate", "mid-inv-flip")
	a.checkinInventory(&agent.Inventory{Apps: []agent.InventoryItem{app("curl", "8.5.0")}, Services: []agent.InventoryItem{service("ssh", "running", "enabled")}})
	if e.storedRows(a.id) == 0 {
		t.Fatal("nothing stored for the corporate device")
	}
	e.call("PATCH", "/api/v1/devices/"+a.id, `{"ownership":"personal"}`, 200, nil)
	if n := e.storedRows(a.id); n != 0 {
		t.Errorf("%d rows left after the device became personal", n)
	}
	// And a report after the change is dropped like any other.
	a.checkinInventory(&agent.Inventory{Apps: []agent.InventoryItem{app("curl", "8.5.0")}})
	if n := e.storedRows(a.id); n != 0 {
		t.Errorf("%d rows stored after the device became personal", n)
	}
}

func TestAuditorsCanReadInventory(t *testing.T) {
	e := setup(t)
	a := e.enrollAgent("corporate", "mid-inv-aud")
	a.checkinInventory(&agent.Inventory{Apps: []agent.InventoryItem{app("curl", "8.5.0")}})
	hash, _ := auth.HashPassword("auditor-password-1")
	if _, err := e.st.CreateUser(context.Background(), "aud@e2e.test", "", hash, store.RoleAuditor); err != nil {
		t.Fatal(err)
	}
	paths := []string{
		"/api/v1/devices/" + a.id + "/inventory",
		"/api/v1/inventory/software",
		"/api/v1/inventory/software/devices?identifier=curl&name=curl",
	}
	e.token = ""
	for _, p := range paths {
		e.call("GET", p, "", 401, nil)
	}
	var login struct{ Token string }
	e.call("POST", "/api/v1/auth/login", `{"email":"aud@e2e.test","password":"auditor-password-1"}`, 200, &login)
	e.token = login.Token
	for _, p := range paths {
		e.call("GET", p, "", 200, nil)
	}
	// Inventory is read-only through the API.
	e.call("POST", "/api/v1/devices/"+a.id+"/inventory", "{}", 405, nil)
	e.call("DELETE", "/api/v1/inventory/software", "", 405, nil)
}

// The same software installed through different package managers is one fleet
// row: what an administrator asks ("which machines have curl?") does not
// depend on the distribution.
func TestFleetSoftwareGroupsAcrossSources(t *testing.T) {
	e := setup(t)
	deb := e.enrollAgent("corporate", "mid-src-deb")
	rpm := e.enrollAgent("corporate", "mid-src-rpm")
	deb.checkinInventory(&agent.Inventory{Apps: []agent.InventoryItem{app("curl", "8.5.0")}})
	rpm.checkinInventory(&agent.Inventory{Apps: []agent.InventoryItem{{Name: "Curl", Identifier: "curl", Version: "8.9.1", Source: "rpm"}}})

	var sw softwareResp
	e.call("GET", "/api/v1/inventory/software?q=curl", "", 200, &sw)
	if sw.Total != 1 || len(sw.Items) != 1 {
		t.Fatalf("want one row for curl across dpkg and rpm: %+v", sw)
	}
	it := sw.Items[0]
	if it.Devices != 2 || len(it.Versions) != 2 || len(it.Sources) != 2 || it.Sources[0] != "dpkg" || it.Sources[1] != "rpm" {
		t.Errorf("curl row: %+v", it)
	}
	var stats struct{ Software struct{ Apps int } }
	e.call("GET", "/api/v1/stats", "", 200, &stats)
	if stats.Software.Apps != 1 {
		t.Errorf("distinct apps = %d, want 1", stats.Software.Apps)
	}

	// The drill-down matches by name; the identifier is an optional filter.
	var sd struct{ Total int }
	e.call("GET", "/api/v1/inventory/software/devices?name=curl", "", 200, &sd)
	if sd.Total != 2 {
		t.Errorf("devices with curl = %d, want 2", sd.Total)
	}
	e.call("GET", "/api/v1/inventory/software/devices?name=curl&version=8.9.1", "", 200, &sd)
	if sd.Total != 1 {
		t.Errorf("devices with curl 8.9.1 = %d, want 1", sd.Total)
	}
}
