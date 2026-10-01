package policy

import (
	"encoding/base64"
	"testing"

	"howett.net/plist"
)

func mobileconfig(t *testing.T, payloads ...map[string]any) string {
	t.Helper()
	b, err := plist.Marshal(map[string]any{"PayloadType": "Configuration", "PayloadContent": payloads}, plist.XMLFormat)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func TestCustomPayloadsAreCorporateOnlyByDefault(t *testing.T) {
	doc := &Document{Custom: &Custom{
		Apple:   []ApplePayload{{Payload: map[string]any{"PayloadType": "com.apple.dock", "orientation": "left"}}},
		Windows: []WindowsNode{{LocURI: "./Device/Vendor/MSFT/Policy/Config/Start/HideSleep", Format: "int", Data: "1"}},
		Android: map[string]any{"kioskCustomLauncherEnabled": true},
	}}
	if err := doc.Validate(); err != nil {
		t.Fatal(err)
	}
	if n := len(doc.ApplePayloads(false)); n != 1 {
		t.Errorf("corporate Apple payloads = %d", n)
	}
	if n := len(doc.ApplePayloads(true)); n != 0 {
		t.Errorf("personal device got %d custom Apple payloads", n)
	}
	if len(doc.WindowsItems(true)) != 0 {
		t.Error("personal Windows device got custom nodes")
	}
	if _, ok := doc.AndroidPolicy(true)["kioskCustomLauncherEnabled"]; ok {
		t.Error("personal Android device got custom fields")
	}
	doc.Custom.Scope = "all"
	if len(doc.WindowsItems(true)) != 1 || doc.WindowsItems(true)[0].Op != "Replace" {
		t.Error("scope all should reach personal devices with Replace as default op")
	}
	if doc.AndroidPolicy(false)["kioskCustomLauncherEnabled"] != true {
		t.Error("custom Android field missing")
	}
}

func TestMobileconfigUpload(t *testing.T) {
	mc := mobileconfig(t, map[string]any{"PayloadType": "com.apple.webcontent-filter", "FilterType": "BuiltIn"},
		map[string]any{"PayloadType": "com.apple.security.firewall", "EnableFirewall": true})
	doc := &Document{Custom: &Custom{Apple: []ApplePayload{{Mobileconfig: mc}}}}
	if err := doc.Validate(); err != nil {
		t.Fatal(err)
	}
	if n := len(doc.ApplePayloads(false)); n != 2 {
		t.Errorf("unpacked %d payloads, want 2", n)
	}
	enroll := mobileconfig(t, map[string]any{"PayloadType": "com.apple.mdm"})
	bad := &Document{Custom: &Custom{Apple: []ApplePayload{{Mobileconfig: enroll}}}}
	if bad.Validate() == nil {
		t.Error("accepted a profile containing an MDM payload")
	}
	if (&Document{Custom: &Custom{Apple: []ApplePayload{{Mobileconfig: "!!"}}}}).Validate() == nil {
		t.Error("accepted invalid base64")
	}
}

func TestMergeCustom(t *testing.T) {
	a := &Document{Custom: &Custom{Windows: []WindowsNode{{LocURI: "./x", Data: "1"}}, Android: map[string]any{"a": 1}}}
	b := &Document{Custom: &Custom{Windows: []WindowsNode{{LocURI: "./x", Data: "2"}, {LocURI: "./y"}}, Android: map[string]any{"b": 2}}}
	m := Merge(a, b)
	if len(m.Custom.Windows) != 2 || m.Custom.Windows[0].Data != "2" {
		t.Errorf("windows merge: %+v", m.Custom.Windows)
	}
	if len(m.Custom.Android) != 2 {
		t.Errorf("android merge: %+v", m.Custom.Android)
	}
	if len(a.Custom.Windows) != 1 || a.Custom.Windows[0].Data != "1" {
		t.Error("merge mutated its input")
	}
}
