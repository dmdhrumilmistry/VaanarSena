// Command vaanarsena-agent is the VaanarSena agent for Linux. It enrolls with
// an enrollment token, then polls the server over mutual TLS, reporting
// inventory and compliance and running queued commands.
//
// On personally owned machines the server marks the agent personal and the
// agent runs in inventory-only mode: it reports basic facts and compliance and
// refuses every command except refresh and retire, even if asked.
package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/dmdhrumilmistry/VaanarSena/internal/agent"
	"github.com/dmdhrumilmistry/VaanarSena/internal/command"
	"github.com/dmdhrumilmistry/VaanarSena/internal/policy"
)

var version = "dev"

const defaultStateDir = "/var/lib/vaanarsena-agent"

type agentState struct {
	Server   string `json:"server"`
	DeviceID string `json:"deviceId"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: vaanarsena-agent enroll --server URL --token TOKEN | run | status | version")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "enroll":
		err = enroll(os.Args[2:])
	case "run":
		err = run(os.Args[2:])
	case "status":
		err = status(os.Args[2:])
	case "version":
		fmt.Println(version)
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func stateFlags(fs *flag.FlagSet) *string {
	return fs.String("state-dir", defaultStateDir, "directory holding the agent identity")
}

func enroll(args []string) error {
	fs := flag.NewFlagSet("enroll", flag.ExitOnError)
	server := fs.String("server", "", "VaanarSena public URL, e.g. https://mdm.example.com")
	token := fs.String("token", "", "enrollment token")
	serverCA := fs.String("server-ca", "", "PEM file to trust for the server's TLS certificate (private PKI); system roots are used otherwise")
	dir := stateFlags(fs)
	_ = fs.Parse(args)
	if *server == "" || *token == "" {
		return errors.New("--server and --token are required")
	}
	if !strings.HasPrefix(*server, "https://") && os.Getenv("VS_AGENT_ALLOW_HTTP") != "1" {
		return errors.New("--server must use https:// (set VS_AGENT_ALLOW_HTTP=1 for local testing only)")
	}
	if err := os.MkdirAll(*dir, 0o700); err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	var caPEM []byte
	if *serverCA != "" {
		if caPEM, err = os.ReadFile(*serverCA); err != nil {
			return err
		}
	}
	httpClient, err := newHTTPClient(caPEM, nil)
	if err != nil {
		return err
	}
	machineID := readMachineID()
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: machineID}}, key)
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	osr := osRelease()
	body, _ := json.Marshal(agent.EnrollRequest{Token: *token, CSR: csr, MachineID: machineID, Hostname: host,
		OS: osr["NAME"], OSVersion: osr["VERSION_ID"]})
	resp, err := httpClient.Post(strings.TrimRight(*server, "/")+"/agent/v1/enroll", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("enroll failed (%d): %s", resp.StatusCode, bytes.TrimSpace(raw))
	}
	var er agent.EnrollResponse
	if err := json.Unmarshal(raw, &er); err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	st, _ := json.Marshal(agentState{Server: strings.TrimRight(*server, "/"), DeviceID: er.DeviceID})
	files := map[string][]byte{
		"key.pem":    pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		"cert.pem":   []byte(er.CertificatePEM),
		"ca.pem":     []byte(er.CACertificatePEM),
		"state.json": st,
	}
	if caPEM != nil {
		files["server-ca.pem"] = caPEM
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(*dir, name), data, 0o600); err != nil {
			return err
		}
	}
	fmt.Println("enrolled as device", er.DeviceID)
	fmt.Println("start the agent with: systemctl enable --now vaanarsena-agent")
	return nil
}

func loadClient(dir string) (*http.Client, *agentState, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		return nil, nil, fmt.Errorf("not enrolled (%w)", err)
	}
	var st agentState
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, nil, err
	}
	pair, err := tls.LoadX509KeyPair(filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))
	if err != nil {
		return nil, nil, err
	}
	caPEM, _ := os.ReadFile(filepath.Join(dir, "server-ca.pem"))
	client, err := newHTTPClient(caPEM, &pair)
	return client, &st, err
}

// newHTTPClient trusts caPEM (if set) instead of the system roots and
// presents identity (if set) as the TLS client certificate.
func newHTTPClient(caPEM []byte, identity *tls.Certificate) (*http.Client, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if len(caPEM) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, errors.New("server CA file contains no PEM certificates")
		}
		cfg.RootCAs = pool
	}
	if identity != nil {
		cfg.Certificates = []tls.Certificate{*identity}
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = cfg
	return &http.Client{Transport: tr, Timeout: 60 * time.Second}, nil
}

func status(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	dir := stateFlags(fs)
	_ = fs.Parse(args)
	_, st, err := loadClient(*dir)
	if err != nil {
		return err
	}
	fmt.Printf("server: %s\ndevice: %s\n", st.Server, st.DeviceID)
	return nil
}

type runner struct {
	dir      string
	client   *http.Client
	st       *agentState
	personal bool
	pending  []agent.Result
	policyV  string
	policy   *policy.Document
	known    bool // the server has answered at least once, so personal is current
	inv      inventoryState
}

func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	dir := stateFlags(fs)
	_ = fs.Parse(args)
	client, st, err := loadClient(*dir)
	if err != nil {
		return err
	}
	r := &runner{dir: *dir, client: client, st: st}
	r.loadPending()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("vaanarsena-agent %s polling %s", version, st.Server)
	interval := agent.CheckinInterval
	for {
		next, err := r.checkin(ctx)
		if errors.Is(err, errRetired) {
			log.Print("device retired by server; removing agent state")
			return r.retire()
		}
		if err != nil {
			log.Printf("check-in failed: %v", err)
		} else if next > 0 {
			interval = next
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
	}
}

var errRetired = errors.New("retired")

// request builds a check-in: facts, results, compliance and, when due,
// software inventory.
func (r *runner) request(ctx context.Context) agent.CheckinRequest {
	req := agent.CheckinRequest{Facts: facts(r.personal), Results: r.pending, PolicyVersion: r.policyV}
	if r.policy != nil {
		req.Compliance = evaluate(r.policy)
	}
	req.Inventory = r.inv.next(ctx, time.Now(), r.personal, r.known, collectInventory)
	return req
}

func (r *runner) checkin(ctx context.Context) (time.Duration, error) {
	body, _ := json.Marshal(r.request(ctx))
	hreq, _ := http.NewRequestWithContext(ctx, http.MethodPost, r.st.Server+"/agent/v1/checkin", bytes.NewReader(body))
	hreq.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(hreq)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusGone {
		return 0, errRetired
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("server returned %d: %s", resp.StatusCode, bytes.TrimSpace(raw))
	}
	var cr agent.CheckinResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return 0, err
	}
	r.pending = nil
	r.personal, r.known = cr.Personal, true
	r.inv.sent(time.Now())
	policyChanged := false
	if cr.PolicyVersion != r.policyV && cr.Policy != nil {
		r.policy, r.policyV = cr.Policy, cr.PolicyVersion
		if !r.personal {
			enforce(cr.Policy)
		}
		policyChanged = true
	}
	retire := false
	for _, c := range cr.Commands {
		res := r.execute(ctx, c)
		r.pending = append(r.pending, res)
		if c.Type == command.Retire && res.OK {
			retire = true
		}
	}
	r.savePending()
	if retire {
		// Report the result, then remove ourselves.
		r.flush(ctx)
		return 0, errRetired
	}
	if len(r.pending) > 0 || policyChanged {
		r.flush(ctx) // report results and fresh compliance without waiting a poll
	}
	return time.Duration(cr.CheckinSeconds) * time.Second, nil
}

// flush sends results immediately rather than waiting for the next poll.
func (r *runner) flush(ctx context.Context) {
	body, _ := json.Marshal(r.request(ctx))
	hreq, _ := http.NewRequestWithContext(ctx, http.MethodPost, r.st.Server+"/agent/v1/checkin", bytes.NewReader(body))
	hreq.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(hreq)
	if err != nil {
		return
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusGone {
		r.pending = nil
		r.inv.sent(time.Now())
		r.savePending()
	}
}

func (r *runner) loadPending() {
	raw, err := os.ReadFile(filepath.Join(r.dir, "pending.json"))
	if err == nil {
		_ = json.Unmarshal(raw, &r.pending)
	}
}

func (r *runner) savePending() {
	b, _ := json.Marshal(r.pending)
	_ = os.WriteFile(filepath.Join(r.dir, "pending.json"), b, 0o600)
}

func (r *runner) retire() error {
	_ = os.Remove("/etc/modprobe.d/vaanarsena-usb.conf")
	_ = exec.Command("systemctl", "disable", "vaanarsena-agent").Run()
	return os.RemoveAll(r.dir)
}

var pkgName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.+_:-]{0,127}$`)

func (r *runner) execute(ctx context.Context, c agent.Command) agent.Result {
	res := agent.Result{CommandID: c.ID}
	p, err := command.ParseParams(c.Params)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	if r.personal && c.Type != command.Refresh && c.Type != command.ApplyPolicy && c.Type != command.Retire {
		res.Error = "refused: personal device in inventory-only mode"
		return res
	}
	var out string
	switch c.Type {
	case command.Refresh, command.ApplyPolicy:
		// Facts are sent on every check-in; policy is applied on receipt. A
		// refresh also asks for a fresh software inventory with the result.
		if c.Type == command.Refresh {
			r.inv.force = true
		}

	case command.Lock:
		out, err = sh(ctx, time.Minute, "loginctl", "lock-sessions")
	case command.Restart:
		out, err = sh(ctx, time.Minute, "shutdown", "-r", "+1", "Restart requested by IT")
	case command.Shutdown:
		out, err = sh(ctx, time.Minute, "shutdown", "-h", "+1", "Shutdown requested by IT")
	case command.OSUpdate:
		out, err = pkgManager(ctx, "upgrade", "")
	case command.InstallApp, command.RemoveApp:
		if !pkgName.MatchString(p.AppID) {
			err = fmt.Errorf("invalid package name %q", p.AppID)
			break
		}
		op := "install"
		if c.Type == command.RemoveApp {
			op = "remove"
		}
		out, err = pkgManager(ctx, op, p.AppID)
	case command.RunScript:
		out, err = sh(ctx, 10*time.Minute, "/bin/sh", "-c", p.Script)
	case command.Retire:
		out = "agent will remove itself"
	default:
		err = fmt.Errorf("command %s not supported by the Linux agent", c.Type)
	}
	res.Output = out
	if err != nil {
		res.Error = err.Error()
	} else {
		res.OK = true
	}
	return res
}

func sh(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	out := buf.String()
	if len(out) > 32<<10 {
		out = out[len(out)-32<<10:]
	}
	return out, err
}

func pkgManager(ctx context.Context, op, pkg string) (string, error) {
	type pm struct{ bin, install, remove, upgrade []string }
	managers := []pm{
		{[]string{"apt-get"}, []string{"apt-get", "install", "-y"}, []string{"apt-get", "remove", "-y"}, []string{"sh", "-c", "apt-get update && apt-get upgrade -y"}},
		{[]string{"dnf"}, []string{"dnf", "install", "-y"}, []string{"dnf", "remove", "-y"}, []string{"dnf", "upgrade", "-y"}},
		{[]string{"zypper"}, []string{"zypper", "--non-interactive", "install"}, []string{"zypper", "--non-interactive", "remove"}, []string{"zypper", "--non-interactive", "update"}},
		{[]string{"pacman"}, []string{"pacman", "-S", "--noconfirm"}, []string{"pacman", "-R", "--noconfirm"}, []string{"pacman", "-Syu", "--noconfirm"}},
	}
	for _, m := range managers {
		if _, err := exec.LookPath(m.bin[0]); err != nil {
			continue
		}
		var argv []string
		switch op {
		case "install":
			argv = append(append([]string{}, m.install...), "--", pkg)
		case "remove":
			argv = append(append([]string{}, m.remove...), "--", pkg)
		default:
			argv = m.upgrade
		}
		if m.bin[0] == "pacman" && op != "upgrade" {
			argv = append(append([]string{}, argv[:len(argv)-2]...), pkg) // pacman has no "--" separator support
		}
		return sh(ctx, 30*time.Minute, argv[0], argv[1:]...)
	}
	return "", errors.New("no supported package manager found")
}

// --- inventory ---

func readMachineID() string {
	for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		if b, err := os.ReadFile(p); err == nil && len(bytes.TrimSpace(b)) > 0 {
			return string(bytes.TrimSpace(b))
		}
	}
	h, _ := os.Hostname()
	return "host-" + h
}

func osRelease() map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			out[k] = strings.Trim(v, `"`)
		}
	}
	return out
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func memTotalMB() int {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	var kb int
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(line, "MemTotal:")), "%d", &kb)
		}
	}
	return kb / 1024
}

func diskEncrypted() bool {
	out, err := exec.Command("lsblk", "-rno", "TYPE").Output()
	return err == nil && bytes.Contains(out, []byte("crypt"))
}

func firewallActive() bool {
	for _, c := range [][]string{{"systemctl", "is-active", "--quiet", "firewalld"}, {"systemctl", "is-active", "--quiet", "ufw"}, {"systemctl", "is-active", "--quiet", "nftables"}} {
		if exec.Command(c[0], c[1:]...).Run() == nil {
			return true
		}
	}
	return false
}

func facts(personal bool) map[string]any {
	host, _ := os.Hostname()
	osr := osRelease()
	f := map[string]any{
		"hostname":      host,
		"osPretty":      osr["PRETTY_NAME"],
		"osId":          osr["ID"],
		"osVersionId":   osr["VERSION_ID"],
		"kernel":        readTrim("/proc/sys/kernel/osrelease"),
		"arch":          runtime.GOARCH,
		"memoryMB":      memTotalMB(),
		"diskEncrypted": diskEncrypted(),
		"firewall":      firewallActive(),
		"agentVersion":  version,
		"uptime":        readTrim("/proc/uptime"),
	}
	if !personal {
		f["serial"] = readTrim("/sys/class/dmi/id/product_serial")
		f["model"] = strings.TrimSpace(readTrim("/sys/class/dmi/id/sys_vendor") + " " + readTrim("/sys/class/dmi/id/product_name"))
		f["biosVersion"] = readTrim("/sys/class/dmi/id/bios_version")
	}
	return f
}

// --- policy ---

const usbBlock = "/etc/modprobe.d/vaanarsena-usb.conf"

// enforce applies what a userspace agent can safely enforce on Linux.
func enforce(doc *policy.Document) {
	if r := doc.Restrictions; r != nil && r.USBStorage != nil {
		if !*r.USBStorage {
			_ = os.WriteFile(usbBlock, []byte("# Managed by VaanarSena\ninstall usb-storage /bin/false\n"), 0o644)
		} else {
			_ = os.Remove(usbBlock)
		}
	}
}

// evaluate reports compliance with the controls the agent can observe.
func evaluate(doc *policy.Document) *agent.Compliance {
	var issues []string
	if doc.Encryption != nil && doc.Encryption.Required && !diskEncrypted() {
		issues = append(issues, "disk encryption required but no LUKS volume found")
	}
	if r := doc.Restrictions; r != nil && r.USBStorage != nil && !*r.USBStorage {
		if _, err := os.Stat(usbBlock); err != nil {
			issues = append(issues, "USB storage must be blocked")
		}
	}
	return &agent.Compliance{Compliant: len(issues) == 0, Issues: issues}
}
