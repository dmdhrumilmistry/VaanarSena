package api

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/dmdhrumilmistry/VaanarSena/internal/auth"
	"github.com/dmdhrumilmistry/VaanarSena/internal/httpx"
)

func (a *API) ssoRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/auth/providers", a.authProviders)
	mux.HandleFunc("GET /api/v1/auth/oidc/start", a.oidcStart)
	mux.HandleFunc("GET /api/v1/auth/oidc/callback", a.oidcCallback)
}

// authProviders tells the sign-in page which methods are offered.
// Unauthenticated: it reveals only whether SSO is on and its label.
func (a *API) authProviders(w http.ResponseWriter, r *http.Request) {
	oidc := map[string]any{"enabled": a.OIDC != nil}
	if a.OIDC != nil {
		oidc["name"] = a.OIDC.Config.Name
	}
	// password: anyone may use a password. passwordRestricted: only break-glass
	// accounts may (the console then tucks the form away).
	httpx.JSON(w, http.StatusOK, map[string]any{
		"password":           a.Auth.Password == nil || a.Auth.Password.Enabled,
		"passwordRestricted": a.Auth.Password.Restricted(),
		"oidc":               oidc,
	})
}

func (a *API) oidcStart(w http.ResponseWriter, r *http.Request) {
	if a.OIDC == nil {
		httpx.Error(w, http.StatusNotFound, "single sign-on is not configured")
		return
	}
	target, err := a.OIDC.Start(r.Context(), w, r.URL.Query().Get("next"))
	if err != nil {
		a.Log.Error("oidc start", "err", err)
		http.Redirect(w, r, "/login?sso_error=unavailable", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// oidcCallback completes SSO and lands the browser in the console, or back on
// the sign-in page with an error code it can explain.
func (a *API) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if a.OIDC == nil {
		httpx.Error(w, http.StatusNotFound, "single sign-on is not configured")
		return
	}
	ctx := r.Context()
	ip := httpx.ClientIP(r)
	failed := func(err error, actor string) {
		var se *auth.SignInError
		code := "invalid"
		if errors.As(err, &se) {
			code = se.Code
		} else {
			a.Log.Error("oidc sign-in", "err", err)
		}
		_ = a.Store.Audit(ctx, actor, "auth.login_failed", "", map[string]any{"method": "oidc", "reason": err.Error()}, ip)
		http.Redirect(w, r, "/login?sso_error="+url.QueryEscape(code), http.StatusSeeOther)
	}
	id, next, err := a.OIDC.Callback(ctx, w, r)
	if err != nil {
		failed(err, "oidc")
		return
	}
	u, changes, err := a.OIDC.Resolve(ctx, a.Store, id)
	if err != nil {
		failed(err, id.Email)
		return
	}
	tok, err := a.Auth.Session(u)
	if err != nil {
		failed(err, u.Email)
		return
	}
	details := map[string]any{"method": "oidc", "subject": id.Subject}
	if len(changes) > 0 {
		details["changes"] = changes
	}
	if err := a.Store.Audit(ctx, u.Email, "auth.login", u.ID, details, ip); err != nil {
		failed(err, u.Email)
		return
	}
	_ = a.Store.TouchLogin(ctx, u.ID)
	a.Auth.SetCookie(w, tok)
	http.Redirect(w, r, next, http.StatusSeeOther)
}
