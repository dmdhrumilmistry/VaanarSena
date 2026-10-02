// Package chromeos manages ChromeOS devices through the Google Admin SDK
// Directory API. ChromeOS devices are enrolled into the Google Workspace or
// Chrome Enterprise domain on the device itself; VaanarSena imports them as
// corporate devices and drives commands. ChromeOS has no BYOD MDM model.
package chromeos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/dmdhrumilmistry/VaanarSena/internal/command"
	"github.com/dmdhrumilmistry/VaanarSena/internal/google"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

const scope = "https://www.googleapis.com/auth/admin.directory.device.chromeos"

// Driver is the ChromeOS driver.
type Driver struct {
	Store    *store.Store
	Customer string
	Log      *slog.Logger
	api      *google.Client
	mu       sync.Mutex
}

// DefaultBase is the Admin SDK Directory API endpoint.
const DefaultBase = "https://admin.googleapis.com/admin/directory/v1/"

// New authenticates to the Admin SDK by impersonating adminSubject.
func New(ctx context.Context, st *store.Store, credsFile, adminSubject, customer string, log *slog.Logger) (*Driver, error) {
	data, err := os.ReadFile(credsFile)
	if err != nil {
		return nil, err
	}
	return NewFromJSON(ctx, st, data, adminSubject, customer, DefaultBase, log)
}

// NewFromJSON is New with an in-memory key and an overridable base URL.
func NewFromJSON(ctx context.Context, st *store.Store, creds []byte, adminSubject, customer, base string, log *slog.Logger) (*Driver, error) {
	api, err := google.DelegatedFromJSON(ctx, creds, adminSubject, base+"customer/"+url.PathEscape(customer), scope)
	if err != nil {
		return nil, err
	}
	return &Driver{Store: st, Customer: customer, Log: log, api: api}, nil
}

// Platforms implements mdm.Driver.
func (d *Driver) Platforms() []string { return []string{store.PlatformChromeOS} }

type chromeDevice struct {
	DeviceID             string `json:"deviceId"`
	SerialNumber         string `json:"serialNumber"`
	Status               string `json:"status"`
	Model                string `json:"model"`
	OSVersion            string `json:"osVersion"`
	AnnotatedUser        string `json:"annotatedUser"`
	AnnotatedAssetID     string `json:"annotatedAssetId"`
	OrgUnitPath          string `json:"orgUnitPath"`
	LastSync             string `json:"lastSync"`
	PlatformVersion      string `json:"platformVersion"`
	BootMode             string `json:"bootMode"`
	AutoUpdateExpiration string `json:"autoUpdateExpiration"`
}

// Sync imports every provisioned ChromeOS device.
func (d *Driver) Sync(ctx context.Context) error {
	page := ""
	for {
		var resp struct {
			ChromeOSDevices []chromeDevice `json:"chromeosdevices"`
			NextPageToken   string         `json:"nextPageToken"`
		}
		path := "/devices/chromeos?projection=BASIC&maxResults=200"
		if page != "" {
			path += "&pageToken=" + url.QueryEscape(page)
		}
		if err := d.api.Do(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return err
		}
		for i := range resp.ChromeOSDevices {
			if err := d.upsert(ctx, &resp.ChromeOSDevices[i]); err != nil {
				d.Log.Warn("chromeos upsert", "device", resp.ChromeOSDevices[i].DeviceID, "err", err)
			}
		}
		if resp.NextPageToken == "" {
			return nil
		}
		page = resp.NextPageToken
	}
}

func (d *Driver) upsert(ctx context.Context, c *chromeDevice) error {
	status := store.StatusEnrolled
	switch c.Status {
	case "DEPROVISIONED":
		status = store.StatusRetired
	case "INACTIVE", "PRE_PROVISIONED":
		status = store.StatusEnrolling
	}
	facts, _ := json.Marshal(map[string]any{"chromeos": c})
	existing, err := d.Store.DeviceByNativeID(ctx, c.DeviceID, store.PlatformChromeOS)
	if err == nil && existing.Status == store.StatusWiped && status == store.StatusEnrolled {
		status = store.StatusWiped
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	now := time.Now()
	_, err = d.Store.UpsertDevice(ctx, &store.Device{
		Platform: store.PlatformChromeOS, Ownership: store.OwnershipCorporate, Status: status,
		Name: firstNonEmpty(c.AnnotatedAssetID, c.SerialNumber), Serial: c.SerialNumber, Model: c.Model,
		OSVersion: c.OSVersion, Assignee: c.AnnotatedUser, NativeID: c.DeviceID, EnrolledAt: &now,
		Facts: facts, PlatformIDs: []byte(fmt.Sprintf(`{"deviceId":%q,"orgUnitPath":%q}`, c.DeviceID, c.OrgUnitPath)),
	})
	return err
}

// Wake executes queued commands through the Admin SDK.
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

func (d *Driver) execute(ctx context.Context, dev *store.Device, c *store.Command) (string, error) {
	id := url.PathEscape(dev.NativeID)
	issue := func(typ string) (string, error) {
		var out map[string]any
		if err := d.api.Do(ctx, http.MethodPost, "/devices/chromeos/"+id+":issueCommand", map[string]any{"commandType": typ}, &out); err != nil {
			return "", err
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	}
	action := func(body map[string]any) error {
		return d.api.Do(ctx, http.MethodPost, "/devices/chromeos/"+id+"/action", body, nil)
	}
	switch c.Type {
	case command.Refresh:
		var cd chromeDevice
		if err := d.api.Do(ctx, http.MethodGet, "/devices/chromeos/"+id+"?projection=BASIC", nil, &cd); err != nil {
			return "", err
		}
		return "", d.upsert(ctx, &cd)
	case command.Restart:
		return issue("REBOOT")
	case command.Lock:
		// "disable" locks the device to a disabled screen until re-enabled.
		return "", action(map[string]any{"action": "disable"})
	case command.Wipe:
		out, err := issue("REMOTE_POWERWASH")
		if err == nil {
			st := store.StatusWiped
			_, _ = d.Store.PatchDevice(ctx, dev.ID, store.DevicePatch{Status: &st})
		}
		return out, err
	case command.Retire:
		if err := action(map[string]any{"action": "deprovision", "deprovisionReason": "retiring_device"}); err != nil {
			return "", err
		}
		st := store.StatusRetired
		_, err := d.Store.PatchDevice(ctx, dev.ID, store.DevicePatch{Status: &st})
		return "", err
	default:
		return "", fmt.Errorf("command %s not supported on ChromeOS", c.Type)
	}
}

// Run syncs periodically until ctx ends.
func (d *Driver) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := d.Sync(ctx); err != nil && ctx.Err() == nil {
			d.Log.Warn("chromeos sync", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
