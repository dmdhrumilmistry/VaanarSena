// Package apple implements the Apple MDM protocol for iOS, iPadOS and macOS:
// enrollment profiles, the check-in protocol, the command protocol and APNs
// wake-ups. Personal devices use User Enrollment (EnrollmentMode BYOD).
package apple

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/smallstep/pkcs7"
	"howett.net/plist"

	"github.com/dmdhrumilmistry/VaanarSena/internal/httpx"
	"github.com/dmdhrumilmistry/VaanarSena/internal/mdm"
	"github.com/dmdhrumilmistry/VaanarSena/internal/pki"
	"github.com/dmdhrumilmistry/VaanarSena/internal/secrets"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

const maxBody = 8 << 20

// Driver is the Apple MDM driver.
type Driver struct {
	Store      *store.Store
	Svc        *mdm.Service
	CA         *pki.CA
	Box        *secrets.Box
	Pusher     *Pusher
	Org        string
	PublicURL  string
	CertHeader string
	Log        *slog.Logger
}

// Platforms implements mdm.Driver.
func (d *Driver) Platforms() []string {
	return []string{store.PlatformIOS, store.PlatformIPadOS, store.PlatformMacOS}
}

// Wake sends an APNs push so the device connects to the command endpoint.
func (d *Driver) Wake(ctx context.Context, dev *store.Device) error {
	ids := platformIDs(dev)
	if ids.Token == "" || ids.PushMagic == "" {
		return errors.New("device has not sent a push token yet")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return d.Pusher.Push(ctx, ids.Token, ids.PushMagic)
}

// Routes registers the device-facing endpoints.
func (d *Driver) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /mdm/apple/enroll", d.handleEnroll)
	mux.HandleFunc("PUT /mdm/apple/checkin", d.handleCheckin)
	mux.HandleFunc("PUT /mdm/apple/server", d.handleServer)
}

// handleEnroll serves the enrollment profile for a valid token. The token is
// consumed here, and the issued identity is bound to it until check-in.
func (d *Driver) handleEnroll(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("token")
	if raw == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}
	tok, err := d.Store.ConsumeEnrollmentToken(r.Context(), secrets.Hash(raw), "apple")
	if err != nil {
		http.Error(w, "enrollment token is invalid, expired or already used", http.StatusForbidden)
		return
	}
	personal := tok.Ownership == store.OwnershipPersonal
	if personal && !strings.Contains(tok.Assignee, "@") {
		http.Error(w, "User Enrollment requires the token's assignee to be a Managed Apple ID", http.StatusBadRequest)
		return
	}
	profile, serial, err := EnrollmentProfile(EnrollmentParams{
		Org: d.Org, PublicURL: d.PublicURL, Topic: d.Pusher.Topic, CA: d.CA,
		Personal: personal, ManagedAppleID: tok.Assignee,
	})
	if err != nil {
		d.Log.Error("build enrollment profile", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := d.Store.AddPendingIdentity(r.Context(), serial, tok.ID); err != nil {
		d.Log.Error("store pending identity", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	_ = d.Store.Audit(r.Context(), "device", "apple.profile_downloaded", tok.ID, map[string]any{"ownership": tok.Ownership}, httpx.ClientIP(r))
	w.Header().Set("Content-Type", "application/x-apple-aspen-config")
	w.Header().Set("Content-Disposition", `attachment; filename="vaanarsena-enroll.mobileconfig"`)
	_, _ = w.Write(profile)
}

// identity authenticates a device request and returns its certificate. Apple
// devices sign every request body with their identity (SignMessage=true); the
// Mdm-Signature header carries a detached CMS signature. A TLS client
// certificate is accepted as an alternative.
func (d *Driver) identity(r *http.Request, body []byte) (*x509.Certificate, error) {
	if sig := r.Header.Get("Mdm-Signature"); sig != "" {
		der, err := base64.StdEncoding.DecodeString(sig)
		if err != nil {
			return nil, fmt.Errorf("decode Mdm-Signature: %w", err)
		}
		p7, err := pkcs7.Parse(der)
		if err != nil {
			return nil, fmt.Errorf("parse Mdm-Signature: %w", err)
		}
		p7.Content = body
		if err := p7.Verify(); err != nil {
			return nil, fmt.Errorf("verify Mdm-Signature: %w", err)
		}
		cert := p7.GetOnlySigner()
		if cert == nil {
			return nil, errors.New("Mdm-Signature has no single signer")
		}
		if err := d.CA.Verify(cert); err != nil {
			return nil, fmt.Errorf("signer not issued by this server: %w", err)
		}
		return cert, nil
	}
	return d.CA.ClientCert(r, d.CertHeader)
}

type checkinMsg struct {
	MessageType   string
	UDID          string
	EnrollmentID  string
	Topic         string
	SerialNumber  string
	Model         string
	ModelName     string
	ProductName   string
	OSVersion     string
	BuildVersion  string
	DeviceName    string
	Token         []byte
	PushMagic     string
	UnlockToken   []byte
	UserID        string
	UserShortName string
}

// nativeID is the stable identity: the UDID for device enrollment, the
// per-enrollment EnrollmentID for User Enrollment (which hides the UDID).
func (m *checkinMsg) nativeID() string {
	if m.UDID != "" {
		return m.UDID
	}
	return m.EnrollmentID
}

func (d *Driver) handleCheckin(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	cert, err := d.identity(r, body)
	if err != nil {
		d.Log.Warn("apple check-in rejected", "err", err, "ip", httpx.ClientIP(r))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var msg checkinMsg
	if _, err := plist.Unmarshal(body, &msg); err != nil || msg.nativeID() == "" {
		http.Error(w, "malformed check-in", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	serial := pki.SerialHex(cert)

	switch msg.MessageType {
	case "Authenticate":
		err = d.authenticate(ctx, &msg, serial, httpx.ClientIP(r))
	case "TokenUpdate":
		err = d.tokenUpdate(ctx, &msg, serial)
	case "CheckOut":
		err = d.checkOut(ctx, &msg, serial, httpx.ClientIP(r))
	case "UserAuthenticate":
		// Per-user channels authenticate without a server challenge; an empty
		// response accepts them. Commands are only sent on the device channel.
		w.WriteHeader(http.StatusOK)
		return
	default:
		// GetBootstrapToken, DeclarativeManagement etc. are not implemented.
		http.Error(w, "unsupported message type", http.StatusBadRequest)
		return
	}
	if err != nil {
		d.Log.Warn("apple check-in", "type", msg.MessageType, "err", err)
		code := http.StatusInternalServerError
		if errors.Is(err, errUnauthorized) {
			code = http.StatusUnauthorized
		}
		http.Error(w, http.StatusText(code), code)
		return
	}
	w.WriteHeader(http.StatusOK)
}

var errUnauthorized = errors.New("unauthorized")

func applePlatform(m *checkinMsg) string {
	p := m.ProductName + " " + m.Model
	switch {
	case strings.HasPrefix(p, "iPad"):
		return store.PlatformIPadOS
	case strings.HasPrefix(p, "iPhone"), strings.HasPrefix(p, "iPod"):
		return store.PlatformIOS
	case strings.Contains(p, "Mac"):
		return store.PlatformMacOS
	}
	// Unknown hardware: refined from DeviceInformation after enrollment.
	return store.PlatformIOS
}

var applePlatforms = []string{store.PlatformIOS, store.PlatformIPadOS, store.PlatformMacOS}

func (d *Driver) authenticate(ctx context.Context, m *checkinMsg, serial, ip string) error {
	tok, err := d.Store.PendingIdentity(ctx, serial)
	var tokenID *string
	ownership, assignee := "", ""
	switch {
	case err == nil:
		tokenID, ownership, assignee = &tok.ID, tok.Ownership, tok.Assignee
	case errors.Is(err, store.ErrNotFound):
		// A known device re-authenticating with its existing identity, e.g.
		// after an OS update. Anything else is an unknown certificate.
		existing, err := d.Store.DeviceByCertSerial(ctx, serial)
		if err != nil || existing.NativeID != m.nativeID() {
			return errUnauthorized
		}
		ownership = existing.Ownership
	default:
		return err
	}
	if m.Topic != "" && m.Topic != d.Pusher.Topic {
		return fmt.Errorf("%w: topic mismatch", errUnauthorized)
	}
	now := time.Now()
	dev, err := d.Store.UpsertDevice(ctx, &store.Device{
		Platform: applePlatform(m), Ownership: ownership, Status: store.StatusEnrolling,
		Name: m.DeviceName, Serial: m.SerialNumber, Model: firstNonEmpty(m.ProductName, m.Model),
		OSVersion: m.OSVersion, Assignee: assignee, NativeID: m.nativeID(), CertSerial: serial,
		EnrollmentTokenID: tokenID, EnrolledAt: &now,
		PlatformIDs: mustJSON(map[string]any{"udid": m.UDID, "enrollmentId": m.EnrollmentID, "userEnrollment": m.UDID == ""}),
	})
	if err != nil {
		return err
	}
	if tok != nil {
		_ = d.Store.DeletePendingIdentity(ctx, serial)
		mdm.AttachToGroup(ctx, d.Store, tok, dev.ID)
	}
	_ = d.Store.Audit(ctx, "device", "device.authenticate", dev.ID, map[string]any{"platform": dev.Platform, "ownership": dev.Ownership}, ip)
	return nil
}

func (d *Driver) deviceFor(ctx context.Context, m *checkinMsg, serial string) (*store.Device, error) {
	dev, err := d.Store.DeviceByCertSerial(ctx, serial)
	if err != nil {
		return nil, errUnauthorized
	}
	if !dev.IsApple() || dev.NativeID != m.nativeID() {
		return nil, errUnauthorized
	}
	return dev, nil
}

func (d *Driver) tokenUpdate(ctx context.Context, m *checkinMsg, serial string) error {
	if m.UserID != "" {
		return nil // user channel token; not used for pushes
	}
	dev, err := d.deviceFor(ctx, m, serial)
	if err != nil {
		return err
	}
	ids := map[string]any{"token": hex.EncodeToString(m.Token), "pushMagic": m.PushMagic}
	if len(m.UnlockToken) > 0 {
		sealed, err := d.Box.Seal(m.UnlockToken)
		if err != nil {
			return err
		}
		ids["unlockToken"] = base64.StdEncoding.EncodeToString(sealed)
	}
	first := dev.Status == store.StatusEnrolling
	status := store.StatusEnrolled
	dev, err = d.Store.PatchDevice(ctx, dev.ID, store.DevicePatch{PlatformIDs: ids, Status: &status, Seen: true, Enrolled: true})
	if err != nil {
		return err
	}
	if first {
		d.Svc.Bootstrap(ctx, dev)
	}
	return nil
}

func (d *Driver) checkOut(ctx context.Context, m *checkinMsg, serial, ip string) error {
	dev, err := d.deviceFor(ctx, m, serial)
	if err != nil {
		return err
	}
	status := store.StatusRetired
	if _, err := d.Store.PatchDevice(ctx, dev.ID, store.DevicePatch{Status: &status, Seen: true}); err != nil {
		return err
	}
	_ = d.Store.CancelPendingCommands(ctx, dev.ID)
	return d.Store.Audit(ctx, "device", "device.checkout", dev.ID, nil, ip)
}

// apple platform identifiers stored in devices.platform_ids.
type appleIDs struct {
	Token       string `json:"token"`
	PushMagic   string `json:"pushMagic"`
	UnlockToken string `json:"unlockToken"`
}

func platformIDs(dev *store.Device) appleIDs {
	var ids appleIDs
	_ = jsonUnmarshal(dev.PlatformIDs, &ids)
	return ids
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
