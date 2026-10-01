package windows

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/dmdhrumilmistry/VaanarSena/internal/command"
	"github.com/dmdhrumilmistry/VaanarSena/internal/httpx"
	"github.com/dmdhrumilmistry/VaanarSena/internal/mdm"
	"github.com/dmdhrumilmistry/VaanarSena/internal/pki"
	"github.com/dmdhrumilmistry/VaanarSena/internal/policy"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

type locURI struct {
	LocURI string `xml:"LocURI"`
}

type item struct {
	Source locURI `xml:"Source"`
	Target locURI `xml:"Target"`
	Meta   struct {
		Type   string `xml:"Type"`
		Format string `xml:"Format"`
	} `xml:"Meta"`
	Data string `xml:"Data"`
}

type dmCmd struct {
	CmdID string `xml:"CmdID"`
	Data  string `xml:"Data"`
	Items []item `xml:"Item"`
}

type dmStatus struct {
	CmdID  string `xml:"CmdID"`
	MsgRef string `xml:"MsgRef"`
	CmdRef string `xml:"CmdRef"`
	Cmd    string `xml:"Cmd"`
	Data   string `xml:"Data"`
}

type dmResults struct {
	CmdID  string `xml:"CmdID"`
	MsgRef string `xml:"MsgRef"`
	CmdRef string `xml:"CmdRef"`
	Items  []item `xml:"Item"`
}

type syncML struct {
	XMLName xml.Name `xml:"SyncML"`
	Hdr     struct {
		SessionID string `xml:"SessionID"`
		MsgID     string `xml:"MsgID"`
		Target    locURI `xml:"Target"`
		Source    locURI `xml:"Source"`
	} `xml:"SyncHdr"`
	Body struct {
		Alert   []dmCmd     `xml:"Alert"`
		Replace []dmCmd     `xml:"Replace"`
		Status  []dmStatus  `xml:"Status"`
		Results []dmResults `xml:"Results"`
	} `xml:"SyncBody"`
}

// sentRecord is stored in commands.native_request so a reply from any replica
// can be matched to the command that produced it.
type sentRecord struct {
	Session string   `json:"session"`
	Msg     string   `json:"msg"`
	CmdIDs  []string `json:"cmdIds"`
	SyncML  string   `json:"syncml"`
}

func (d *Driver) handleManagement(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cert, err := d.CA.ClientCert(r, d.CertHeader)
	if err != nil {
		d.Log.Warn("windows management rejected", "err", err, "ip", httpx.ClientIP(r))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil || bytes.Contains(body, []byte("<!DOCTYPE")) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var msg syncML
	if err := xml.Unmarshal(body, &msg); err != nil {
		http.Error(w, "malformed SyncML", http.StatusBadRequest)
		return
	}
	dev, err := d.Store.DeviceByCertSerial(ctx, pki.SerialHex(cert))
	if err != nil || dev.Platform != store.PlatformWindows || dev.NativeID != msg.Hdr.Source.LocURI {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	patch := store.DevicePatch{Seen: true}
	if dev.Status == store.StatusEnrolling {
		st := store.StatusEnrolled
		patch.Status, patch.Enrolled = &st, true
	}
	facts := map[string]any{}
	for _, rep := range msg.Body.Replace {
		for _, it := range rep.Items {
			facts[it.Source.LocURI] = it.Data
		}
	}
	unenrolled := false
	for _, a := range msg.Body.Alert {
		// 1226 generic alert: the user removed the work account on the device.
		for _, it := range a.Items {
			if a.Data == "1226" && strings.Contains(it.Meta.Type, "unenrollment") {
				unenrolled = true
			}
		}
	}
	if len(facts) > 0 {
		patch.Facts = map[string]any{"windows": facts}
		if v, ok := facts["./DevInfo/Mod"]; ok {
			s := v.(string)
			patch.Model = &s
		}
	}
	first := dev.Status == store.StatusEnrolling
	dev, err = d.Store.PatchDevice(ctx, dev.ID, patch)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if first {
		d.Svc.Bootstrap(ctx, dev)
	}
	if unenrolled {
		st := store.StatusRetired
		_, _ = d.Store.PatchDevice(ctx, dev.ID, store.DevicePatch{Status: &st})
		_ = d.Store.CancelPendingCommands(ctx, dev.ID)
		_ = d.Store.Audit(ctx, "device", "device.unenrolled", dev.ID, nil, httpx.ClientIP(r))
	}

	if msg.Hdr.MsgID == "1" {
		// A new session: anything sent in an earlier session that was never
		// answered (dropped connection) is delivered again.
		_ = d.Store.RequeueSent(ctx, dev.ID)
	} else {
		d.recordResults(ctx, dev, &msg)
	}

	// Build the reply: statuses for the header and every client command, then
	// any queued commands.
	out := newReply(msg.Hdr.SessionID, msg.Hdr.MsgID, msg.Hdr.Target.LocURI, msg.Hdr.Source.LocURI)
	out.status("0", "SyncHdr", "212") // authentication accepted for the session
	for _, a := range msg.Body.Alert {
		out.status(a.CmdID, "Alert", "200")
	}
	for _, rep := range msg.Body.Replace {
		out.status(rep.CmdID, "Replace", "200")
	}
	for _, res := range msg.Body.Results {
		out.status(res.CmdID, "Results", "200")
	}

	if !unenrolled {
		cmds, err := d.Store.PendingCommands(ctx, dev.ID, 10)
		if err != nil {
			d.Log.Error("pending commands", "err", err)
		}
		for _, c := range cmds {
			start := out.next
			if err := d.translate(ctx, dev, c, out); err != nil {
				_ = d.Store.CompleteCommand(ctx, c.ID, dev.ID, store.CmdError, "", err.Error())
				continue
			}
			var ids []string
			for i := start; i < out.next; i++ {
				ids = append(ids, strconv.Itoa(i))
			}
			rec, _ := json.Marshal(sentRecord{Session: msg.Hdr.SessionID, Msg: out.msgID, CmdIDs: ids, SyncML: out.since(start)})
			_ = d.Store.MarkCommandSent(ctx, c.ID, string(rec))
		}
	}

	w.Header().Set("Content-Type", "application/vnd.syncml.dm+xml")
	_, _ = io.WriteString(w, out.finish())
}

// recordResults matches Status and Results elements to the commands sent in
// the previous message of this session.
func (d *Driver) recordResults(ctx context.Context, dev *store.Device, msg *syncML) {
	if len(msg.Body.Status) == 0 && len(msg.Body.Results) == 0 {
		return
	}
	sent, err := d.Store.SentCommands(ctx, dev.ID)
	if err != nil {
		return
	}
	statusByRef := map[string]dmStatus{}
	for _, s := range msg.Body.Status {
		statusByRef[s.MsgRef+"/"+s.CmdRef] = s
	}
	resultsByRef := map[string][]item{}
	for _, res := range msg.Body.Results {
		resultsByRef[res.MsgRef+"/"+res.CmdRef] = append(resultsByRef[res.MsgRef+"/"+res.CmdRef], res.Items...)
	}
	for _, c := range sent {
		var rec sentRecord
		if json.Unmarshal([]byte(c.NativeRequest), &rec) != nil || rec.Session != msg.Hdr.SessionID {
			continue
		}
		var failures []string
		answered := 0
		results := map[string]any{}
		for _, id := range rec.CmdIDs {
			key := rec.Msg + "/" + id
			s, ok := statusByRef[key]
			if !ok {
				continue
			}
			answered++
			code, _ := strconv.Atoi(s.Data)
			// 404 on a Get just means the node is absent on this build.
			if code >= 300 && !(c.Type == command.Refresh && code == 404) {
				failures = append(failures, fmt.Sprintf("%s CmdID %s: status %s", s.Cmd, id, s.Data))
			}
			for _, it := range resultsByRef[key] {
				results[it.Source.LocURI] = it.Data
			}
		}
		if answered == 0 {
			continue
		}
		resp, _ := json.Marshal(results)
		if len(failures) > 0 {
			_ = d.Store.CompleteCommand(ctx, c.ID, dev.ID, store.CmdError, string(resp), strings.Join(failures, "; "))
			continue
		}
		_ = d.Store.CompleteCommand(ctx, c.ID, dev.ID, store.CmdAcknowledged, string(resp), "")
		d.afterAck(ctx, dev, c, results)
	}
}

func (d *Driver) afterAck(ctx context.Context, dev *store.Device, c *store.Command, results map[string]any) {
	switch c.Type {
	case command.Refresh:
		p := store.DevicePatch{Facts: map[string]any{"windows": results}}
		str := func(k string) *string {
			if v, ok := results[k].(string); ok && v != "" {
				return &v
			}
			return nil
		}
		p.OSVersion = str("./DevDetail/SwV")
		p.Name = str("./DevDetail/Ext/Microsoft/DeviceName")
		p.Serial = str("./DevDetail/Ext/Microsoft/SMBIOSSerialNumber")
		p.Model = str("./DevInfo/Mod")
		if enc, ok := results["./Device/Vendor/MSFT/DeviceStatus/Compliance/EncryptionCompliance"].(string); ok {
			compliant := enc == "1"
			p.Compliant = &compliant
		}
		_, _ = d.Store.PatchDevice(ctx, dev.ID, p)
	case command.Retire:
		st := store.StatusRetired
		_, _ = d.Store.PatchDevice(ctx, dev.ID, store.DevicePatch{Status: &st})
	case command.Wipe:
		st := store.StatusWiped
		_, _ = d.Store.PatchDevice(ctx, dev.ID, store.DevicePatch{Status: &st})
		_ = d.Store.CancelPendingCommands(ctx, dev.ID)
	}
}

var refreshNodes = []string{
	"./DevDetail/SwV", "./DevInfo/Mod", "./DevInfo/Man", "./DevDetail/Ext/Microsoft/DeviceName",
	"./DevDetail/Ext/Microsoft/OSPlatform", "./DevDetail/Ext/Microsoft/TotalStorage", "./DevDetail/Ext/Microsoft/TotalRAM",
	"./Device/Vendor/MSFT/DeviceStatus/Compliance/EncryptionCompliance",
	"./Device/Vendor/MSFT/DeviceStatus/Firewall/Status",
	"./Device/Vendor/MSFT/DeviceStatus/Antivirus/Status",
}

// Hardware identifiers are not read from personal devices.
var refreshNodesCorporate = []string{"./DevDetail/Ext/Microsoft/SMBIOSSerialNumber", "./Device/Vendor/MSFT/DeviceStatus/NetworkIdentifiers"}

func (d *Driver) translate(ctx context.Context, dev *store.Device, c *store.Command, out *reply) error {
	p, err := command.ParseParams(c.Params)
	if err != nil {
		return err
	}
	switch c.Type {
	case command.Refresh:
		nodes := refreshNodes
		if !dev.IsPersonal() {
			nodes = append(append([]string{}, nodes...), refreshNodesCorporate...)
		}
		for _, n := range nodes {
			out.get(n)
		}
	case command.ApplyPolicy:
		doc, err := mdm.EffectivePolicy(ctx, d.Store, dev.ID)
		if err != nil {
			return err
		}
		items := doc.WindowsItems(dev.IsPersonal())
		if len(items) == 0 {
			out.get("./DevDetail/SwV") // nothing to set; keep the command answerable
		}
		for _, it := range items {
			out.replace(it)
		}
		for _, a := range doc.AppsFor("windows") {
			if a.Install == "required" && a.URL != "" {
				// MSI installs need a hash and version, which policies do not
				// carry; use the install_app command for those.
				d.Log.Debug("windows policy app requires install_app command", "app", a.ID)
			}
		}
	case command.Restart:
		out.exec("./Device/Vendor/MSFT/Reboot/RebootNow", "")
	case command.Wipe:
		out.exec("./Device/Vendor/MSFT/RemoteWipe/doWipe", "")
	case command.Retire:
		out.exec("./Device/Vendor/MSFT/DMClient/Unenroll", ProviderID)
	case command.InstallApp:
		if p.URL == "" || p.Hash == "" || p.Version == "" || p.AppID == "" {
			return errors.New("windows install_app needs appId (MSI ProductCode), url, hash and version")
		}
		job := `<MsiInstallJob id="` + esc(p.AppID) + `"><Product Version="` + esc(p.Version) + `"><Download><ContentURLList><ContentURL>` +
			esc(p.URL) + `</ContentURL></ContentURLList></Download><Validation><FileHash>` + esc(p.Hash) +
			`</FileHash></Validation><Enforcement><CommandLine>/quiet</CommandLine><TimeOut>10</TimeOut><RetryCount>3</RetryCount><RetryInterval>5</RetryInterval></Enforcement></Product></MsiInstallJob>`
		base := "./Device/Vendor/MSFT/EnterpriseDesktopAppManagement/MSI/" + url64(p.AppID)
		out.add(base + "/DownloadInstall")
		out.execXML(base+"/DownloadInstall", job)
	case command.RemoveApp:
		out.delete("./Device/Vendor/MSFT/EnterpriseDesktopAppManagement/MSI/" + url64(p.AppID))
	default:
		return fmt.Errorf("command %s not supported on Windows", c.Type)
	}
	return nil
}

// url64 escapes a ProductCode for use in a LocURI ({ and } are reserved).
func url64(s string) string {
	return strings.NewReplacer("{", "%7B", "}", "%7D").Replace(s)
}

// reply builds an outgoing SyncML message in order.
type reply struct {
	b         strings.Builder
	next      int
	marks     map[int]int
	msgID     string
	clientMsg string
}

func newReply(session, clientMsg, serverURI, deviceURI string) *reply {
	n, _ := strconv.Atoi(clientMsg)
	r := &reply{next: 1, marks: map[int]int{}, msgID: strconv.Itoa(n)}
	r.b.WriteString(`<SyncML xmlns="SYNCML:SYNCML1.2"><SyncHdr><VerDTD>1.2</VerDTD><VerProto>DM/1.2</VerProto>`)
	r.b.WriteString(`<SessionID>` + esc(session) + `</SessionID><MsgID>` + r.msgID + `</MsgID>`)
	r.b.WriteString(`<Target><LocURI>` + esc(deviceURI) + `</LocURI></Target><Source><LocURI>` + esc(serverURI) + `</LocURI></Source></SyncHdr><SyncBody>`)
	r.clientMsg = clientMsg
	return r
}

func (r *reply) id() string {
	r.marks[r.next] = r.b.Len()
	id := strconv.Itoa(r.next)
	r.next++
	return id
}

func (r *reply) status(cmdRef, cmd, code string) {
	r.b.WriteString(`<Status><CmdID>` + r.id() + `</CmdID><MsgRef>` + esc(r.clientMsg) + `</MsgRef><CmdRef>` + esc(cmdRef) +
		`</CmdRef><Cmd>` + cmd + `</Cmd><Data>` + code + `</Data></Status>`)
}

func (r *reply) get(uri string) {
	r.b.WriteString(`<Get><CmdID>` + r.id() + `</CmdID><Item><Target><LocURI>` + esc(uri) + `</LocURI></Target></Item></Get>`)
}

func (r *reply) replace(it policy.SyncMLItem) {
	r.b.WriteString(`<Replace><CmdID>` + r.id() + `</CmdID><Item><Target><LocURI>` + esc(it.LocURI) +
		`</LocURI></Target><Meta><Format xmlns="syncml:metinf">` + it.Format + `</Format></Meta><Data>` + esc(it.Data) + `</Data></Item></Replace>`)
}

func (r *reply) add(uri string) {
	r.b.WriteString(`<Add><CmdID>` + r.id() + `</CmdID><Item><Target><LocURI>` + esc(uri) + `</LocURI></Target></Item></Add>`)
}

func (r *reply) delete(uri string) {
	r.b.WriteString(`<Delete><CmdID>` + r.id() + `</CmdID><Item><Target><LocURI>` + esc(uri) + `</LocURI></Target></Item></Delete>`)
}

func (r *reply) exec(uri, data string) {
	r.b.WriteString(`<Exec><CmdID>` + r.id() + `</CmdID><Item><Target><LocURI>` + esc(uri) + `</LocURI></Target>`)
	if data != "" {
		r.b.WriteString(`<Meta><Format xmlns="syncml:metinf">chr</Format></Meta><Data>` + esc(data) + `</Data>`)
	}
	r.b.WriteString(`</Item></Exec>`)
}

func (r *reply) execXML(uri, xmlData string) {
	r.b.WriteString(`<Exec><CmdID>` + r.id() + `</CmdID><Item><Target><LocURI>` + esc(uri) +
		`</LocURI></Target><Meta><Format xmlns="syncml:metinf">xml</Format><Type xmlns="syncml:metinf">text/plain</Type></Meta><Data>` +
		esc(xmlData) + `</Data></Item></Exec>`)
}

// since returns the markup written from command start onwards.
func (r *reply) since(start int) string {
	off, ok := r.marks[start]
	if !ok {
		return ""
	}
	return r.b.String()[off:]
}

func (r *reply) finish() string {
	r.b.WriteString(`<Final/></SyncBody></SyncML>`)
	return r.b.String()
}
