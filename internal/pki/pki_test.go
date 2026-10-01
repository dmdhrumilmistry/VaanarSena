package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dmdhrumilmistry/VaanarSena/internal/secrets"
)

func newCA(t *testing.T) *CA {
	t.Helper()
	box, err := secrets.NewBox("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	enc, err := generate(box, "Test")
	if err != nil {
		t.Fatal(err)
	}
	ca, err := decode(box, enc)
	if err != nil {
		t.Fatal(err)
	}
	return ca
}

func TestCARoundTripAndWrongKey(t *testing.T) {
	box, _ := secrets.NewBox("0123456789abcdef0123456789abcdef")
	enc, _ := generate(box, "Test")
	other, _ := secrets.NewBox("ffffffffffffffffffffffffffffffff")
	if _, err := decode(other, enc); err == nil {
		t.Fatal("CA decrypted with the wrong key")
	}
}

func TestSignCSRSetsSubjectServerSide(t *testing.T) {
	ca := newCA(t)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	csr, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "attacker-chosen"}}, key)
	cert, err := ca.SignCSR(csr, "vaanarsena-linux-abc")
	if err != nil {
		t.Fatal(err)
	}
	if cert.Subject.CommonName != "vaanarsena-linux-abc" {
		t.Errorf("CN = %q", cert.Subject.CommonName)
	}
	if err := ca.Verify(cert); err != nil {
		t.Errorf("issued cert does not verify: %v", err)
	}
}

func TestVerifyRejectsForeignCert(t *testing.T) {
	ca, other := newCA(t), newCA(t)
	cert, _, err := other.NewIdentity("x")
	if err != nil {
		t.Fatal(err)
	}
	if err := ca.Verify(cert); err == nil {
		t.Fatal("certificate from another CA verified")
	}
}

// Helm's genCA emits an RSA CA with a PKCS#1 key; the chart relies on this.
func TestLoadFilesPKCS1(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "helm-ca"}, NotBefore: time.Now(),
		NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
	_ = os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	_ = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600)
	ca, err := LoadFiles(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	cert, _, err := ca.NewIdentity("dev")
	if err != nil {
		t.Fatal(err)
	}
	if err := ca.Verify(cert); err != nil {
		t.Fatal(err)
	}
}

func TestClientCertHeader(t *testing.T) {
	ca := newCA(t)
	cert, _, _ := ca.NewIdentity("dev")
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("Ssl-Client-Cert", url.QueryEscape(pemStr))
	if _, err := ca.ClientCert(r, ""); err == nil {
		t.Error("header honoured while header auth is disabled")
	}
	got, err := ca.ClientCert(r, "Ssl-Client-Cert")
	if err != nil || SerialHex(got) != SerialHex(cert) {
		t.Errorf("header cert: %v", err)
	}
	// Caddy forwards base64 DER instead.
	r.Header.Set("X-Client-Cert", base64.StdEncoding.EncodeToString(cert.Raw))
	if got, err := ca.ClientCert(r, "X-Client-Cert"); err != nil || SerialHex(got) != SerialHex(cert) {
		t.Errorf("base64 DER header: %v", err)
	}
}
