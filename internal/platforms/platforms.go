// Package platforms configures the platform integrations that need
// third-party credentials (Apple push certificate, Google service account,
// Android Enterprise, ChromeOS) at runtime, from the console or the API.
//
// Credentials are stored encrypted in the settings table. Environment
// variables still take precedence: a platform configured through VS_* is
// reported as managed by the environment and cannot be changed here.
package platforms

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dmdhrumilmistry/VaanarSena/internal/android"
	"github.com/dmdhrumilmistry/VaanarSena/internal/apple"
	"github.com/dmdhrumilmistry/VaanarSena/internal/chromeos"
	"github.com/dmdhrumilmistry/VaanarSena/internal/google"
	"github.com/dmdhrumilmistry/VaanarSena/internal/mdm"
	"github.com/dmdhrumilmistry/VaanarSena/internal/pki"
	"github.com/dmdhrumilmistry/VaanarSena/internal/secrets"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

// Settings keys. All values are JSON sealed with the server secret.
const (
	keyApple         = "platform.apple"
	keyApplePending  = "platform.apple.pending_key"
	keyGoogle        = "platform.google"
	keyAndroid       = "platform.android"
	keyAndroidSignup = "platform.android.signup"
	keyChromeOS      = "platform.chromeos"
)

// Sources of a platform's configuration.
const (
	SourceNone    = ""
	SourceEnv     = "environment"
	SourceConsole = "console"
)

// ErrEnvManaged is returned when a change targets env-configured settings.
var ErrEnvManaged = errors.New("this platform is configured by environment variables on the server; remove them to manage it here")

// ErrInvalid wraps input validation failures (mapped to HTTP 400).
var ErrInvalid = errors.New("invalid")

// Env holds the VS_* settings, which win over console settings.
type Env struct {
	APNsCertFile, APNsKeyFile, APNsTopic   string
	GoogleCredentialsFile, GoogleProjectID string
	AndroidEnterprise                      string
	GoogleAdminSubject, GoogleCustomerID   string
}

// Manager owns the configurable drivers.
type Manager struct {
	Store      *store.Store
	Box        *secrets.Box
	Svc        *mdm.Service
	CA         *pki.CA
	Org        string
	PublicURL  string
	CertHeader string
	Log        *slog.Logger
	Env        Env
	// AndroidBase and AdminBase override Google endpoints (tests).
	AndroidBase string
	AdminBase   string

	mu        sync.Mutex
	root      context.Context
	apple     *apple.Driver
	appleMux  *http.ServeMux
	appleSrc  string
	appleErr  string
	androidD  *android.Driver
	androidX  context.CancelFunc
	androidSr string
	androidEr string
	chromeD   *chromeos.Driver
	chromeX   context.CancelFunc
	chromeSrc string
	chromeErr string
}

type appleConf struct {
	CertPEM string `json:"certPem"`
	KeyPEM  string `json:"keyPem"`
	Topic   string `json:"topic"`
}

type googleConf struct {
	Credentials string `json:"credentials"` // service account key JSON
	ProjectID   string `json:"projectId"`
}

type androidConf struct {
	Enterprise string `json:"enterprise"`
}

type signupConf struct {
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	StateHash string    `json:"stateHash"`
	Expires   time.Time `json:"expires"`
}

type chromeConf struct {
	AdminSubject string `json:"adminSubject"`
	CustomerID   string `json:"customerId"`
}

func (m *Manager) androidBase() string {
	if m.AndroidBase != "" {
		return m.AndroidBase
	}
	return android.DefaultBase
}

func (m *Manager) adminBase() string {
	if m.AdminBase != "" {
		return m.AdminBase
	}
	return chromeos.DefaultBase
}

func (m *Manager) get(ctx context.Context, key string, v any) (bool, error) {
	raw, enc, err := m.Store.GetSetting(ctx, key)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if enc {
		if raw, err = m.Box.Open(raw); err != nil {
			return false, fmt.Errorf("decrypt %s (was VS_SECRET_KEY changed?): %w", key, err)
		}
	}
	return true, json.Unmarshal(raw, v)
}

func (m *Manager) put(ctx context.Context, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	sealed, err := m.Box.Seal(b)
	if err != nil {
		return err
	}
	return m.Store.PutSetting(ctx, key, sealed, true)
}

// Start loads every platform and keeps sync loops running until ctx ends.
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	m.root = ctx
	m.mu.Unlock()
	if err := m.reloadApple(ctx); err != nil {
		m.Log.Warn("apple not loaded", "err", err)
	}
	m.reloadGoogle(ctx)
}

// --- Apple ---

// Routes registers the Apple device endpoints once. They answer 404 with an
// explanation until Apple is configured, and switch to the current driver
// immediately when it is.
func (m *Manager) Routes(mux *http.ServeMux) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		am := m.appleMux
		m.mu.Unlock()
		if am == nil {
			http.Error(w, "Apple device management is not configured on this server (Settings > Platforms > Apple)", http.StatusNotFound)
			return
		}
		am.ServeHTTP(w, r)
	})
	mux.Handle("GET /mdm/apple/enroll", h)
	mux.Handle("PUT /mdm/apple/checkin", h)
	mux.Handle("PUT /mdm/apple/server", h)
}

func (m *Manager) reloadApple(ctx context.Context) error {
	var conf appleConf
	src := SourceNone
	var pusher *apple.Pusher
	var err error
	switch {
	case m.Env.APNsCertFile != "":
		src = SourceEnv
		pusher, err = apple.NewPusher(m.Env.APNsCertFile, m.Env.APNsKeyFile, m.Env.APNsTopic)
	default:
		var ok bool
		ok, err = m.get(ctx, keyApple, &conf)
		if ok && err == nil {
			src = SourceConsole
			pusher, err = apple.NewPusherPEM([]byte(conf.CertPEM), []byte(conf.KeyPEM), conf.Topic)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.appleSrc, m.appleErr = src, ""
	if err != nil {
		m.appleErr = err.Error()
	}
	if pusher == nil {
		m.apple, m.appleMux = nil, nil
		m.Svc.Unregister(store.PlatformIOS, store.PlatformIPadOS, store.PlatformMacOS)
		return err
	}
	if time.Until(pusher.Expiry) < 30*24*time.Hour {
		m.Log.Warn("APNs push certificate expires soon; renew it at identity.apple.com with the same Apple ID", "expires", pusher.Expiry)
	}
	drv := &apple.Driver{Store: m.Store, Svc: m.Svc, CA: m.CA, Box: m.Box, Pusher: pusher, Org: m.Org,
		PublicURL: m.PublicURL, CertHeader: m.CertHeader, Log: m.Log}
	am := http.NewServeMux()
	drv.Routes(am)
	m.apple, m.appleMux = drv, am
	m.Svc.Register(drv)
	m.Log.Info("apple MDM enabled", "topic", pusher.Topic, "source", src)
	return nil
}

// AppleCSR generates and stores a new RSA key and returns a CSR for the MDM
// push certificate. The key never leaves the server.
func (m *Manager) AppleCSR(ctx context.Context) ([]byte, error) {
	if m.Env.APNsCertFile != "" {
		return nil, ErrEnvManaged
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: m.Org + " MDM push", Organization: []string{m.Org}},
	}, key)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := m.put(ctx, keyApplePending, map[string]string{"keyPem": string(keyPEM)}); err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr}), nil
}

// SetApple stores and activates a push certificate. keyPEM may be empty to
// use the key generated by AppleCSR. A DER .cer from Apple is accepted too.
func (m *Manager) SetApple(ctx context.Context, certData, keyPEM []byte, topic string) error {
	if m.Env.APNsCertFile != "" {
		return ErrEnvManaged
	}
	certPEM := certData
	if !strings.Contains(string(certData), "-----BEGIN") {
		if _, err := x509.ParseCertificate(certData); err != nil {
			return fmt.Errorf("%w: the certificate is neither PEM nor DER", ErrInvalid)
		}
		certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certData})
	}
	if len(keyPEM) == 0 {
		var pending struct {
			KeyPEM string `json:"keyPem"`
		}
		ok, err := m.get(ctx, keyApplePending, &pending)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: upload the private key, or generate a signing request first", ErrInvalid)
		}
		keyPEM = []byte(pending.KeyPEM)
	}
	if _, err := apple.NewPusherPEM(certPEM, keyPEM, topic); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := m.put(ctx, keyApple, appleConf{CertPEM: string(certPEM), KeyPEM: string(keyPEM), Topic: topic}); err != nil {
		return err
	}
	_ = m.Store.DeleteSetting(ctx, keyApplePending)
	return m.reloadApple(ctx)
}

// ClearApple removes the console-configured push certificate.
func (m *Manager) ClearApple(ctx context.Context) error {
	if m.Env.APNsCertFile != "" {
		return ErrEnvManaged
	}
	if err := m.Store.DeleteSetting(ctx, keyApple); err != nil {
		return err
	}
	return m.reloadApple(ctx)
}

// --- Google: service account, Android Enterprise, ChromeOS ---

func (m *Manager) googleCreds(ctx context.Context) (creds []byte, projectID, src string, err error) {
	if m.Env.GoogleCredentialsFile != "" {
		b, err := os.ReadFile(m.Env.GoogleCredentialsFile)
		return b, m.Env.GoogleProjectID, SourceEnv, err
	}
	var g googleConf
	ok, err := m.get(ctx, keyGoogle, &g)
	if err != nil || !ok {
		return nil, "", SourceNone, err
	}
	return []byte(g.Credentials), g.ProjectID, SourceConsole, nil
}

func (m *Manager) reloadGoogle(ctx context.Context) {
	creds, _, _, credErr := m.googleCreds(ctx)

	// Android
	ent, entSrc := m.Env.AndroidEnterprise, SourceEnv
	if ent == "" {
		var a androidConf
		if ok, err := m.get(ctx, keyAndroid, &a); err == nil && ok {
			ent, entSrc = a.Enterprise, SourceConsole
		} else {
			entSrc = SourceNone
		}
	}
	var drv *android.Driver
	var aErr error
	if ent != "" {
		if len(creds) == 0 {
			aErr = errors.New("an Android enterprise is set but the Google service account is missing")
			if credErr != nil {
				aErr = credErr
			}
		} else {
			drv, aErr = android.NewFromJSON(m.rootCtx(), m.Store, m.Svc, creds, ent, m.androidBase(), m.Log)
		}
	}

	// ChromeOS
	subject, customer, cSrc := m.Env.GoogleAdminSubject, m.Env.GoogleCustomerID, SourceEnv
	if subject == "" {
		var c chromeConf
		if ok, err := m.get(ctx, keyChromeOS, &c); err == nil && ok {
			subject, customer, cSrc = c.AdminSubject, c.CustomerID, SourceConsole
		} else {
			cSrc = SourceNone
		}
	}
	if customer == "" {
		customer = "my_customer"
	}
	var cdrv *chromeos.Driver
	var cErr error
	if subject != "" {
		if len(creds) == 0 {
			cErr = errors.New("a Workspace admin is set but the Google service account is missing")
		} else {
			cdrv, cErr = chromeos.NewFromJSON(m.rootCtx(), m.Store, creds, subject, customer, m.adminBase(), m.Log)
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.androidX != nil {
		m.androidX()
		m.androidX = nil
	}
	m.androidD, m.androidSr, m.androidEr = drv, entSrc, errString(aErr)
	if drv != nil {
		m.Svc.Register(drv)
		c, cancel := context.WithCancel(m.root)
		m.androidX = cancel
		go drv.Run(c, 5*time.Minute)
		m.Log.Info("android management enabled", "enterprise", ent, "source", entSrc)
	} else {
		m.Svc.Unregister(store.PlatformAndroid)
	}
	if m.chromeX != nil {
		m.chromeX()
		m.chromeX = nil
	}
	m.chromeD, m.chromeSrc, m.chromeErr = cdrv, cSrc, errString(cErr)
	if cdrv != nil {
		m.Svc.Register(cdrv)
		c, cancel := context.WithCancel(m.root)
		m.chromeX = cancel
		go cdrv.Run(c, 15*time.Minute)
		m.Log.Info("chromeos management enabled", "source", cSrc)
	} else {
		m.Svc.Unregister(store.PlatformChromeOS)
	}
}

func (m *Manager) rootCtx() context.Context {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.root == nil {
		return context.Background()
	}
	return m.root
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// SetGoogle stores the service account key. projectID defaults to the key's.
func (m *Manager) SetGoogle(ctx context.Context, credsJSON []byte, projectID string) (*google.ServiceAccountInfo, error) {
	if m.Env.GoogleCredentialsFile != "" {
		return nil, ErrEnvManaged
	}
	info, err := google.ParseServiceAccount(credsJSON)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if projectID == "" {
		projectID = info.ProjectID
	}
	if err := m.put(ctx, keyGoogle, googleConf{Credentials: string(credsJSON), ProjectID: projectID}); err != nil {
		return nil, err
	}
	m.reloadGoogle(ctx)
	return info, nil
}

// ClearGoogle removes the service account, which disables Android and ChromeOS.
func (m *Manager) ClearGoogle(ctx context.Context) error {
	if m.Env.GoogleCredentialsFile != "" {
		return ErrEnvManaged
	}
	if err := m.Store.DeleteSetting(ctx, keyGoogle); err != nil {
		return err
	}
	m.reloadGoogle(ctx)
	return nil
}

// StartAndroidSignup begins Android Enterprise signup and returns the Google
// URL to send the admin to. Google redirects back to the callback with the
// returned state, which is single use and expires after an hour.
func (m *Manager) StartAndroidSignup(ctx context.Context) (string, error) {
	if m.Env.AndroidEnterprise != "" {
		return "", ErrEnvManaged
	}
	creds, projectID, _, err := m.googleCreds(ctx)
	if err != nil {
		return "", err
	}
	if len(creds) == 0 {
		return "", fmt.Errorf("%w: add a Google service account first", ErrInvalid)
	}
	if projectID == "" {
		return "", fmt.Errorf("%w: the Google Cloud project ID is required", ErrInvalid)
	}
	state := secrets.Token(24)
	cb := m.PublicURL + "/api/v1/platforms/android/callback?state=" + url.QueryEscape(state)
	su, err := android.CreateSignupURL(ctx, creds, m.androidBase(), projectID, cb)
	if err != nil {
		return "", err
	}
	if err := m.put(ctx, keyAndroidSignup, signupConf{Name: su.Name, URL: su.URL,
		StateHash: secrets.Hash(state), Expires: time.Now().Add(time.Hour)}); err != nil {
		return "", err
	}
	return su.URL, nil
}

// ErrSignupState is returned for an unknown, used or expired signup state.
var ErrSignupState = errors.New("this Android Enterprise signup link is invalid or expired; start the signup again from Settings")

// CompleteAndroidSignup finishes signup with the token Google returned.
func (m *Manager) CompleteAndroidSignup(ctx context.Context, state, enterpriseToken string) (string, error) {
	var su signupConf
	ok, err := m.get(ctx, keyAndroidSignup, &su)
	if err != nil {
		return "", err
	}
	if !ok || state == "" || secrets.Hash(state) != su.StateHash || time.Now().After(su.Expires) {
		return "", ErrSignupState
	}
	// Single use, even if completion fails below.
	_ = m.Store.DeleteSetting(ctx, keyAndroidSignup)
	if enterpriseToken == "" {
		return "", fmt.Errorf("%w: Google did not return an enterprise token (signup was cancelled?)", ErrInvalid)
	}
	creds, projectID, _, err := m.googleCreds(ctx)
	if err != nil || len(creds) == 0 {
		return "", fmt.Errorf("Google service account unavailable: %v", err)
	}
	name, err := android.CreateEnterprise(ctx, creds, m.androidBase(), projectID, su.Name, enterpriseToken, m.Org)
	if err != nil {
		return "", err
	}
	if err := m.put(ctx, keyAndroid, androidConf{Enterprise: name}); err != nil {
		return "", err
	}
	m.reloadGoogle(ctx)
	return name, nil
}

// SetAndroidEnterprise binds an existing enterprise (enterprises/LC0...).
func (m *Manager) SetAndroidEnterprise(ctx context.Context, name string) error {
	if m.Env.AndroidEnterprise != "" {
		return ErrEnvManaged
	}
	name = strings.TrimSpace(name)
	if !strings.HasPrefix(name, "enterprises/") || len(name) < len("enterprises/")+3 {
		return fmt.Errorf("%w: the enterprise name looks like enterprises/LC01abcdef", ErrInvalid)
	}
	if err := m.put(ctx, keyAndroid, androidConf{Enterprise: name}); err != nil {
		return err
	}
	m.reloadGoogle(ctx)
	return nil
}

// ClearAndroid unbinds the enterprise. Devices stay enrolled with Google.
func (m *Manager) ClearAndroid(ctx context.Context) error {
	if m.Env.AndroidEnterprise != "" {
		return ErrEnvManaged
	}
	if err := m.Store.DeleteSetting(ctx, keyAndroid); err != nil {
		return err
	}
	m.reloadGoogle(ctx)
	return nil
}

// SetChromeOS sets the Workspace admin to impersonate for the Admin SDK.
func (m *Manager) SetChromeOS(ctx context.Context, adminSubject, customerID string) error {
	if m.Env.GoogleAdminSubject != "" {
		return ErrEnvManaged
	}
	if !strings.Contains(adminSubject, "@") {
		return fmt.Errorf("%w: the Workspace admin must be an email address", ErrInvalid)
	}
	if err := m.put(ctx, keyChromeOS, chromeConf{AdminSubject: strings.TrimSpace(adminSubject), CustomerID: strings.TrimSpace(customerID)}); err != nil {
		return err
	}
	m.reloadGoogle(ctx)
	return nil
}

// ClearChromeOS disables ChromeOS sync.
func (m *Manager) ClearChromeOS(ctx context.Context) error {
	if m.Env.GoogleAdminSubject != "" {
		return ErrEnvManaged
	}
	if err := m.Store.DeleteSetting(ctx, keyChromeOS); err != nil {
		return err
	}
	m.reloadGoogle(ctx)
	return nil
}

// Android returns the active Android driver, or nil.
func (m *Manager) Android() *android.Driver {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.androidD
}

// Status describes one platform for the console.
type Status struct {
	Configured bool           `json:"configured"`
	Source     string         `json:"source"`
	Error      string         `json:"error,omitempty"`
	Details    map[string]any `json:"details"`
}

// Statuses reports every platform. Secrets are never included.
func (m *Manager) Statuses(ctx context.Context) map[string]Status {
	out := map[string]Status{
		"windows": {Configured: true, Source: "built-in", Details: map[string]any{
			"discoveryUrl": m.PublicURL + "/EnrollmentServer/Discovery.svc"}},
		"linux": {Configured: true, Source: "built-in", Details: map[string]any{}},
	}
	creds, projectID, gsrc, gerr := m.googleCreds(ctx)
	g := Status{Source: gsrc, Details: map[string]any{"projectId": projectID}, Error: errString(gerr)}
	if len(creds) > 0 {
		if info, err := google.ParseServiceAccount(creds); err == nil {
			g.Configured = true
			g.Details["serviceAccount"] = info.ClientEmail
		} else {
			g.Error = err.Error()
		}
	}
	out["google"] = g

	var su signupConf
	pending, _ := m.get(ctx, keyAndroidSignup, &su)

	m.mu.Lock()
	defer m.mu.Unlock()
	a := Status{Source: m.appleSrc, Error: m.appleErr, Details: map[string]any{}}
	if m.apple != nil {
		a.Configured = true
		a.Details["topic"] = m.apple.Pusher.Topic
		a.Details["expires"] = m.apple.Pusher.Expiry
	}
	if _, _, err := m.Store.GetSetting(ctx, keyApplePending); err == nil {
		a.Details["csrPending"] = true
	}
	out["apple"] = a

	an := Status{Source: m.androidSr, Error: m.androidEr, Details: map[string]any{}}
	if m.androidD != nil {
		an.Configured = true
		an.Details["enterprise"] = m.androidD.Enterprise
	}
	if pending && time.Now().Before(su.Expires) {
		an.Details["signupPending"] = true
	}
	out["android"] = an

	c := Status{Source: m.chromeSrc, Error: m.chromeErr, Details: map[string]any{}}
	if m.chromeD != nil {
		c.Configured = true
		c.Details["customerId"] = m.chromeD.Customer
	}
	out["chromeos"] = c
	return out
}
