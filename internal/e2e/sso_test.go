package e2e

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/dmdhrumilmistry/VaanarSena/internal/auth"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

const ssoClientID = "vaanarsena-console"

// fakeIdP is an OpenID provider: discovery, a JWKS with one RSA key, and a
// token endpoint that checks the PKCE verifier and signs ID tokens.
type fakeIdP struct {
	t     *testing.T
	srv   *httptest.Server
	key   *rsa.PrivateKey
	mu    sync.Mutex
	codes map[string]pendingCode
	n     int
}

type pendingCode struct {
	challenge string
	claims    jwt.MapClaims
}

func newFakeIdP(t *testing.T) *fakeIdP {
	p := &fakeIdP{t: t, codes: map[string]pendingCode{}}
	p.key, _ = rsa.GenerateKey(rand.Reader, 2048)
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]string{"issuer": p.srv.URL, "authorization_endpoint": p.srv.URL + "/authorize",
			"token_endpoint": p.srv.URL + "/token", "jwks_uri": p.srv.URL + "/jwks"})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, r *http.Request) {
		b64 := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
		reply(w, map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "k1", "use": "sig",
			"n": b64(p.key.N.Bytes()), "e": b64(big.NewInt(int64(p.key.E)).Bytes())}}})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		p.mu.Lock()
		pc, ok := p.codes[r.Form.Get("code")]
		delete(p.codes, r.Form.Get("code"))
		p.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		id, secret, _ := r.BasicAuth()
		if id == "" {
			id, secret = r.Form.Get("client_id"), r.Form.Get("client_secret")
		}
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != pc.challenge || id != ssoClientID || secret != "s3cret" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, pc.claims)
		tok.Header["kid"] = "k1"
		signed, _ := tok.SignedString(p.key)
		reply(w, map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 300, "id_token": signed})
	})
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

// signIn runs the browser side of a sign-in. claims are merged over valid
// defaults; tamper can break the authorize response. It returns the final
// redirect target and the session cookie, if one was set.
func (e *env) signIn(p *fakeIdP, claims jwt.MapClaims, tamper func(q url.Values)) (string, string) {
	e.t.Helper()
	client := *e.srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := client.Get(e.srv.URL + "/api/v1/auth/oidc/start?next=/devices")
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		e.t.Fatalf("start: %d", resp.StatusCode)
	}
	authz, _ := url.Parse(resp.Header.Get("Location"))
	aq := authz.Query()
	if aq.Get("code_challenge_method") != "S256" || aq.Get("nonce") == "" || aq.Get("client_id") != ssoClientID ||
		aq.Get("redirect_uri") != e.srv.URL+"/api/v1/auth/oidc/callback" {
		e.t.Fatalf("authorize request: %v", aq)
	}
	var flow *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == auth.FlowCookie {
			flow = c
		}
	}
	if flow == nil || !flow.HttpOnly || flow.SameSite != http.SameSiteLaxMode {
		e.t.Fatalf("flow cookie: %+v", flow)
	}

	p.mu.Lock()
	p.n++
	code := "code-" + strconv.Itoa(p.n)
	now := time.Now()
	full := jwt.MapClaims{"iss": p.srv.URL, "aud": ssoClientID, "sub": "user-1", "email": "ops@e2e.test",
		"email_verified": true, "name": "Ops", "nonce": aq.Get("nonce"), "iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix()}
	for k, v := range claims {
		if v == nil {
			delete(full, k)
		} else {
			full[k] = v
		}
	}
	p.codes[code] = pendingCode{challenge: aq.Get("code_challenge"), claims: full}
	p.mu.Unlock()

	cb := url.Values{"code": {code}, "state": {aq.Get("state")}}
	if tamper != nil {
		tamper(cb)
	}
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/v1/auth/oidc/callback?"+cb.Encode(), nil)
	req.AddCookie(flow)
	resp, err = client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	session := ""
	for _, c := range resp.Cookies() {
		if c.Name == auth.SessionCookie && c.MaxAge > 0 {
			session = c.Value
		}
	}
	return resp.Header.Get("Location"), session
}

func (e *env) useSSO(p *fakeIdP, roles map[string][]string, domains ...string) {
	e.t.Helper()
	o, err := auth.NewOIDC(auth.OIDCConfig{Issuer: p.srv.URL, ClientID: ssoClientID, ClientSecret: "s3cret",
		RedirectURL: e.srv.URL + "/api/v1/auth/oidc/callback", RoleGroups: roles, AllowedDomains: domains, Name: "Example IdP"}, e.box, true)
	if err != nil {
		e.t.Fatal(err)
	}
	e.api.OIDC = o
}

func (e *env) whoAmI(session string) *store.User {
	e.t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/v1/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: session})
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		e.t.Fatalf("me: %d", resp.StatusCode)
	}
	var u store.User
	_ = json.NewDecoder(resp.Body).Decode(&u)
	return &u
}

func TestSSOWithGroupRoles(t *testing.T) {
	e := setup(t)
	p := newFakeIdP(t)

	var providers struct{ OIDC struct{ Enabled bool } }
	e.call("GET", "/api/v1/auth/providers", "", 200, &providers)
	if providers.OIDC.Enabled {
		t.Fatal("SSO reported enabled without configuration")
	}
	e.call("GET", "/api/v1/auth/oidc/start", "", 404, nil)

	e.useSSO(p, map[string][]string{"admin": {"vs-admins"}, "operator": {"vs-ops"}}, "e2e.test")
	e.call("GET", "/api/v1/auth/providers", "", 200, &providers)
	if !providers.OIDC.Enabled {
		t.Fatal("SSO not reported enabled")
	}

	// A new user in a mapped group is created with that role and signed in.
	loc, session := e.signIn(p, jwt.MapClaims{"groups": []string{"vs-ops", "everyone"}}, nil)
	if loc != "/devices" || session == "" {
		t.Fatalf("sign-in landed on %q (session %t)", loc, session != "")
	}
	if u := e.whoAmI(session); u.Email != "ops@e2e.test" || u.Role != store.RoleOperator || !u.SSO {
		t.Fatalf("created user = %+v", u)
	}
	// Groups decide the role at every sign-in.
	_, session = e.signIn(p, jwt.MapClaims{"groups": []string{"vs-admins"}}, nil)
	if u := e.whoAmI(session); u.Role != store.RoleAdmin {
		t.Fatalf("role not updated from groups: %s", u.Role)
	}
	// An SSO-created user has no password.
	e.call("POST", "/api/v1/auth/login", `{"email":"ops@e2e.test","password":"!sso"}`, 401, nil)

	fails := map[string]struct {
		claims jwt.MapClaims
		tamper func(url.Values)
	}{
		"no_access":  {jwt.MapClaims{"sub": "user-2", "email": "nobody@e2e.test", "groups": []string{"everyone"}}, nil},
		"domain":     {jwt.MapClaims{"sub": "user-3", "email": "x@elsewhere.test", "groups": []string{"vs-ops"}}, nil},
		"unverified": {jwt.MapClaims{"sub": "user-4", "email": "new@e2e.test", "email_verified": false, "groups": []string{"vs-ops"}}, nil},
		"invalid":    {jwt.MapClaims{"nonce": "replayed", "groups": []string{"vs-ops"}}, nil},
		"expired":    {jwt.MapClaims{"groups": []string{"vs-ops"}}, func(q url.Values) { q.Set("state", "forged") }},
		// The password admin's email, from a different identity, after the
		// admin has linked their own.
		"conflict": {jwt.MapClaims{"sub": "intruder", "email": "admin@e2e.test", "groups": []string{"vs-admins"}}, nil},
	}
	// Link admin@e2e.test to its real identity first.
	if _, s := e.signIn(p, jwt.MapClaims{"sub": "admin-sub", "email": "admin@e2e.test", "groups": []string{"vs-admins"}}, nil); s == "" {
		t.Fatal("existing admin could not sign in with SSO")
	}
	for code, f := range fails {
		loc, session := e.signIn(p, f.claims, f.tamper)
		if session != "" || loc != "/login?sso_error="+code {
			t.Errorf("%s: landed on %q, session %t", code, loc, session != "")
		}
	}
	for _, aud := range []any{"someone-else", []string{"someone-else", ssoClientID}} {
		if loc, s := e.signIn(p, jwt.MapClaims{"aud": aud, "groups": []string{"vs-ops"}}, nil); s != "" || loc != "/login?sso_error=invalid" {
			t.Errorf("aud %v accepted: %q", aud, loc)
		}
	}
	if loc, s := e.signIn(p, jwt.MapClaims{"exp": time.Now().Add(-time.Hour).Unix(), "groups": []string{"vs-ops"}}, nil); s != "" {
		t.Errorf("expired id token accepted: %q", loc)
	}

	// Disabled users stay out.
	ops, _ := e.st.UserByEmail(context.Background(), "ops@e2e.test")
	e.call("PATCH", "/api/v1/users/"+ops.ID, `{"disabled":true}`, 200, nil)
	if loc, s := e.signIn(p, jwt.MapClaims{"groups": []string{"vs-admins"}}, nil); s != "" || loc != "/login?sso_error=disabled" {
		t.Errorf("disabled user signed in: %q", loc)
	}

	var audit struct{ Entries []store.AuditEntry }
	e.call("GET", "/api/v1/audit?limit=100", "", 200, &audit)
	failed, ok := 0, 0
	for _, a := range audit.Entries {
		switch a.Action {
		case "auth.login_failed":
			failed++
		case "auth.login":
			if strings.Contains(string(a.Details), `"oidc"`) {
				ok++
			}
		}
	}
	if failed < len(fails)+3 || ok < 3 {
		t.Errorf("audit: %d failed, %d successful SSO sign-ins", failed, ok)
	}
}

func TestSSOWithoutGroupMapping(t *testing.T) {
	e := setup(t)
	p := newFakeIdP(t)
	e.useSSO(p, nil)

	// Without a mapping, SSO never creates accounts.
	if loc, s := e.signIn(p, nil, nil); s != "" || loc != "/login?sso_error=not_provisioned" {
		t.Fatalf("unknown user: %q", loc)
	}
	// An existing user signs in with the role an admin gave them.
	hash, _ := auth.HashPassword("auditor-password-1")
	if _, err := e.st.CreateUser(context.Background(), "ops@e2e.test", "", hash, store.RoleAuditor); err != nil {
		t.Fatal(err)
	}
	_, session := e.signIn(p, jwt.MapClaims{"groups": []string{"vs-admins"}}, nil)
	if u := e.whoAmI(session); u.Role != store.RoleAuditor || !u.SSO {
		t.Fatalf("user = %+v", u)
	}
}

func TestSSOKeepsLastAdmin(t *testing.T) {
	e := setup(t)
	p := newFakeIdP(t)
	e.useSSO(p, map[string][]string{"operator": {"vs-ops"}})
	_, session := e.signIn(p, jwt.MapClaims{"sub": "admin-sub", "email": "admin@e2e.test", "groups": []string{"vs-ops"}}, nil)
	if u := e.whoAmI(session); u.Role != store.RoleAdmin {
		t.Fatalf("group mapping demoted the last admin to %s", u.Role)
	}
}

func TestSSONextIsLocal(t *testing.T) {
	for in, want := range map[string]string{"/devices?x=1": "/devices?x=1", "//evil.example": "/", "https://evil.example": "/",
		"/\\evil.example": "/", "": "/", "/\t/evil.example": "/", "/%09/x": "/%09/x", "/a\x7f": "/"} {
		if got := auth.SafeNext(in); got != want {
			t.Errorf("SafeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSSORequiresVerifiedEmailToLink pins the account-takeover fix: a
// provider that does not vouch for the email (no email_verified claim, as Entra
// ID sends) cannot link the sign-in to an existing account with that email.
func TestSSORequiresVerifiedEmailToLink(t *testing.T) {
	e := setup(t)
	p := newFakeIdP(t)
	e.useSSO(p, map[string][]string{"operator": {"vs-ops"}})
	claims := jwt.MapClaims{"sub": "attacker", "email": "admin@e2e.test", "email_verified": nil, "groups": []string{"vs-ops"}}
	if loc, s := e.signIn(p, claims, nil); s != "" || loc != "/login?sso_error=unverified" {
		t.Fatalf("unverified email linked to the admin: %q", loc)
	}
	if u, _ := e.st.UserByEmail(context.Background(), "admin@e2e.test"); u.SSO {
		t.Fatal("admin account was bound to the unverified identity")
	}
	// An operator who opts in for a provider that never sends the claim.
	e.api.OIDC.Config.TrustUnverifiedEmail = true
	if _, s := e.signIn(p, jwt.MapClaims{"sub": "new-1", "email": "new@e2e.test", "email_verified": nil, "groups": []string{"vs-ops"}}, nil); s == "" {
		t.Fatal("TrustUnverifiedEmail did not allow the sign-in")
	}
}
