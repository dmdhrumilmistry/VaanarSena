package apple

import (
	"context"
	"fmt"

	"howett.net/plist"

	"github.com/dmdhrumilmistry/VaanarSena/internal/command"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

// Inventory runs after a Refresh is acknowledged: the device is connected
// and the next commands in its queue are the two inventory reads.
func (d *Driver) queueInventory(ctx context.Context, dev *store.Device) {
	for _, typ := range []string{command.AppInventory, command.ProfileInventory} {
		if err := d.Svc.EnqueueInternal(ctx, dev, typ, nil); err != nil {
			d.Log.Debug("queue inventory", "device", dev.ID, "cmd", typ, "err", err)
		}
	}
}

// ingestInventory stores the response to an inventory command.
func (d *Driver) ingestInventory(ctx context.Context, dev *store.Device, typ string, body []byte) {
	var items []store.InventoryItem
	var kind string
	var err error
	switch typ {
	case command.AppInventory:
		kind = store.KindApp
		items, err = parseApps(body, dev.IsPersonal())
	case command.ProfileInventory:
		kind = store.KindProfile
		items, err = parseProfiles(body)
	}
	if err != nil {
		d.Log.Warn("parse apple inventory", "device", dev.ID, "cmd", typ, "err", err)
		return
	}
	if err := d.Store.ReplaceInventory(ctx, dev.ID, kind, items); err != nil {
		d.Log.Warn("store apple inventory", "device", dev.ID, "cmd", typ, "err", err)
	}
}

// parseApps reads an InstalledApplicationList response. With managedOnly
// (personal devices, where the request carried ManagedAppsOnly) every app in
// the response is one the MDM manages.
func parseApps(body []byte, managedOnly bool) ([]store.InventoryItem, error) {
	var resp struct {
		InstalledApplicationList []struct {
			Identifier       string
			Name             string
			ShortVersion     string
			Version          string
			Installing       bool
			IsValidated      bool
			IsManaged        bool
			BundleSize       uint64
			DynamicSize      uint64
			HasUpdateAvail   bool `plist:"HasUpdateAvailable"`
			AppStoreVendable bool
		}
	}
	if _, err := plist.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	if resp.InstalledApplicationList == nil {
		return nil, fmt.Errorf("response has no InstalledApplicationList")
	}
	out := make([]store.InventoryItem, 0, len(resp.InstalledApplicationList))
	for _, a := range resp.InstalledApplicationList {
		it := store.InventoryItem{
			Name: firstNonEmpty(a.Name, a.Identifier), Identifier: a.Identifier,
			Version: firstNonEmpty(a.ShortVersion, a.Version), Source: "apple", State: "installed",
			Managed: managedOnly || a.IsManaged,
		}
		if a.Installing {
			it.State = "installing"
		}
		det := map[string]any{}
		if a.ShortVersion != "" && a.Version != "" && a.Version != a.ShortVersion {
			det["build"] = a.Version
		}
		if a.BundleSize > 0 {
			det["bundleSize"] = a.BundleSize
		}
		if a.HasUpdateAvail {
			det["updateAvailable"] = true
		}
		it.Details = mustJSON(det)
		out = append(out, it)
	}
	return out, nil
}

// parseProfiles reads a ProfileList response.
func parseProfiles(body []byte) ([]store.InventoryItem, error) {
	var resp struct {
		ProfileList []struct {
			PayloadIdentifier        string
			PayloadDisplayName       string
			PayloadOrganization      string
			PayloadDescription       string
			PayloadUUID              string
			PayloadVersion           int
			PayloadRemovalDisallowed bool
			IsManaged                bool
			IsEncrypted              bool
			HasRemovalPasscode       bool
			PayloadContent           []struct {
				PayloadType string
			}
		}
	}
	if _, err := plist.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	if resp.ProfileList == nil {
		return nil, fmt.Errorf("response has no ProfileList")
	}
	out := make([]store.InventoryItem, 0, len(resp.ProfileList))
	for _, p := range resp.ProfileList {
		det := map[string]any{"uuid": p.PayloadUUID}
		if p.PayloadDescription != "" {
			det["description"] = p.PayloadDescription
		}
		if p.PayloadRemovalDisallowed {
			det["removalDisallowed"] = true
		}
		var types []string
		for _, c := range p.PayloadContent {
			if c.PayloadType != "" {
				types = append(types, c.PayloadType)
			}
		}
		if len(types) > 0 {
			det["payloadTypes"] = types
		}
		out = append(out, store.InventoryItem{
			Name: firstNonEmpty(p.PayloadDisplayName, p.PayloadIdentifier), Identifier: p.PayloadIdentifier,
			Version: versionString(p.PayloadVersion), Publisher: p.PayloadOrganization, Source: "apple",
			State: "installed", Managed: p.IsManaged, Details: mustJSON(det),
		})
	}
	return out, nil
}

func versionString(v int) string {
	if v == 0 {
		return ""
	}
	return fmt.Sprint(v)
}
