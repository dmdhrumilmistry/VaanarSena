package apple

import (
	"crypto/rand"
	"crypto/x509"
	"fmt"
	"net/url"

	"github.com/smallstep/pkcs7"
	"howett.net/plist"
	pkcs12 "software.sslmate.com/src/go-pkcs12"

	"github.com/dmdhrumilmistry/VaanarSena/internal/pki"
	"github.com/dmdhrumilmistry/VaanarSena/internal/policy"
	"github.com/dmdhrumilmistry/VaanarSena/internal/secrets"
)

// Profile identifiers. RemoveProfile with MDMProfileID un-enrolls the device,
// which removes every managed profile, app and account with it.
const (
	MDMProfileID    = "io.vaanarsena.mdm"
	PolicyProfileID = "io.vaanarsena.policy"
)

// AccessRights 8191 grants every MDM right (bits 1-4096). User Enrollment
// ignores rights it cannot grant.
const accessRightsAll = 8191

func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%X-%X-%X-%X-%X", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// EnrollmentParams describes one enrollment profile.
type EnrollmentParams struct {
	Org       string
	PublicURL string
	Topic     string
	CA        *pki.CA
	// Personal produces a User Enrollment (BYOD) profile.
	Personal bool
	// ManagedAppleID is required for User Enrollment.
	ManagedAppleID string
}

// EnrollmentProfile builds the .mobileconfig and returns it with the serial of
// the identity certificate it carries.
func EnrollmentProfile(p EnrollmentParams) ([]byte, string, error) {
	cert, key, err := p.CA.NewIdentity("vaanarsena-apple-" + newUUID())
	if err != nil {
		return nil, "", err
	}
	// The PKCS#12 password travels in the same profile, so its encryption is
	// not a security boundary; legacy 3DES is used because every supported
	// iOS and macOS release can read it.
	pass := secrets.Token(18)
	p12, err := pkcs12.LegacyDES.Encode(key, cert, []*x509.Certificate{p.CA.Cert}, pass)
	if err != nil {
		return nil, "", err
	}

	identityUUID := newUUID()
	caPayload := map[string]any{
		"PayloadType":        "com.apple.security.root",
		"PayloadIdentifier":  MDMProfileID + ".ca",
		"PayloadUUID":        newUUID(),
		"PayloadVersion":     1,
		"PayloadDisplayName": p.Org + " MDM CA",
		"PayloadContent":     p.CA.Cert.Raw,
	}
	idPayload := map[string]any{
		"PayloadType":        "com.apple.security.pkcs12",
		"PayloadIdentifier":  MDMProfileID + ".identity",
		"PayloadUUID":        identityUUID,
		"PayloadVersion":     1,
		"PayloadDisplayName": "Device identity",
		"PayloadContent":     p12,
		"Password":           pass,
	}
	mdm := map[string]any{
		"PayloadType":             "com.apple.mdm",
		"PayloadIdentifier":       MDMProfileID + ".mdm",
		"PayloadUUID":             newUUID(),
		"PayloadVersion":          1,
		"PayloadDisplayName":      p.Org + " device management",
		"ServerURL":               p.PublicURL + "/mdm/apple/server",
		"CheckInURL":              p.PublicURL + "/mdm/apple/checkin",
		"Topic":                   p.Topic,
		"IdentityCertificateUUID": identityUUID,
		"AccessRights":            accessRightsAll,
		"SignMessage":             true,
		"CheckOutWhenRemoved":     true,
		"ServerCapabilities":      []string{"com.apple.mdm.per-user-connections"},
	}
	if p.Personal {
		mdm["EnrollmentMode"] = "BYOD"
		mdm["AssignedManagedAppleID"] = p.ManagedAppleID
	}

	desc := "Installs " + p.Org + " device management. Your organisation will be able to manage this device."
	if p.Personal {
		desc = "Enrolls this personal device with " + p.Org + ". Your organisation can manage work apps and data only; it cannot see or erase your personal data."
	}
	profile := map[string]any{
		"PayloadType":              "Configuration",
		"PayloadIdentifier":        MDMProfileID,
		"PayloadUUID":              newUUID(),
		"PayloadVersion":           1,
		"PayloadDisplayName":       p.Org + " MDM",
		"PayloadDescription":       desc,
		"PayloadOrganization":      p.Org,
		"PayloadRemovalDisallowed": false,
		"PayloadContent":           []any{caPayload, idPayload, mdm},
	}
	b, err := plist.Marshal(profile, plist.XMLFormat)
	if err != nil {
		return nil, "", err
	}
	return sign(b, p.CA), pki.SerialHex(cert), nil
}

// PolicyProfile wraps translated policy payloads in a configuration profile.
func PolicyProfile(org string, doc *policy.Document, personal bool, ca *pki.CA) ([]byte, error) {
	payloads := []any{}
	for i, pl := range doc.ApplePayloads(personal) {
		pl["PayloadIdentifier"] = fmt.Sprintf("%s.%d", PolicyProfileID, i)
		pl["PayloadUUID"] = newUUID()
		pl["PayloadVersion"] = 1
		if _, ok := pl["PayloadDisplayName"]; !ok {
			pl["PayloadDisplayName"] = pl["PayloadType"]
		}
		payloads = append(payloads, pl)
	}
	profile := map[string]any{
		"PayloadType":              "Configuration",
		"PayloadIdentifier":        PolicyProfileID,
		"PayloadUUID":              newUUID(),
		"PayloadVersion":           1,
		"PayloadDisplayName":       org + " policy",
		"PayloadOrganization":      org,
		"PayloadRemovalDisallowed": true,
		"PayloadContent":           payloads,
	}
	b, err := plist.Marshal(profile, plist.XMLFormat)
	if err != nil {
		return nil, err
	}
	return sign(b, ca), nil
}

// sign wraps a profile in a CMS SignedData so the device can verify it came
// from the server. If signing fails the unsigned profile is still valid; the
// device then shows it as "Unverified".
func sign(profile []byte, ca *pki.CA) []byte {
	sd, err := pkcs7.NewSignedData(profile)
	if err != nil {
		return profile
	}
	if err := sd.AddSigner(ca.Cert, ca.Key, pkcs7.SignerInfoConfig{}); err != nil {
		return profile
	}
	out, err := sd.Finish()
	if err != nil {
		return profile
	}
	return out
}

// EnrollURL returns the profile download URL for an enrollment token.
func EnrollURL(publicURL, token string) string {
	return publicURL + "/mdm/apple/enroll?token=" + url.QueryEscape(token)
}
