package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/dmdhrumilmistry/VaanarSena/internal/auth"
	"github.com/dmdhrumilmistry/VaanarSena/internal/httpx"
	"github.com/dmdhrumilmistry/VaanarSena/internal/secrets"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "malformed request")
		return
	}
	ip := httpx.ClientIP(r)
	tok, u, err := a.Auth.Login(r.Context(), req.Email, req.Password, ip)
	switch {
	case errors.Is(err, auth.ErrRateLimited):
		httpx.Error(w, http.StatusTooManyRequests, err.Error())
		return
	case errors.Is(err, auth.ErrInvalidCredentials):
		_ = a.Store.Audit(r.Context(), strings.ToLower(req.Email), "auth.login_failed", "", nil, ip)
		httpx.Error(w, http.StatusUnauthorized, err.Error())
		return
	case err != nil:
		a.fail(w, err)
		return
	}
	_ = a.Store.Audit(r.Context(), u.Email, "auth.login", u.ID, nil, ip)
	a.Auth.SetCookie(w, tok)
	httpx.JSON(w, http.StatusOK, map[string]any{"user": u, "token": tok})
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	a.Auth.ClearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, principal(r).User)
}

func (a *API) changePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "malformed request")
		return
	}
	u := principal(r).User
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Current)) != nil {
		httpx.Error(w, http.StatusForbidden, "current password is incorrect")
		return
	}
	hash, err := auth.HashPassword(req.New)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if !a.audit(w, r, "user.password_changed", u.ID, nil) {
		return
	}
	if err := a.Store.SetPassword(r.Context(), u.ID, hash); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.Store.ListUsers(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"users": nonNil(users)})
}

func (a *API) createUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "malformed request")
		return
	}
	if !strings.Contains(req.Email, "@") || !auth.ValidRole(req.Role) {
		httpx.Error(w, http.StatusBadRequest, "a valid email and role (admin, operator, auditor) are required")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := a.Store.CreateUser(r.Context(), req.Email, req.Name, hash, req.Role)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") {
			httpx.Error(w, http.StatusConflict, "a user with that email exists")
			return
		}
		a.fail(w, err)
		return
	}
	if !a.audit(w, r, "user.create", u.ID, map[string]any{"email": u.Email, "role": u.Role}) {
		return
	}
	httpx.JSON(w, http.StatusCreated, u)
}

func (a *API) patchUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Name     *string `json:"name"`
		Role     *string `json:"role"`
		Disabled *bool   `json:"disabled"`
		Password *string `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "malformed request")
		return
	}
	u, err := a.Store.UserByID(r.Context(), id)
	if err != nil {
		a.fail(w, err)
		return
	}
	name, role, disabled := u.Name, u.Role, u.Disabled
	if req.Name != nil {
		name = *req.Name
	}
	if req.Role != nil {
		if !auth.ValidRole(*req.Role) {
			httpx.Error(w, http.StatusBadRequest, "invalid role")
			return
		}
		role = *req.Role
	}
	if req.Disabled != nil {
		disabled = *req.Disabled
	}
	// Never lock the organisation out of its own MDM.
	if u.Role == store.RoleAdmin && !u.Disabled && (role != store.RoleAdmin || disabled) {
		if n, err := a.Store.CountAdmins(r.Context()); err == nil && n <= 1 {
			httpx.Error(w, http.StatusConflict, "cannot demote or disable the last admin")
			return
		}
	}
	if !a.audit(w, r, "user.update", id, map[string]any{"role": role, "disabled": disabled, "passwordReset": req.Password != nil}) {
		return
	}
	if req.Password != nil {
		hash, err := auth.HashPassword(*req.Password)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := a.Store.SetPassword(r.Context(), id, hash); err != nil {
			a.fail(w, err)
			return
		}
	}
	u, err = a.Store.UpdateUser(r.Context(), id, name, role, disabled)
	if err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, u)
}

func (a *API) deleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == principal(r).User.ID {
		httpx.Error(w, http.StatusConflict, "you cannot delete yourself")
		return
	}
	u, err := a.Store.UserByID(r.Context(), id)
	if err != nil {
		a.fail(w, err)
		return
	}
	if u.Role == store.RoleAdmin {
		if n, err := a.Store.CountAdmins(r.Context()); err == nil && n <= 1 {
			httpx.Error(w, http.StatusConflict, "cannot delete the last admin")
			return
		}
	}
	if !a.audit(w, r, "user.delete", id, map[string]any{"email": u.Email}) {
		return
	}
	if err := a.Store.DeleteUser(r.Context(), id); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) listAPITokens(w http.ResponseWriter, r *http.Request) {
	toks, err := a.Store.ListAPITokens(r.Context(), principal(r).User.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"tokens": nonNil(toks)})
}

func (a *API) createAPIToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name          string `json:"name"`
		ExpiresInDays int    `json:"expiresInDays"`
	}
	if err := decode(r, &req); err != nil || req.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "name is required")
		return
	}
	var exp *time.Time
	if req.ExpiresInDays > 0 {
		t := time.Now().AddDate(0, 0, req.ExpiresInDays)
		exp = &t
	}
	raw := auth.APITokenPrefix + secrets.Token(32)
	t, err := a.Store.CreateAPIToken(r.Context(), principal(r).User.ID, req.Name, secrets.Hash(raw), exp)
	if err != nil {
		a.fail(w, err)
		return
	}
	if !a.audit(w, r, "api_token.create", t.ID, map[string]any{"name": t.Name}) {
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"token": raw, "meta": t})
}

func (a *API) deleteAPIToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Store.DeleteAPIToken(r.Context(), principal(r).User.ID, id); err != nil {
		a.fail(w, err)
		return
	}
	if !a.audit(w, r, "api_token.delete", id, nil) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
