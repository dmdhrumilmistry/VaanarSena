// Package command defines the platform-neutral command catalogue and the rules
// that decide who may send which command to which device. The BYOD guard lives
// here, in the server, so no client or UI bug can bypass it.
package command

import (
	"encoding/json"
	"fmt"

	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

// Command types.
const (
	Refresh         = "refresh"        // re-read inventory
	ApplyPolicy     = "apply_policy"   // push the effective policy
	Lock            = "lock"           // lock the screen
	Restart         = "restart"        // reboot
	Shutdown        = "shutdown"       // power off
	ClearPasscode   = "clear_passcode" // remove the unlock passcode
	EnableLostMode  = "enable_lost_mode"
	DisableLostMode = "disable_lost_mode"
	Locate          = "locate"
	OSUpdate        = "os_update"
	InstallApp      = "install_app"
	RemoveApp       = "remove_app"
	RunScript       = "run_script" // Linux agent only
	Retire          = "retire"     // remove management and managed data only
	Wipe            = "wipe"       // factory reset
)

// Spec describes a command.
type Spec struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	MinRole     string `json:"minRole"`
	Destructive bool   `json:"destructive"`
	// Personal reports whether the command may target a BYOD device.
	Personal  bool     `json:"personal"`
	Platforms []string `json:"platforms"`
}

var (
	apple   = []string{store.PlatformIOS, store.PlatformIPadOS, store.PlatformMacOS}
	all     = []string{store.PlatformIOS, store.PlatformIPadOS, store.PlatformMacOS, store.PlatformWindows, store.PlatformAndroid, store.PlatformChromeOS, store.PlatformLinux}
	agentOS = []string{store.PlatformLinux}
)

func join(a ...[]string) []string {
	var out []string
	for _, s := range a {
		out = append(out, s...)
	}
	return out
}

// Catalogue lists every command.
var Catalogue = []Spec{
	{Refresh, "Refresh inventory", store.RoleOperator, false, true, all},
	{ApplyPolicy, "Re-apply effective policy", store.RoleOperator, false, true,
		join(apple, []string{store.PlatformWindows, store.PlatformAndroid, store.PlatformLinux})},
	// Android apps are managed declaratively through the policy apps list.
	{InstallApp, "Install a managed app", store.RoleOperator, false, true,
		join(apple, []string{store.PlatformWindows, store.PlatformLinux})},
	{RemoveApp, "Remove a managed app", store.RoleOperator, false, true,
		join(apple, []string{store.PlatformWindows, store.PlatformLinux})},
	// Windows desktop has no remote lock (the RemoteLock CSP is mobile/HoloLens only).
	{Lock, "Lock the device", store.RoleOperator, false, false,
		join(apple, []string{store.PlatformAndroid, store.PlatformChromeOS, store.PlatformLinux})},
	{Restart, "Restart the device", store.RoleOperator, false, false, all},
	{Shutdown, "Shut down the device", store.RoleOperator, false, false,
		join(apple, []string{store.PlatformLinux})},
	{ClearPasscode, "Clear the device passcode", store.RoleAdmin, false, false,
		[]string{store.PlatformIOS, store.PlatformIPadOS, store.PlatformAndroid}},
	{EnableLostMode, "Enable lost mode", store.RoleOperator, false, false, []string{store.PlatformIOS, store.PlatformIPadOS}},
	{DisableLostMode, "Disable lost mode", store.RoleOperator, false, false, []string{store.PlatformIOS, store.PlatformIPadOS}},
	{Locate, "Report device location (lost mode only on Apple)", store.RoleAdmin, false, false, []string{store.PlatformIOS, store.PlatformIPadOS}},
	// Windows updates are governed by the osUpdates policy section instead.
	{OSUpdate, "Install available OS updates", store.RoleOperator, false, false,
		join(apple, []string{store.PlatformLinux})},
	{RunScript, "Run a shell script as root", store.RoleAdmin, true, false, agentOS},
	{Retire, "Remove management and corporate data", store.RoleAdmin, true, true, all},
	// Linux has no factory reset; retire removes the agent and its state.
	{Wipe, "Factory reset, erasing all data", store.RoleAdmin, true, false,
		join(apple, []string{store.PlatformWindows, store.PlatformAndroid, store.PlatformChromeOS})},
}

var byType = func() map[string]Spec {
	m := map[string]Spec{}
	for _, s := range Catalogue {
		m[s.Type] = s
	}
	return m
}()

// Lookup returns the spec for a command type.
func Lookup(typ string) (Spec, bool) { s, ok := byType[typ]; return s, ok }

// Params carried by commands.
type Params struct {
	// Message and Phone are shown on a locked / lost device.
	Message string `json:"message,omitempty"`
	Phone   string `json:"phone,omitempty"`
	// PIN is the six-digit find-my lock PIN required by macOS DeviceLock and
	// EraseDevice.
	PIN string `json:"pin,omitempty"`
	// AppID identifies the app for install_app/remove_app.
	AppID string `json:"appId,omitempty"`
	// URL is a manifest (Apple), MSI (Windows) or package URL.
	URL string `json:"url,omitempty"`
	// Hash and Version describe a Windows MSI: the SHA-256 of the file and the
	// product version, both required by the EnterpriseDesktopAppManagement CSP.
	Hash    string `json:"hash,omitempty"`
	Version string `json:"version,omitempty"`
	// Script is the run_script body.
	Script string `json:"script,omitempty"`
	// PreserveDataPlan keeps the eSIM plan on wipe (iOS).
	PreserveDataPlan bool `json:"preserveDataPlan,omitempty"`
}

// ParseParams decodes params, tolerating empty input.
func ParseParams(raw json.RawMessage) (Params, error) {
	var p Params
	if len(raw) == 0 {
		return p, nil
	}
	err := json.Unmarshal(raw, &p)
	return p, err
}

// Authorize decides whether role may send typ to the device.
func Authorize(role string, d *store.Device, typ string, params Params) error {
	spec, ok := Lookup(typ)
	if !ok {
		return fmt.Errorf("unknown command %q", typ)
	}
	if !roleAtLeast(role, spec.MinRole) {
		return fmt.Errorf("command %s requires role %s", typ, spec.MinRole)
	}
	if !contains(spec.Platforms, d.Platform) {
		return fmt.Errorf("command %s is not supported on %s", typ, d.Platform)
	}
	if d.IsPersonal() && !spec.Personal {
		return fmt.Errorf("command %s is not permitted on personally owned (BYOD) devices; use retire to remove corporate data", typ)
	}
	if d.Status == store.StatusRetired || d.Status == store.StatusWiped {
		return fmt.Errorf("device is %s", d.Status)
	}
	switch typ {
	case InstallApp, RemoveApp:
		if params.AppID == "" && params.URL == "" {
			return fmt.Errorf("%s requires appId or url", typ)
		}
	case RunScript:
		if params.Script == "" {
			return fmt.Errorf("run_script requires script")
		}
	case Wipe, Lock:
		if d.Platform == store.PlatformMacOS && params.PIN != "" && !sixDigits(params.PIN) {
			return fmt.Errorf("pin must be six digits")
		}
	}
	return nil
}

func sixDigits(s string) bool {
	if len(s) != 6 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func roleAtLeast(have, want string) bool {
	r := map[string]int{store.RoleAuditor: 1, store.RoleOperator: 2, store.RoleAdmin: 3}
	return r[have] >= r[want]
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
