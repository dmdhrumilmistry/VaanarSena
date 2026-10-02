package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/dmdhrumilmistry/VaanarSena/internal/command"
	"github.com/dmdhrumilmistry/VaanarSena/internal/httpx"
	"github.com/dmdhrumilmistry/VaanarSena/internal/mdm"
	"github.com/dmdhrumilmistry/VaanarSena/internal/pki"
	"github.com/dmdhrumilmistry/VaanarSena/internal/policy"
	"github.com/dmdhrumilmistry/VaanarSena/internal/secrets"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

// CheckinInterval is how often agents poll.
const CheckinInterval = 60 * time.Second

const maxBody = 4 << 20

// Driver is the Linux agent driver.
type Driver struct {
	Store      *store.Store
	Svc        *mdm.Service
	CA         *pki.CA
	CertHeader string
	Log        *slog.Logger
}

// Platforms implements mdm.Driver.
func (d *Driver) Platforms() []string { return []string{store.PlatformLinux} }

// Wake is a no-op: agents poll every CheckinInterval.
func (d *Driver) Wake(context.Context, *store.Device) error { return nil }

// Routes registers the agent endpoints.
func (d *Driver) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /agent/v1/enroll", d.handleEnroll)
	mux.HandleFunc("POST /agent/v1/checkin", d.handleCheckin)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (d *Driver) handleEnroll(w http.ResponseWriter, r *http.Request) {
	var req EnrollRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed request"})
		return
	}
	if req.MachineID == "" || len(req.CSR) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "machineId and csr are required"})
		return
	}
	ctx := r.Context()
	tok, err := d.Store.ConsumeEnrollmentToken(ctx, secrets.Hash(strings.TrimSpace(req.Token)), "linux")
	if err != nil {
		_ = d.Store.Audit(ctx, "device", "linux.enroll_denied", req.Hostname, nil, httpx.ClientIP(r))
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "enrollment token is invalid, expired or already used"})
		return
	}
	cert, err := d.CA.SignCSR(req.CSR, "vaanarsena-linux-"+req.MachineID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	now := time.Now()
	dev, err := d.Store.UpsertDevice(ctx, &store.Device{
		Platform: store.PlatformLinux, Ownership: tok.Ownership, Status: store.StatusEnrolled,
		Name: req.Hostname, OSVersion: strings.TrimSpace(req.OS + " " + req.OSVersion), Assignee: tok.Assignee,
		NativeID: req.MachineID, CertSerial: pki.SerialHex(cert), EnrollmentTokenID: &tok.ID, EnrolledAt: &now,
	})
	if err != nil {
		d.Log.Error("store linux device", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	mdm.AttachToGroup(ctx, d.Store, tok, dev.ID)
	_ = d.Store.Audit(ctx, "device", "device.enroll", dev.ID, map[string]any{"platform": "linux", "ownership": tok.Ownership}, httpx.ClientIP(r))
	d.Svc.Bootstrap(ctx, dev)
	writeJSON(w, http.StatusOK, EnrollResponse{
		DeviceID:         dev.ID,
		CertificatePEM:   string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})),
		CACertificatePEM: string(d.CA.CertPEM),
		CheckinSeconds:   int(CheckinInterval.Seconds()),
	})
}

func (d *Driver) handleCheckin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cert, err := d.CA.ClientCert(r, d.CertHeader)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "client certificate required"})
		return
	}
	dev, err := d.Store.DeviceByCertSerial(ctx, pki.SerialHex(cert))
	if err != nil || dev.Platform != store.PlatformLinux {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unknown device"})
		return
	}
	if dev.Status == store.StatusRetired {
		// Tell the agent to remove itself.
		writeJSON(w, http.StatusGone, map[string]string{"error": "device retired"})
		return
	}
	var req CheckinRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed request"})
		return
	}

	for _, res := range req.Results {
		status, errMsg := store.CmdAcknowledged, ""
		if !res.OK {
			status, errMsg = store.CmdError, res.Error
		}
		out := res.Output
		if len(out) > 64<<10 {
			out = out[:64<<10]
		}
		if err := d.Store.CompleteCommand(ctx, res.CommandID, dev.ID, status, out, errMsg); err == nil && res.OK {
			if c, err := d.Store.CommandByID(ctx, res.CommandID); err == nil && c.Type == command.Retire {
				st := store.StatusRetired
				_, _ = d.Store.PatchDevice(ctx, dev.ID, store.DevicePatch{Status: &st})
			}
		}
	}

	patch := store.DevicePatch{Seen: true}
	if req.Facts != nil {
		if dev.IsPersonal() {
			// Inventory-only mode for BYOD: drop identifiers if a modified
			// agent sends them anyway.
			for _, k := range []string{"serial", "macAddresses", "users", "packages"} {
				delete(req.Facts, k)
			}
		}
		patch.Facts = map[string]any{"linux": req.Facts}
		if v, ok := req.Facts["hostname"].(string); ok && v != "" {
			patch.Name = &v
		}
		if v, ok := req.Facts["osPretty"].(string); ok && v != "" {
			patch.OSVersion = &v
		}
		if v, ok := req.Facts["model"].(string); ok && v != "" {
			patch.Model = &v
		}
		if v, ok := req.Facts["serial"].(string); ok && v != "" && !dev.IsPersonal() {
			patch.Serial = &v
		}
	}
	if req.Compliance != nil {
		c := req.Compliance.Compliant
		patch.Compliant = &c
		patch.Facts = merge(patch.Facts, map[string]any{"compliance": req.Compliance})
	}
	if _, err := d.Store.PatchDevice(ctx, dev.ID, patch); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	doc, err := mdm.EffectivePolicy(ctx, d.Store, dev.ID)
	if err != nil {
		d.Log.Warn("effective policy", "device", dev.ID, "err", err)
	}
	d.storeInventory(ctx, dev, req.Inventory, doc)
	resp := CheckinResponse{Policy: doc, CheckinSeconds: int(CheckinInterval.Seconds()), Personal: dev.IsPersonal()}
	if doc != nil {
		b, _ := json.Marshal(doc)
		sum := sha256.Sum256(b)
		resp.PolicyVersion = hex.EncodeToString(sum[:8])
	}
	cmds, err := d.Store.PendingCommands(ctx, dev.ID, 20)
	if err != nil {
		d.Log.Error("pending commands", "err", err)
	}
	for _, c := range cmds {
		resp.Commands = append(resp.Commands, Command{ID: c.ID, Type: c.Type, Params: c.Params})
	}
	if resp.Commands == nil {
		resp.Commands = []Command{}
	}
	writeJSON(w, http.StatusOK, resp)
}

// storeInventory records the packages and services an agent reported. BYOD
// rule: personal devices never have software inventory stored, whatever the
// agent sends. The agent already stays silent in personal mode; this is the
// guard against a modified agent.
func (d *Driver) storeInventory(ctx context.Context, dev *store.Device, inv *Inventory, doc *policy.Document) {
	if inv == nil || dev.IsPersonal() {
		return
	}
	managed := map[string]bool{}
	if doc != nil {
		for _, a := range doc.AppsFor("linux") {
			managed[a.ID] = true
		}
	}
	if len(inv.Apps) > 0 {
		if err := d.Store.ReplaceInventory(ctx, dev.ID, store.KindApp, inventoryItems(inv.Apps, "app", managed)); err != nil {
			d.Log.Warn("store app inventory", "device", dev.ID, "err", err)
		}
	}
	if len(inv.Services) > 0 {
		if err := d.Store.ReplaceInventory(ctx, dev.ID, store.KindService, inventoryItems(inv.Services, "service", nil)); err != nil {
			d.Log.Warn("store service inventory", "device", dev.ID, "err", err)
		}
	}
}

func inventoryItems(in []InventoryItem, kind string, managed map[string]bool) []store.InventoryItem {
	if len(in) > store.MaxInventoryItems {
		in = in[:store.MaxInventoryItems]
	}
	out := make([]store.InventoryItem, 0, len(in))
	for _, it := range in {
		si := store.InventoryItem{Name: it.Name, Identifier: it.Identifier, Version: it.Version, Publisher: it.Publisher, Source: it.Source, State: it.State}
		if si.Identifier == "" {
			si.Identifier = it.Name
		}
		if kind == "app" {
			si.State = "installed"
			si.Managed = managed[si.Identifier]
		}
		if len(it.Details) > 0 {
			si.Details, _ = json.Marshal(it.Details)
		}
		out = append(out, si)
	}
	return out
}

func merge(a, b map[string]any) map[string]any {
	if a == nil {
		a = map[string]any{}
	}
	for k, v := range b {
		a[k] = v
	}
	return a
}
