package android

import (
	"context"
	"encoding/json"

	"github.com/dmdhrumilmistry/VaanarSena/internal/mdm"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

// statusReporting is the statusReportingSettings of every policy: device
// facts plus application reports (without removed apps). Hardware status is
// withheld on personal devices. On work-profile devices AMAPI limits
// application reports to the work profile.
func statusReporting(personal bool) map[string]any {
	return map[string]any{
		"softwareInfoEnabled": true, "hardwareStatusEnabled": !personal, "deviceSettingsEnabled": true,
		"applicationReportsEnabled":    true,
		"applicationReportingSettings": map[string]any{"includeRemovedApps": false},
	}
}

// applicationReport is an AMAPI ApplicationReport.
type applicationReport struct {
	PackageName          string `json:"packageName"`
	DisplayName          string `json:"displayName"`
	VersionName          string `json:"versionName"`
	VersionCode          int    `json:"versionCode"`
	State                string `json:"state"` // INSTALLED or REMOVED
	ApplicationSource    string `json:"applicationSource"`
	InstallerPackageName string `json:"installerPackageName"`
}

// mapApplicationReports converts AMAPI application reports to inventory
// items. An app is managed when it came from managed Google Play or is in the
// policy's app list (managedIDs).
func mapApplicationReports(reports []applicationReport, managedIDs map[string]bool) []store.InventoryItem {
	out := make([]store.InventoryItem, 0, len(reports))
	for _, r := range reports {
		if r.PackageName == "" || r.State == "REMOVED" {
			continue
		}
		det := map[string]any{}
		if r.VersionCode != 0 {
			det["versionCode"] = r.VersionCode
		}
		if r.ApplicationSource != "" {
			det["applicationSource"] = r.ApplicationSource
		}
		b, _ := json.Marshal(det)
		name := r.DisplayName
		if name == "" {
			name = r.PackageName
		}
		out = append(out, store.InventoryItem{
			Name: name, Identifier: r.PackageName, Version: r.VersionName, Publisher: r.InstallerPackageName,
			Source: "android", State: "installed", Details: b,
			Managed: r.ApplicationSource == "INSTALLED_FROM_PLAY_STORE" || managedIDs[r.PackageName],
		})
	}
	return out
}

// storeApps records the application reports of a device. A device that sent
// none (reporting not yet applied) keeps the inventory it has.
func (d *Driver) storeApps(ctx context.Context, dev *store.Device, a *amapiDevice) {
	if len(a.ApplicationReports) == 0 {
		return
	}
	managed := map[string]bool{}
	if doc, err := mdm.EffectivePolicy(ctx, d.Store, dev.ID); err == nil && doc != nil {
		for _, app := range doc.AppsFor("android") {
			managed[app.ID] = true
		}
	}
	if err := d.Store.ReplaceInventory(ctx, dev.ID, store.KindApp, mapApplicationReports(a.ApplicationReports, managed)); err != nil {
		d.Log.Warn("store android inventory", "device", dev.ID, "err", err)
	}
}
