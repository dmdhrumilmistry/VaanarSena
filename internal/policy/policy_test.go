package policy

import (
	"strings"
	"testing"
)

func bp(b bool) *bool { return &b }

func TestParseRejectsUnknownFields(t *testing.T) {
	if _, err := Parse([]byte(`{"passcode":{"required":true,"minLenght":8}}`)); err == nil {
		t.Fatal("typo in field name was accepted")
	}
}

func TestValidate(t *testing.T) {
	cases := []string{
		`{"passcode":{"required":true,"minLength":100}}`,
		`{"wifi":[{"ssid":"","security":"WPA2"}]}`,
		`{"wifi":[{"ssid":"x","security":"WPA9"}]}`,
		`{"apps":[{"id":"a","platform":"symbian","install":"required"}]}`,
	}
	for _, c := range cases {
		if _, err := Parse([]byte(c)); err == nil {
			t.Errorf("accepted invalid policy %s", c)
		}
	}
}

func TestMergeLaterWins(t *testing.T) {
	base := &Document{
		Passcode:     &Passcode{Required: true, MinLength: 6},
		Restrictions: &Restrictions{Camera: bp(true), Bluetooth: bp(true)},
		WiFi:         []WiFi{{SSID: "corp", Security: "WPA2", Password: "old"}},
	}
	override := &Document{
		Passcode:     &Passcode{Required: true, MinLength: 10},
		Restrictions: &Restrictions{Camera: bp(false)},
		WiFi:         []WiFi{{SSID: "corp", Security: "WPA3", Password: "new"}, {SSID: "guest", Security: "NONE"}},
	}
	m := Merge(base, override)
	if m.Passcode.MinLength != 10 {
		t.Errorf("minLength = %d", m.Passcode.MinLength)
	}
	if *m.Restrictions.Camera || !*m.Restrictions.Bluetooth {
		t.Errorf("restrictions not merged field by field: %+v", m.Restrictions)
	}
	if len(m.WiFi) != 2 || m.WiFi[0].Password != "new" {
		t.Errorf("wifi = %+v", m.WiFi)
	}
	if base.Passcode.MinLength != 6 {
		t.Error("merge mutated its input")
	}
}

func TestPersonalTranslationsStayInTheWorkContainer(t *testing.T) {
	d := &Document{
		Passcode:     &Passcode{Required: true, MinLength: 8},
		Encryption:   &Encryption{Required: true},
		Restrictions: &Restrictions{Camera: bp(false), USBStorage: bp(false), Bluetooth: bp(false)},
	}
	for _, it := range d.WindowsItems(true) {
		if !strings.Contains(it.LocURI, "DeviceLock") {
			t.Errorf("personal Windows device got device-wide policy %s", it.LocURI)
		}
	}
	a := d.AndroidPolicy(true)
	for _, k := range []string{"encryptionPolicy", "usbDataAccess", "bluetoothDisabled", "maximumTimeToLock"} {
		if _, ok := a[k]; ok {
			t.Errorf("personal Android policy contains device-wide %s", k)
		}
	}
	pw := a["passwordPolicies"].([]any)[0].(map[string]any)
	if pw["passwordScope"] != "SCOPE_PROFILE" {
		t.Errorf("personal password scope = %v", pw["passwordScope"])
	}
	for _, pl := range d.ApplePayloads(true) {
		if pl["PayloadType"] == "com.apple.applicationaccess" {
			t.Error("personal Apple device got device restrictions")
		}
	}
}

func TestCorporateTranslations(t *testing.T) {
	d := &Document{
		Passcode:     &Passcode{Required: true, MinLength: 8, Complex: true},
		Encryption:   &Encryption{Required: true},
		Restrictions: &Restrictions{Camera: bp(false)},
		Apps:         []App{{ID: "com.example", Platform: "android", Install: "required"}},
	}
	win := map[string]string{}
	for _, it := range d.WindowsItems(false) {
		win[it.LocURI] = it.Data
	}
	if win["./Device/Vendor/MSFT/Policy/Config/DeviceLock/DevicePasswordEnabled"] != "0" {
		t.Error("DevicePasswordEnabled must be 0 (enabled)")
	}
	if win["./Device/Vendor/MSFT/Policy/Config/Camera/AllowCamera"] != "0" {
		t.Error("camera not disabled")
	}
	if win["./Device/Vendor/MSFT/Policy/Config/BitLocker/RequireDeviceEncryption"] != "1" {
		t.Error("BitLocker not required")
	}
	a := d.AndroidPolicy(false)
	if a["cameraAccess"] != "CAMERA_ACCESS_DISABLED" {
		t.Errorf("cameraAccess = %v", a["cameraAccess"])
	}
	apps := a["applications"].([]any)
	if apps[0].(map[string]any)["installType"] != "FORCE_INSTALLED" {
		t.Error("required app not force installed")
	}
}
