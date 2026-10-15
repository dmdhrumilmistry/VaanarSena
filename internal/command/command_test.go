package command

import (
	"strings"
	"testing"

	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

func dev(platform, ownership string) *store.Device {
	return &store.Device{Platform: platform, Ownership: ownership, Status: store.StatusEnrolled}
}

func TestBYODGuardRefusesDeviceWideCommands(t *testing.T) {
	for _, platform := range []string{store.PlatformIOS, store.PlatformMacOS, store.PlatformWindows, store.PlatformAndroid, store.PlatformLinux} {
		d := dev(platform, store.OwnershipPersonal)
		for _, typ := range []string{Wipe, Lock, Restart, Locate, ClearPasscode, RunScript, EnableLostMode} {
			spec, _ := Lookup(typ)
			if !contains(spec.Platforms, platform) {
				continue
			}
			err := Authorize(store.RoleAdmin, d, typ, Params{Script: "x"})
			if err == nil || !strings.Contains(err.Error(), "BYOD") {
				t.Errorf("%s on personal %s: want BYOD refusal, got %v", typ, platform, err)
			}
		}
		if err := Authorize(store.RoleAdmin, d, Retire, Params{}); err != nil {
			t.Errorf("retire on personal %s should be allowed: %v", platform, err)
		}
	}
}

func TestRoles(t *testing.T) {
	d := dev(store.PlatformIOS, store.OwnershipCorporate)
	if err := Authorize(store.RoleAuditor, d, Refresh, Params{}); err == nil {
		t.Error("auditor must not send commands")
	}
	if err := Authorize(store.RoleOperator, d, Lock, Params{}); err != nil {
		t.Errorf("operator lock: %v", err)
	}
	if err := Authorize(store.RoleOperator, d, Wipe, Params{}); err == nil {
		t.Error("operator must not wipe")
	}
	if err := Authorize(store.RoleAdmin, d, Wipe, Params{}); err != nil {
		t.Errorf("admin wipe: %v", err)
	}
}

func TestPlatformSupport(t *testing.T) {
	if err := Authorize(store.RoleAdmin, dev(store.PlatformWindows, store.OwnershipCorporate), Lock, Params{}); err == nil {
		t.Error("Windows desktop has no remote lock")
	}
	if err := Authorize(store.RoleAdmin, dev(store.PlatformLinux, store.OwnershipCorporate), Wipe, Params{}); err == nil {
		t.Error("Linux wipe is not supported")
	}
	if err := Authorize(store.RoleAdmin, dev(store.PlatformLinux, store.OwnershipCorporate), RunScript, Params{}); err == nil {
		t.Error("run_script requires a script")
	}
}

func TestRetiredDevicesRefuseCommands(t *testing.T) {
	d := dev(store.PlatformIOS, store.OwnershipCorporate)
	d.Status = store.StatusRetired
	if err := Authorize(store.RoleAdmin, d, Refresh, Params{}); err == nil {
		t.Error("retired device accepted a command")
	}
}

func TestAppParamsPerPlatform(t *testing.T) {
	corp := func(p string) *store.Device { return dev(p, store.OwnershipCorporate) }
	urlOnly := Params{URL: "https://apps.example.com/x"}
	cases := []struct {
		platform, typ string
		p             Params
		ok            bool
	}{
		// A manifest URL is how Apple installs in-house apps.
		{store.PlatformIOS, InstallApp, urlOnly, true},
		// Removal is by bundle ID; an empty identifier must not be sent.
		{store.PlatformIOS, RemoveApp, urlOnly, false},
		{store.PlatformMacOS, RemoveApp, Params{AppID: "com.example.app"}, true},
		// Windows MSI install needs the product code, URL, hash and version;
		// removal with only a URL would delete the MSI root node.
		{store.PlatformWindows, InstallApp, urlOnly, false},
		{store.PlatformWindows, InstallApp, Params{AppID: "{A-B}", URL: "https://x/a.msi", Hash: "ab", Version: "1.0"}, true},
		{store.PlatformWindows, RemoveApp, urlOnly, false},
		{store.PlatformWindows, RemoveApp, Params{AppID: "{A-B}"}, true},
		// Linux installs packages by name only.
		{store.PlatformLinux, InstallApp, urlOnly, false},
		{store.PlatformLinux, InstallApp, Params{AppID: "--force"}, false},
		{store.PlatformLinux, InstallApp, Params{AppID: "osquery"}, true},
	}
	for _, c := range cases {
		err := Authorize(store.RoleAdmin, corp(c.platform), c.typ, c.p)
		if (err == nil) != c.ok {
			t.Errorf("%s %s %+v: err = %v, want ok=%v", c.platform, c.typ, c.p, err, c.ok)
		}
	}
}

func TestCheckParams(t *testing.T) {
	if CheckParams(InstallApp, Params{}) == nil {
		t.Error("install_app without appId or url accepted")
	}
	if CheckParams(RunScript, Params{}) == nil {
		t.Error("run_script without a script accepted")
	}
	if err := CheckParams(Refresh, Params{}); err != nil {
		t.Errorf("refresh: %v", err)
	}
}

func TestMacPIN(t *testing.T) {
	d := dev(store.PlatformMacOS, store.OwnershipCorporate)
	if err := Authorize(store.RoleAdmin, d, Wipe, Params{PIN: "12a456"}); err == nil {
		t.Error("non-numeric PIN accepted")
	}
	if err := Authorize(store.RoleAdmin, d, Wipe, Params{PIN: "123456"}); err != nil {
		t.Errorf("valid PIN rejected: %v", err)
	}
}
