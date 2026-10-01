// Package auth authenticates console and API users and enforces roles.
package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/dmdhrumilmistry/VaanarSena/internal/secrets"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

// APITokenPrefix marks VaanarSena API tokens so they are recognisable in
// secret scanners and distinguishable from session JWTs.
const APITokenPrefix = "vsat_"

// SessionCookie is the console session cookie name.
const SessionCookie = "vs_session"

// MinPasswordLength is enforced on every password set through the API.
const MinPasswordLength = 12

var rank = map[string]int{store.RoleAuditor: 1, store.RoleOperator: 2, store.RoleAdmin: 3}

// ValidRole reports whether role is known.
func ValidRole(role string) bool { _, ok := rank[role]; return ok }

// HashPassword returns a bcrypt hash.
func HashPassword(pw string) (string, error) {
	if len(pw) < MinPasswordLength {
		return "", errors.New("password must be at least 12 characters")
	}
	if len(pw) > 72 {
		return "", errors.New("password must be at most 72 bytes")
	}
	b, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	return string(b), err
}

// dummyHash equalises login timing for unknown users.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("vaanarsena-timing-dummy"), 12)

// Authenticator issues and verifies credentials.
type Authenticator struct {
	st      *store.Store
	key     []byte
	ttl     time.Duration
	secure  bool
	limiter *loginLimiter
}

// New returns an Authenticator. secure sets the Secure flag on cookies.
func New(st *store.Store, box *secrets.Box, ttl time.Duration, secure bool) *Authenticator {
	return &Authenticator{st: st, key: box.SessionKey(), ttl: ttl, secure: secure, limiter: newLoginLimiter()}
}

// ErrInvalidCredentials is returned for any failed login.
var ErrInvalidCredentials = errors.New("invalid email or password")

// ErrRateLimited is returned when too many logins failed recently.
var ErrRateLimited = errors.New("too many failed attempts, try again later")

// Login verifies a password and returns a session token.
func (a *Authenticator) Login(ctx context.Context, email, password, remoteIP string) (string, *store.User, error) {
	key := strings.ToLower(email) + "|" + remoteIP
	if !a.limiter.allow(key) {
		return "", nil, ErrRateLimited
	}
	u, err := a.st.UserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			bcrypt.CompareHashAndPassword(dummyHash, []byte(password)) //nolint:errcheck
			a.limiter.fail(key)
			return "", nil, ErrInvalidCredentials
		}
		return "", nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil || u.Disabled {
		a.limiter.fail(key)
		return "", nil, ErrInvalidCredentials
	}
	a.limiter.reset(key)
	_ = a.st.TouchLogin(ctx, u.ID)
	tok, err := a.issue(u)
	return tok, u, err
}

type claims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

func (a *Authenticator) issue(u *store.User) (string, error) {
	now := time.Now()
	c := claims{
		Role: u.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   u.ID,
			Issuer:    "vaanarsena",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(a.ttl)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(a.key)
}

// SetCookie writes the session cookie.
func (a *Authenticator) SetCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: a.secure,
		SameSite: http.SameSiteStrictMode, MaxAge: int(a.ttl.Seconds()),
	})
}

// ClearCookie removes the session cookie.
func (a *Authenticator) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: a.secure,
		SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

// Principal is the authenticated caller.
type Principal struct {
	User *store.User
	// ViaCookie is true for browser sessions; those need CSRF protection.
	ViaCookie bool
}

type ctxKey struct{}

// FromContext returns the principal set by Middleware.
func FromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(ctxKey{}).(*Principal)
	return p
}

// Authenticate resolves the caller from a Bearer API token, a Bearer session
// JWT, or the session cookie.
func (a *Authenticator) Authenticate(r *http.Request) (*Principal, error) {
	var raw string
	viaCookie := false
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		raw = strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	} else if c, err := r.Cookie(SessionCookie); err == nil {
		raw, viaCookie = c.Value, true
	}
	if raw == "" {
		return nil, errors.New("authentication required")
	}
	if strings.HasPrefix(raw, APITokenPrefix) {
		u, err := a.st.UserByAPIToken(r.Context(), secrets.Hash(raw))
		if err != nil {
			return nil, errors.New("invalid API token")
		}
		return &Principal{User: u}, nil
	}
	var c claims
	_, err := jwt.ParseWithClaims(raw, &c, func(t *jwt.Token) (any, error) { return a.key, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("vaanarsena"), jwt.WithExpirationRequired())
	if err != nil {
		return nil, errors.New("invalid or expired session")
	}
	// Re-read the user so disabling an account or changing a role takes
	// effect immediately rather than at token expiry.
	u, err := a.st.UserByID(r.Context(), c.Subject)
	if err != nil || u.Disabled {
		return nil, errors.New("invalid or expired session")
	}
	return &Principal{User: u, ViaCookie: viaCookie}, nil
}

// Middleware requires authentication and at least minRole. Cookie-authenticated
// state-changing requests must carry X-Requested-With, which a cross-site form
// cannot set (SameSite=Strict is the primary defence; this is belt and braces).
func (a *Authenticator) Middleware(minRole string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := a.Authenticate(r)
		if err != nil {
			httpError(w, http.StatusUnauthorized, err.Error())
			return
		}
		if p.ViaCookie && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Requested-With") == "" {
			httpError(w, http.StatusForbidden, "missing X-Requested-With header")
			return
		}
		if rank[p.User.Role] < rank[minRole] {
			httpError(w, http.StatusForbidden, "requires role "+minRole)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, p)))
	})
}

// Has reports whether the principal holds at least role.
func (p *Principal) Has(role string) bool { return p != nil && rank[p.User.Role] >= rank[role] }

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(`{"error":` + quote(msg) + `}`))
}

func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			if r < 0x20 {
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// loginLimiter allows 10 failures per key per 15 minutes.
type loginLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newLoginLimiter() *loginLimiter { return &loginLimiter{hits: map[string][]time.Time{}} }

const (
	limitWindow   = 15 * time.Minute
	limitFailures = 10
)

func (l *loginLimiter) prune(key string, now time.Time) []time.Time {
	h := l.hits[key]
	i := 0
	for i < len(h) && now.Sub(h[i]) > limitWindow {
		i++
	}
	h = h[i:]
	if len(h) == 0 {
		delete(l.hits, key)
	} else {
		l.hits[key] = h
	}
	return h
}

func (l *loginLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.prune(key, time.Now())) < limitFailures
}

func (l *loginLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hits[key] = append(l.prune(key, time.Now()), time.Now())
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}
