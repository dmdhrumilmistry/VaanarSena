// Package android manages Android devices through the Android Management API
// (AMAPI). Corporate devices are fully managed; personal devices get a work
// profile, and every command on them is scoped to that profile by Android.
package android

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dmdhrumilmistry/VaanarSena/internal/command"
	"github.com/dmdhrumilmistry/VaanarSena/internal/google"
	"github.com/dmdhrumilmistry/VaanarSena/internal/mdm"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

const scope = "https://www.googleapis.com/auth/androidmanagement"

// Driver is the Android driver.
type Driver struct {
	Store      *store.Store
	Svc        *mdm.Service
	Enterprise string // enterprises/LC0xxxx
	Log        *slog.Logger
	api        *google.Client
	mu         sync.Mutex // serialises queue processing per process
}

// DefaultBase is the Android Management API endpoint.
const DefaultBase = "https://androidmanagement.googleapis.com/v1/"

// New authenticates to AMAPI with a key file.
func New(ctx context.Context, st *store.Store, svc *mdm.Service, credsFile, enterprise string, log *slog.Logger) (*Driver, error) {
	data, err := os.ReadFile(credsFile)
	if err != nil {
		return nil, err
	}
	return NewFromJSON(ctx, st, svc, data, enterprise, DefaultBase, log)
}

// NewFromJSON authenticates to AMAPI with an in-memory key. base is normally
// DefaultBase; tests point it at a fake.
func NewFromJSON(ctx context.Context, st *store.Store, svc *mdm.Service, creds []byte, enterprise, base string, log *slog.Logger) (*Driver, error) {
	api, err := google.ServiceAccountFromJSON(ctx, creds, base, scope)
	if err != nil {
		return nil, err
	}
	return &Driver{Store: st, Svc: svc, Enterprise: enterprise, Log: log, api: api}, nil
}

// Signup is a pending Android Enterprise signup.
type Signup struct {
	Name string `json:"name"` // signupUrls/...
	URL  string `json:"url"`  // where the admin completes signup with Google
}

// CreateSignupURL starts the Android Enterprise signup. Google redirects the
// admin's browser to callbackURL with an enterpriseToken query parameter.
func CreateSignupURL(ctx context.Context, creds []byte, base, projectID, callbackURL string) (*Signup, error) {
	api, err := google.ServiceAccountFromJSON(ctx, creds, base, scope)
	if err != nil {
		return nil, err
	}
	var s Signup
	q := url.Values{"projectId": {projectID}, "callbackUrl": {callbackURL}}
	if err := api.Do(ctx, http.MethodPost, "signupUrls?"+q.Encode(), map[string]any{}, &s); err != nil {
		return nil, err
	}
	if s.Name == "" || s.URL == "" {
		return nil, errors.New("Google returned an empty signup URL")
	}
	return &s, nil
}

// CreateEnterprise completes signup and returns the enterprise name
// (enterprises/LC0...).
func CreateEnterprise(ctx context.Context, creds []byte, base, projectID, signupName, enterpriseToken, displayName string) (string, error) {
	api, err := google.ServiceAccountFromJSON(ctx, creds, base, scope)
	if err != nil {
		return "", err
	}
	var e struct {
		Name string `json:"name"`
	}
	q := url.Values{"projectId": {projectID}, "signupUrlName": {signupName}, "enterpriseToken": {enterpriseToken}}
	if err := api.Do(ctx, http.MethodPost, "enterprises?"+q.Encode(), map[string]any{"enterpriseDisplayName": displayName}, &e); err != nil {
		return "", err
	}
	if !strings.HasPrefix(e.Name, "enterprises/") {
		return "", fmt.Errorf("unexpected enterprise name %q", e.Name)
	}
	return e.Name, nil
}

// Platforms implements mdm.Driver.
func (d *Driver) Platforms() []string { return []string{store.PlatformAndroid} }

// tokenData is embedded in the AMAPI enrollment token so the synced device can
// be bound to the VaanarSena token (and therefore its ownership).
type tokenData struct {
	TokenID string `json:"vsToken"`
}

func basePolicy(ownership string) string { return "vs-base-" + ownership }

// CreateEnrollment creates an AMAPI enrollment token for a VaanarSena token and
// returns the artefacts the user needs: a QR code payload for fully managed
// provisioning and an enrollment link for work profiles.
func (d *Driver) CreateEnrollment(ctx context.Context, t *store.EnrollmentToken) (map[string]any, error) {
	personal := t.Ownership == store.OwnershipPersonal
	pol := basePolicy(t.Ownership)
	if err := d.api.Do(ctx, http.MethodPatch, d.Enterprise+"/policies/"+pol, map[string]any{"statusReportingSettings": statusReporting(personal)}, nil); err != nil {
		return nil, fmt.Errorf("create base policy: %w", err)
	}
	extra, _ := json.Marshal(tokenData{TokenID: t.ID})
	usage := "PERSONAL_USAGE_DISALLOWED"
	if personal {
		usage = "PERSONAL_USAGE_ALLOWED"
	}
	dur := time.Until(t.ExpiresAt)
	if dur < time.Hour {
		dur = time.Hour
	}
	if dur > 90*24*time.Hour {
		dur = 90 * 24 * time.Hour
	}
	var out struct {
		Name   string `json:"name"`
		Value  string `json:"value"`
		QRCode string `json:"qrCode"`
	}
	err := d.api.Do(ctx, http.MethodPost, d.Enterprise+"/enrollmentTokens", map[string]any{
		"policyName":         d.Enterprise + "/policies/" + pol,
		"duration":           fmt.Sprintf("%ds", int(dur.Seconds())),
		"additionalData":     string(extra),
		"allowPersonalUsage": usage,
		"oneTimeOnly":        t.MaxUses == 1,
	}, &out)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"amapiToken": out.Name,
		"value":      out.Value,
		"qrCode":     out.QRCode,
		"enrollUrl":  "https://enterprise.google.com/android/enroll?et=" + url.QueryEscape(out.Value),
	}, nil
}

type amapiDevice struct {
	Name                string `json:"name"`
	ManagementMode      string `json:"managementMode"`
	State               string `json:"state"`
	AppliedState        string `json:"appliedState"`
	PolicyName          string `json:"policyName"`
	PolicyCompliant     bool   `json:"policyCompliant"`
	EnrollmentTokenData string `json:"enrollmentTokenData"`
	LastStatusReport    string `json:"lastStatusReportTime"`
	HardwareInfo        struct {
		Brand        string `json:"brand"`
		Model        string `json:"model"`
		Manufacturer string `json:"manufacturer"`
		SerialNumber string `json:"serialNumber"`
	} `json:"hardwareInfo"`
	SoftwareInfo struct {
		AndroidVersion string `json:"androidVersion"`
		SecurityPatch  string `json:"securityPatchLevel"`
	} `json:"softwareInfo"`
	Ownership          string              `json:"ownership"`
	ApplicationReports []applicationReport `json:"applicationReports"`
}

// Sync pulls the device list and reconciles it with the database. AMAPI can
// push changes via Cloud Pub/Sub; polling keeps the deployment self-contained.
func (d *Driver) Sync(ctx context.Context) error {
	page := ""
	for {
		var resp struct {
			Devices       []amapiDevice `json:"devices"`
			NextPageToken string        `json:"nextPageToken"`
		}
		path := d.Enterprise + "/devices?pageSize=100"
		if page != "" {
			path += "&pageToken=" + url.QueryEscape(page)
		}
		if err := d.api.Do(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return err
		}
		for i := range resp.Devices {
			if err := d.reconcile(ctx, &resp.Devices[i]); err != nil {
				d.Log.Warn("android reconcile", "device", resp.Devices[i].Name, "err", err)
			}
		}
		if resp.NextPageToken == "" {
			return nil
		}
		page = resp.NextPageToken
	}
}

func (d *Driver) reconcile(ctx context.Context, a *amapiDevice) error {
	existing, err := d.Store.DeviceByNativeID(ctx, a.Name, store.PlatformAndroid)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	status := store.StatusEnrolled
	if a.State == "DELETED" {
		status = store.StatusRetired
	}
	facts := map[string]any{"android": map[string]any{
		"managementMode": a.ManagementMode, "state": a.State, "appliedState": a.AppliedState,
		"securityPatchLevel": a.SoftwareInfo.SecurityPatch, "manufacturer": a.HardwareInfo.Manufacturer,
		"lastStatusReport": a.LastStatusReport, "appliedPolicy": a.PolicyName,
	}}
	model := strings.TrimSpace(a.HardwareInfo.Manufacturer + " " + a.HardwareInfo.Model)
	compliant := a.PolicyCompliant

	if existing != nil && err == nil {
		p := store.DevicePatch{Model: &model, OSVersion: &a.SoftwareInfo.AndroidVersion, Facts: facts, Compliant: &compliant, Seen: true}
		if existing.Status != store.StatusWiped {
			p.Status = &status
		}
		if !existing.IsPersonal() {
			p.Serial = &a.HardwareInfo.SerialNumber
		}
		if _, err := d.Store.PatchDevice(ctx, existing.ID, p); err != nil {
			return err
		}
		d.storeApps(ctx, existing, a)
		return nil
	}

	// New device: bind it to the VaanarSena token it enrolled with.
	var td tokenData
	_ = json.Unmarshal([]byte(a.EnrollmentTokenData), &td)
	var tok *store.EnrollmentToken
	if td.TokenID != "" {
		tok, _ = d.Store.EnrollmentTokenByID(ctx, td.TokenID)
	}
	if tok == nil {
		d.Log.Info("ignoring Android device not enrolled through VaanarSena", "device", a.Name)
		return nil
	}
	if _, err := d.Store.DB.Exec(ctx, `UPDATE enrollment_tokens SET uses = uses + 1 WHERE id = $1`, tok.ID); err != nil {
		return err
	}
	serial := a.HardwareInfo.SerialNumber
	if tok.Ownership == store.OwnershipPersonal {
		serial = "" // not collected for BYOD
	}
	now := time.Now()
	dev, err := d.Store.UpsertDevice(ctx, &store.Device{
		Platform: store.PlatformAndroid, Ownership: tok.Ownership, Status: status, Model: model,
		Serial: serial, OSVersion: a.SoftwareInfo.AndroidVersion, Assignee: tok.Assignee, NativeID: a.Name,
		EnrollmentTokenID: &tok.ID, EnrolledAt: &now, Facts: mustJSON(facts),
		PlatformIDs: mustJSON(map[string]any{"amapiName": a.Name}),
	})
	if err != nil {
		return err
	}
	mdm.AttachToGroup(ctx, d.Store, tok, dev.ID)
	d.storeApps(ctx, dev, a)
	_ = d.Store.Audit(ctx, "device", "device.enroll", dev.ID, map[string]any{"platform": "android", "ownership": tok.Ownership}, "")
	d.Svc.Bootstrap(ctx, dev)
	return nil
}

// Wake executes the device's queue immediately against AMAPI.
func (d *Driver) Wake(ctx context.Context, dev *store.Device) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	cmds, err := d.Store.PendingCommands(ctx, dev.ID, 50)
	if err != nil {
		return err
	}
	for _, c := range cmds {
		resp, err := d.execute(ctx, dev, c)
		if err != nil {
			_ = d.Store.CompleteCommand(ctx, c.ID, dev.ID, store.CmdError, "", err.Error())
			continue
		}
		_ = d.Store.CompleteCommand(ctx, c.ID, dev.ID, store.CmdAcknowledged, resp, "")
	}
	return nil
}

func (d *Driver) devicePolicy(dev *store.Device) string { return "vs-device-" + dev.ID }

func (d *Driver) execute(ctx context.Context, dev *store.Device, c *store.Command) (string, error) {
	p, err := command.ParseParams(c.Params)
	if err != nil {
		return "", err
	}
	issue := func(body map[string]any) (string, error) {
		var op map[string]any
		if err := d.api.Do(ctx, http.MethodPost, dev.NativeID+":issueCommand", body, &op); err != nil {
			return "", err
		}
		b, _ := json.Marshal(op)
		return string(b), nil
	}
	switch c.Type {
	case command.Refresh:
		var a amapiDevice
		if err := d.api.Do(ctx, http.MethodGet, dev.NativeID, nil, &a); err != nil {
			return "", err
		}
		return "", d.reconcile(ctx, &a)
	case command.ApplyPolicy:
		doc, err := mdm.EffectivePolicy(ctx, d.Store, dev.ID)
		if err != nil {
			return "", err
		}
		pol := doc.AndroidPolicy(dev.IsPersonal())
		pol["statusReportingSettings"] = statusReporting(dev.IsPersonal())
		name := d.Enterprise + "/policies/" + d.devicePolicy(dev)
		if err := d.api.Do(ctx, http.MethodPatch, name, pol, nil); err != nil {
			return "", err
		}
		if err := d.api.Do(ctx, http.MethodPatch, dev.NativeID+"?updateMask=policyName", map[string]any{"policyName": name}, nil); err != nil {
			return "", err
		}
		b, _ := json.Marshal(pol)
		_ = d.Store.MarkCommandSent(ctx, c.ID, string(b))
		return "", nil
	case command.Lock:
		return issue(map[string]any{"type": "LOCK"})
	case command.Restart:
		return issue(map[string]any{"type": "REBOOT"})
	case command.ClearPasscode:
		return issue(map[string]any{"type": "RESET_PASSWORD", "newPassword": ""})
	case command.Retire:
		if dev.IsPersonal() {
			// Deleting a work-profile device removes only the work profile.
			return "", d.deleteDevice(ctx, dev, store.StatusRetired, "")
		}
		// Company-owned work profile devices can be handed to the user; fully
		// managed devices cannot be unmanaged without a wipe.
		return issue(map[string]any{"type": "RELINQUISH_OWNERSHIP"})
	case command.Wipe:
		return "", d.deleteDevice(ctx, dev, store.StatusWiped, "WIPE_EXTERNAL_STORAGE")
	default:
		_ = p
		return "", fmt.Errorf("command %s not supported on Android", c.Type)
	}
}

func (d *Driver) deleteDevice(ctx context.Context, dev *store.Device, status, flags string) error {
	path := dev.NativeID
	if flags != "" {
		path += "?wipeDataFlags=" + flags
	}
	if err := d.api.Do(ctx, http.MethodDelete, path, nil, nil); err != nil {
		return err
	}
	_, err := d.Store.PatchDevice(ctx, dev.ID, store.DevicePatch{Status: &status})
	_ = d.Store.CancelPendingCommands(ctx, dev.ID)
	return err
}

// Run syncs periodically until ctx ends.
func (d *Driver) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := d.Sync(ctx); err != nil && ctx.Err() == nil {
			d.Log.Warn("android sync", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
