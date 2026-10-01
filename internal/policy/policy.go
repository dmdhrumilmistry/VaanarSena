// Package policy defines the platform-neutral policy document and merges the
// policies that apply to a device into one effective document.
package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Document is the neutral policy. Every section is optional; nil means "not
// managed by this policy", which lets several policies layer cleanly.
type Document struct {
	Passcode     *Passcode     `json:"passcode,omitempty"`
	Encryption   *Encryption   `json:"encryption,omitempty"`
	Restrictions *Restrictions `json:"restrictions,omitempty"`
	WiFi         []WiFi        `json:"wifi,omitempty"`
	OSUpdates    *OSUpdates    `json:"osUpdates,omitempty"`
	Apps         []App         `json:"apps,omitempty"`
	Custom       *Custom       `json:"custom,omitempty"`
}

// Passcode controls the device or work-profile unlock secret.
type Passcode struct {
	Required             bool `json:"required"`
	MinLength            int  `json:"minLength,omitempty"`
	Complex              bool `json:"complex,omitempty"`
	MaxInactivityMinutes int  `json:"maxInactivityMinutes,omitempty"`
	MaxFailedAttempts    int  `json:"maxFailedAttempts,omitempty"`
	ExpiryDays           int  `json:"expiryDays,omitempty"`
	HistoryLength        int  `json:"historyLength,omitempty"`
}

// Encryption requires storage encryption.
type Encryption struct {
	Required bool `json:"required"`
}

// Restrictions toggles device features. A nil pointer leaves the feature
// unmanaged; false disables it.
type Restrictions struct {
	Camera        *bool `json:"camera,omitempty"`
	ScreenCapture *bool `json:"screenCapture,omitempty"`
	USBStorage    *bool `json:"usbStorage,omitempty"`
	Bluetooth     *bool `json:"bluetooth,omitempty"`
	AppInstalls   *bool `json:"appInstalls,omitempty"`
	FactoryReset  *bool `json:"factoryReset,omitempty"`
	DeveloperMode *bool `json:"developerMode,omitempty"`
}

// WiFi is a managed wireless network.
type WiFi struct {
	SSID     string `json:"ssid"`
	Security string `json:"security"` // NONE, WEP, WPA2, WPA3
	Password string `json:"password,omitempty"`
	Hidden   bool   `json:"hidden,omitempty"`
	AutoJoin bool   `json:"autoJoin,omitempty"`
}

// OSUpdates controls update behaviour.
type OSUpdates struct {
	AutoInstall bool `json:"autoInstall"`
	DeferDays   int  `json:"deferDays,omitempty"`
}

// App is a managed application. ID is the bundle ID (Apple), package name
// (Android), Store product ID or MSI URL (Windows), or package name (Linux).
type App struct {
	ID       string `json:"id"`
	Platform string `json:"platform"` // apple, windows, android, linux
	Install  string `json:"install"`  // required, available, blocked
	URL      string `json:"url,omitempty"`
}

// Parse decodes and validates a document, rejecting unknown fields so a typo
// cannot silently disable a control.
func Parse(raw []byte) (*Document, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var d Document
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("policy: %w", err)
	}
	return &d, d.Validate()
}

// Validate checks value ranges.
func (d *Document) Validate() error {
	var errs []error
	if p := d.Passcode; p != nil {
		if p.MinLength < 0 || p.MinLength > 64 {
			errs = append(errs, errors.New("passcode.minLength must be 0-64"))
		}
		if p.MaxFailedAttempts != 0 && (p.MaxFailedAttempts < 2 || p.MaxFailedAttempts > 16) {
			errs = append(errs, errors.New("passcode.maxFailedAttempts must be 2-16"))
		}
		if p.MaxInactivityMinutes < 0 || p.MaxInactivityMinutes > 1440 {
			errs = append(errs, errors.New("passcode.maxInactivityMinutes must be 0-1440"))
		}
	}
	for i, w := range d.WiFi {
		if w.SSID == "" {
			errs = append(errs, fmt.Errorf("wifi[%d].ssid is required", i))
		}
		switch w.Security {
		case "NONE", "WEP", "WPA2", "WPA3":
		default:
			errs = append(errs, fmt.Errorf("wifi[%d].security must be NONE, WEP, WPA2 or WPA3", i))
		}
	}
	for i, a := range d.Apps {
		if a.ID == "" {
			errs = append(errs, fmt.Errorf("apps[%d].id is required", i))
		}
		switch a.Platform {
		case "apple", "windows", "android", "linux":
		default:
			errs = append(errs, fmt.Errorf("apps[%d].platform must be apple, windows, android or linux", i))
		}
		switch a.Install {
		case "required", "available", "blocked":
		default:
			errs = append(errs, fmt.Errorf("apps[%d].install must be required, available or blocked", i))
		}
	}
	if d.Custom != nil {
		if err := d.Custom.validate(); err != nil {
			errs = append(errs, err)
		}
	}
	if u := d.OSUpdates; u != nil && (u.DeferDays < 0 || u.DeferDays > 90) {
		errs = append(errs, errors.New("osUpdates.deferDays must be 0-90"))
	}
	return errors.Join(errs...)
}

// Merge layers docs in order: later documents override earlier ones section by
// section and field by field. Wi-Fi and apps are unioned, keyed by SSID and
// (platform, id); a later entry with the same key replaces the earlier one.
func Merge(docs ...*Document) *Document {
	out := &Document{}
	wifi := map[string]int{}
	apps := map[string]int{}
	for _, d := range docs {
		if d == nil {
			continue
		}
		if d.Passcode != nil {
			p := *d.Passcode
			out.Passcode = &p
		}
		if d.Encryption != nil {
			e := *d.Encryption
			out.Encryption = &e
		}
		if d.Restrictions != nil {
			if out.Restrictions == nil {
				out.Restrictions = &Restrictions{}
			}
			r, s := out.Restrictions, d.Restrictions
			pick(&r.Camera, s.Camera)
			pick(&r.ScreenCapture, s.ScreenCapture)
			pick(&r.USBStorage, s.USBStorage)
			pick(&r.Bluetooth, s.Bluetooth)
			pick(&r.AppInstalls, s.AppInstalls)
			pick(&r.FactoryReset, s.FactoryReset)
			pick(&r.DeveloperMode, s.DeveloperMode)
		}
		if d.OSUpdates != nil {
			u := *d.OSUpdates
			out.OSUpdates = &u
		}
		for _, w := range d.WiFi {
			if i, ok := wifi[w.SSID]; ok {
				out.WiFi[i] = w
				continue
			}
			wifi[w.SSID] = len(out.WiFi)
			out.WiFi = append(out.WiFi, w)
		}
		out.Custom = mergeCustom(out.Custom, d.Custom)
		for _, a := range d.Apps {
			k := a.Platform + "/" + a.ID
			if i, ok := apps[k]; ok {
				out.Apps[i] = a
				continue
			}
			apps[k] = len(out.Apps)
			out.Apps = append(out.Apps, a)
		}
	}
	return out
}

func pick(dst **bool, src *bool) {
	if src != nil {
		v := *src
		*dst = &v
	}
}

// AppsFor returns apps targeted at a platform family.
func (d *Document) AppsFor(family string) []App {
	var out []App
	for _, a := range d.Apps {
		if a.Platform == family {
			out = append(out, a)
		}
	}
	return out
}

// Allowed reports a restriction's value, treating unmanaged as allowed.
func Allowed(b *bool) bool { return b == nil || *b }
