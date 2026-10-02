package apple

import (
	"encoding/json"
	"testing"

	"howett.net/plist"

	"github.com/dmdhrumilmistry/VaanarSena/internal/command"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

const appListResponse = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CommandUUID</key>
	<string>3F1D5E5E-7C0B-4B52-9B8E-0E5C1F7A1B11</string>
	<key>InstalledApplicationList</key>
	<array>
		<dict>
			<key>BundleSize</key>
			<integer>81920000</integer>
			<key>DynamicSize</key>
			<integer>1048576</integer>
			<key>Identifier</key>
			<string>com.apple.Safari</string>
			<key>Installing</key>
			<false/>
			<key>IsValidated</key>
			<true/>
			<key>Name</key>
			<string>Safari</string>
			<key>ShortVersion</key>
			<string>17.4.1</string>
			<key>Version</key>
			<string>19618.1.15.14.13</string>
		</dict>
		<dict>
			<key>Identifier</key>
			<string>com.slack.Slack</string>
			<key>Installing</key>
			<true/>
			<key>IsValidated</key>
			<true/>
			<key>Name</key>
			<string>Slack</string>
			<key>ShortVersion</key>
			<string></string>
			<key>Version</key>
			<string>4.37.101</string>
		</dict>
		<dict>
			<key>Identifier</key>
			<string>com.acme.vpn</string>
			<key>IsManaged</key>
			<true/>
			<key>Name</key>
			<string>Acme VPN</string>
			<key>ShortVersion</key>
			<string>2.1</string>
			<key>Version</key>
			<string>2100</string>
		</dict>
	</array>
	<key>Status</key>
	<string>Acknowledged</string>
	<key>UDID</key>
	<string>00008110-001A2C3E0E12801E</string>
</dict>
</plist>`

const profileListResponse = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CommandUUID</key>
	<string>9A0C0A4E-2B64-4F0B-8A52-6F0C2E4D7C21</string>
	<key>ProfileList</key>
	<array>
		<dict>
			<key>HasRemovalPasscode</key>
			<false/>
			<key>IsEncrypted</key>
			<false/>
			<key>IsManaged</key>
			<true/>
			<key>PayloadContent</key>
			<array>
				<dict>
					<key>PayloadType</key>
					<string>com.apple.security.pem</string>
				</dict>
				<dict>
					<key>PayloadType</key>
					<string>com.apple.wifi.managed</string>
				</dict>
			</array>
			<key>PayloadDescription</key>
			<string>Corporate Wi-Fi and root certificate</string>
			<key>PayloadDisplayName</key>
			<string>Acme Wi-Fi</string>
			<key>PayloadIdentifier</key>
			<string>com.acme.wifi</string>
			<key>PayloadOrganization</key>
			<string>Acme Labs</string>
			<key>PayloadRemovalDisallowed</key>
			<true/>
			<key>PayloadUUID</key>
			<string>6C2D9B6E-0000-4D5B-8E3A-AAAAAAAAAAAA</string>
			<key>PayloadVersion</key>
			<integer>3</integer>
		</dict>
		<dict>
			<key>IsManaged</key>
			<false/>
			<key>PayloadIdentifier</key>
			<string>com.example.manual</string>
			<key>PayloadUUID</key>
			<string>6C2D9B6E-0000-4D5B-8E3A-BBBBBBBBBBBB</string>
			<key>PayloadVersion</key>
			<integer>1</integer>
		</dict>
	</array>
	<key>Status</key>
	<string>Acknowledged</string>
	<key>UDID</key>
	<string>00008110-001A2C3E0E12801E</string>
</dict>
</plist>`

func TestParseApps(t *testing.T) {
	items, err := parseApps([]byte(appListResponse), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("got %d items", len(items))
	}
	safari := items[0]
	if safari.Name != "Safari" || safari.Identifier != "com.apple.Safari" || safari.Version != "17.4.1" ||
		safari.State != "installed" || safari.Managed || safari.Source != "apple" {
		t.Errorf("safari: %+v", safari)
	}
	var det map[string]any
	if err := json.Unmarshal(safari.Details, &det); err != nil || det["build"] != "19618.1.15.14.13" {
		t.Errorf("safari details: %s", safari.Details)
	}
	// An empty ShortVersion falls back to Version; Installing sets the state.
	if slack := items[1]; slack.Version != "4.37.101" || slack.State != "installing" {
		t.Errorf("slack: %+v", slack)
	}
	if !items[2].Managed {
		t.Error("IsManaged app not marked managed")
	}
}

func TestParseAppsManagedOnlyMarksAllManaged(t *testing.T) {
	items, err := parseApps([]byte(appListResponse), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if !it.Managed {
			t.Errorf("%s not managed in a ManagedAppsOnly response", it.Identifier)
		}
	}
}

func TestParseAppsRejectsOtherResponses(t *testing.T) {
	if _, err := parseApps([]byte(profileListResponse), false); err == nil {
		t.Error("a ProfileList response parsed as an app list")
	}
	if _, err := parseApps([]byte("not a plist"), false); err == nil {
		t.Error("garbage parsed")
	}
}

func TestParseProfiles(t *testing.T) {
	items, err := parseProfiles([]byte(profileListResponse))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items", len(items))
	}
	wifi := items[0]
	if wifi.Name != "Acme Wi-Fi" || wifi.Identifier != "com.acme.wifi" || wifi.Publisher != "Acme Labs" ||
		wifi.Version != "3" || !wifi.Managed {
		t.Errorf("wifi: %+v", wifi)
	}
	var det struct {
		UUID              string   `json:"uuid"`
		RemovalDisallowed bool     `json:"removalDisallowed"`
		PayloadTypes      []string `json:"payloadTypes"`
	}
	if err := json.Unmarshal(wifi.Details, &det); err != nil || det.UUID == "" || !det.RemovalDisallowed || len(det.PayloadTypes) != 2 {
		t.Errorf("wifi details: %s", wifi.Details)
	}
	// A profile without a display name is shown by identifier.
	if m := items[1]; m.Name != "com.example.manual" || m.Managed {
		t.Errorf("manual: %+v", m)
	}
}

func TestParseProfilesEmptyList(t *testing.T) {
	body := `<?xml version="1.0"?><plist version="1.0"><dict><key>ProfileList</key><array/><key>Status</key><string>Acknowledged</string></dict></plist>`
	items, err := parseProfiles([]byte(body))
	if err != nil || len(items) != 0 {
		t.Errorf("items=%v err=%v", items, err)
	}
}

func TestInventoryCommandRules(t *testing.T) {
	personal := &store.Device{Platform: store.PlatformIOS, Ownership: store.OwnershipPersonal, Status: store.StatusEnrolled}
	for _, typ := range []string{command.AppInventory, command.ProfileInventory} {
		if err := command.AuthorizeInternal(personal, typ); err != nil {
			t.Errorf("%s on personal iOS: %v", typ, err)
		}
		if _, ok := command.Lookup(typ); ok {
			t.Errorf("%s is in the user-facing catalogue", typ)
		}
		if command.Label(typ) == typ {
			t.Errorf("%s has no readable label", typ)
		}
	}
}

func TestInventoryCommandPlists(t *testing.T) {
	d := &Driver{}
	for _, tc := range []struct {
		ownership, typ, requestType, key string
		want                             bool
	}{
		{store.OwnershipPersonal, command.AppInventory, "InstalledApplicationList", "ManagedAppsOnly", true},
		{store.OwnershipCorporate, command.AppInventory, "InstalledApplicationList", "ManagedAppsOnly", false},
		{store.OwnershipPersonal, command.ProfileInventory, "ProfileList", "ManagedOnly", true},
		{store.OwnershipCorporate, command.ProfileInventory, "ProfileList", "ManagedOnly", false},
	} {
		dev := &store.Device{Platform: store.PlatformIOS, Ownership: tc.ownership}
		out, err := d.build(t.Context(), dev, &store.Command{ID: "cmd-1", Type: tc.typ})
		if err != nil {
			t.Fatal(err)
		}
		var msg struct {
			CommandUUID string
			Command     map[string]any
		}
		if _, err := plist.Unmarshal(out, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Command["RequestType"] != tc.requestType || msg.Command[tc.key] != tc.want {
			t.Errorf("%s/%s: %v", tc.ownership, tc.typ, msg.Command)
		}
	}
}
