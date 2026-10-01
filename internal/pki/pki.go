// Package pki runs the VaanarSena certificate authority. The CA is generated on
// first boot and stored, encrypted, in the settings table so every replica
// shares it. It issues the identity certificates devices use to authenticate.
package pki

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/dmdhrumilmistry/VaanarSena/internal/secrets"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

const settingKey = "pki.ca"

// DeviceCertValidity is the lifetime of issued device identity certificates.
const DeviceCertValidity = 2 * 365 * 24 * time.Hour

// CA is the server certificate authority.
type CA struct {
	Cert *x509.Certificate
	Key  crypto.Signer
	// CertPEM is the PEM encoding of Cert, handy for trust profiles.
	CertPEM []byte
	pool    *x509.CertPool
}

type storedCA struct {
	CertDER []byte `json:"cert"`
	KeyDER  []byte `json:"key"`
}

// LoadOrCreate returns the CA from the database, generating it on first boot.
// Concurrent first boots converge on whichever replica inserted first.
func LoadOrCreate(ctx context.Context, st *store.Store, box *secrets.Box, org string) (*CA, error) {
	for attempt := 0; attempt < 2; attempt++ {
		raw, _, err := st.GetSetting(ctx, settingKey)
		if err == nil {
			return decode(box, raw)
		}
		if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		enc, err := generate(box, org)
		if err != nil {
			return nil, err
		}
		if _, err := st.InsertSettingIfAbsent(ctx, settingKey, enc, true); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("pki: could not load CA")
}

// LoadFiles reads an operator-supplied CA certificate and key (PEM; PKCS#1,
// PKCS#8 or SEC 1 keys). Used when the CA must also be known to a proxy, for
// example so an ingress can request client certificates issued by it.
func LoadFiles(certFile, keyFile string) (*CA, error) {
	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, err
	}
	cb, _ := pem.Decode(certPEM)
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || kb == nil {
		return nil, errors.New("pki: CA certificate or key is not PEM")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, err
	}
	if !cert.IsCA {
		return nil, errors.New("pki: VS_CA_CERT_FILE is not a CA certificate")
	}
	var key any
	switch kb.Type {
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(kb.Bytes)
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(kb.Bytes)
	default:
		key, err = x509.ParsePKCS8PrivateKey(kb.Bytes)
	}
	if err != nil {
		return nil, fmt.Errorf("pki: parse CA key: %w", err)
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, errors.New("pki: CA key is not a signer")
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &CA{Cert: cert, Key: signer, CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), pool: pool}, nil
}

// NewInMemory creates a CA that is not persisted, for tests and tooling.
func NewInMemory(box *secrets.Box, org string) (*CA, error) {
	enc, err := generate(box, org)
	if err != nil {
		return nil, err
	}
	return decode(box, enc)
}

func generate(box *secrets.Box, org string) ([]byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	// Apple and Windows MDM clients accept P-256 CAs; RSA is only needed for
	// device keys on some Windows builds, and those are client-generated.
	tmpl := &x509.Certificate{
		SerialNumber:          randSerial(),
		Subject:               pkix.Name{CommonName: org + " MDM CA", Organization: []string{org}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(20, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	plain, err := json.Marshal(storedCA{CertDER: der, KeyDER: keyDER})
	if err != nil {
		return nil, err
	}
	return box.Seal(plain)
}

func decode(box *secrets.Box, enc []byte) (*CA, error) {
	plain, err := box.Open(enc)
	if err != nil {
		return nil, fmt.Errorf("pki: decrypt CA (was VS_SECRET_KEY changed?): %w", err)
	}
	var s storedCA
	if err := json.Unmarshal(plain, &s); err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(s.CertDER)
	if err != nil {
		return nil, err
	}
	k, err := x509.ParsePKCS8PrivateKey(s.KeyDER)
	if err != nil {
		return nil, err
	}
	signer, ok := k.(crypto.Signer)
	if !ok {
		return nil, errors.New("pki: CA key is not a signer")
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &CA{
		Cert:    cert,
		Key:     signer,
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}),
		pool:    pool,
	}, nil
}

// Pool returns a cert pool containing only the CA.
func (ca *CA) Pool() *x509.CertPool { return ca.pool }

// IssueClient signs a client-authentication certificate for pub.
func (ca *CA) IssueClient(pub crypto.PublicKey, commonName string, validity time.Duration) (*x509.Certificate, error) {
	tmpl := &x509.Certificate{
		SerialNumber: randSerial(),
		Subject:      pkix.Name{CommonName: commonName, Organization: ca.Cert.Subject.Organization},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(validity),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, pub, ca.Key)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}

// SignCSR verifies a PKCS#10 request and issues a client certificate for it.
// The subject is set by the server, not taken from the request.
func (ca *CA) SignCSR(csrDER []byte, commonName string) (*x509.Certificate, error) {
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		return nil, fmt.Errorf("parse CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("CSR signature: %w", err)
	}
	return ca.IssueClient(csr.PublicKey, commonName, DeviceCertValidity)
}

// NewIdentity generates an RSA key and certificate. Apple devices require RSA
// for identities delivered inside a PKCS#12 payload.
func (ca *CA) NewIdentity(commonName string) (*x509.Certificate, *rsa.PrivateKey, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	cert, err := ca.IssueClient(&key.PublicKey, commonName, DeviceCertValidity)
	return cert, key, err
}

// Verify checks that cert was issued by this CA and is currently valid for
// client authentication.
func (ca *CA) Verify(cert *x509.Certificate) error {
	_, err := cert.Verify(x509.VerifyOptions{
		Roots:     ca.pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	return err
}

// SerialHex returns the canonical serial representation stored on devices.
func SerialHex(cert *x509.Certificate) string { return hex.EncodeToString(cert.SerialNumber.Bytes()) }

// Thumbprint returns the uppercase hex SHA-1 fingerprint. Windows certificate
// stores key certificates by SHA-1 thumbprint; it is an identifier here, not a
// security control.
func Thumbprint(cert *x509.Certificate) string {
	sum := sha1.Sum(cert.Raw) //nolint:gosec
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// Fingerprint returns the hex SHA-256 fingerprint of cert.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// ClientCert extracts a verified client certificate from the request: either
// from the TLS connection or, behind a TLS-terminating proxy, from header (a
// URL-encoded PEM, as nginx's $ssl_client_escaped_cert produces). The header
// must only be trusted when the proxy strips it from client requests.
func (ca *CA) ClientCert(r *http.Request, header string) (*x509.Certificate, error) {
	var cert *x509.Certificate
	switch {
	case r.TLS != nil && len(r.TLS.PeerCertificates) > 0:
		cert = r.TLS.PeerCertificates[0]
	case header != "" && r.Header.Get(header) != "":
		raw, err := url.QueryUnescape(r.Header.Get(header))
		if err != nil {
			return nil, err
		}
		// URL-encoded PEM (nginx $ssl_client_escaped_cert) or base64 DER
		// (Caddy {http.request.tls.client.certificate_der_base64}).
		der := []byte(nil)
		if block, _ := pem.Decode([]byte(raw)); block != nil {
			der = block.Bytes
		} else if b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(r.Header.Get(header))); err == nil {
			der = b
		} else {
			return nil, errors.New("client certificate header is neither PEM nor base64 DER")
		}
		cert, err = x509.ParseCertificate(der)
		if err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("no client certificate presented")
	}
	if err := ca.Verify(cert); err != nil {
		return nil, fmt.Errorf("client certificate not trusted: %w", err)
	}
	return cert, nil
}

func randSerial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		panic(err)
	}
	return n
}
