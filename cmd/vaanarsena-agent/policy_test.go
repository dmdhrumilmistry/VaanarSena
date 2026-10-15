package main

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dmdhrumilmistry/VaanarSena/internal/policy"
)

// fakeHost points the policy code at a temporary root with dconf installed and
// records the tools it runs.
func fakeHost(t *testing.T, withDconf bool) *[]string {
	t.Helper()
	oldRoot, oldHave, oldRun, oldEnc := fsRoot, haveTool, runTool, encrypted
	t.Cleanup(func() { fsRoot, haveTool, runTool, encrypted = oldRoot, oldHave, oldRun, oldEnc })
	fsRoot = t.TempDir()
	var ran []string
	haveTool = func(name string) bool { return name == "dconf" && withDconf }
	runTool = func(name string, args ...string) error {
		ran = append(ran, name+" "+strings.Join(args, " "))
		if dconfFails {
			return errors.New("dconf: failed")
		}
		// A real dconf update compiles local.d into the database file.
		return writeIfChanged(rooted(dconfDB), time.Now().String())
	}
	dconfFails = false
	encrypted = func() bool { return true }
	return &ran
}

// dconfFails makes the fake dconf update fail.
var dconfFails bool

func exists(p string) bool { _, err := os.Stat(rooted(p)); return err == nil }

func no() *bool { b := false; return &b }

func TestUSBBlockFollowsPolicy(t *testing.T) {
	fakeHost(t, false)
	block := &policy.Document{Restrictions: &policy.Restrictions{USBStorage: no()}}
	enforce(block, false)
	if !exists(usbBlock) {
		t.Fatal("usb block not written")
	}
	if c := evaluate(block, false); !c.Compliant {
		t.Fatalf("compliance after enforce: %+v", c)
	}
	// A policy that stops managing USB storage must lift the block.
	enforce(&policy.Document{}, false)
	if exists(usbBlock) {
		t.Fatal("usb block left behind after the policy stopped managing it")
	}
	if c := evaluate(block, false); c.Compliant {
		t.Fatal("missing block reported compliant")
	}
}

func TestPersonalDevicesAreNotEnforced(t *testing.T) {
	fakeHost(t, true)
	doc := &policy.Document{
		Restrictions: &policy.Restrictions{USBStorage: no()},
		Passcode:     &policy.Passcode{Required: true, MaxInactivityMinutes: 5},
	}
	enforce(doc, false)
	if !exists(usbBlock) || !exists(dconfKeyfile) {
		t.Fatal("corporate policy not enforced")
	}
	// Becoming personal removes what was applied, and the device is not
	// reported non-compliant for controls the agent does not apply to it.
	enforce(doc, true)
	if exists(usbBlock) || exists(dconfKeyfile) || exists(dconfLocks) {
		t.Fatal("corporate settings left on a personal device")
	}
	if c := evaluate(doc, true); !c.Compliant {
		t.Fatalf("personal device non-compliant for unenforced controls: %+v", c.Issues)
	}
	encrypted = func() bool { return false }
	doc.Encryption = &policy.Encryption{Required: true}
	if c := evaluate(doc, true); c.Compliant {
		t.Fatal("encryption is observed on personal devices too and must be reported")
	}
}

func TestScreenLockThroughDconf(t *testing.T) {
	ran := fakeHost(t, true)
	doc := &policy.Document{Passcode: &policy.Passcode{Required: true, MaxInactivityMinutes: 10}}
	if c := evaluate(doc, false); c.Compliant {
		t.Fatal("unapplied screen lock reported compliant")
	}
	enforce(doc, false)
	kf := readFile(rooted(dconfKeyfile))
	for _, want := range []string{"idle-delay=uint32 600", "lock-enabled=true", "[org/gnome/desktop/screensaver]"} {
		if !strings.Contains(kf, want) {
			t.Errorf("keyfile lacks %q:\n%s", want, kf)
		}
	}
	if !strings.Contains(readFile(rooted(dconfLocks)), "/org/gnome/desktop/session/idle-delay") {
		t.Error("idle-delay not locked")
	}
	if !strings.Contains(readFile(rooted(dconfProfile)), "system-db:local") {
		t.Error("profile not created")
	}
	if c := evaluate(doc, false); !c.Compliant {
		t.Fatalf("applied screen lock non-compliant: %+v", c.Issues)
	}
	if len(*ran) != 1 || (*ran)[0] != "dconf update" {
		t.Fatalf("ran = %v, want one dconf update", *ran)
	}
	// Re-applying an unchanged policy does not rebuild the database.
	enforce(doc, false)
	if len(*ran) != 1 {
		t.Fatalf("dconf update re-run for an unchanged policy: %v", *ran)
	}
	enforce(&policy.Document{}, false)
	if exists(dconfKeyfile) || exists(dconfLocks) || len(*ran) != 2 {
		t.Fatalf("screen lock not removed (ran %v)", *ran)
	}
}

func TestExistingDconfProfileIsKept(t *testing.T) {
	fakeHost(t, true)
	if err := writeIfChanged(rooted(dconfProfile), "user-db:user\n"); err != nil {
		t.Fatal(err)
	}
	doc := &policy.Document{Passcode: &policy.Passcode{Required: true}}
	enforce(doc, false)
	if readFile(rooted(dconfProfile)) != "user-db:user\n" {
		t.Fatal("an existing dconf profile was rewritten")
	}
	c := evaluate(doc, false)
	if c.Compliant || !strings.Contains(strings.Join(c.Issues, ";"), "system-db:local") {
		t.Fatalf("a profile without the system database must be reported: %+v", c)
	}
}

func TestNoDesktopNoScreenLockIssue(t *testing.T) {
	fakeHost(t, false)
	doc := &policy.Document{Passcode: &policy.Passcode{Required: true, MaxInactivityMinutes: 5}}
	enforce(doc, false)
	if exists(dconfKeyfile) {
		t.Fatal("dconf files written on a machine without dconf")
	}
	if c := evaluate(doc, false); !c.Compliant {
		t.Fatalf("headless machine non-compliant for screen lock: %+v", c.Issues)
	}
}

func TestFailedDconfUpdateIsRetriedAndReported(t *testing.T) {
	ran := fakeHost(t, true)
	doc := &policy.Document{Passcode: &policy.Passcode{Required: true, MaxInactivityMinutes: 5}}
	dconfFails = true
	if errs := enforce(doc, false); len(errs) != 1 {
		t.Fatalf("errs = %v, want the dconf failure", errs)
	}
	// The files are in place but not compiled: the lock is not enforced.
	if c := evaluate(doc, false); c.Compliant {
		t.Fatal("uncompiled screen lock reported compliant")
	}
	// The next enforcement retries even though the files did not change.
	dconfFails = false
	if errs := enforce(doc, false); len(errs) != 0 {
		t.Fatalf("retry: %v", errs)
	}
	if len(*ran) != 2 {
		t.Fatalf("ran = %v, want a retry of dconf update", *ran)
	}
	if c := evaluate(doc, false); !c.Compliant {
		t.Fatalf("compiled screen lock not compliant: %v", c.Issues)
	}
}
