package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dmdhrumilmistry/VaanarSena/internal/agent"
	"github.com/dmdhrumilmistry/VaanarSena/internal/policy"
)

// fsRoot prefixes every path the policy code writes, so tests can run against
// a temporary directory. It is "" in production.
var fsRoot = ""

// Seams for tests: whether a tool is installed, and running it.
var (
	haveTool = func(name string) bool { _, err := exec.LookPath(name); return err == nil }
	runTool  = func(name string, args ...string) error { return exec.Command(name, args...).Run() }
	// encrypted reports whether a LUKS volume is present.
	encrypted = diskEncrypted
)

func rooted(p string) string { return filepath.Join(fsRoot, p) }

const managedHeader = "# Managed by VaanarSena. Local changes are overwritten.\n"

// usbBlock stops the usb-storage kernel module from loading.
const usbBlock = "/etc/modprobe.d/vaanarsena-usb.conf"

const usbBlockContent = managedHeader + "install usb-storage /bin/false\n"

// Screen lock is enforced for GNOME through the system dconf database, as the
// GNOME system administration guide describes: a keyfile in a local.d
// directory sets the values and a locks file stops users changing them.
// Desktops other than GNOME (and machines without dconf) are not enforced.
const (
	dconfProfile = "/etc/dconf/profile/user"
	dconfKeyfile = "/etc/dconf/db/local.d/00-vaanarsena"
	dconfLocks   = "/etc/dconf/db/local.d/locks/vaanarsena"
)

const dconfLockContent = managedHeader +
	"/org/gnome/desktop/session/idle-delay\n" +
	"/org/gnome/desktop/screensaver/lock-enabled\n" +
	"/org/gnome/desktop/screensaver/lock-delay\n"

// screenLockKeyfile returns the dconf keyfile for a passcode policy, or "" when
// the policy does not manage the screen lock.
func screenLockKeyfile(p *policy.Passcode) string {
	if p == nil || (!p.Required && p.MaxInactivityMinutes == 0) {
		return ""
	}
	var b strings.Builder
	b.WriteString(managedHeader)
	if p.MaxInactivityMinutes > 0 {
		fmt.Fprintf(&b, "[org/gnome/desktop/session]\nidle-delay=uint32 %d\n\n", p.MaxInactivityMinutes*60)
	}
	b.WriteString("[org/gnome/desktop/screensaver]\nlock-enabled=true\nlock-delay=uint32 0\n")
	return b.String()
}

// enforce applies what the agent can enforce on Linux and removes what the
// policy no longer asks for. Only files the agent owns are written or removed.
// Personal devices get nothing enforced (inventory-only mode), so anything a
// corporate policy left behind is removed when a device becomes personal.
func enforce(doc *policy.Document, personal bool) []error {
	var errs []error
	keep := func(path, content string) {
		if err := writeIfChanged(rooted(path), content); err != nil {
			errs = append(errs, err)
		}
	}
	drop := func(path string) {
		if err := os.Remove(rooted(path)); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	if doc == nil || personal {
		doc = &policy.Document{}
	}

	if r := doc.Restrictions; r != nil && r.USBStorage != nil && !*r.USBStorage {
		keep(usbBlock, usbBlockContent)
	} else {
		drop(usbBlock)
	}

	if haveTool("dconf") {
		before := readFile(rooted(dconfKeyfile)) + readFile(rooted(dconfLocks))
		if kf := screenLockKeyfile(doc.Passcode); kf != "" {
			if _, err := os.Stat(rooted(dconfProfile)); os.IsNotExist(err) {
				keep(dconfProfile, "user-db:user\nsystem-db:local\n")
			}
			keep(dconfKeyfile, kf)
			keep(dconfLocks, dconfLockContent)
		} else {
			drop(dconfKeyfile)
			drop(dconfLocks)
		}
		// Rebuild the database when our files changed, or when it is older
		// than them (an earlier dconf update failed): the files alone do not
		// lock anything until they are compiled.
		changed := readFile(rooted(dconfKeyfile))+readFile(rooted(dconfLocks)) != before
		if changed || !dconfCompiled() {
			if err := runTool("dconf", "update"); err != nil {
				errs = append(errs, fmt.Errorf("dconf update: %w", err))
			}
		}
	}
	return errs
}

// dconfDB is the database dconf update compiles from local.d.
const dconfDB = "/etc/dconf/db/local"

// dconfCompiled reports whether the compiled database is at least as new as
// the agent's keyfile and locks. With neither file present there is nothing
// of ours to compile.
func dconfCompiled() bool {
	db, err := os.Stat(rooted(dconfDB))
	for _, p := range []string{dconfKeyfile, dconfLocks} {
		fi, ferr := os.Stat(rooted(p))
		if ferr != nil {
			continue
		}
		if err != nil || db.ModTime().Before(fi.ModTime()) {
			return false
		}
	}
	return true
}

// evaluate reports compliance with the controls the agent can observe. On
// personal devices only what is observed, not enforced, is checked: reporting
// a control the agent deliberately does not apply would mark every personal
// machine non-compliant.
func evaluate(doc *policy.Document, personal bool) *agent.Compliance {
	var issues []string
	if doc.Encryption != nil && doc.Encryption.Required && !encrypted() {
		issues = append(issues, "disk encryption required but no LUKS volume found")
	}
	if !personal {
		if r := doc.Restrictions; r != nil && r.USBStorage != nil && !*r.USBStorage &&
			readFile(rooted(usbBlock)) != usbBlockContent {
			issues = append(issues, "USB storage must be blocked")
		}
		if kf := screenLockKeyfile(doc.Passcode); kf != "" && haveTool("dconf") {
			if readFile(rooted(dconfKeyfile)) != kf || readFile(rooted(dconfLocks)) != dconfLockContent || !dconfCompiled() {
				issues = append(issues, "screen lock settings are not applied")
			} else if !strings.Contains(readFile(rooted(dconfProfile)), "system-db:local") {
				issues = append(issues, dconfProfile+" does not include system-db:local, so the screen lock is not enforced")
			}
		}
	}
	return &agent.Compliance{Compliant: len(issues) == 0, Issues: issues}
}

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// writeIfChanged writes content to path, creating parent directories, unless
// the file already holds exactly that content.
func writeIfChanged(path, content string) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, []byte(content)) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
