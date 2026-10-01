package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/dmdhrumilmistry/VaanarSena/internal/apple"
	"github.com/dmdhrumilmistry/VaanarSena/internal/command"
	"github.com/dmdhrumilmistry/VaanarSena/internal/httpx"
	"github.com/dmdhrumilmistry/VaanarSena/internal/mdm"
	"github.com/dmdhrumilmistry/VaanarSena/internal/policy"
	"github.com/dmdhrumilmistry/VaanarSena/internal/secrets"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
	"github.com/dmdhrumilmistry/VaanarSena/internal/windows"
)

func (a *API) listDevices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	devs, total, err := a.Store.ListDevices(r.Context(), store.DeviceFilter{
		Platform: q.Get("platform"), Ownership: q.Get("ownership"), Status: q.Get("status"),
		GroupID: q.Get("group"), Tag: q.Get("tag"), Search: q.Get("q"), Limit: queryInt(r, "limit", 100), Offset: queryInt(r, "offset", 0),
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"devices": nonNil(devs), "total": total})
}

func (a *API) getDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d, err := a.Store.DeviceByID(ctx, r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	groups, err := a.Store.DeviceGroups(ctx, d.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	eff, err := mdm.EffectivePolicy(ctx, a.Store, d.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"device": redact(d), "groups": nonNil(groups), "effectivePolicy": eff})
}

// redact removes secrets from platform identifiers before they leave the server.
func redact(d *store.Device) *store.Device {
	var ids map[string]any
	if json.Unmarshal(d.PlatformIDs, &ids) == nil {
		for _, k := range []string{"unlockToken", "pushMagic", "token"} {
			if _, ok := ids[k]; ok {
				ids[k] = "[redacted]"
			}
		}
		d.PlatformIDs, _ = json.Marshal(ids)
	}
	return d
}

func (a *API) patchDevice(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      *string `json:"name"`
		Assignee  *string `json:"assignee"`
		Ownership *string `json:"ownership"`
	}
	if err := decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "malformed request")
		return
	}
	id := r.PathValue("id")
	if req.Ownership != nil {
		// Reclassifying ownership changes what may be done to the device
		// (personal -> corporate unlocks wipe), so it is an admin decision.
		if !principal(r).Has(store.RoleAdmin) {
			httpx.Error(w, http.StatusForbidden, "changing ownership requires role admin")
			return
		}
		if *req.Ownership != store.OwnershipCorporate && *req.Ownership != store.OwnershipPersonal {
			httpx.Error(w, http.StatusBadRequest, "ownership must be corporate or personal")
			return
		}
	}
	if !a.audit(w, r, "device.update", id, req) {
		return
	}
	d, err := a.Store.PatchDevice(r.Context(), id, store.DevicePatch{Name: req.Name, Assignee: req.Assignee, Ownership: req.Ownership})
	if err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, redact(d))
}

func (a *API) deleteDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !a.audit(w, r, "device.delete", id, nil) {
		return
	}
	if err := a.Store.DeleteDevice(r.Context(), id); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) listCommands(w http.ResponseWriter, r *http.Request) {
	cmds, err := a.Store.ListCommands(r.Context(), r.PathValue("id"), queryInt(r, "limit", 100))
	if err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"commands": nonNil(cmds)})
}

func (a *API) sendCommand(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type   string          `json:"type"`
		Params json.RawMessage `json:"params"`
	}
	if err := decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "malformed request")
		return
	}
	ctx := r.Context()
	d, err := a.Store.DeviceByID(ctx, r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	p := principal(r)
	// Authorise before auditing so refusals are recorded as such.
	params, _ := command.ParseParams(req.Params)
	if err := command.Authorize(p.User.Role, d, req.Type, params); err != nil {
		_ = a.Store.Audit(ctx, p.User.Email, "command.denied", d.ID, map[string]any{"type": req.Type, "reason": err.Error()}, httpx.ClientIP(r))
		httpx.Error(w, http.StatusForbidden, err.Error())
		return
	}
	details := map[string]any{"type": req.Type, "platform": d.Platform, "ownership": d.Ownership}
	if req.Type == command.RunScript {
		details["script"] = params.Script // record exactly what ran as root
	}
	if !a.audit(w, r, "command."+req.Type, d.ID, details) {
		return
	}
	cmd, err := a.Svc.Enqueue(ctx, p.User.Role, &p.User.ID, d, req.Type, req.Params)
	if err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, cmd)
}

func (a *API) cancelCommand(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !a.audit(w, r, "command.cancel", id, nil) {
		return
	}
	if err := a.Store.CancelCommand(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.Error(w, http.StatusConflict, "command not found or already completed")
			return
		}
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) catalogue(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]any{"commands": command.Catalogue})
}

// --- enrollment tokens ---

func (a *API) listTokens(w http.ResponseWriter, r *http.Request) {
	toks, err := a.Store.ListEnrollmentTokens(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"tokens": nonNil(toks)})
}

func (a *API) createToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Platform       string  `json:"platform"`
		Ownership      string  `json:"ownership"`
		Assignee       string  `json:"assignee"`
		GroupID        *string `json:"groupId"`
		MaxUses        int     `json:"maxUses"`
		ExpiresInHours int     `json:"expiresInHours"`
	}
	if err := decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "malformed request")
		return
	}
	switch req.Platform {
	case "apple", "windows", "android", "linux":
	default:
		httpx.Error(w, http.StatusBadRequest, "platform must be apple, windows, android or linux (ChromeOS devices are imported from Google Admin)")
		return
	}
	if req.Ownership != store.OwnershipCorporate && req.Ownership != store.OwnershipPersonal {
		httpx.Error(w, http.StatusBadRequest, "ownership must be corporate or personal")
		return
	}
	driverPlatform := map[string]string{"apple": store.PlatformIOS, "windows": store.PlatformWindows, "android": store.PlatformAndroid, "linux": store.PlatformLinux}[req.Platform]
	if !a.Svc.Enabled(driverPlatform) {
		httpx.Error(w, http.StatusConflict, req.Platform+" management is not configured on this server")
		return
	}
	if req.Platform == "apple" && req.Ownership == store.OwnershipPersonal && !strings.Contains(req.Assignee, "@") {
		httpx.Error(w, http.StatusBadRequest, "Apple User Enrollment (personal) requires assignee to be the user's Managed Apple ID")
		return
	}
	if req.GroupID != nil && *req.GroupID == "" {
		req.GroupID = nil
	}
	if req.MaxUses <= 0 {
		req.MaxUses = 1
	}
	if req.MaxUses > 10000 {
		req.MaxUses = 10000
	}
	if req.ExpiresInHours <= 0 || req.ExpiresInHours > 24*90 {
		req.ExpiresInHours = 72
	}
	p := principal(r)
	raw := secrets.Token(24)
	t, err := a.Store.CreateEnrollmentToken(r.Context(), &store.EnrollmentToken{
		Platform: req.Platform, Ownership: req.Ownership, GroupID: req.GroupID, Assignee: req.Assignee,
		MaxUses: req.MaxUses, ExpiresAt: time.Now().Add(time.Duration(req.ExpiresInHours) * time.Hour), CreatedBy: &p.User.ID,
	}, secrets.Hash(raw))
	if err != nil {
		a.fail(w, err)
		return
	}
	instructions := map[string]any{}
	switch req.Platform {
	case "apple":
		instructions["profileUrl"] = apple.EnrollURL(a.PublicURL, raw)
		instructions["steps"] = "Open the profile URL on the device in Safari, then install the profile from Settings."
	case "windows":
		instructions["enrollUrl"] = windows.EnrollURL(a.PublicURL, req.Assignee)
		instructions["server"] = a.PublicURL + "/EnrollmentServer/Discovery.svc"
		instructions["password"] = raw
		instructions["steps"] = "Settings > Accounts > Access work or school > Enroll only in device management. Sign in with the user's email; use the token as the password."
	case "linux":
		instructions["command"] = "sudo vaanarsena-agent enroll --server " + a.PublicURL + " --token " + raw
	case "android":
		extra, err := a.Android.CreateEnrollment(r.Context(), t)
		if err != nil {
			_ = a.Store.RevokeEnrollmentToken(r.Context(), t.ID)
			a.Log.Error("android enrollment token", "err", err)
			httpx.Error(w, http.StatusBadGateway, "could not create Android enrollment token: "+err.Error())
			return
		}
		b, _ := json.Marshal(extra)
		_ = a.Store.SetEnrollmentTokenExtra(r.Context(), t.ID, b)
		t.Extra = b
		for k, v := range extra {
			instructions[k] = v
		}
		if req.Ownership == store.OwnershipPersonal {
			instructions["steps"] = "Open the enrollment link on the phone to create a work profile."
		} else {
			instructions["steps"] = "Factory reset the device, tap the welcome screen six times and scan the QR code."
		}
	}
	if !a.audit(w, r, "enrollment_token.create", t.ID, map[string]any{"platform": t.Platform, "ownership": t.Ownership, "assignee": t.Assignee, "maxUses": t.MaxUses}) {
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"token": raw, "meta": t, "instructions": instructions})
}

// tokenQR renders an enrollment QR code: the AMAPI provisioning payload for
// Android, or the enrollment URL for Apple.
func (a *API) tokenQR(w http.ResponseWriter, r *http.Request) {
	t, err := a.Store.EnrollmentTokenByID(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	var extra struct {
		QRCode string `json:"qrCode"`
	}
	_ = json.Unmarshal(t.Extra, &extra)
	if extra.QRCode == "" {
		httpx.Error(w, http.StatusNotFound, "this token has no QR code")
		return
	}
	png, err := qrcode.Encode(extra.QRCode, qrcode.Medium, 512)
	if err != nil {
		a.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

func (a *API) revokeToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !a.audit(w, r, "enrollment_token.revoke", id, nil) {
		return
	}
	if err := a.Store.RevokeEnrollmentToken(r.Context(), id); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- policies ---

func (a *API) listPolicies(w http.ResponseWriter, r *http.Request) {
	ps, err := a.Store.ListPolicies(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	for _, p := range ps {
		redactPolicy(p)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"policies": nonNil(ps)})
}

func (a *API) getPolicy(w http.ResponseWriter, r *http.Request) {
	p, err := a.Store.PolicyByID(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	if !principal(r).Has(store.RoleAdmin) {
		redactPolicy(p)
	}
	httpx.JSON(w, http.StatusOK, p)
}

// maskedSecret replaces secrets in responses to non-admins and listings.
const maskedSecret = "********"

// redactPolicy hides Wi-Fi passphrases from non-admin readers and listings.
func redactPolicy(p *store.Policy) {
	doc, err := policy.Parse(p.Document)
	if err != nil {
		return
	}
	for i := range doc.WiFi {
		if doc.WiFi[i].Password != "" {
			doc.WiFi[i].Password = maskedSecret
		}
	}
	p.Document, _ = json.Marshal(doc)
}

func (a *API) savePolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Priority    int             `json:"priority"`
		Document    json.RawMessage `json:"document"`
		GroupIDs    []string        `json:"groupIds"`
		DeviceIDs   []string        `json:"deviceIds"`
	}
	if err := decode(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		httpx.Error(w, http.StatusBadRequest, "name and document are required")
		return
	}
	doc, err := policy.Parse(req.Document)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := r.Context()
	id := r.PathValue("id")
	// Devices that lose this policy must be updated too.
	var before []string
	if id != "" {
		before, _ = a.Store.DevicesForPolicy(ctx, id)
		if existing, err := a.Store.PolicyByID(ctx, id); err == nil {
			restoreMaskedPasswords(doc, existing.Document)
		}
	}
	canon, _ := json.Marshal(doc)
	if req.Priority == 0 {
		req.Priority = 100
	}
	if !a.audit(w, r, map[bool]string{true: "policy.create", false: "policy.update"}[id == ""], firstNonEmpty(id, req.Name),
		map[string]any{"name": req.Name, "groups": req.GroupIDs, "devices": req.DeviceIDs}) {
		return
	}
	p, err := a.Store.SavePolicy(ctx, &store.Policy{
		ID: id, Name: strings.TrimSpace(req.Name), Description: req.Description, Priority: req.Priority,
		Document: canon, GroupIDs: req.GroupIDs, DeviceIDs: req.DeviceIDs,
	})
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") {
			httpx.Error(w, http.StatusConflict, "a policy with that name exists")
			return
		}
		a.fail(w, err)
		return
	}
	after, _ := a.Store.DevicesForPolicy(ctx, p.ID)
	a.Svc.PolicyChanged(ctx, union(before, after))
	code := http.StatusOK
	if id == "" {
		code = http.StatusCreated
	}
	httpx.JSON(w, code, p)
}

// restoreMaskedPasswords keeps stored Wi-Fi passphrases when an editor
// round-trips the masked value.
func restoreMaskedPasswords(doc *policy.Document, stored json.RawMessage) {
	old, err := policy.Parse(stored)
	if err != nil {
		return
	}
	prev := map[string]string{}
	for _, w := range old.WiFi {
		prev[w.SSID] = w.Password
	}
	for i := range doc.WiFi {
		if doc.WiFi[i].Password == maskedSecret {
			doc.WiFi[i].Password = prev[doc.WiFi[i].SSID]
		}
	}
}

func (a *API) deletePolicy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	affected, _ := a.Store.DevicesForPolicy(ctx, id)
	if !a.audit(w, r, "policy.delete", id, nil) {
		return
	}
	if err := a.Store.DeletePolicy(ctx, id); err != nil {
		a.fail(w, err)
		return
	}
	a.Svc.PolicyChanged(ctx, affected)
	w.WriteHeader(http.StatusNoContent)
}

func union(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append(append([]string{}, a...), b...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
