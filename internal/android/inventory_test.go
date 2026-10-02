package android

import (
	"encoding/json"
	"testing"
)

const deviceWithReports = `{
  "name": "enterprises/LC0fake/devices/3a1b2c4d5e6f",
  "managementMode": "PROFILE_OWNER",
  "state": "ACTIVE",
  "policyCompliant": true,
  "softwareInfo": {"androidVersion": "14", "securityPatchLevel": "2026-08-05"},
  "applicationReports": [
    {"packageName": "com.android.chrome", "displayName": "Chrome", "versionName": "126.0.6478.122", "versionCode": 647812231,
     "state": "INSTALLED", "applicationSource": "INSTALLED_FROM_PLAY_STORE", "installerPackageName": "com.android.vending"},
    {"packageName": "com.acme.mail", "displayName": "Acme Mail", "versionName": "5.2.0", "versionCode": 520,
     "state": "INSTALLED", "applicationSource": "ANDROID_APP_SOURCE_UNSPECIFIED", "installerPackageName": "com.android.vending"},
    {"packageName": "com.android.vending", "versionName": "41.2.25", "versionCode": 84122500,
     "state": "INSTALLED", "applicationSource": "SYSTEM_APP_UPDATED_VERSION"},
    {"packageName": "com.old.app", "displayName": "Old", "versionName": "1.0", "state": "REMOVED"},
    {"displayName": "no package name", "state": "INSTALLED"}
  ]
}`

func TestMapApplicationReports(t *testing.T) {
	var dev amapiDevice
	if err := json.Unmarshal([]byte(deviceWithReports), &dev); err != nil {
		t.Fatal(err)
	}
	items := mapApplicationReports(dev.ApplicationReports, map[string]bool{"com.acme.mail": true})
	if len(items) != 3 {
		t.Fatalf("removed apps and apps without a package name must be skipped: %+v", items)
	}
	chrome := items[0]
	if chrome.Name != "Chrome" || chrome.Identifier != "com.android.chrome" || chrome.Version != "126.0.6478.122" ||
		chrome.Source != "android" || chrome.State != "installed" || !chrome.Managed || chrome.Publisher != "com.android.vending" {
		t.Errorf("chrome: %+v", chrome)
	}
	var det map[string]any
	if err := json.Unmarshal(chrome.Details, &det); err != nil || det["applicationSource"] != "INSTALLED_FROM_PLAY_STORE" {
		t.Errorf("details: %s", chrome.Details)
	}
	// In the policy app list, so managed although the source is unspecified.
	if !items[1].Managed {
		t.Error("app in the policy list is not managed")
	}
	// A system app with no display name is shown by package name and is not managed.
	if sys := items[2]; sys.Name != "com.android.vending" || sys.Managed {
		t.Errorf("system app: %+v", sys)
	}
}

func TestStatusReportingEnablesApplicationReports(t *testing.T) {
	for _, personal := range []bool{false, true} {
		s := statusReporting(personal)
		if s["applicationReportsEnabled"] != true {
			t.Errorf("personal=%v: application reports not enabled", personal)
		}
		if s["hardwareStatusEnabled"] != !personal {
			t.Errorf("personal=%v: hardware status = %v", personal, s["hardwareStatusEnabled"])
		}
		if r, _ := s["applicationReportingSettings"].(map[string]any); r["includeRemovedApps"] != false {
			t.Errorf("personal=%v: removed apps are included", personal)
		}
	}
}
