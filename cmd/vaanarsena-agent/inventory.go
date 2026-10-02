package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/dmdhrumilmistry/VaanarSena/internal/agent"
)

// Software inventory. The agent lists packages from every package manager it
// finds and the systemd services, and sends them when they change, once a day
// and after a refresh. Personal (BYOD) machines never collect it.
const (
	collectEvery  = 15 * time.Minute // how often to look for changes
	resendEvery   = 24 * time.Hour   // report even if nothing changed
	collectLimit  = 20 * time.Second // per external command
	maxItemsKind  = 5000
	maxOutputByte = 16 << 20
)

// collect runs a read-only command and returns its standard output. A missing
// tool, a failure or a timeout yields ok == false, so the agent keeps working
// on systems without some package managers.
func collect(ctx context.Context, name string, args ...string) (string, bool) {
	if _, err := exec.LookPath(name); err != nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(ctx, collectLimit)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var buf bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &buf, left: maxOutputByte}
	if err := cmd.Run(); err != nil && buf.Len() == 0 {
		return "", false
	}
	return buf.String(), true
}

// limitedWriter keeps the first left bytes and discards the rest.
type limitedWriter struct {
	w    *bytes.Buffer
	left int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	n := len(p)
	if l.left <= 0 {
		return n, nil
	}
	if len(p) > l.left {
		p = p[:l.left]
	}
	l.left -= len(p)
	l.w.Write(p)
	return n, nil
}

// collectInventory gathers packages and services. A list is nil when no tool
// able to produce it exists, so the server keeps its previous copy.
func collectInventory(ctx context.Context) *agent.Inventory {
	var apps []agent.InventoryItem
	if out, ok := collect(ctx, "dpkg-query", "-W", "-f", "${Package}\t${Version}\t${Maintainer}\t${db:Status-Abbrev}\n"); ok {
		apps = append(apps, parseDpkg(out)...)
	}
	if out, ok := collect(ctx, "rpm", "-qa", "--qf", "%{NAME}\t%{VERSION}-%{RELEASE}\t%{VENDOR}\n"); ok {
		apps = append(apps, parseRPM(out)...)
	}
	if out, ok := collect(ctx, "pacman", "-Q"); ok {
		apps = append(apps, parsePacman(out)...)
	}
	if out, ok := collect(ctx, "apk", "info", "-v"); ok {
		apps = append(apps, parseApk(out)...)
	}
	if out, ok := collect(ctx, "flatpak", "list", "--app", "--columns=application,name,version,origin"); ok {
		apps = append(apps, parseFlatpak(out)...)
	}
	if out, ok := collect(ctx, "snap", "list"); ok {
		apps = append(apps, parseSnap(out)...)
	}
	var services []agent.InventoryItem
	if units, ok := collect(ctx, "systemctl", "list-units", "--type=service", "--all", "--no-legend", "--plain", "--no-pager"); ok {
		files, _ := collect(ctx, "systemctl", "list-unit-files", "--type=service", "--no-legend", "--no-pager")
		services = parseServices(units, files)
	}
	inv := &agent.Inventory{Apps: capItems(apps), Services: capItems(services)}
	inv.Hash = inventoryHash(inv)
	return inv
}

func capItems(items []agent.InventoryItem) []agent.InventoryItem {
	if len(items) > maxItemsKind {
		items = items[:maxItemsKind]
	}
	return items
}

// inventoryHash identifies the content regardless of listing order.
func inventoryHash(inv *agent.Inventory) string {
	var lines []string
	for _, kind := range []struct {
		tag   string
		items []agent.InventoryItem
	}{{"a", inv.Apps}, {"s", inv.Services}} {
		for _, it := range kind.items {
			lines = append(lines, strings.Join([]string{kind.tag, it.Source, it.Name, it.Identifier, it.Version, it.State, it.Details["enabled"]}, "\x1f"))
		}
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:8])
}

// inventoryState decides when inventory is sent. It lives in the runner and is
// not persisted: a restarted agent reports once and resumes from there.
type inventoryState struct {
	lastCollect time.Time
	lastSent    time.Time
	sentHash    string
	pending     *agent.Inventory // collected, awaiting a successful check-in
	force       bool             // a refresh asked for a fresh report
}

// next returns the inventory to attach to a check-in, or nil. It never
// collects in personal mode, and not before the server has said whether the
// device is personal (the first response).
func (s *inventoryState) next(ctx context.Context, now time.Time, personal, known bool, collectFn func(context.Context) *agent.Inventory) *agent.Inventory {
	if personal || !known {
		s.pending, s.force = nil, false
		return nil
	}
	if s.pending != nil {
		return s.pending // the last attempt did not reach the server
	}
	if !s.force && !s.lastCollect.IsZero() && now.Sub(s.lastCollect) < collectEvery {
		return nil
	}
	inv := collectFn(ctx)
	s.lastCollect = now
	due := s.force || s.lastSent.IsZero() || now.Sub(s.lastSent) >= resendEvery || inv.Hash != s.sentHash
	if !due || (inv.Apps == nil && inv.Services == nil) {
		return nil
	}
	s.pending = inv
	return inv
}

// sent records a successful delivery.
func (s *inventoryState) sent(now time.Time) {
	if s.pending == nil {
		return
	}
	s.lastSent, s.sentHash = now, s.pending.Hash
	s.pending, s.force = nil, false
}

func item(source, name, version, publisher string) agent.InventoryItem {
	return agent.InventoryItem{Name: name, Identifier: name, Version: version, Publisher: publisher, Source: source}
}

// cleanPublisher drops an email address and non-ASCII marks from a maintainer
// or vendor string.
func cleanPublisher(s string) string {
	if i := strings.Index(s, " <"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r > 127 {
			return -1
		}
		return r
	}, s))
	return strings.TrimRight(s, "* ")
}

// parseDpkg reads dpkg-query output: package, version, maintainer, status
// abbreviation. Only installed packages (status "ii" or held "hi") count.
func parseDpkg(out string) []agent.InventoryItem {
	var items []agent.InventoryItem
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 4 || f[0] == "" {
			continue
		}
		if st := strings.TrimSpace(f[3]); st != "ii" && st != "hi" {
			continue
		}
		items = append(items, item("dpkg", f[0], f[1], cleanPublisher(f[2])))
	}
	return items
}

// parseRPM reads rpm -qa output: name, version-release, vendor.
func parseRPM(out string) []agent.InventoryItem {
	var items []agent.InventoryItem
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 2 || f[0] == "" || f[0] == "gpg-pubkey" {
			continue
		}
		vendor := ""
		if len(f) > 2 && f[2] != "(none)" {
			vendor = cleanPublisher(f[2])
		}
		items = append(items, item("rpm", f[0], f[1], vendor))
	}
	return items
}

// parsePacman reads pacman -Q output: "name version".
func parsePacman(out string) []agent.InventoryItem {
	var items []agent.InventoryItem
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			items = append(items, item("pacman", f[0], f[1], ""))
		}
	}
	return items
}

// parseApk reads apk info -v output: "name-version-rN". The version starts at
// the last-but-one dash.
func parseApk(out string) []agent.InventoryItem {
	var items []agent.InventoryItem
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		r := strings.LastIndex(line, "-")
		if r <= 0 {
			continue
		}
		v := strings.LastIndex(line[:r], "-")
		if v <= 0 {
			continue
		}
		items = append(items, item("apk", line[:v], line[v+1:], ""))
	}
	return items
}

// parseFlatpak reads flatpak list output: application ID, name, version, origin.
func parseFlatpak(out string) []agent.InventoryItem {
	var items []agent.InventoryItem
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 2 || strings.TrimSpace(f[0]) == "" {
			continue
		}
		it := agent.InventoryItem{Name: strings.TrimSpace(f[1]), Identifier: strings.TrimSpace(f[0]), Source: "flatpak"}
		if it.Name == "" {
			it.Name = it.Identifier
		}
		if len(f) > 2 {
			it.Version = strings.TrimSpace(f[2])
		}
		if len(f) > 3 {
			it.Publisher = strings.TrimSpace(f[3])
		}
		items = append(items, it)
	}
	return items
}

// parseSnap reads snap list output, skipping the header.
func parseSnap(out string) []agent.InventoryItem {
	var items []agent.InventoryItem
	for i, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if i == 0 && len(f) > 0 && f[0] == "Name" {
			continue
		}
		if len(f) < 3 {
			continue
		}
		pub := ""
		if len(f) >= 5 {
			pub = cleanPublisher(f[4])
		}
		items = append(items, item("snap", f[0], f[1], pub))
	}
	return items
}

// parseServices combines systemctl list-units (active state) with
// list-unit-files (enabled state).
func parseServices(units, files string) []agent.InventoryItem {
	enabled := map[string]string{}
	for _, line := range strings.Split(files, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 {
			enabled[f[0]] = f[1]
		}
	}
	var items []agent.InventoryItem
	for _, line := range strings.Split(units, "\n") {
		f := strings.Fields(line)
		// UNIT LOAD ACTIVE SUB DESCRIPTION...
		if len(f) < 4 || !strings.HasSuffix(f[0], ".service") || f[1] == "not-found" {
			continue
		}
		state := "stopped"
		switch {
		case f[2] == "failed":
			state = "failed"
		case f[3] == "running":
			state = "running"
		}
		det := map[string]string{"sub": f[3]}
		if e := enabled[f[0]]; e != "" {
			det["enabled"] = e
		}
		if len(f) > 4 {
			det["description"] = strings.Join(f[4:], " ")
		}
		items = append(items, agent.InventoryItem{
			Name: strings.TrimSuffix(f[0], ".service"), Identifier: f[0], Source: "systemd", State: state, Details: det,
		})
	}
	return items
}
