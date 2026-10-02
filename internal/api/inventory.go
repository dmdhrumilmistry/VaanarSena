package api

import (
	"net/http"
	"time"

	"github.com/dmdhrumilmistry/VaanarSena/internal/httpx"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

// inventorySupport reports which kinds a device can have, and explains
// anything withheld or unavailable. It encodes the BYOD rules: Linux and
// Windows personal devices have no inventory at all; Apple personal devices
// list only managed apps and MDM profiles; Android personal devices list the
// work profile.
func inventorySupport(d *store.Device) (map[string]bool, string) {
	s := map[string]bool{store.KindApp: false, store.KindService: false, store.KindProfile: false}
	personal := d.IsPersonal()
	switch {
	case d.Platform == store.PlatformLinux && personal:
		return s, "This is a personally owned device. Installed software and services are not collected."
	case d.Platform == store.PlatformLinux:
		s[store.KindApp], s[store.KindService] = true, true
		return s, ""
	case d.IsApple() && personal:
		s[store.KindApp], s[store.KindProfile] = true, true
		return s, "This is a personally owned device. Only apps managed by VaanarSena and profiles installed by VaanarSena are listed; other apps on the device are not visible."
	case d.IsApple():
		s[store.KindApp], s[store.KindProfile] = true, true
		return s, ""
	case d.Platform == store.PlatformWindows && personal:
		return s, "This is a personally owned device. Installed software is not collected."
	case d.Platform == store.PlatformWindows:
		s[store.KindApp] = true
		return s, "Windows reports Microsoft Store and packaged apps, and desktop apps installed with MSI. Other desktop installers and services are not reported through MDM."
	case d.Platform == store.PlatformAndroid && personal:
		s[store.KindApp] = true
		return s, "This is a personally owned device. Only apps in the work profile are listed."
	case d.Platform == store.PlatformAndroid:
		s[store.KindApp] = true
		return s, ""
	case d.Platform == store.PlatformChromeOS:
		return s, "Software inventory is not available for ChromeOS devices."
	}
	return s, "Software inventory is not available for this platform."
}

func (a *API) deviceInventory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d, err := a.Store.DeviceByID(ctx, r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	q := r.URL.Query()
	kind := q.Get("kind")
	if kind == "" {
		kind = store.KindApp
	}
	if !store.ValidInventoryKind(kind) {
		httpx.Error(w, http.StatusBadRequest, "kind must be app, service or profile")
		return
	}
	supported, note := inventorySupport(d)
	sync, err := a.Store.InventorySync(ctx, d.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	// Kinds the device may not have are reported empty even if rows linger
	// from before it was reclassified.
	counts := map[string]int{store.KindApp: 0, store.KindService: 0, store.KindProfile: 0}
	for k, sy := range sync {
		if supported[k] {
			counts[k] = sy.Count
		}
	}
	items, total := []store.InventoryItem{}, 0
	var collected *time.Time
	if supported[kind] {
		items, total, err = a.Store.DeviceInventory(ctx, d.ID, kind, q.Get("q"), queryInt(r, "limit", 200), queryInt(r, "offset", 0))
		if err != nil {
			a.fail(w, err)
			return
		}
		if sy, ok := sync[kind]; ok {
			collected = &sy.CollectedAt
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"kind": kind, "items": items, "total": total, "collectedAt": collected,
		"counts": counts, "supported": supported, "note": note,
	})
}

func (a *API) fleetSoftware(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind := q.Get("kind")
	if kind == "" {
		kind = store.KindApp
	}
	if !store.ValidInventoryKind(kind) {
		httpx.Error(w, http.StatusBadRequest, "kind must be app, service or profile")
		return
	}
	items, total, err := a.Store.FleetSoftware(r.Context(), kind, q.Get("q"), queryInt(r, "limit", 100), queryInt(r, "offset", 0))
	if err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (a *API) softwareDevices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind := q.Get("kind")
	if kind == "" {
		kind = store.KindApp
	}
	if !store.ValidInventoryKind(kind) {
		httpx.Error(w, http.StatusBadRequest, "kind must be app, service or profile")
		return
	}
	if q.Get("name") == "" {
		httpx.Error(w, http.StatusBadRequest, "name is required")
		return
	}
	items, total, err := a.Store.SoftwareDevices(r.Context(), kind, q.Get("identifier"), q.Get("name"), q.Get("version"),
		queryInt(r, "limit", 100), queryInt(r, "offset", 0))
	if err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}
