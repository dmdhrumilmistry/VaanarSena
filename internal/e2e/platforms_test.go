package e2e

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dmdhrumilmistry/VaanarSena/internal/auth"
)

// fakeGoogle stands in for Google's OAuth token endpoint and the Android
// Management API, recording what VaanarSena sent.
type fakeGoogle struct {
	srv         *httptest.Server
	key         *rsa.PrivateKey
	mu          sync.Mutex
	callbackURL string
	created     url.Values
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	f := &fakeGoogle{}
	f.key, _ = rsa.GenerateKey(rand.Reader, 2048)
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"access_token": "fake-token", "token_type": "Bearer", "expires_in": 3600})
	})
	authed := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer fake-token" {
				http.Error(w, `{"error":"unauthenticated"}`, http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("POST /v1/signupUrls", authed(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.callbackURL = r.URL.Query().Get("callbackUrl")
		f.mu.Unlock()
		reply(w, map[string]string{"name": "signupUrls/C123", "url": "https://enterprise.google.com/signup/fake"})
	}))
	mux.HandleFunc("POST /v1/enterprises", authed(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.created = r.URL.Query()
		f.mu.Unlock()
		if r.URL.Query().Get("enterpriseToken") != "ent-token-1" || r.URL.Query().Get("signupUrlName") != "signupUrls/C123" {
			http.Error(w, `{"error":"bad signup"}`, http.StatusBadRequest)
			return
		}
		reply(w, map[string]string{"name": "enterprises/LC0fake"})
	}))
	mux.HandleFunc("PATCH /v1/enterprises/LC0fake/policies/{p}", authed(func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]string{"name": "enterprises/LC0fake/policies/" + r.PathValue("p")})
	}))
	mux.HandleFunc("POST /v1/enterprises/LC0fake/enrollmentTokens", authed(func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]string{"name": "enterprises/LC0fake/enrollmentTokens/T1", "value": "TOKENVALUE", "qrCode": `{"x":"y"}`})
	}))
	mux.HandleFunc("GET /v1/enterprises/LC0fake/devices", authed(func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"devices": []any{}})
	}))
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// credentials returns a service account key whose token endpoint is the fake.
func (f *fakeGoogle) credentials() string {
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(f.key)})
	b, _ := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "vs-test-project", "private_key_id": "k1",
		"private_key": string(keyPEM), "client_email": "mdm@vs-test-project.iam.gserviceaccount.com",
		"client_id": "1", "token_uri": f.srv.URL + "/token",
	})
	return string(b)
}

func (e *env) get(path string) *http.Response {
	e.t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+path, nil)
	client := *e.srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp
}

type platformStatus struct {
	Platforms map[string]struct {
		Configured bool
		Source     string
		Error      string
		Details    map[string]any
	}
}

func TestAndroidEnterpriseFromConsole(t *testing.T) {
	e := setup(t)

	// Before setup: a clear, actionable error and nothing left behind.
	var nc struct{ Error, Setup string }
	e.call("POST", "/api/v1/enrollment-tokens", `{"platform":"android","ownership":"corporate"}`, 409, &nc)
	if !strings.Contains(nc.Error, "Settings > Platforms") || nc.Setup != "/settings/platforms" {
		t.Errorf("not-configured error is not actionable: %+v", nc)
	}

	var st platformStatus
	e.call("GET", "/api/v1/platforms", "", 200, &st)
	if st.Platforms["android"].Configured || st.Platforms["google"].Configured || !st.Platforms["windows"].Configured {
		t.Fatalf("initial status: %+v", st)
	}

	// A malformed key is refused.
	e.call("PUT", "/api/v1/platforms/google", `{"credentials":"{\"type\":\"authorized_user\"}"}`, 400, nil)

	body, _ := json.Marshal(map[string]string{"credentials": e.google.credentials()})
	e.call("PUT", "/api/v1/platforms/google", string(body), 200, &st)
	g := st.Platforms["google"]
	if !g.Configured || g.Details["serviceAccount"] != "mdm@vs-test-project.iam.gserviceaccount.com" || g.Details["projectId"] != "vs-test-project" {
		t.Fatalf("google status: %+v", g)
	}
	if strings.Contains(string(e.call("GET", "/api/v1/platforms", "", 200, nil)), "PRIVATE KEY") {
		t.Fatal("status leaks the service account key")
	}

	// Start signup: VaanarSena asks Google for a signup URL with a callback
	// carrying a single-use state.
	var su struct{ SignupURL string }
	e.call("POST", "/api/v1/platforms/android/signup", "", 200, &su)
	if su.SignupURL != "https://enterprise.google.com/signup/fake" {
		t.Fatalf("signup url = %q", su.SignupURL)
	}
	cb, err := url.Parse(e.google.callbackURL)
	if err != nil || !strings.HasPrefix(e.google.callbackURL, e.srv.URL+"/api/v1/platforms/android/callback") {
		t.Fatalf("callback url = %q", e.google.callbackURL)
	}
	state := cb.Query().Get("state")

	// A forged state does not complete (or cancel) the signup.
	if loc := e.get("/api/v1/platforms/android/callback?state=forged&enterpriseToken=ent-token-1").Header.Get("Location"); !strings.Contains(loc, "android=error") {
		t.Fatalf("forged state redirect = %q", loc)
	}

	// Google redirects back with the token; the callback needs no session.
	saved := e.token
	e.token = ""
	resp := e.get("/api/v1/platforms/android/callback?state=" + url.QueryEscape(state) + "&enterpriseToken=ent-token-1")
	e.token = saved
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/settings/platforms?android=connected" {
		t.Fatalf("callback: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if e.google.created.Get("projectId") != "vs-test-project" {
		t.Errorf("enterprise created with %v", e.google.created)
	}

	// Single use.
	if loc := e.get("/api/v1/platforms/android/callback?state=" + url.QueryEscape(state) + "&enterpriseToken=ent-token-1").Header.Get("Location"); !strings.Contains(loc, "android=error") {
		t.Fatalf("reused state accepted: %q", loc)
	}

	e.call("GET", "/api/v1/platforms", "", 200, &st)
	if a := st.Platforms["android"]; !a.Configured || a.Details["enterprise"] != "enterprises/LC0fake" || a.Source != "console" {
		t.Fatalf("android status: %+v", a)
	}

	// Enrollment works now, with no restart.
	var tok struct {
		Instructions map[string]any
	}
	e.call("POST", "/api/v1/enrollment-tokens", `{"platform":"android","ownership":"personal"}`, 201, &tok)
	if !strings.Contains(tok.Instructions["enrollUrl"].(string), "TOKENVALUE") {
		t.Errorf("android instructions: %+v", tok.Instructions)
	}

	// Disconnect.
	e.call("DELETE", "/api/v1/platforms/android", "", 200, &st)
	if st.Platforms["android"].Configured {
		t.Error("android still configured after disconnect")
	}
	e.call("POST", "/api/v1/enrollment-tokens", `{"platform":"android","ownership":"corporate"}`, 409, nil)
}

func TestAppleFromConsole(t *testing.T) {
	e := setup(t)
	if resp := e.get("/mdm/apple/enroll?token=x"); resp.StatusCode != 404 {
		t.Fatalf("apple endpoint before setup: %d", resp.StatusCode)
	}

	// Generate a signing request; the key stays on the server.
	var c struct{ CSR string }
	e.call("POST", "/api/v1/platforms/apple/csr", "", 200, &c)
	block, _ := pem.Decode([]byte(c.CSR))
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || csr.CheckSignature() != nil {
		t.Fatalf("csr: %v", err)
	}

	// Stand in for Apple: sign it with a test CA, with the push topic as UID.
	caKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Fake Apple"}, IsCA: true,
		BasicConstraintsValid: true, NotBefore: time.Now(), NotAfter: time.Now().AddDate(1, 0, 0), KeyUsage: x509.KeyUsageCertSign}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	caCert, _ := x509.ParseCertificate(caDER)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: time.Now(), NotAfter: time.Now().AddDate(1, 0, 0),
		Subject: pkix.Name{CommonName: "APSP:test", ExtraNames: []pkix.AttributeTypeAndValue{
			{Type: asn1.ObjectIdentifier{0, 9, 2342, 19200300, 100, 1, 1}, Value: "com.apple.mgmt.External.test-topic"}}},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, _ := x509.CreateCertificate(rand.Reader, leaf, caCert, csr.PublicKey, caKey)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	body, _ := json.Marshal(map[string]string{"certificate": string(certPEM)})
	var st platformStatus
	e.call("PUT", "/api/v1/platforms/apple", string(body), 200, &st)
	if a := st.Platforms["apple"]; !a.Configured || a.Details["topic"] != "com.apple.mgmt.External.test-topic" {
		t.Fatalf("apple status: %+v", a)
	}
	// The device endpoints switched on without a restart: now the driver
	// answers (and refuses the bogus token) instead of a 404.
	if resp := e.get("/mdm/apple/enroll?token=x"); resp.StatusCode != http.StatusForbidden {
		t.Errorf("apple endpoint after setup: %d", resp.StatusCode)
	}
	e.call("POST", "/api/v1/enrollment-tokens", `{"platform":"apple","ownership":"corporate"}`, 201, nil)
}

func TestPlatformsAdminOnly(t *testing.T) {
	e := setup(t)
	hash, _ := authHash("operator-password-1")
	if _, err := e.st.CreateUser(t.Context(), "op2@e2e.test", "", hash, "operator"); err != nil {
		t.Fatal(err)
	}
	e.token = ""
	var login struct{ Token string }
	e.call("POST", "/api/v1/auth/login", `{"email":"op2@e2e.test","password":"operator-password-1"}`, 200, &login)
	e.token = login.Token
	e.call("GET", "/api/v1/platforms", "", 403, nil)
	e.call("PUT", "/api/v1/platforms/google", `{"credentials":"{}"}`, 403, nil)
	e.call("POST", "/api/v1/platforms/android/signup", "", 403, nil)
}

func authHash(pw string) (string, error) { return auth.HashPassword(pw) }
