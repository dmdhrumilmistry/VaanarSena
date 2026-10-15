package auth

import (
	"context"
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"

	"github.com/dmdhrumilmistry/VaanarSena/internal/secrets"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

// OIDCConfig configures single sign-on with an OpenID Connect provider
// (Entra ID, Okta, Google, Keycloak, Authentik...). It follows OpenID Connect
// Core 1.0 (authorization code flow) with PKCE (RFC 7636) and Discovery 1.0.
type OIDCConfig struct {
	// Issuer is the provider's issuer URL; discovery is read from
	// <Issuer>/.well-known/openid-configuration.
	Issuer       string
	ClientID     string
	ClientSecret string
	// RedirectURL is <public URL>/api/v1/auth/oidc/callback, registered with
	// the provider.
	RedirectURL string
	Scopes      []string
	// Name labels the sign-in button.
	Name string
	// GroupsClaim names the ID token claim that lists the user's groups.
	GroupsClaim string
	// RoleGroups maps a role to the groups that grant it. When any is set, the
	// mapping decides every SSO user's role at each sign-in and users are
	// created on first sign-in; otherwise only existing users may sign in.
	RoleGroups map[string][]string
	// AllowedDomains, when set, limits SSO to these email domains.
	AllowedDomains []string
	// TrustUnverifiedEmail accepts an email address the provider does not
	// mark email_verified: true when linking or creating accounts. Off by
	// default: an address the user can set themselves would let them take
	// over the account that owns it. Turn it on only for a provider that
	// never sends the claim and does not let users choose their address.
	TrustUnverifiedEmail bool
}

// Validate checks the configuration.
func (c *OIDCConfig) Validate() error {
	var errs []error
	u, err := url.Parse(c.Issuer)
	switch {
	case err != nil || u.Host == "":
		errs = append(errs, errors.New("VS_OIDC_ISSUER must be an absolute URL"))
	case u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(u.Hostname())):
		errs = append(errs, errors.New("VS_OIDC_ISSUER must use https:// (http:// only for localhost)"))
	}
	if c.ClientID == "" {
		errs = append(errs, errors.New("VS_OIDC_CLIENT_ID is required when VS_OIDC_ISSUER is set"))
	}
	for role := range c.RoleGroups {
		if !ValidRole(role) {
			errs = append(errs, fmt.Errorf("unknown role %q in the OIDC group mapping", role))
		}
	}
	return errors.Join(errs...)
}

// MapsRoles reports whether roles come from the provider's groups.
func (c *OIDCConfig) MapsRoles() bool {
	for _, g := range c.RoleGroups {
		if len(g) > 0 {
			return true
		}
	}
	return false
}

// RoleFor returns the highest role any of groups grants, or "".
func (c *OIDCConfig) RoleFor(groups []string) string {
	have := map[string]bool{}
	for _, g := range groups {
		have[g] = true
	}
	best := ""
	for role, gs := range c.RoleGroups {
		for _, g := range gs {
			if have[g] && rank[role] > rank[best] {
				best = role
			}
		}
	}
	return best
}

// DomainAllowed reports whether email is in an allowed domain.
func (c *OIDCConfig) DomainAllowed(email string) bool {
	if len(c.AllowedDomains) == 0 {
		return true
	}
	at := strings.LastIndexByte(email, '@')
	if at < 0 {
		return false
	}
	domain := strings.ToLower(email[at+1:])
	for _, d := range c.AllowedDomains {
		if strings.EqualFold(strings.TrimPrefix(d, "@"), domain) {
			return true
		}
	}
	return false
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Identity is a verified OpenID Connect sign-in.
type Identity struct {
	// Subject is "<issuer>|<sub>", unique across providers.
	Subject string
	Email   string
	// EmailVerified is nil when the provider does not send the claim.
	EmailVerified *bool
	Name          string
	Groups        []string
}

// OIDC runs the sign-in flow against one provider.
type OIDC struct {
	Config OIDCConfig
	box    *secrets.Box
	secure bool
	client *http.Client
	now    func() time.Time

	mu     sync.Mutex
	disc   *discovery
	discAt time.Time
	keys   map[string]crypto.PublicKey
	keysAt time.Time
}

type discovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

// NewOIDC returns a provider client. secure sets the Secure flag on the flow
// cookie. Discovery happens on first use, so the server starts even when the
// provider is unreachable.
func NewOIDC(cfg OIDCConfig, box *secrets.Box, secure bool) (*OIDC, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"openid", "email", "profile"}
	}
	if cfg.GroupsClaim == "" {
		cfg.GroupsClaim = "groups"
	}
	if cfg.Name == "" {
		cfg.Name = "single sign-on"
	}
	return &OIDC{Config: cfg, box: box, secure: secure, client: &http.Client{Timeout: 15 * time.Second}, now: time.Now}, nil
}

// SetHTTPClient replaces the client used to reach the provider (tests).
func (o *OIDC) SetHTTPClient(c *http.Client) { o.client = c }

// FlowCookie holds the state of a sign-in in progress.
const FlowCookie = "vs_oidc"

const flowTTL = 10 * time.Minute

type flowState struct {
	State    string    `json:"s"`
	Nonce    string    `json:"n"`
	Verifier string    `json:"v"`
	Next     string    `json:"r"`
	Expires  time.Time `json:"e"`
}

// Start begins a sign-in: it returns the provider URL to redirect to and sets
// the encrypted flow cookie. next is where to land after signing in.
func (o *OIDC) Start(ctx context.Context, w http.ResponseWriter, next string) (string, error) {
	d, err := o.discovery(ctx)
	if err != nil {
		return "", err
	}
	fs := flowState{State: secrets.Token(24), Nonce: secrets.Token(24), Verifier: secrets.Token(48),
		Next: SafeNext(next), Expires: o.now().Add(flowTTL)}
	raw, _ := json.Marshal(fs)
	sealed, err := o.box.Seal(raw)
	if err != nil {
		return "", err
	}
	// Lax, not Strict: the provider sends the browser back with a cross-site
	// top-level GET, and a Strict cookie would not come with it.
	http.SetCookie(w, &http.Cookie{Name: FlowCookie, Value: base64.RawURLEncoding.EncodeToString(sealed),
		Path: "/api/v1/auth/oidc", HttpOnly: true, Secure: o.secure, SameSite: http.SameSiteLaxMode, MaxAge: int(flowTTL.Seconds())})
	return o.oauth(d).AuthCodeURL(fs.State, oauth2.SetAuthURLParam("nonce", fs.Nonce), oauth2.S256ChallengeOption(fs.Verifier)), nil
}

func (o *OIDC) oauth(d *discovery) *oauth2.Config {
	return &oauth2.Config{ClientID: o.Config.ClientID, ClientSecret: o.Config.ClientSecret, RedirectURL: o.Config.RedirectURL,
		Scopes: o.Config.Scopes, Endpoint: oauth2.Endpoint{AuthURL: d.AuthorizationEndpoint, TokenURL: d.TokenEndpoint}}
}

// SafeNext returns next if it is a path on this site, or "/". Browsers drop
// tabs and newlines and treat a backslash as a slash, so any control
// character or backslash is refused before the structural checks.
func SafeNext(next string) string {
	for _, r := range next {
		if r < 0x20 || r == 0x7f || r == '\\' {
			return "/"
		}
	}
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/"
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") {
		return "/"
	}
	return next
}

// SignInError is a failed sign-in. Code is a stable identifier the console
// turns into a message; Detail is for the audit log.
type SignInError struct {
	Code   string
	Detail string
}

func (e *SignInError) Error() string { return e.Code + ": " + e.Detail }

func signInErr(code, format string, a ...any) error {
	return &SignInError{Code: code, Detail: fmt.Sprintf(format, a...)}
}

// Callback completes a sign-in from the provider's redirect. It clears the
// flow cookie and returns the verified identity and where to go next.
func (o *OIDC) Callback(ctx context.Context, w http.ResponseWriter, r *http.Request) (*Identity, string, error) {
	http.SetCookie(w, &http.Cookie{Name: FlowCookie, Value: "", Path: "/api/v1/auth/oidc", HttpOnly: true, Secure: o.secure,
		SameSite: http.SameSiteLaxMode, MaxAge: -1})
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		return nil, "/", signInErr("denied", "provider returned %s: %s", e, q.Get("error_description"))
	}
	c, err := r.Cookie(FlowCookie)
	if err != nil {
		return nil, "/", signInErr("expired", "no sign-in in progress (flow cookie missing)")
	}
	sealed, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return nil, "/", signInErr("expired", "flow cookie malformed")
	}
	raw, err := o.box.Open(sealed)
	if err != nil {
		return nil, "/", signInErr("expired", "flow cookie cannot be decrypted")
	}
	var fs flowState
	if json.Unmarshal(raw, &fs) != nil || o.now().After(fs.Expires) {
		return nil, "/", signInErr("expired", "sign-in took too long")
	}
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(fs.State)) != 1 {
		return nil, fs.Next, signInErr("expired", "state does not match")
	}
	code := q.Get("code")
	if code == "" {
		return nil, fs.Next, signInErr("invalid", "no authorization code")
	}
	d, err := o.discovery(ctx)
	if err != nil {
		return nil, fs.Next, signInErr("unavailable", "%v", err)
	}
	tok, err := o.oauth(d).Exchange(context.WithValue(ctx, oauth2.HTTPClient, o.client), code, oauth2.VerifierOption(fs.Verifier))
	if err != nil {
		return nil, fs.Next, signInErr("invalid", "token exchange failed: %v", err)
	}
	rawID, _ := tok.Extra("id_token").(string)
	if rawID == "" {
		return nil, fs.Next, signInErr("invalid", "token response has no id_token")
	}
	id, err := o.verify(ctx, d, rawID, fs.Nonce)
	if err != nil {
		return nil, fs.Next, signInErr("invalid", "%v", err)
	}
	return id, fs.Next, nil
}

type idClaims struct {
	Nonce         string `json:"nonce"`
	Email         string `json:"email"`
	EmailVerified any    `json:"email_verified"`
	Name          string `json:"name"`
	AZP           string `json:"azp"`
	jwt.RegisteredClaims
}

// verify checks an ID token as OpenID Connect Core 3.1.3.7 requires:
// signature by a provider key, issuer, audience (and azp when there are
// several audiences), expiry and nonce.
func (o *OIDC) verify(ctx context.Context, d *discovery, raw, nonce string) (*Identity, error) {
	var c idClaims
	_, err := jwt.ParseWithClaims(raw, &c, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		return o.key(ctx, d, kid)
	},
		jwt.WithValidMethods([]string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384"}),
		jwt.WithIssuer(d.Issuer), jwt.WithAudience(o.Config.ClientID), jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(), jwt.WithLeeway(time.Minute), jwt.WithTimeFunc(o.now))
	if err != nil {
		return nil, fmt.Errorf("id token: %w", err)
	}
	if len(c.Audience) > 1 && c.AZP != o.Config.ClientID {
		return nil, errors.New("id token: azp does not name this client")
	}
	if subtle.ConstantTimeCompare([]byte(c.Nonce), []byte(nonce)) != 1 {
		return nil, errors.New("id token: nonce does not match")
	}
	if c.Subject == "" {
		return nil, errors.New("id token: no subject")
	}
	id := &Identity{Subject: d.Issuer + "|" + c.Subject, Email: strings.ToLower(strings.TrimSpace(c.Email)), Name: c.Name}
	switch v := c.EmailVerified.(type) {
	case bool:
		id.EmailVerified = &v
	case string: // some providers send "true"/"false"
		b := v == "true"
		id.EmailVerified = &b
	}
	// The groups claim is read from the raw payload: its name is configurable.
	id.Groups = groupsFrom(raw, o.Config.GroupsClaim)
	return id, nil
}

func groupsFrom(raw, claim string) []string {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(payload, &m) != nil {
		return nil
	}
	switch v := m[claim].(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, g := range v {
			if s, ok := g.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func (o *OIDC) get(ctx context.Context, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", u, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v)
}

// discovery returns the provider metadata, cached for an hour.
func (o *OIDC) discovery(ctx context.Context) (*discovery, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.disc != nil && o.now().Sub(o.discAt) < time.Hour {
		return o.disc, nil
	}
	var d discovery
	if err := o.get(ctx, strings.TrimRight(o.Config.Issuer, "/")+"/.well-known/openid-configuration", &d); err != nil {
		return nil, fmt.Errorf("OIDC discovery: %w", err)
	}
	// Discovery 1.0 section 4.3: the issuer must be exactly the configured one.
	if d.Issuer != o.Config.Issuer {
		return nil, fmt.Errorf("OIDC discovery: issuer %q does not match VS_OIDC_ISSUER %q", d.Issuer, o.Config.Issuer)
	}
	if d.AuthorizationEndpoint == "" || d.TokenEndpoint == "" || d.JWKSURI == "" {
		return nil, errors.New("OIDC discovery: authorization_endpoint, token_endpoint and jwks_uri are required")
	}
	o.disc, o.discAt = &d, o.now()
	return o.disc, nil
}

// key returns the provider key with the given ID, refreshing the key set when
// the ID is unknown (the provider rotated keys) at most once a minute.
func (o *OIDC) key(ctx context.Context, d *discovery, kid string) (crypto.PublicKey, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	find := func() crypto.PublicKey {
		if kid == "" && len(o.keys) == 1 {
			for _, k := range o.keys {
				return k
			}
		}
		return o.keys[kid]
	}
	if k := find(); k != nil {
		return k, nil
	}
	if o.keys != nil && o.now().Sub(o.keysAt) < time.Minute {
		return nil, fmt.Errorf("no provider key with id %q", kid)
	}
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := o.get(ctx, d.JWKSURI, &set); err != nil {
		return nil, err
	}
	keys := map[string]crypto.PublicKey{}
	for _, j := range set.Keys {
		if j.Use != "" && j.Use != "sig" {
			continue
		}
		if k, err := j.public(); err == nil {
			keys[j.Kid] = k
		}
	}
	o.keys, o.keysAt = keys, o.now()
	if k := find(); k != nil {
		return k, nil
	}
	return nil, fmt.Errorf("no provider key with id %q", kid)
}

// jwk is a JSON Web Key (RFC 7517) holding an RSA or EC public key (RFC 7518).
type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func (j jwk) public() (crypto.PublicKey, error) {
	dec := base64.RawURLEncoding.DecodeString
	switch j.Kty {
	case "RSA":
		n, err1 := dec(j.N)
		e, err2 := dec(j.E)
		if err1 != nil || err2 != nil || len(n) < 256 || len(e) == 0 || len(e) > 4 {
			return nil, errors.New("invalid RSA key (at least 2048 bits required)")
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}, nil
	case "EC":
		var curve elliptic.Curve
		var ec ecdh.Curve
		switch j.Crv {
		case "P-256":
			curve, ec = elliptic.P256(), ecdh.P256()
		case "P-384":
			curve, ec = elliptic.P384(), ecdh.P384()
		default:
			return nil, fmt.Errorf("unsupported curve %q", j.Crv)
		}
		x, err1 := dec(j.X)
		y, err2 := dec(j.Y)
		size := (curve.Params().BitSize + 7) / 8
		if err1 != nil || err2 != nil || len(x) != size || len(y) != size {
			return nil, errors.New("invalid EC key")
		}
		// Parsing the uncompressed point through crypto/ecdh rejects points
		// that are not on the curve.
		if _, err := ec.NewPublicKey(append(append([]byte{4}, x...), y...)); err != nil {
			return nil, errors.New("EC key is not on its curve")
		}
		return &ecdsa.PublicKey{Curve: curve, X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}, nil
	}
	return nil, fmt.Errorf("unsupported key type %q", j.Kty)
}

// Resolve maps a verified identity to a VaanarSena user, creating, binding
// or updating the user as the configuration allows. changes describes what
// happened, for the audit log.
func (o *OIDC) Resolve(ctx context.Context, st *store.Store, id *Identity) (u *store.User, changes []string, err error) {
	if id.Email == "" {
		return nil, nil, signInErr("no_email", "the identity provider sent no email address; add the email scope or claim")
	}
	if !o.Config.DomainAllowed(id.Email) {
		return nil, nil, signInErr("domain", "%s is not in an allowed domain", id.Email)
	}
	mapped := ""
	if o.Config.MapsRoles() {
		mapped = o.Config.RoleFor(id.Groups)
		if mapped == "" {
			return nil, nil, signInErr("no_access", "%s is in none of the groups mapped to a role (groups: %v)", id.Email, id.Groups)
		}
	}

	// The email is only trusted to link or create an account when the provider
	// vouches for it. Later sign-ins match on the bound subject instead.
	emailTrusted := (id.EmailVerified != nil && *id.EmailVerified) || o.Config.TrustUnverifiedEmail
	u, err = st.UserByOIDCSubject(ctx, id.Subject)
	if errors.Is(err, store.ErrNotFound) {
		if !emailTrusted {
			return nil, nil, signInErr("unverified", "the provider did not mark %s as verified (email_verified)", id.Email)
		}
		u, err = st.UserByEmail(ctx, id.Email)
		switch {
		case err == nil:
			// First SSO sign-in of an existing user: bind by email.
			if err := st.BindOIDCSubject(ctx, u.ID, id.Subject); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return nil, nil, signInErr("conflict", "%s is already linked to a different single sign-on identity", id.Email)
				}
				return nil, nil, err
			}
			changes = append(changes, "linked")
		case errors.Is(err, store.ErrNotFound):
			if mapped == "" {
				return nil, nil, signInErr("not_provisioned", "%s has no VaanarSena account; an admin must add it, or map groups to roles", id.Email)
			}
			u, err = st.CreateSSOUser(ctx, id.Email, id.Name, mapped, id.Subject)
			if err != nil {
				return nil, nil, err
			}
			changes = append(changes, "created")
		default:
			return nil, nil, err
		}
	} else if err != nil {
		return nil, nil, err
	}
	if u.Disabled {
		return nil, nil, signInErr("disabled", "%s is disabled", u.Email)
	}
	if mapped != "" && mapped != u.Role {
		// The store refuses, atomically, to demote the last enabled admin: an
		// identity provider misconfiguration must not lock the organisation
		// out of its own MDM. It also refuses a user disabled meanwhile.
		updated, err := st.SetRoleKeepingAnAdmin(ctx, u.ID, mapped)
		switch {
		case errors.Is(err, store.ErrLastAdmin):
			changes = append(changes, "role change to "+mapped+" refused: last admin")
		case errors.Is(err, store.ErrNotFound):
			return nil, nil, signInErr("disabled", "%s was disabled during sign-in", u.Email)
		case err != nil:
			return nil, nil, err
		default:
			changes = append(changes, "role "+u.Role+" -> "+mapped)
			u = updated
		}
	}
	return u, changes, nil
}

// Session issues a console session for a user signed in another way (SSO).
func (a *Authenticator) Session(u *store.User) (string, error) { return a.issue(u) }
