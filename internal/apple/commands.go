package apple

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"howett.net/plist"

	"github.com/dmdhrumilmistry/VaanarSena/internal/command"
	"github.com/dmdhrumilmistry/VaanarSena/internal/mdm"
	"github.com/dmdhrumilmistry/VaanarSena/internal/pki"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

type report struct {
	UDID         string
	EnrollmentID string
	UserID       string
	Status       string
	CommandUUID  string
	ErrorChain   []struct {
		ErrorCode            int
		ErrorDomain          string
		LocalizedDescription string
	}
}

// handleServer implements the command protocol. The device reports the result
// of the previous command (or Idle) and the response carries the next one; an
// empty 200 response ends the session.
func (d *Driver) handleServer(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	cert, err := d.identity(r, body)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var rep report
	if _, err := plist.Unmarshal(body, &rep); err != nil {
		http.Error(w, "malformed report", http.StatusBadRequest)
		return
	}
	if rep.UserID != "" {
		w.WriteHeader(http.StatusOK) // nothing queued for user channels
		return
	}
	ctx := r.Context()
	native := rep.UDID
	if native == "" {
		native = rep.EnrollmentID
	}
	dev, err := d.Store.DeviceByCertSerial(ctx, pki.SerialHex(cert))
	if err != nil || dev.NativeID != native {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	dev, _ = d.Store.PatchDevice(ctx, dev.ID, store.DevicePatch{Seen: true})

	sessionStart := rep.Status == "Idle"
	if !sessionStart && rep.CommandUUID != "" {
		d.recordResult(ctx, dev, &rep, body)
	}

	for {
		cmd, err := d.Store.NextCommand(ctx, dev.ID, sessionStart)
		if errors.Is(err, store.ErrNotFound) {
			w.WriteHeader(http.StatusOK)
			return
		}
		if err != nil {
			d.Log.Error("next command", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		out, err := d.build(ctx, dev, cmd)
		if err != nil {
			// Unbuildable commands fail permanently instead of blocking the queue.
			_ = d.Store.CompleteCommand(ctx, cmd.ID, dev.ID, store.CmdError, "", err.Error())
			continue
		}
		if err := d.Store.MarkCommandSent(ctx, cmd.ID, string(out)); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write(out)
		return
	}
}

func (d *Driver) recordResult(ctx context.Context, dev *store.Device, rep *report, body []byte) {
	cmd, err := d.Store.CommandByID(ctx, rep.CommandUUID)
	if err != nil || cmd.DeviceID != dev.ID {
		return
	}
	status, msg := store.CmdAcknowledged, ""
	switch rep.Status {
	case "Acknowledged":
	case "NotNow":
		status = store.CmdNotNow
	default: // Error, CommandFormatError
		status = store.CmdError
		var parts []string
		for _, e := range rep.ErrorChain {
			parts = append(parts, fmt.Sprintf("%s %d: %s", e.ErrorDomain, e.ErrorCode, e.LocalizedDescription))
		}
		msg = strings.Join(parts, "; ")
		if msg == "" {
			msg = rep.Status
		}
	}
	_ = d.Store.CompleteCommand(ctx, cmd.ID, dev.ID, status, string(body), msg)
	if status != store.CmdAcknowledged {
		return
	}
	switch cmd.Type {
	case command.Refresh:
		d.ingestDeviceInformation(ctx, dev, body)
	case command.ApplyPolicy:
		d.queueRequiredApps(ctx, dev)
	case command.Retire:
		st := store.StatusRetired
		_, _ = d.Store.PatchDevice(ctx, dev.ID, store.DevicePatch{Status: &st})
	case command.Wipe:
		st := store.StatusWiped
		_, _ = d.Store.PatchDevice(ctx, dev.ID, store.DevicePatch{Status: &st})
		_ = d.Store.CancelPendingCommands(ctx, dev.ID)
	}
}

func (d *Driver) ingestDeviceInformation(ctx context.Context, dev *store.Device, body []byte) {
	var resp struct {
		QueryResponses map[string]any
	}
	if _, err := plist.Unmarshal(body, &resp); err != nil || resp.QueryResponses == nil {
		return
	}
	q := resp.QueryResponses
	str := func(k string) *string {
		if v, ok := q[k].(string); ok && v != "" {
			return &v
		}
		return nil
	}
	patch := store.DevicePatch{
		Name: str("DeviceName"), OSVersion: str("OSVersion"), Model: str("ProductName"), Serial: str("SerialNumber"),
		Facts: map[string]any{"apple": q},
	}
	if pn := str("ProductName"); pn != nil {
		m := &checkinMsg{ProductName: *pn}
		p := applePlatform(m)
		if p != dev.Platform && (strings.HasPrefix(*pn, "iP") || strings.Contains(*pn, "Mac")) {
			// Platform is part of the unique key; only fix it if it was a guess.
			_, _ = d.Store.DB.Exec(ctx, `UPDATE devices SET platform = $2 WHERE id = $1`, dev.ID, p)
		}
	}
	_, _ = d.Store.PatchDevice(ctx, dev.ID, patch)
}

func (d *Driver) queueRequiredApps(ctx context.Context, dev *store.Device) {
	doc, err := mdm.EffectivePolicy(ctx, d.Store, dev.ID)
	if err != nil {
		return
	}
	for _, a := range doc.AppsFor("apple") {
		if a.Install != "required" {
			continue
		}
		params, _ := json.Marshal(command.Params{AppID: a.ID, URL: a.URL})
		_, _ = d.Store.EnqueueCommand(ctx, dev.ID, command.InstallApp, params, nil)
	}
}

var queriesCorporate = []string{
	"DeviceName", "OSVersion", "BuildVersion", "ModelName", "Model", "ProductName", "SerialNumber",
	"DeviceCapacity", "AvailableDeviceCapacity", "BatteryLevel", "WiFiMAC", "BluetoothMAC", "IMEI",
	"IsSupervised", "IsActivationLockEnabled", "IsDeviceLocatorServiceEnabled", "IsCloudBackupEnabled",
}

// Personal devices: hardware identifiers are not collected. Apple withholds
// most of them under User Enrollment anyway; this keeps the server honest too.
var queriesPersonal = []string{"DeviceName", "OSVersion", "BuildVersion", "ModelName", "ProductName"}

// build translates a neutral command into an Apple command plist.
func (d *Driver) build(ctx context.Context, dev *store.Device, cmd *store.Command) ([]byte, error) {
	p, err := command.ParseParams(cmd.Params)
	if err != nil {
		return nil, err
	}
	c := map[string]any{}
	switch cmd.Type {
	case command.Refresh:
		q := queriesCorporate
		if dev.IsPersonal() {
			q = queriesPersonal
		}
		c["RequestType"], c["Queries"] = "DeviceInformation", q
	case command.ApplyPolicy:
		doc, err := mdm.EffectivePolicy(ctx, d.Store, dev.ID)
		if err != nil {
			return nil, err
		}
		prof, err := PolicyProfile(d.Org, doc, dev.IsPersonal(), d.CA)
		if err != nil {
			return nil, err
		}
		c["RequestType"], c["Payload"] = "InstallProfile", prof
	case command.Lock:
		c["RequestType"] = "DeviceLock"
		if p.Message != "" {
			c["Message"] = p.Message
		}
		if p.Phone != "" {
			c["PhoneNumber"] = p.Phone
		}
		if dev.Platform == store.PlatformMacOS && p.PIN != "" {
			c["PIN"] = p.PIN
		}
	case command.Restart:
		c["RequestType"] = "RestartDevice"
	case command.Shutdown:
		c["RequestType"] = "ShutDownDevice"
	case command.ClearPasscode:
		ids := platformIDs(dev)
		if ids.UnlockToken == "" {
			return nil, errors.New("device has not escrowed an unlock token")
		}
		sealed, err := base64.StdEncoding.DecodeString(ids.UnlockToken)
		if err != nil {
			return nil, err
		}
		tok, err := d.Box.Open(sealed)
		if err != nil {
			return nil, err
		}
		c["RequestType"], c["UnlockToken"] = "ClearPasscode", tok
	case command.EnableLostMode:
		c["RequestType"] = "EnableLostMode"
		c["Message"] = firstNonEmpty(p.Message, "This device is managed by "+d.Org+" and has been reported lost.")
		if p.Phone != "" {
			c["PhoneNumber"] = p.Phone
		}
	case command.DisableLostMode:
		c["RequestType"] = "DisableLostMode"
	case command.Locate:
		c["RequestType"] = "DeviceLocation"
	case command.OSUpdate:
		c["RequestType"] = "ScheduleOSUpdate"
		c["Updates"] = []map[string]any{{"InstallAction": "Default"}}
	case command.InstallApp:
		c["RequestType"] = "InstallApplication"
		c["ManagementFlags"] = 1 // remove app when MDM profile is removed
		if p.URL != "" {
			c["ManifestURL"] = p.URL
		} else {
			c["Identifier"] = p.AppID // requires an Apps and Books licence
		}
	case command.RemoveApp:
		c["RequestType"], c["Identifier"] = "RemoveApplication", p.AppID
	case command.Retire:
		c["RequestType"], c["Identifier"] = "RemoveProfile", MDMProfileID
	case command.Wipe:
		c["RequestType"] = "EraseDevice"
		if dev.Platform == store.PlatformMacOS && p.PIN != "" {
			c["PIN"] = p.PIN
		}
		if p.PreserveDataPlan {
			c["PreserveDataPlan"] = true
		}
	default:
		return nil, fmt.Errorf("command %s not supported on Apple", cmd.Type)
	}
	return plist.Marshal(map[string]any{"CommandUUID": cmd.ID, "Command": c}, plist.XMLFormat)
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func jsonUnmarshal(raw []byte, v any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, v)
}
