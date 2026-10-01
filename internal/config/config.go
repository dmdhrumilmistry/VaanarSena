// Package config loads server configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the full server configuration. Every field maps to a VS_* variable.
type Config struct {
	// ListenAddr is the HTTP(S) listen address, e.g. ":8080".
	ListenAddr string
	// PublicURL is the externally reachable base URL, e.g. https://mdm.example.com.
	// Devices are told to talk to this URL, so it must be reachable from them.
	PublicURL string
	// DatabaseURL is a PostgreSQL connection string.
	DatabaseURL string
	// SecretKey encrypts secrets at rest and signs sessions. At least 32 bytes.
	SecretKey string
	// TLSCertFile and TLSKeyFile enable in-process TLS. Leave empty behind an ingress.
	TLSCertFile string
	TLSKeyFile  string
	// ClientCertHeader is the header a TLS-terminating proxy uses to forward the
	// URL-encoded PEM client certificate (nginx: ssl-client-cert). Empty disables it.
	ClientCertHeader string
	// CACertFile and CAKeyFile supply the device CA instead of the generated,
	// database-stored one.
	CACertFile string
	CAKeyFile  string
	// OrgName appears in enrollment profiles and certificates.
	OrgName string
	// SessionTTL is the console session lifetime.
	SessionTTL time.Duration
	// LogLevel is debug, info, warn or error.
	LogLevel string

	// Apple MDM push certificate (from identity.apple.com), PEM encoded.
	APNsCertFile string
	APNsKeyFile  string
	// APNsTopic is the push topic (the certificate's UID, com.apple.mgmt.External...).
	// Detected from the certificate when empty.
	APNsTopic string

	// GoogleCredentialsFile is a service account JSON used for the Android
	// Management API and the Admin SDK.
	GoogleCredentialsFile string
	// GoogleProjectID is the Cloud project that owns the Android enterprise.
	GoogleProjectID string
	// AndroidEnterprise is the AMAPI enterprise name, e.g. enterprises/LC0123abcd.
	AndroidEnterprise string
	// GoogleAdminSubject is the Workspace admin the service account impersonates
	// for the Admin SDK (domain-wide delegation).
	GoogleAdminSubject string
	// GoogleCustomerID is the Workspace customer ID, "my_customer" by default.
	GoogleCustomerID string
}

// Load reads configuration from the environment and validates it.
func Load() (*Config, error) {
	c := &Config{
		ListenAddr:            env("VS_LISTEN_ADDR", ":8080"),
		PublicURL:             strings.TrimRight(env("VS_PUBLIC_URL", "http://localhost:8080"), "/"),
		DatabaseURL:           env("VS_DATABASE_URL", "postgres://vaanarsena:vaanarsena@localhost:5432/vaanarsena?sslmode=disable"),
		SecretKey:             os.Getenv("VS_SECRET_KEY"),
		TLSCertFile:           os.Getenv("VS_TLS_CERT_FILE"),
		TLSKeyFile:            os.Getenv("VS_TLS_KEY_FILE"),
		ClientCertHeader:      os.Getenv("VS_CLIENT_CERT_HEADER"),
		CACertFile:            os.Getenv("VS_CA_CERT_FILE"),
		CAKeyFile:             os.Getenv("VS_CA_KEY_FILE"),
		OrgName:               env("VS_ORG_NAME", "VaanarSena"),
		LogLevel:              env("VS_LOG_LEVEL", "info"),
		APNsCertFile:          os.Getenv("VS_APNS_CERT_FILE"),
		APNsKeyFile:           os.Getenv("VS_APNS_KEY_FILE"),
		APNsTopic:             os.Getenv("VS_APNS_TOPIC"),
		GoogleCredentialsFile: os.Getenv("VS_GOOGLE_CREDENTIALS_FILE"),
		GoogleProjectID:       os.Getenv("VS_GOOGLE_PROJECT_ID"),
		AndroidEnterprise:     os.Getenv("VS_ANDROID_ENTERPRISE"),
		GoogleAdminSubject:    os.Getenv("VS_GOOGLE_ADMIN_SUBJECT"),
		GoogleCustomerID:      env("VS_GOOGLE_CUSTOMER_ID", "my_customer"),
	}
	ttl, err := time.ParseDuration(env("VS_SESSION_TTL", "12h"))
	if err != nil {
		return nil, fmt.Errorf("VS_SESSION_TTL: %w", err)
	}
	c.SessionTTL = ttl
	return c, c.validate()
}

func (c *Config) validate() error {
	var errs []error
	if len(c.SecretKey) < 32 {
		errs = append(errs, errors.New("VS_SECRET_KEY must be set and at least 32 characters (try: openssl rand -hex 32)"))
	}
	if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
		errs = append(errs, errors.New("VS_TLS_CERT_FILE and VS_TLS_KEY_FILE must be set together"))
	}
	if (c.CACertFile == "") != (c.CAKeyFile == "") {
		errs = append(errs, errors.New("VS_CA_CERT_FILE and VS_CA_KEY_FILE must be set together"))
	}
	if (c.APNsCertFile == "") != (c.APNsKeyFile == "") {
		errs = append(errs, errors.New("VS_APNS_CERT_FILE and VS_APNS_KEY_FILE must be set together"))
	}
	if !strings.HasPrefix(c.PublicURL, "https://") && !strings.HasPrefix(c.PublicURL, "http://") {
		errs = append(errs, errors.New("VS_PUBLIC_URL must start with https:// or http://"))
	}
	return errors.Join(errs...)
}

// AppleEnabled reports whether an APNs certificate is configured.
func (c *Config) AppleEnabled() bool { return c.APNsCertFile != "" }

// AndroidEnabled reports whether the Android Management API is configured.
func (c *Config) AndroidEnabled() bool {
	return c.GoogleCredentialsFile != "" && c.AndroidEnterprise != ""
}

// ChromeOSEnabled reports whether the Admin SDK is configured.
func (c *Config) ChromeOSEnabled() bool {
	return c.GoogleCredentialsFile != "" && c.GoogleAdminSubject != ""
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// EnvBool parses a boolean environment variable.
func EnvBool(key string, def bool) bool {
	v, err := strconv.ParseBool(os.Getenv(key))
	if err != nil {
		return def
	}
	return v
}
