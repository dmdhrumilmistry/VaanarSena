// Package api is the admin REST API under /api/v1.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/dmdhrumilmistry/VaanarSena/internal/auth"
	"github.com/dmdhrumilmistry/VaanarSena/internal/httpx"
	"github.com/dmdhrumilmistry/VaanarSena/internal/mdm"
	"github.com/dmdhrumilmistry/VaanarSena/internal/pki"
	"github.com/dmdhrumilmistry/VaanarSena/internal/platforms"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

// API serves the admin endpoints.
type API struct {
	Store     *store.Store
	Auth      *auth.Authenticator
	Svc       *mdm.Service
	CA        *pki.CA
	PublicURL string
	Org       string
	Platforms *platforms.Manager
	Version   string
	Log       *slog.Logger
}

// Routes registers the API on mux.
func (a *API) Routes(mux *http.ServeMux) {
	auditor := func(h http.HandlerFunc) http.Handler { return a.Auth.Middleware(store.RoleAuditor, h) }
	operator := func(h http.HandlerFunc) http.Handler { return a.Auth.Middleware(store.RoleOperator, h) }
	admin := func(h http.HandlerFunc) http.Handler { return a.Auth.Middleware(store.RoleAdmin, h) }

	mux.HandleFunc("POST /api/v1/auth/login", a.login)
	mux.HandleFunc("POST /api/v1/auth/logout", a.logout)
	mux.Handle("GET /api/v1/auth/me", auditor(a.me))
	mux.Handle("POST /api/v1/auth/password", auditor(a.changePassword))

	mux.Handle("GET /api/v1/info", auditor(a.info))
	mux.Handle("GET /api/v1/stats", auditor(a.stats))

	mux.Handle("GET /api/v1/devices", auditor(a.listDevices))
	mux.Handle("GET /api/v1/devices/{id}", auditor(a.getDevice))
	mux.Handle("PATCH /api/v1/devices/{id}", operator(a.patchDevice))
	mux.Handle("DELETE /api/v1/devices/{id}", admin(a.deleteDevice))
	mux.Handle("GET /api/v1/devices/{id}/commands", auditor(a.listCommands))
	mux.Handle("POST /api/v1/devices/{id}/commands", operator(a.sendCommand))
	mux.Handle("POST /api/v1/commands/{id}/cancel", operator(a.cancelCommand))
	mux.Handle("GET /api/v1/commands/catalogue", auditor(a.catalogue))

	mux.Handle("GET /api/v1/devices/{id}/inventory", auditor(a.deviceInventory))
	mux.Handle("GET /api/v1/inventory/software", auditor(a.fleetSoftware))
	mux.Handle("GET /api/v1/inventory/software/devices", auditor(a.softwareDevices))

	mux.Handle("GET /api/v1/enrollment-tokens", operator(a.listTokens))
	mux.Handle("POST /api/v1/enrollment-tokens", operator(a.createToken))
	mux.Handle("DELETE /api/v1/enrollment-tokens/{id}", operator(a.revokeToken))
	mux.Handle("GET /api/v1/enrollment-tokens/{id}/qr.png", operator(a.tokenQR))

	mux.Handle("PUT /api/v1/devices/{id}/tags", operator(a.setTags))

	mux.Handle("GET /api/v1/groups", auditor(a.listGroups))
	mux.Handle("POST /api/v1/groups", admin(a.saveGroup))
	mux.Handle("GET /api/v1/groups/{id}", auditor(a.getGroup))
	mux.Handle("PUT /api/v1/groups/{id}", admin(a.saveGroup))
	mux.Handle("DELETE /api/v1/groups/{id}", admin(a.deleteGroup))
	mux.Handle("POST /api/v1/groups/{id}/devices", operator(a.addGroupDevice))
	mux.Handle("DELETE /api/v1/groups/{id}/devices/{deviceId}", operator(a.removeGroupDevice))
	mux.Handle("POST /api/v1/groups/preview", auditor(a.previewRules))
	mux.Handle("GET /api/v1/groups/schema", auditor(a.ruleSchema))

	mux.Handle("GET /api/v1/blueprints", auditor(a.listBlueprints))
	mux.Handle("POST /api/v1/blueprints", admin(a.saveBlueprint))
	mux.Handle("GET /api/v1/blueprints/{id}", auditor(a.getBlueprint))
	mux.Handle("PUT /api/v1/blueprints/{id}", admin(a.saveBlueprint))
	mux.Handle("DELETE /api/v1/blueprints/{id}", admin(a.deleteBlueprint))

	// Declarative configuration (GitOps): YAML or JSON manifests.
	mux.Handle("POST /api/v1/apply", admin(a.apply))
	mux.Handle("GET /api/v1/export", admin(a.export))

	mux.Handle("GET /api/v1/policies", auditor(a.listPolicies))
	mux.Handle("POST /api/v1/policies", admin(a.savePolicy))
	mux.Handle("GET /api/v1/policies/{id}", auditor(a.getPolicy))
	mux.Handle("PUT /api/v1/policies/{id}", admin(a.savePolicy))
	mux.Handle("DELETE /api/v1/policies/{id}", admin(a.deletePolicy))

	mux.Handle("GET /api/v1/users", admin(a.listUsers))
	mux.Handle("POST /api/v1/users", admin(a.createUser))
	mux.Handle("PATCH /api/v1/users/{id}", admin(a.patchUser))
	mux.Handle("DELETE /api/v1/users/{id}", admin(a.deleteUser))

	mux.Handle("GET /api/v1/api-tokens", auditor(a.listAPITokens))
	mux.Handle("POST /api/v1/api-tokens", auditor(a.createAPIToken))
	mux.Handle("DELETE /api/v1/api-tokens/{id}", auditor(a.deleteAPIToken))

	mux.Handle("GET /api/v1/audit", auditor(a.listAudit))

	a.platformRoutes(mux)

	mux.HandleFunc("GET /mdm/ca.pem", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-pem-file")
		_, _ = w.Write(a.CA.CertPEM)
	})
}

// decode reads a JSON body, rejecting unknown fields.
func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func (a *API) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not found")
	case errors.Is(err, mdm.ErrForbidden):
		httpx.Error(w, http.StatusForbidden, err.Error())
	default:
		a.Log.Error("api error", "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal error")
	}
}

// audit records an admin action. A failed audit write fails the request:
// unaudited changes are worse than failed ones.
func (a *API) audit(w http.ResponseWriter, r *http.Request, action, target string, details any) bool {
	actor := "anonymous"
	if p := auth.FromContext(r.Context()); p != nil {
		actor = p.User.Email
	}
	if err := a.Store.Audit(r.Context(), actor, action, target, details, httpx.ClientIP(r)); err != nil {
		a.Log.Error("audit write failed", "action", action, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "audit log unavailable")
		return false
	}
	return true
}

func principal(r *http.Request) *auth.Principal { return auth.FromContext(r.Context()) }

func queryInt(r *http.Request, key string, def int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return def
	}
	return v
}

func (a *API) info(w http.ResponseWriter, r *http.Request) {
	platforms := map[string]bool{}
	for _, p := range []string{store.PlatformIOS, store.PlatformWindows, store.PlatformAndroid, store.PlatformChromeOS, store.PlatformLinux} {
		platforms[p] = a.Svc.Enabled(p)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"version": a.Version, "org": a.Org, "publicUrl": a.PublicURL, "platforms": platforms,
		"caFingerprint": pki.Fingerprint(a.CA.Cert),
	})
}

func (a *API) stats(w http.ResponseWriter, r *http.Request) {
	st, err := a.Store.Stats(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, st)
}

func (a *API) listAudit(w http.ResponseWriter, r *http.Request) {
	entries, err := a.Store.ListAudit(r.Context(), int64(queryInt(r, "before", 0)), queryInt(r, "limit", 100))
	if err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"entries": nonNil(entries)})
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
