package apple

import (
	"bytes"
	"encoding/base64"
	"net/http/httptest"
	"testing"

	"github.com/smallstep/pkcs7"
	"howett.net/plist"

	"github.com/dmdhrumilmistry/VaanarSena/internal/pki"
	"github.com/dmdhrumilmistry/VaanarSena/internal/policy"
	"github.com/dmdhrumilmistry/VaanarSena/internal/secrets"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

func testCA(t *testing.T) *pki.CA {
	t.Helper()
	ca, err := pki.NewInMemory(mustBox(), "Test")
	if err != nil {
		t.Fatal(err)
	}
	return ca
}

func unwrap(t *testing.T, signed []byte) map[string]any {
	t.Helper()
	p7, err := pkcs7.Parse(signed)
	if err != nil {
		t.Fatalf("profile is not CMS signed: %v", err)
	}
	var out map[string]any
	if _, err := plist.Unmarshal(p7.Content, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestEnrollmentProfile(t *testing.T) {
	ca := testCA(t)
	b, serial, err := EnrollmentProfile(EnrollmentParams{Org: "Acme", PublicURL: "https://mdm.example.com", Topic: "com.apple.mgmt.External.x", CA: ca})
	if err != nil {
		t.Fatal(err)
	}
	if serial == "" {
		t.Fatal("no identity serial")
	}
	prof := unwrap(t, b)
	var mdm map[string]any
	for _, p := range prof["PayloadContent"].([]any) {
		if pl := p.(map[string]any); pl["PayloadType"] == "com.apple.mdm" {
			mdm = pl
		}
	}
	if mdm == nil {
		t.Fatal("no MDM payload")
	}
	if mdm["ServerURL"] != "https://mdm.example.com/mdm/apple/server" || mdm["SignMessage"] != true {
		t.Errorf("mdm payload: %v", mdm)
	}
	if _, ok := mdm["EnrollmentMode"]; ok {
		t.Error("corporate profile must not be a User Enrollment")
	}

	b, _, err = EnrollmentProfile(EnrollmentParams{Org: "Acme", PublicURL: "https://x", Topic: "t", CA: ca, Personal: true, ManagedAppleID: "a@b.c"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range unwrap(t, b)["PayloadContent"].([]any) {
		if pl := p.(map[string]any); pl["PayloadType"] == "com.apple.mdm" {
			if pl["EnrollmentMode"] != "BYOD" || pl["AssignedManagedAppleID"] != "a@b.c" {
				t.Errorf("BYOD payload: %v", pl)
			}
		}
	}
}

func TestMdmSignatureVerification(t *testing.T) {
	ca := testCA(t)
	cert, key, _ := ca.NewIdentity("device")
	body := []byte("<plist>checkin</plist>")
	sd, _ := pkcs7.NewSignedData(body)
	if err := sd.AddSigner(cert, key, pkcs7.SignerInfoConfig{}); err != nil {
		t.Fatal(err)
	}
	sd.Detach()
	sig, _ := sd.Finish()

	d := &Driver{CA: ca}
	r := httptest.NewRequest("PUT", "/mdm/apple/checkin", bytes.NewReader(body))
	r.Header.Set("Mdm-Signature", base64.StdEncoding.EncodeToString(sig))
	got, err := d.identity(r, body)
	if err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if pki.SerialHex(got) != pki.SerialHex(cert) {
		t.Error("wrong signer")
	}
	if _, err := d.identity(r, []byte("tampered")); err == nil {
		t.Error("tampered body accepted")
	}

	other, _ := pki.NewInMemory(mustBox(), "Other")
	foreign, fkey, _ := other.NewIdentity("device")
	sd2, _ := pkcs7.NewSignedData(body)
	_ = sd2.AddSigner(foreign, fkey, pkcs7.SignerInfoConfig{})
	sd2.Detach()
	sig2, _ := sd2.Finish()
	r.Header.Set("Mdm-Signature", base64.StdEncoding.EncodeToString(sig2))
	if _, err := d.identity(r, body); err == nil {
		t.Error("signature from a foreign CA accepted")
	}
}

func mustBox() *secrets.Box {
	b, _ := secrets.NewBox("0123456789abcdef0123456789abcdef")
	return b
}

func TestPolicyProfileAndCommands(t *testing.T) {
	ca := testCA(t)
	doc := &policy.Document{Passcode: &policy.Passcode{Required: true, MinLength: 6}}
	b, err := PolicyProfile("Acme", doc, false, ca)
	if err != nil {
		t.Fatal(err)
	}
	prof := unwrap(t, b)
	if prof["PayloadIdentifier"] != PolicyProfileID || len(prof["PayloadContent"].([]any)) != 1 {
		t.Errorf("policy profile: %v", prof)
	}
}

func TestPlatformDetection(t *testing.T) {
	cases := map[string]string{"iPhone15,2": store.PlatformIOS, "iPad13,1": store.PlatformIPadOS, "MacBookPro18,1": store.PlatformMacOS, "Mac14,2": store.PlatformMacOS}
	for in, want := range cases {
		if got := applePlatform(&checkinMsg{ProductName: in}); got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
}
