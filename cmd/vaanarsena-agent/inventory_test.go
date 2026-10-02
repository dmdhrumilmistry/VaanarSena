package main

import (
	"context"
	"testing"
	"time"

	"github.com/dmdhrumilmistry/VaanarSena/internal/agent"
)

func TestParseDpkg(t *testing.T) {
	out := "openssl\t3.0.13-0ubuntu3.4\tUbuntu Developers <ubuntu-devel-discuss@lists.ubuntu.com>\tii \n" +
		"curl\t8.5.0-2ubuntu10.2\tUbuntu Developers <ubuntu-devel-discuss@lists.ubuntu.com>\tii \n" +
		"oldpkg\t1.0\tSomeone <a@b.c>\trc \n" +
		"held\t2.0\tMaint\thi \n" +
		"\n"
	items := parseDpkg(out)
	if len(items) != 3 {
		t.Fatalf("items = %+v (removed-but-configured packages must be skipped)", items)
	}
	if o := items[0]; o.Name != "openssl" || o.Identifier != "openssl" || o.Version != "3.0.13-0ubuntu3.4" ||
		o.Publisher != "Ubuntu Developers" || o.Source != "dpkg" {
		t.Errorf("openssl: %+v", o)
	}
}

func TestParseRPM(t *testing.T) {
	out := "gpg-pubkey\t105ef944-65ca83d1\t(none)\n" +
		"kernel-core\t6.11.4-301.fc41\tFedora Project\n" +
		"firefox\t131.0-1.fc41\tFedora Project\n" +
		"localpkg\t1.0-1\t(none)\n"
	items := parseRPM(out)
	if len(items) != 3 {
		t.Fatalf("items = %+v (gpg-pubkey is not software)", items)
	}
	if k := items[0]; k.Name != "kernel-core" || k.Version != "6.11.4-301.fc41" || k.Publisher != "Fedora Project" || k.Source != "rpm" {
		t.Errorf("kernel: %+v", k)
	}
	if items[2].Publisher != "" {
		t.Errorf("(none) vendor kept: %+v", items[2])
	}
}

func TestParsePacman(t *testing.T) {
	items := parsePacman("linux 6.11.5.arch1-1\nopenssl 3.3.2-1\n\nbroken\n")
	if len(items) != 2 || items[1].Name != "openssl" || items[1].Version != "3.3.2-1" || items[1].Source != "pacman" {
		t.Errorf("items = %+v", items)
	}
}

func TestParseApk(t *testing.T) {
	items := parseApk("musl-1.2.5-r0\nca-certificates-bundle-20240705-r0\nlibssl3-3.3.2-r0\n")
	if len(items) != 3 {
		t.Fatalf("items = %+v", items)
	}
	if c := items[1]; c.Name != "ca-certificates-bundle" || c.Version != "20240705-r0" || c.Source != "apk" {
		t.Errorf("bundle: %+v", c)
	}
}

func TestParseFlatpak(t *testing.T) {
	items := parseFlatpak("org.mozilla.firefox\tFirefox\t131.0\tflathub\ncom.slack.Slack\tSlack\t4.39.95\tflathub\n")
	if len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}
	if f := items[0]; f.Identifier != "org.mozilla.firefox" || f.Name != "Firefox" || f.Version != "131.0" || f.Publisher != "flathub" || f.Source != "flatpak" {
		t.Errorf("firefox: %+v", f)
	}
}

func TestParseSnap(t *testing.T) {
	out := "Name      Version        Rev    Tracking       Publisher   Notes\n" +
		"core22    20240111       1380   latest/stable  canonical** base\n" +
		"firefox   131.0.2-1      5187   latest/stable  mozilla**   -\n" +
		"hello     2.10           42     latest/stable  -           -\n"
	items := parseSnap(out)
	if len(items) != 3 {
		t.Fatalf("items = %+v (the header must be skipped)", items)
	}
	if f := items[1]; f.Name != "firefox" || f.Version != "131.0.2-1" || f.Publisher != "mozilla" || f.Source != "snap" {
		t.Errorf("firefox: %+v", f)
	}
}

func TestParseServices(t *testing.T) {
	units := "ssh.service                loaded    active   running OpenBSD Secure Shell server\n" +
		"cron.service               loaded    active   running Regular background program processing daemon\n" +
		"apt-daily.service          loaded    inactive dead    Daily apt download activities\n" +
		"cloud-init.service         loaded    active   exited  Initial cloud-init job\n" +
		"nginx.service              loaded    failed   failed  A high performance web server\n" +
		"ghost.service              not-found inactive dead    ghost.service\n" +
		"session-1.scope            loaded    active   running Session 1 of User dev\n"
	files := "ssh.service                enabled         enabled\n" +
		"cron.service               enabled         enabled\n" +
		"apt-daily.service          static          -\n" +
		"nginx.service              disabled        enabled\n" +
		"getty@.service             enabled         enabled\n"
	items := parseServices(units, files)
	if len(items) != 5 {
		t.Fatalf("items = %+v (not-found units and non-services must be skipped)", items)
	}
	want := map[string]string{"ssh": "running", "cron": "running", "apt-daily": "stopped", "cloud-init": "stopped", "nginx": "failed"}
	for _, it := range items {
		if want[it.Name] != it.State {
			t.Errorf("%s state = %s, want %s", it.Name, it.State, want[it.Name])
		}
		if it.Source != "systemd" || it.Identifier != it.Name+".service" {
			t.Errorf("%s: %+v", it.Name, it)
		}
	}
	if ssh := items[0]; ssh.Details["enabled"] != "enabled" || ssh.Details["description"] != "OpenBSD Secure Shell server" || ssh.Details["sub"] != "running" {
		t.Errorf("ssh details: %v", ssh.Details)
	}
	if items[2].Details["enabled"] != "static" {
		t.Errorf("apt-daily details: %v", items[2].Details)
	}
}

func TestInventoryHashIgnoresOrder(t *testing.T) {
	a := &agent.Inventory{Apps: []agent.InventoryItem{item("dpkg", "a", "1", ""), item("dpkg", "b", "2", "")}}
	b := &agent.Inventory{Apps: []agent.InventoryItem{item("dpkg", "b", "2", ""), item("dpkg", "a", "1", "")}}
	c := &agent.Inventory{Apps: []agent.InventoryItem{item("dpkg", "a", "1", ""), item("dpkg", "b", "3", "")}}
	if inventoryHash(a) != inventoryHash(b) {
		t.Error("hash depends on order")
	}
	if inventoryHash(a) == inventoryHash(c) {
		t.Error("hash ignores a version change")
	}
}

func TestInventorySchedule(t *testing.T) {
	ctx := context.Background()
	collected := 0
	inv := &agent.Inventory{Apps: []agent.InventoryItem{item("dpkg", "curl", "8", "")}, Hash: "h1"}
	collectFn := func(context.Context) *agent.Inventory { collected++; return inv }
	var s inventoryState
	now := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)

	// Before the server has said whether the device is personal: nothing.
	if s.next(ctx, now, false, false, collectFn) != nil || collected != 0 {
		t.Fatal("inventory collected before the personal flag is known")
	}
	// First report.
	if s.next(ctx, now, false, true, collectFn) == nil {
		t.Fatal("no first inventory")
	}
	// Not delivered yet: the same report is offered again without collecting.
	if s.next(ctx, now.Add(time.Second), false, true, collectFn) == nil || collected != 1 {
		t.Fatalf("pending report dropped or recollected (collected=%d)", collected)
	}
	s.sent(now)
	// Unchanged and recent: nothing, and no collection inside the interval.
	if s.next(ctx, now.Add(time.Minute), false, true, collectFn) != nil || collected != 1 {
		t.Fatal("inventory resent or recollected too soon")
	}
	// Collected again after the interval, but unchanged: still nothing.
	if s.next(ctx, now.Add(collectEvery+time.Second), false, true, collectFn) != nil || collected != 2 {
		t.Fatalf("unchanged inventory resent (collected=%d)", collected)
	}
	// A change is sent.
	inv = &agent.Inventory{Apps: []agent.InventoryItem{item("dpkg", "curl", "9", "")}, Hash: "h2"}
	t2 := now.Add(2*collectEvery + time.Minute)
	if s.next(ctx, t2, false, true, collectFn) == nil {
		t.Fatal("changed inventory not sent")
	}
	s.sent(t2)
	// A refresh forces a report even when nothing changed.
	s.force = true
	t3 := t2.Add(time.Minute)
	if s.next(ctx, t3, false, true, collectFn) == nil {
		t.Fatal("refresh did not force a report")
	}
	s.sent(t3)
	// Unchanged for a day: resent.
	t4 := t3.Add(resendEvery + time.Minute)
	if s.next(ctx, t4, false, true, collectFn) == nil {
		t.Fatal("daily resend missing")
	}
}

func TestInventoryNeverInPersonalMode(t *testing.T) {
	collected := 0
	collectFn := func(context.Context) *agent.Inventory {
		collected++
		return &agent.Inventory{Apps: []agent.InventoryItem{item("dpkg", "curl", "8", "")}, Hash: "h"}
	}
	s := inventoryState{force: true}
	if s.next(context.Background(), time.Now(), true, true, collectFn) != nil {
		t.Error("personal agent produced inventory")
	}
	if collected != 0 {
		t.Error("personal agent ran the collectors")
	}
	if s.force {
		t.Error("a pending refresh request survived into personal mode")
	}
}

func TestCollectSkipsMissingTools(t *testing.T) {
	if out, ok := collect(context.Background(), "definitely-not-a-real-tool-xyz"); ok || out != "" {
		t.Error("missing tool reported as collected")
	}
}
