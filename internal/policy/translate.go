package policy

import (
	"fmt"
	"maps"
	"strconv"
)

// ApplePayloads translates the document into configuration profile payload
// dictionaries (without the PayloadUUID/Identifier boilerplate, which the
// Apple driver adds). personal restricts output to what User Enrollment
// permits: Apple ignores device-wide restrictions on user-enrolled devices.
func (d *Document) ApplePayloads(personal bool) []map[string]any {
	var out []map[string]any
	if p := d.Passcode; p != nil && p.Required {
		pl := map[string]any{
			"PayloadType":         "com.apple.mobiledevice.passwordpolicy",
			"forcePIN":            true,
			"allowSimple":         !p.Complex,
			"requireAlphanumeric": p.Complex,
		}
		if p.MinLength > 0 {
			pl["minLength"] = p.MinLength
		}
		if p.MaxInactivityMinutes > 0 {
			pl["maxInactivity"] = p.MaxInactivityMinutes
		}
		if p.MaxFailedAttempts > 0 {
			pl["maxFailedAttempts"] = p.MaxFailedAttempts
		}
		if p.ExpiryDays > 0 {
			pl["maxPINAgeInDays"] = p.ExpiryDays
		}
		if p.HistoryLength > 0 {
			pl["pinHistory"] = p.HistoryLength
		}
		out = append(out, pl)
	}
	if r := d.Restrictions; r != nil && !personal {
		pl := map[string]any{"PayloadType": "com.apple.applicationaccess"}
		set := func(key string, b *bool) {
			if b != nil {
				pl[key] = *b
			}
		}
		set("allowCamera", r.Camera)
		set("allowScreenShot", r.ScreenCapture)
		set("allowAppInstallation", r.AppInstalls)
		set("allowEraseContentAndSettings", r.FactoryReset)
		if len(pl) > 1 {
			out = append(out, pl)
		}
	}
	for _, w := range d.WiFi {
		pl := map[string]any{
			"PayloadType":    "com.apple.wifi.managed",
			"SSID_STR":       w.SSID,
			"HIDDEN_NETWORK": w.Hidden,
			"AutoJoin":       w.AutoJoin,
		}
		switch w.Security {
		case "NONE":
			pl["EncryptionType"] = "None"
		case "WEP":
			pl["EncryptionType"] = "WEP"
		case "WPA3":
			pl["EncryptionType"] = "WPA3"
		default:
			pl["EncryptionType"] = "WPA2"
		}
		if w.Password != "" {
			pl["Password"] = w.Password
		}
		out = append(out, pl)
	}
	if u := d.OSUpdates; u != nil && u.DeferDays > 0 && !personal {
		out = append(out, map[string]any{
			"PayloadType":                 "com.apple.applicationaccess",
			"forceDelayedSoftwareUpdates": true,
			"enforcedSoftwareUpdateDelay": u.DeferDays,
		})
	}
	if d.Custom.AppliesTo(personal) {
		out = append(out, d.Custom.applePayloads()...)
	}
	return out
}

// SyncMLItem is one OMA-DM node write for Windows.
type SyncMLItem struct {
	Op     string // Replace (default), Add, Exec, Delete
	LocURI string
	Format string // int, chr, bool, xml, b64
	Data   string
}

// WindowsItems translates the document into Policy CSP node values. personal
// limits output to user-scoped policies, which is what Windows applies on an
// MDM-only ("work or school account") enrollment of a personal device.
func (d *Document) WindowsItems(personal bool) []SyncMLItem {
	const dev = "./Device/Vendor/MSFT/Policy/Config/"
	var out []SyncMLItem
	add := func(path, format, data string) {
		out = append(out, SyncMLItem{LocURI: dev + path, Format: format, Data: data})
	}
	b2i := func(b bool) string {
		if b {
			return "1"
		}
		return "0"
	}
	if p := d.Passcode; p != nil {
		add("DeviceLock/DevicePasswordEnabled", "int", b2i(!p.Required)) // 0 = enabled, per the CSP
		if p.Required {
			if p.MinLength > 0 {
				add("DeviceLock/MinDevicePasswordLength", "int", strconv.Itoa(p.MinLength))
			}
			if p.Complex {
				// 0 = alphanumeric required; the default (2) accepts a PIN.
				add("DeviceLock/AlphanumericDevicePasswordRequired", "int", "0")
				add("DeviceLock/MinDevicePasswordComplexCharacters", "int", "3")
			}
			if p.MaxInactivityMinutes > 0 {
				add("DeviceLock/MaxInactivityTimeDeviceLock", "int", strconv.Itoa(p.MaxInactivityMinutes))
			}
			if p.MaxFailedAttempts > 0 {
				add("DeviceLock/MaxDevicePasswordFailedAttempts", "int", strconv.Itoa(p.MaxFailedAttempts))
			}
			if p.ExpiryDays > 0 {
				add("DeviceLock/DevicePasswordExpiration", "int", strconv.Itoa(p.ExpiryDays))
			}
			if p.HistoryLength > 0 {
				add("DeviceLock/DevicePasswordHistory", "int", strconv.Itoa(p.HistoryLength))
			}
		}
	}
	if personal {
		// Device-scoped hardware and encryption policies are not applied to
		// personal devices: they would affect the owner's whole machine.
		return append(out, d.customWindows(personal)...)
	}
	if e := d.Encryption; e != nil && e.Required {
		add("BitLocker/RequireDeviceEncryption", "int", "1")
		add("BitLocker/AllowWarningForOtherDiskEncryption", "int", "0")
	}
	if r := d.Restrictions; r != nil {
		if r.Camera != nil {
			add("Camera/AllowCamera", "int", b2i(*r.Camera))
		}
		if r.Bluetooth != nil {
			add("Connectivity/AllowBluetooth", "int", map[bool]string{true: "2", false: "0"}[*r.Bluetooth])
		}
		if r.USBStorage != nil {
			add("Storage/RemovableDiskDenyWriteAccess", "int", b2i(!*r.USBStorage))
		}
		if r.AppInstalls != nil {
			add("ApplicationManagement/AllowAllTrustedApps", "int", map[bool]string{true: "1", false: "0"}[*r.AppInstalls])
		}
		if r.DeveloperMode != nil {
			add("ApplicationManagement/AllowDeveloperUnlock", "int", b2i(*r.DeveloperMode))
		}
	}
	if u := d.OSUpdates; u != nil {
		if u.AutoInstall {
			add("Update/AllowAutoUpdate", "int", "3") // auto install and restart at scheduled time
		}
		if u.DeferDays > 0 {
			add("Update/DeferQualityUpdatesPeriodInDays", "int", strconv.Itoa(min(u.DeferDays, 30)))
		}
	}
	return append(out, d.customWindows(personal)...)
}

func (d *Document) customWindows(personal bool) []SyncMLItem {
	if !d.Custom.AppliesTo(personal) {
		return nil
	}
	var out []SyncMLItem
	for _, n := range d.Custom.Windows {
		op := n.Op
		if op == "" {
			op = "Replace"
		}
		out = append(out, SyncMLItem{Op: op, LocURI: n.LocURI, Format: n.Format, Data: n.Data})
	}
	return out
}

// AndroidPolicy translates the document into an Android Management API Policy
// resource. personal produces a work-profile policy: AMAPI applies password
// requirements to the work profile and ignores device-wide controls there.
func (d *Document) AndroidPolicy(personal bool) map[string]any {
	pol := map[string]any{}
	if p := d.Passcode; p != nil && p.Required {
		req := map[string]any{"passwordScope": "SCOPE_DEVICE"}
		if personal {
			req["passwordScope"] = "SCOPE_PROFILE"
		}
		switch {
		case p.Complex:
			req["passwordQuality"] = "COMPLEX"
		case p.MinLength > 0:
			req["passwordQuality"] = "NUMERIC_COMPLEX"
		default:
			req["passwordQuality"] = "SOMETHING"
		}
		if p.MinLength > 0 {
			req["passwordMinimumLength"] = p.MinLength
		}
		if p.MaxFailedAttempts > 0 {
			req["maximumFailedPasswordsForWipe"] = p.MaxFailedAttempts
		}
		if p.ExpiryDays > 0 {
			req["passwordExpirationTimeout"] = fmt.Sprintf("%ds", p.ExpiryDays*86400)
		}
		if p.HistoryLength > 0 {
			req["passwordHistoryLength"] = p.HistoryLength
		}
		pol["passwordPolicies"] = []any{req}
		if p.MaxInactivityMinutes > 0 && !personal {
			pol["maximumTimeToLock"] = strconv.Itoa(p.MaxInactivityMinutes * 60 * 1000)
		}
	}
	if e := d.Encryption; e != nil && e.Required && !personal {
		pol["encryptionPolicy"] = "ENABLED_WITH_PASSWORD"
	}
	if r := d.Restrictions; r != nil {
		if r.Camera != nil {
			pol["cameraAccess"] = map[bool]string{true: "CAMERA_ACCESS_USER_CHOICE", false: "CAMERA_ACCESS_DISABLED"}[*r.Camera]
		}
		if r.ScreenCapture != nil {
			pol["screenCaptureDisabled"] = !*r.ScreenCapture
		}
		if !personal {
			if r.USBStorage != nil {
				pol["usbDataAccess"] = map[bool]string{true: "ALLOW_USB_DATA_TRANSFER", false: "DISALLOW_USB_FILE_TRANSFER"}[*r.USBStorage]
			}
			if r.Bluetooth != nil {
				pol["bluetoothDisabled"] = !*r.Bluetooth
			}
			if r.FactoryReset != nil {
				pol["factoryResetDisabled"] = !*r.FactoryReset
			}
			if r.DeveloperMode != nil {
				pol["advancedSecurityOverrides"] = map[string]any{
					"developerSettings": map[bool]string{true: "DEVELOPER_SETTINGS_ALLOWED", false: "DEVELOPER_SETTINGS_DISABLED"}[*r.DeveloperMode],
				}
			}
		}
		if r.AppInstalls != nil && !*r.AppInstalls {
			pol["installAppsDisabled"] = true
		}
	}
	if len(d.WiFi) > 0 && !personal {
		var nets []any
		for _, w := range d.WiFi {
			sec := map[string]string{"NONE": "None", "WEP": "WEP-PSK", "WPA2": "WPA-PSK", "WPA3": "WPA3-SAE"}[w.Security]
			wifi := map[string]any{"SSID": w.SSID, "Security": sec, "AutoConnect": w.AutoJoin, "HiddenSSID": w.Hidden}
			if w.Password != "" {
				wifi["Passphrase"] = w.Password
			}
			nets = append(nets, map[string]any{"GUID": "vs-" + w.SSID, "Name": w.SSID, "Type": "WiFi", "WiFi": wifi})
		}
		pol["openNetworkConfiguration"] = map[string]any{"NetworkConfigurations": nets}
	}
	if u := d.OSUpdates; u != nil && !personal {
		if u.AutoInstall {
			pol["systemUpdate"] = map[string]any{"type": "AUTOMATIC"}
		} else if u.DeferDays > 0 {
			pol["systemUpdate"] = map[string]any{"type": "POSTPONE"}
		}
	}
	var apps []any
	for _, a := range d.AppsFor("android") {
		it := map[string]string{"required": "FORCE_INSTALLED", "available": "AVAILABLE", "blocked": "BLOCKED"}[a.Install]
		apps = append(apps, map[string]any{"packageName": a.ID, "installType": it})
	}
	if len(apps) > 0 {
		pol["applications"] = apps
	}
	if d.Custom.AppliesTo(personal) {
		maps.Copy(pol, d.Custom.Android) // raw AMAPI fields override the translation
	}
	return pol
}
