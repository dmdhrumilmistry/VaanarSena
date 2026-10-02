package api

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/dmdhrumilmistry/VaanarSena/internal/httpx"
	"github.com/dmdhrumilmistry/VaanarSena/internal/platforms"
)

func (a *API) platformRoutes(mux *http.ServeMux) {
	admin := func(h http.HandlerFunc) http.Handler { return a.Auth.Middleware("admin", h) }
	mux.Handle("GET /api/v1/platforms", admin(a.platformStatus))
	mux.Handle("POST /api/v1/platforms/apple/csr", admin(a.appleCSR))
	mux.Handle("PUT /api/v1/platforms/apple", admin(a.setApple))
	mux.Handle("DELETE /api/v1/platforms/apple", admin(a.clearApple))
	mux.Handle("PUT /api/v1/platforms/google", admin(a.setGoogle))
	mux.Handle("DELETE /api/v1/platforms/google", admin(a.clearGoogle))
	mux.Handle("POST /api/v1/platforms/android/signup", admin(a.androidSignup))
	mux.Handle("PUT /api/v1/platforms/android", admin(a.setAndroid))
	mux.Handle("DELETE /api/v1/platforms/android", admin(a.clearAndroid))
	mux.Handle("PUT /api/v1/platforms/chromeos", admin(a.setChromeOS))
	mux.Handle("DELETE /api/v1/platforms/chromeos", admin(a.clearChromeOS))
	// Google redirects the admin's browser here. The session cookie is
	// SameSite=Strict and is not sent on that cross-site redirect, so the
	// single-use state minted at signup start authenticates the request.
	mux.HandleFunc("GET /api/v1/platforms/android/callback", a.androidCallback)
}

func (a *API) platformErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, platforms.ErrEnvManaged):
		httpx.Error(w, http.StatusConflict, err.Error())
	case errors.Is(err, platforms.ErrInvalid):
		httpx.Error(w, http.StatusBadRequest, err.Error())
	default:
		a.Log.Error("platform configuration", "err", err)
		httpx.Error(w, http.StatusBadGateway, err.Error())
	}
}

func (a *API) platformStatus(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]any{"platforms": a.Platforms.Statuses(r.Context()), "publicUrl": a.PublicURL})
}

func (a *API) appleCSR(w http.ResponseWriter, r *http.Request) {
	if !a.audit(w, r, "platform.apple.csr", "apple", nil) {
		return
	}
	csr, err := a.Platforms.AppleCSR(r.Context())
	if err != nil {
		a.platformErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"csr": string(csr)})
}

func (a *API) setApple(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Certificate string `json:"certificate"` // PEM, or base64 of a DER .cer
		PrivateKey  string `json:"privateKey"`  // PEM; empty to use the generated key
		Topic       string `json:"topic"`
		DER         bool   `json:"der"`
	}
	if err := decode(r, &req); err != nil || req.Certificate == "" {
		httpx.Error(w, http.StatusBadRequest, "certificate is required")
		return
	}
	cert := []byte(req.Certificate)
	if req.DER {
		b, err := decodeBase64(req.Certificate)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "certificate is not base64")
			return
		}
		cert = b
	}
	if !a.audit(w, r, "platform.apple.set", "apple", map[string]any{"ownKey": req.PrivateKey != ""}) {
		return
	}
	if err := a.Platforms.SetApple(r.Context(), cert, []byte(req.PrivateKey), req.Topic); err != nil {
		a.platformErr(w, err)
		return
	}
	a.platformStatus(w, r)
}

func (a *API) clearApple(w http.ResponseWriter, r *http.Request) {
	if !a.audit(w, r, "platform.apple.clear", "apple", nil) {
		return
	}
	if err := a.Platforms.ClearApple(r.Context()); err != nil {
		a.platformErr(w, err)
		return
	}
	a.platformStatus(w, r)
}

func (a *API) setGoogle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Credentials string `json:"credentials"` // the service account key JSON
		ProjectID   string `json:"projectId"`
	}
	if err := decode(r, &req); err != nil || req.Credentials == "" {
		httpx.Error(w, http.StatusBadRequest, "credentials (the service account key JSON) are required")
		return
	}
	if !a.audit(w, r, "platform.google.set", "google", map[string]any{"projectId": req.ProjectID}) {
		return
	}
	if _, err := a.Platforms.SetGoogle(r.Context(), []byte(req.Credentials), req.ProjectID); err != nil {
		a.platformErr(w, err)
		return
	}
	a.platformStatus(w, r)
}

func (a *API) clearGoogle(w http.ResponseWriter, r *http.Request) {
	if !a.audit(w, r, "platform.google.clear", "google", nil) {
		return
	}
	if err := a.Platforms.ClearGoogle(r.Context()); err != nil {
		a.platformErr(w, err)
		return
	}
	a.platformStatus(w, r)
}

func (a *API) androidSignup(w http.ResponseWriter, r *http.Request) {
	if !a.audit(w, r, "platform.android.signup", "android", nil) {
		return
	}
	u, err := a.Platforms.StartAndroidSignup(r.Context())
	if err != nil {
		a.platformErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"signupUrl": u})
}

func (a *API) androidCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	name, err := a.Platforms.CompleteAndroidSignup(r.Context(), q.Get("state"), q.Get("enterpriseToken"))
	dest := "/settings/platforms?android="
	if err != nil {
		a.Log.Warn("android enterprise signup failed", "err", err)
		_ = a.Store.Audit(r.Context(), "google-callback", "platform.android.signup_failed", "android", map[string]any{"error": err.Error()}, clientIPOf(r))
		http.Redirect(w, r, dest+"error&message="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	_ = a.Store.Audit(r.Context(), "google-callback", "platform.android.connected", name, nil, clientIPOf(r))
	http.Redirect(w, r, dest+"connected", http.StatusSeeOther)
}

func (a *API) setAndroid(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enterprise string `json:"enterprise"`
	}
	if err := decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "malformed request")
		return
	}
	if !a.audit(w, r, "platform.android.set", req.Enterprise, nil) {
		return
	}
	if err := a.Platforms.SetAndroidEnterprise(r.Context(), req.Enterprise); err != nil {
		a.platformErr(w, err)
		return
	}
	a.platformStatus(w, r)
}

func (a *API) clearAndroid(w http.ResponseWriter, r *http.Request) {
	if !a.audit(w, r, "platform.android.clear", "android", nil) {
		return
	}
	if err := a.Platforms.ClearAndroid(r.Context()); err != nil {
		a.platformErr(w, err)
		return
	}
	a.platformStatus(w, r)
}

func (a *API) setChromeOS(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AdminSubject string `json:"adminSubject"`
		CustomerID   string `json:"customerId"`
	}
	if err := decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "malformed request")
		return
	}
	if !a.audit(w, r, "platform.chromeos.set", req.AdminSubject, nil) {
		return
	}
	if err := a.Platforms.SetChromeOS(r.Context(), req.AdminSubject, req.CustomerID); err != nil {
		a.platformErr(w, err)
		return
	}
	a.platformStatus(w, r)
}

func (a *API) clearChromeOS(w http.ResponseWriter, r *http.Request) {
	if !a.audit(w, r, "platform.chromeos.clear", "chromeos", nil) {
		return
	}
	if err := a.Platforms.ClearChromeOS(r.Context()); err != nil {
		a.platformErr(w, err)
		return
	}
	a.platformStatus(w, r)
}
