package config

import (
	"strings"
	"testing"
)

func TestPasswordLoginOffNeedsAWayIn(t *testing.T) {
	base := Config{SecretKey: strings.Repeat("k", 32), PublicURL: "https://mdm.example.com", PasswordLogin: true}

	locked := base
	if err := locked.validate(); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	locked.PasswordLogin = false
	if err := locked.validate(); err == nil || !strings.Contains(err.Error(), "nobody can sign in") {
		t.Fatalf("password sign-in off with no SSO and no break-glass account accepted: %v", err)
	}

	sso := locked
	sso.OIDCIssuer = "https://accounts.google.com"
	if err := sso.validate(); err != nil {
		t.Errorf("with SSO: %v", err)
	}
	breakGlass := locked
	breakGlass.PasswordLoginAllow = []string{"admin@example.com"}
	if err := breakGlass.validate(); err != nil {
		t.Errorf("with a break-glass account: %v", err)
	}
}
