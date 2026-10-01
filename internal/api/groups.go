package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dmdhrumilmistry/VaanarSena/internal/blueprint"
	"github.com/dmdhrumilmistry/VaanarSena/internal/groups"
	"github.com/dmdhrumilmistry/VaanarSena/internal/httpx"
	"github.com/dmdhrumilmistry/VaanarSena/internal/manifest"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

func (a *API) applier() *manifest.Applier { return &manifest.Applier{Store: a.Store, Svc: a.Svc} }

// applyOne runs a single resource through the manifest applier so console and
// API edits share validation and side effects with GitOps applies. Console
// edits are owned by "-", which no manifest source may claim, so a prune never
// removes them; editing a manifest-owned resource in the console adopts it.
func (a *API) applyOne(w http.ResponseWriter, r *http.Request, res manifest.Resource) (*manifest.Change, bool) {
	out, err := a.applier().Apply(r.Context(), []manifest.Resource{res}, manifest.Options{Owner: "-"})
	if err != nil {
		var ve *manifest.ValidationError
		if errors.As(err, &ve) {
			httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "invalid " + res.Kind, "problems": ve.Problems})
			return nil, false
		}
		a.fail(w, err)
		return nil, false
	}
	return &out.Changes[0], true
}

// --- groups ---

func (a *API) listGroups(w http.ResponseWriter, r *http.Request) {
	gs, err := a.Store.ListGroups(r.Context(), r.URL.Query().Get("kind"))
	if err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"groups": nonNil(gs)})
}

func (a *API) getGroup(w http.ResponseWriter, r *http.Request) {
	g, err := a.Store.GroupByID(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	members, err := a.Store.GroupMembers(r.Context(), g.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"group": g, "members": nonNil(members)})
}

type groupBody struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Kind        string          `json:"kind"`
	Rules       json.RawMessage `json:"rules,omitempty"`
}

// saveGroup handles POST /groups and PUT /groups/{id}. Static membership is
// not part of the body: it is edited through the members endpoints.
func (a *API) saveGroup(w http.ResponseWriter, r *http.Request) {
	var req groupBody
	if err := decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "malformed request")
		return
	}
	ctx := r.Context()
	if id := r.PathValue("id"); id != "" {
		existing, err := a.Store.GroupByID(ctx, id)
		if err != nil {
			a.fail(w, err)
			return
		}
		if existing.Name != req.Name {
			// Renames go straight to the store; apply identifies by name.
			existing.Name = strings.TrimSpace(req.Name)
			if _, err := a.Store.SaveGroup(ctx, existing); err != nil {
				a.fail(w, err)
				return
			}
		}
	}
	spec := map[string]any{"kind": req.Kind}
	if len(req.Rules) > 0 && string(req.Rules) != "null" {
		spec["rules"] = req.Rules
	}
	b, _ := json.Marshal(spec)
	res := manifest.Resource{APIVersion: manifest.APIVersion, Kind: manifest.KindGroup,
		Metadata: manifest.Metadata{Name: strings.TrimSpace(req.Name), Description: req.Description}, Spec: b}
	ch, ok := a.applyOne(w, r, res)
	if !ok {
		return
	}
	if !a.audit(w, r, "group."+ch.Action, req.Name, map[string]any{"kind": req.Kind, "rules": req.Rules}) {
		return
	}
	g, err := a.Store.GroupByName(ctx, res.Metadata.Name)
	if err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, map[bool]int{true: http.StatusCreated, false: http.StatusOK}[ch.Action == "created"], g)
}

func (a *API) deleteGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !a.audit(w, r, "group.delete", id, nil) {
		return
	}
	members, err := a.Store.DeleteGroup(r.Context(), id)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.Svc.MembershipChanged(r.Context(), members)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) addGroupDevice(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceID string `json:"deviceId"`
	}
	if err := decode(r, &req); err != nil || req.DeviceID == "" {
		httpx.Error(w, http.StatusBadRequest, "deviceId is required")
		return
	}
	gid := r.PathValue("id")
	if !a.audit(w, r, "group.add_device", gid, map[string]any{"deviceId": req.DeviceID}) {
		return
	}
	if err := a.Store.AddDeviceToGroup(r.Context(), gid, req.DeviceID); err != nil {
		if errors.Is(err, store.ErrSmartGroup) {
			httpx.Error(w, http.StatusConflict, err.Error())
			return
		}
		a.fail(w, err)
		return
	}
	a.Svc.MembershipChanged(r.Context(), []string{req.DeviceID})
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) removeGroupDevice(w http.ResponseWriter, r *http.Request) {
	gid, did := r.PathValue("id"), r.PathValue("deviceId")
	if !a.audit(w, r, "group.remove_device", gid, map[string]any{"deviceId": did}) {
		return
	}
	if err := a.Store.RemoveDeviceFromGroup(r.Context(), gid, did); err != nil {
		if errors.Is(err, store.ErrSmartGroup) {
			httpx.Error(w, http.StatusConflict, err.Error())
			return
		}
		a.fail(w, err)
		return
	}
	a.Svc.MembershipChanged(r.Context(), []string{did})
	w.WriteHeader(http.StatusNoContent)
}

// previewRules evaluates rules without saving, so admins can see who a smart
// group would contain before creating it.
func (a *API) previewRules(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "malformed request")
		return
	}
	rule, err := groups.Parse(body)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	devices, err := a.Store.AllDevices(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	now := time.Now()
	var matched []*store.Device
	for _, d := range devices {
		if d.Status != store.StatusRetired && rule.Matches(d, now) {
			matched = append(matched, redact(d))
		}
	}
	total := len(matched)
	if len(matched) > 200 {
		matched = matched[:200]
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"total": total, "devices": nonNil(matched)})
}

func (a *API) ruleSchema(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]any{"fields": groups.Fields, "factsPrefix": "facts.", "ops": groups.Ops})
}

// --- device tags ---

var tagRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]{0,62}$`)

func (a *API) setTags(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tags []string `json:"tags"`
	}
	if err := decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "malformed request")
		return
	}
	seen := map[string]bool{}
	tags := []string{}
	for _, t := range req.Tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if !tagRE.MatchString(t) {
			httpx.Error(w, http.StatusBadRequest, "tags must be lowercase letters, digits and . _ : / - (max 63)")
			return
		}
		if !seen[t] {
			seen[t] = true
			tags = append(tags, t)
		}
	}
	sort.Strings(tags)
	id := r.PathValue("id")
	if !a.audit(w, r, "device.tags", id, map[string]any{"tags": tags}) {
		return
	}
	d, err := a.Store.PatchDevice(r.Context(), id, store.DevicePatch{Tags: &tags})
	if err != nil {
		a.fail(w, err)
		return
	}
	// Tags feed smart group rules: re-evaluate this device right away.
	if changed, err := a.Svc.ReconcileDevice(r.Context(), d); err == nil && changed {
		a.Svc.MembershipChanged(r.Context(), []string{d.ID})
	}
	httpx.JSON(w, http.StatusOK, redact(d))
}

// --- blueprints ---

func (a *API) listBlueprints(w http.ResponseWriter, r *http.Request) {
	bs, err := a.Store.ListBlueprints(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	for _, b := range bs {
		redactBlueprint(b)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"blueprints": nonNil(bs)})
}

func (a *API) getBlueprint(w http.ResponseWriter, r *http.Request) {
	b, err := a.Store.BlueprintByID(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	if !principal(r).Has(store.RoleAdmin) {
		redactBlueprint(b)
	}
	httpx.JSON(w, http.StatusOK, b)
}

// restoreBlueprintSecrets puts stored Wi-Fi passphrases back where an editor
// round-tripped the masked value.
func restoreBlueprintSecrets(spec map[string]any, stored json.RawMessage) {
	pol, _ := spec["policy"].(map[string]any)
	wifi, _ := pol["wifi"].([]any)
	if len(wifi) == 0 {
		return
	}
	old, err := blueprint.Parse(stored)
	if err != nil || old.Policy == nil {
		return
	}
	prev := map[string]string{}
	for _, w := range old.Policy.WiFi {
		prev[w.SSID] = w.Password
	}
	for _, it := range wifi {
		m, _ := it.(map[string]any)
		if m != nil && m["password"] == maskedSecret {
			ssid, _ := m["ssid"].(string)
			m["password"] = prev[ssid]
		}
	}
}

// redactBlueprint masks Wi-Fi passphrases in a blueprint's inline policy, as
// redactPolicy does for policies.
func redactBlueprint(b *store.Blueprint) {
	spec, err := blueprint.Parse(b.Spec)
	if err != nil || spec.Policy == nil {
		return
	}
	for i := range spec.Policy.WiFi {
		if spec.Policy.WiFi[i].Password != "" {
			spec.Policy.WiFi[i].Password = maskedSecret
		}
	}
	b.Spec, _ = json.Marshal(spec)
}

func (a *API) saveBlueprint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Priority    int             `json:"priority"`
		GroupIDs    []string        `json:"groupIds"`
		Spec        json.RawMessage `json:"spec"`
	}
	if err := decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "malformed request")
		return
	}
	ctx := r.Context()
	var spec map[string]any
	if err := json.Unmarshal(req.Spec, &spec); err != nil || spec == nil {
		httpx.Error(w, http.StatusBadRequest, "spec must be an object")
		return
	}
	var names []string
	for _, id := range req.GroupIDs {
		g, err := a.Store.GroupByID(ctx, id)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "unknown group "+id)
			return
		}
		names = append(names, g.Name)
	}
	spec["groups"], spec["priority"] = names, req.Priority
	if id := r.PathValue("id"); id != "" {
		existing, err := a.Store.BlueprintByID(ctx, id)
		if err != nil {
			a.fail(w, err)
			return
		}
		restoreBlueprintSecrets(spec, existing.Spec)
		if existing.Name != req.Name {
			existing.Name = strings.TrimSpace(req.Name)
			if _, err := a.Store.SaveBlueprint(ctx, existing); err != nil {
				a.fail(w, err)
				return
			}
		}
	}
	b, _ := json.Marshal(spec)
	res := manifest.Resource{APIVersion: manifest.APIVersion, Kind: manifest.KindBlueprint,
		Metadata: manifest.Metadata{Name: strings.TrimSpace(req.Name), Description: req.Description}, Spec: b}
	ch, ok := a.applyOne(w, r, res)
	if !ok {
		return
	}
	if !a.audit(w, r, "blueprint."+ch.Action, req.Name, map[string]any{"groups": names}) {
		return
	}
	bp, err := a.Store.BlueprintByName(ctx, res.Metadata.Name)
	if err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, map[bool]int{true: http.StatusCreated, false: http.StatusOK}[ch.Action == "created"], bp)
}

func (a *API) deleteBlueprint(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	affected, _ := a.Store.DevicesForBlueprint(ctx, id)
	if !a.audit(w, r, "blueprint.delete", id, nil) {
		return
	}
	if err := a.Store.DeleteBlueprint(ctx, id); err != nil {
		a.fail(w, err)
		return
	}
	a.Svc.PolicyChanged(ctx, affected)
	w.WriteHeader(http.StatusNoContent)
}

// --- declarative apply / export ---

// apply accepts a YAML or JSON manifest. Query: dryRun=true, prune=true,
// owner=<name> (scopes prune; default "manifest").
func (a *API) apply(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "could not read body")
		return
	}
	rs, err := manifest.Decode(body)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	q := r.URL.Query()
	opt := manifest.Options{DryRun: q.Get("dryRun") == "true", Prune: q.Get("prune") == "true", Owner: q.Get("owner")}
	if opt.Owner == "-" {
		httpx.Error(w, http.StatusBadRequest, `owner "-" is reserved for console edits`)
		return
	}
	if !opt.DryRun {
		names := make([]string, 0, len(rs))
		for _, res := range rs {
			names = append(names, res.Kind+"/"+res.Metadata.Name)
		}
		if !a.audit(w, r, "manifest.apply", opt.Owner, map[string]any{"resources": names, "prune": opt.Prune}) {
			return
		}
	}
	out, err := a.applier().Apply(r.Context(), rs, opt)
	if err != nil {
		var ve *manifest.ValidationError
		if errors.As(err, &ve) {
			httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "manifest is invalid; nothing was changed", "problems": ve.Problems})
			return
		}
		// Partial progress is reported so the caller knows where it stopped.
		httpx.JSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error(), "result": out})
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// export returns every group, policy and blueprint as a manifest (YAML by
// default, JSON with ?format=json). It includes secrets such as Wi-Fi
// passphrases, so it requires the admin role.
func (a *API) export(w http.ResponseWriter, r *http.Request) {
	rs, err := a.applier().Export(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	if r.URL.Query().Get("format") == "json" {
		httpx.JSON(w, http.StatusOK, map[string]any{"items": rs})
		return
	}
	y, err := manifest.EncodeYAML(rs)
	if err != nil {
		a.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(y)
}
