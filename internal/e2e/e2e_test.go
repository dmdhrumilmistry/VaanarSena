// Package e2e runs the server in-process against a real PostgreSQL and drives
// it over HTTPS like an admin, a GitOps pipeline and Linux agents would.
//
//	VS_TEST_DATABASE_URL=postgres://vs:vs@localhost:5432/vs_test?sslmode=disable go test ./internal/e2e/
//
// The database is wiped first: point it at a throwaway database only.
package e2e

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dmdhrumilmistry/VaanarSena/internal/agent"
	"github.com/dmdhrumilmistry/VaanarSena/internal/api"
	"github.com/dmdhrumilmistry/VaanarSena/internal/auth"
	"github.com/dmdhrumilmistry/VaanarSena/internal/mdm"
	"github.com/dmdhrumilmistry/VaanarSena/internal/pki"
	"github.com/dmdhrumilmistry/VaanarSena/internal/platforms"
	"github.com/dmdhrumilmistry/VaanarSena/internal/secrets"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

type env struct {
	t      *testing.T
	srv    *httptest.Server
	st     *store.Store
	svc    *mdm.Service
	token  string
	google *fakeGoogle
}

func setup(t *testing.T) *env {
	url := os.Getenv("VS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set VS_TEST_DATABASE_URL to run end-to-end tests against PostgreSQL")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if _, err := st.DB.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	box, _ := secrets.NewBox("e2e-0123456789abcdef0123456789abcdef")
	ca, err := pki.LoadOrCreate(ctx, st, box, "E2E")
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := mdm.New(st, log)
	authn := auth.New(st, box, time.Hour, true)
	mux := http.NewServeMux()
	lin := &agent.Driver{Store: st, Svc: svc, CA: ca, Log: log}
	svc.Register(lin)
	lin.Routes(mux)

	srv := httptest.NewUnstartedServer(mux)
	srv.TLS = &tls.Config{ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: ca.Pool(), MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	google := newFakeGoogle(t)
	plat := &platforms.Manager{Store: st, Box: box, Svc: svc, CA: ca, Org: "E2E", PublicURL: srv.URL, Log: log,
		AndroidBase: google.srv.URL + "/v1/"}
	plat.Routes(mux)
	plat.Start(ctx)
	(&api.API{Store: st, Auth: authn, Svc: svc, CA: ca, PublicURL: srv.URL, Org: "E2E", Version: "test", Log: log,
		Platforms: plat}).Routes(mux)

	hash, _ := auth.HashPassword("e2e-admin-password")
	if _, err := st.CreateUser(ctx, "admin@e2e.test", "Admin", hash, store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, srv: srv, st: st, svc: svc, google: google}
	var login struct{ Token string }
	e.call("POST", "/api/v1/auth/login", `{"email":"admin@e2e.test","password":"e2e-admin-password"}`, 200, &login)
	e.token = login.Token
	return e
}

// call sends an admin request and decodes the response into out.
func (e *env) call(method, path, body string, wantCode int, out any) []byte {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if e.token != "" {
		req.Header.Set("Authorization", "Bearer "+e.token)
	}
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantCode {
		e.t.Fatalf("%s %s: got %d, want %d: %s", method, path, resp.StatusCode, wantCode, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			e.t.Fatalf("decode %s: %v: %s", path, err, raw)
		}
	}
	return raw
}

type changes struct {
	DryRun  bool `json:"dryRun"`
	Changes []struct{ Kind, Name, Action string }
}

func (c changes) actions() map[string]string {
	m := map[string]string{}
	for _, ch := range c.Changes {
		m[ch.Kind+"/"+ch.Name] = ch.Action
	}
	return m
}

// fakeAgent is a Linux agent speaking the real protocol over mTLS.
type fakeAgent struct {
	e      *env
	id     string
	client *http.Client
}

func (e *env) enrollAgent(ownership, machineID string) *fakeAgent {
	e.t.Helper()
	var tok struct{ Token string }
	e.call("POST", "/api/v1/enrollment-tokens", `{"platform":"linux","ownership":"`+ownership+`","assignee":"user@e2e.test"}`, 201, &tok)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	csr, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	body, _ := json.Marshal(agent.EnrollRequest{Token: tok.Token, CSR: csr, MachineID: machineID, Hostname: machineID, OS: "Ubuntu", OSVersion: "24.04"})
	resp, err := e.srv.Client().Post(e.srv.URL+"/agent/v1/enroll", "application/json", bytes.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var er agent.EnrollResponse
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&er) != nil {
		e.t.Fatalf("enroll failed: %d", resp.StatusCode)
	}
	block, _ := pem.Decode([]byte(er.CertificatePEM))
	keyDER, _ := x509.MarshalECPrivateKey(key)
	pair, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: block.Bytes}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		e.t.Fatal(err)
	}
	tr := e.srv.Client().Transport.(*http.Transport).Clone()
	tr.TLSClientConfig.Certificates = []tls.Certificate{pair}
	return &fakeAgent{e: e, id: er.DeviceID, client: &http.Client{Transport: tr}}
}

func (a *fakeAgent) checkin(facts map[string]any, results []agent.Result) agent.CheckinResponse {
	a.e.t.Helper()
	body, _ := json.Marshal(agent.CheckinRequest{Facts: facts, Results: results})
	resp, err := a.client.Post(a.e.srv.URL+"/agent/v1/checkin", "application/json", bytes.NewReader(body))
	if err != nil {
		a.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var cr agent.CheckinResponse
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&cr) != nil {
		a.e.t.Fatalf("checkin failed: %d", resp.StatusCode)
	}
	return cr
}

func ackAll(cmds []agent.Command) []agent.Result {
	var out []agent.Result
	for _, c := range cmds {
		out = append(out, agent.Result{CommandID: c.ID, OK: true})
	}
	return out
}

func types(cmds []agent.Command) map[string]bool {
	m := map[string]bool{}
	for _, c := range cmds {
		m[c.Type] = true
	}
	return m
}

const fleet = `
apiVersion: vaanarsena.io/v1
kind: Group
metadata: {name: lab}
spec: {kind: static}
---
apiVersion: vaanarsena.io/v1
kind: Group
metadata: {name: linux-unencrypted, description: Linux machines without LUKS}
spec:
  kind: smart
  rules:
    match: all
    conditions:
      - {field: platform, op: eq, value: linux}
      - {field: facts.linux.diskEncrypted, op: eq, value: false}
---
apiVersion: vaanarsena.io/v1
kind: Policy
metadata: {name: require-encryption}
spec:
  priority: 20
  groups: [linux-unencrypted]
  document:
    encryption: {required: true}
---
apiVersion: vaanarsena.io/v1
kind: Blueprint
metadata: {name: remediate-encryption}
spec:
  priority: 10
  groups: [linux-unencrypted]
  policies: [require-encryption]
  policy:
    restrictions: {usbStorage: false}
  onEnroll:
    - {type: run_script, params: {script: "echo remediation-guide"}}
`

func TestDeclarativeFleet(t *testing.T) {
	e := setup(t)

	// A dry run reports creations and writes nothing.
	var dry changes
	e.call("POST", "/api/v1/apply?dryRun=true&owner=git", fleet, 200, &dry)
	if !dry.DryRun || dry.actions()["Blueprint/remediate-encryption"] != "created" {
		t.Fatalf("dry run: %+v", dry)
	}
	var gl struct{ Groups []store.Group }
	e.call("GET", "/api/v1/groups", "", 200, &gl)
	if len(gl.Groups) != 0 {
		t.Fatal("dry run wrote groups")
	}

	// An invalid manifest is rejected as a whole.
	var bad struct{ Problems []string }
	e.call("POST", "/api/v1/apply", strings.Replace(fleet, "policies: [require-encryption]", "policies: [nope]", 1), 400, &bad)
	if len(bad.Problems) == 0 || !strings.Contains(bad.Problems[0], "nope") {
		t.Fatalf("problems: %v", bad.Problems)
	}

	var applied changes
	e.call("POST", "/api/v1/apply?owner=git", fleet, 200, &applied)
	for k, v := range applied.actions() {
		if v != "created" {
			t.Errorf("%s = %s, want created", k, v)
		}
	}

	// A corporate and a personal Linux machine, both unencrypted.
	corp := e.enrollAgent("corporate", "corp-1")
	byod := e.enrollAgent("personal", "byod-1")
	facts := map[string]any{"hostname": "x", "diskEncrypted": false}
	first := corp.checkin(facts, nil)
	byod.checkin(facts, nil)
	if err := e.svc.ReconcileGroups(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Both are now in the smart group.
	g, err := e.st.GroupByName(context.Background(), "linux-unencrypted")
	if err != nil {
		t.Fatal(err)
	}
	members, _ := e.st.GroupMembers(context.Background(), g.ID)
	if len(members) != 2 {
		t.Fatalf("smart group has %d members, want 2", len(members))
	}

	// The corporate device receives the blueprint's merged policy and its
	// onboarding script; the personal device gets the policy but the script
	// is refused by the BYOD guard.
	cr := corp.checkin(facts, ackAll(first.Commands))
	if !types(cr.Commands)["run_script"] {
		t.Errorf("corporate device did not get the onboarding script: %v", types(cr.Commands))
	}
	if cr.Policy == nil || cr.Policy.Encryption == nil || !cr.Policy.Encryption.Required ||
		cr.Policy.Restrictions == nil || cr.Policy.Restrictions.USBStorage == nil || *cr.Policy.Restrictions.USBStorage {
		t.Errorf("effective policy missing blueprint layers: %+v", cr.Policy)
	}
	br := byod.checkin(facts, nil)
	if types(br.Commands)["run_script"] {
		t.Error("personal device received a run_script onboarding step")
	}
	var audit struct{ Entries []store.AuditEntry }
	e.call("GET", "/api/v1/audit?limit=200", "", 200, &audit)
	skipped := false
	for _, a := range audit.Entries {
		if a.Action == "command.skipped" && a.Target == byod.id && strings.HasPrefix(a.Actor, "blueprint:") {
			skipped = true
		}
	}
	if !skipped {
		t.Error("skipped BYOD onboarding step was not audited")
	}

	// Onboarding runs once: reconciling again queues no second script.
	corp.checkin(facts, ackAll(cr.Commands))
	if err := e.svc.ReconcileGroups(context.Background()); err != nil {
		t.Fatal(err)
	}
	if types(corp.checkin(facts, nil).Commands)["run_script"] {
		t.Error("onboarding script ran twice")
	}

	// Re-applying the same manifest is a no-op.
	var again changes
	e.call("POST", "/api/v1/apply?owner=git", fleet, 200, &again)
	for k, v := range again.actions() {
		if v != "unchanged" {
			t.Errorf("re-apply: %s = %s", k, v)
		}
	}

	// Encrypting the disk takes the device out of the smart group.
	corp.checkin(map[string]any{"hostname": "x", "diskEncrypted": true}, nil)
	_ = e.svc.ReconcileGroups(context.Background())
	members, _ = e.st.GroupMembers(context.Background(), g.ID)
	if len(members) != 1 || members[0] != byod.id {
		t.Errorf("after encryption members = %v, want only the BYOD device", members)
	}

	// Smart group membership cannot be edited by hand.
	e.call("POST", "/api/v1/groups/"+g.ID+"/devices", `{"deviceId":"`+corp.id+`"}`, 409, nil)

	// Tags feed smart groups immediately.
	var tagged struct{ Groups []store.Group }
	e.call("POST", "/api/v1/groups", `{"name":"vip","kind":"smart","rules":{"match":"all","conditions":[{"field":"tags","op":"contains","value":"vip"}]}}`, 201, nil)
	e.call("PUT", "/api/v1/devices/"+corp.id+"/tags", `{"tags":["VIP","emea"]}`, 200, nil)
	e.call("GET", "/api/v1/groups?kind=smart", "", 200, &tagged)
	for _, gr := range tagged.Groups {
		if gr.Name == "vip" && gr.DeviceCount != 1 {
			t.Errorf("vip group has %d devices after tagging", gr.DeviceCount)
		}
	}

	// Preview shows who rules would match without saving.
	var pv struct{ Total int }
	e.call("POST", "/api/v1/groups/preview", `{"match":"any","conditions":[{"field":"ownership","op":"eq","value":"personal"}]}`, 200, &pv)
	if pv.Total != 1 {
		t.Errorf("preview total = %d, want 1", pv.Total)
	}

	// Prune removes only what this owner applied; the console-made "vip"
	// group survives.
	withoutBlueprint := fleet[:strings.Index(fleet, "---\napiVersion: vaanarsena.io/v1\nkind: Blueprint")]
	var pruned changes
	e.call("POST", "/api/v1/apply?owner=git&prune=true", withoutBlueprint, 200, &pruned)
	if pruned.actions()["Blueprint/remediate-encryption"] != "deleted" {
		t.Errorf("prune: %+v", pruned.actions())
	}
	if _, ok := pruned.actions()["Group/vip"]; ok {
		t.Error("prune touched a console-made group")
	}

	// Export round-trips through apply as all-unchanged.
	exported := e.call("GET", "/api/v1/export", "", 200, nil)
	var rt changes
	e.call("POST", "/api/v1/apply?dryRun=true&owner=git", string(exported), 200, &rt)
	for k, v := range rt.actions() {
		if v != "unchanged" && !strings.HasPrefix(k, "Group/vip") {
			t.Errorf("export round trip: %s = %s", k, v)
		}
	}
}

func TestBlueprintSecretsMasked(t *testing.T) {
	e := setup(t)
	e.call("POST", "/api/v1/apply", `
apiVersion: vaanarsena.io/v1
kind: Blueprint
metadata: {name: wifi}
spec:
  policy:
    wifi: [{ssid: corp, security: WPA2, password: s3cret-pass}]
`, 200, nil)
	var bl struct{ Blueprints []store.Blueprint }
	e.call("GET", "/api/v1/blueprints", "", 200, &bl)
	if strings.Contains(string(bl.Blueprints[0].Spec), "s3cret-pass") {
		t.Fatal("blueprint listing leaks the Wi-Fi passphrase")
	}
	id := bl.Blueprints[0].ID

	// Saving the masked value back keeps the stored passphrase.
	var b store.Blueprint
	e.call("GET", "/api/v1/blueprints/"+id, "", 200, &b) // admin sees it
	if !strings.Contains(string(b.Spec), "s3cret-pass") {
		t.Fatal("admin cannot read the passphrase")
	}
	e.call("PUT", "/api/v1/blueprints/"+id, `{"name":"wifi","priority":100,"groupIds":[],"spec":{"policy":{"wifi":[{"ssid":"corp","security":"WPA2","password":"********"}]}}}`, 200, nil)
	exported := string(e.call("GET", "/api/v1/export", "", 200, nil))
	if !strings.Contains(exported, "s3cret-pass") {
		t.Error("masked round trip overwrote the stored passphrase")
	}

	// Non-admins get the masked value.
	hash, _ := auth.HashPassword("auditor-password-1")
	if _, err := e.st.CreateUser(context.Background(), "aud@e2e.test", "", hash, store.RoleAuditor); err != nil {
		t.Fatal(err)
	}
	e.token = ""
	var login struct{ Token string }
	e.call("POST", "/api/v1/auth/login", `{"email":"aud@e2e.test","password":"auditor-password-1"}`, 200, &login)
	e.token = login.Token
	e.call("GET", "/api/v1/blueprints/"+id, "", 200, &b)
	if strings.Contains(string(b.Spec), "s3cret-pass") {
		t.Error("auditor can read the Wi-Fi passphrase")
	}
}

func TestOperatorsCannotApply(t *testing.T) {
	e := setup(t)
	hash, _ := auth.HashPassword("operator-password-1")
	if _, err := e.st.CreateUser(context.Background(), "op@e2e.test", "", hash, store.RoleOperator); err != nil {
		t.Fatal(err)
	}
	e.token = ""
	var login struct{ Token string }
	e.call("POST", "/api/v1/auth/login", `{"email":"op@e2e.test","password":"operator-password-1"}`, 200, &login)
	e.token = login.Token
	e.call("POST", "/api/v1/apply", fleet, 403, nil)
	e.call("GET", "/api/v1/export", "", 403, nil)
}
