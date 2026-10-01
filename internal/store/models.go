package store

import (
	"encoding/json"
	"time"
)

// Roles, from least to most privileged.
const (
	RoleAuditor  = "auditor"
	RoleOperator = "operator"
	RoleAdmin    = "admin"
)

// Ownership models.
const (
	OwnershipCorporate = "corporate"
	OwnershipPersonal  = "personal"
)

// Device platforms.
const (
	PlatformIOS      = "ios"
	PlatformIPadOS   = "ipados"
	PlatformMacOS    = "macos"
	PlatformWindows  = "windows"
	PlatformAndroid  = "android"
	PlatformChromeOS = "chromeos"
	PlatformLinux    = "linux"
)

// Device statuses.
const (
	StatusEnrolling = "enrolling"
	StatusEnrolled  = "enrolled"
	StatusRetired   = "retired"
	StatusWiped     = "wiped"
)

// Command statuses.
const (
	CmdQueued       = "queued"
	CmdSent         = "sent"
	CmdAcknowledged = "acknowledged"
	CmdError        = "error"
	CmdNotNow       = "not_now"
	CmdCancelled    = "cancelled"
)

// User is a console or API user.
type User struct {
	ID           string     `json:"id"`
	Email        string     `json:"email"`
	Name         string     `json:"name"`
	PasswordHash string     `json:"-"`
	Role         string     `json:"role"`
	Disabled     bool       `json:"disabled"`
	CreatedAt    time.Time  `json:"createdAt"`
	LastLoginAt  *time.Time `json:"lastLoginAt,omitempty"`
}

// APIToken is a long-lived automation credential.
type APIToken struct {
	ID         string     `json:"id"`
	UserID     string     `json:"userId"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
}

// EnrollmentToken authorises devices to enroll.
type EnrollmentToken struct {
	ID        string          `json:"id"`
	Platform  string          `json:"platform"`
	Ownership string          `json:"ownership"`
	GroupID   *string         `json:"groupId,omitempty"`
	Assignee  string          `json:"assignee"`
	MaxUses   int             `json:"maxUses"`
	Uses      int             `json:"uses"`
	ExpiresAt time.Time       `json:"expiresAt"`
	CreatedBy *string         `json:"createdBy,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
	Revoked   bool            `json:"revoked"`
	Extra     json.RawMessage `json:"extra"`
}

// Usable reports whether the token can still enroll a device.
func (t *EnrollmentToken) Usable(now time.Time) bool {
	return !t.Revoked && t.Uses < t.MaxUses && now.Before(t.ExpiresAt)
}

// Device is an enrolled (or enrolling) device.
type Device struct {
	ID                string          `json:"id"`
	Platform          string          `json:"platform"`
	Ownership         string          `json:"ownership"`
	Status            string          `json:"status"`
	Name              string          `json:"name"`
	Serial            string          `json:"serial"`
	Model             string          `json:"model"`
	OSVersion         string          `json:"osVersion"`
	Assignee          string          `json:"assignee"`
	NativeID          string          `json:"nativeId"`
	PlatformIDs       json.RawMessage `json:"platformIds"`
	Facts             json.RawMessage `json:"facts"`
	Compliant         *bool           `json:"compliant,omitempty"`
	Tags              []string        `json:"tags"`
	CertSerial        string          `json:"-"`
	EnrollmentTokenID *string         `json:"enrollmentTokenId,omitempty"`
	EnrolledAt        *time.Time      `json:"enrolledAt,omitempty"`
	LastSeenAt        *time.Time      `json:"lastSeenAt,omitempty"`
	CreatedAt         time.Time       `json:"createdAt"`
	UpdatedAt         time.Time       `json:"updatedAt"`
}

// IsPersonal reports whether the device is BYOD.
func (d *Device) IsPersonal() bool { return d.Ownership == OwnershipPersonal }

// IsApple reports whether the device speaks the Apple MDM protocol.
func (d *Device) IsApple() bool {
	return d.Platform == PlatformIOS || d.Platform == PlatformIPadOS || d.Platform == PlatformMacOS
}

// Group kinds.
const (
	GroupStatic = "static"
	GroupSmart  = "smart"
)

// Group is a set of devices: static (managed by hand) or smart (membership
// computed from Rules).
type Group struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Kind        string          `json:"kind"`
	Rules       json.RawMessage `json:"rules,omitempty"`
	ManagedBy   string          `json:"managedBy,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
	DeviceCount int             `json:"deviceCount"`
}

// Blueprint bundles configuration and onboarding for the groups it targets.
type Blueprint struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Priority    int             `json:"priority"`
	Spec        json.RawMessage `json:"spec"`
	Version     int             `json:"version"`
	ManagedBy   string          `json:"managedBy,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
	GroupIDs    []string        `json:"groupIds"`
}

// Policy is a platform-neutral policy document.
type Policy struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Priority    int             `json:"priority"`
	Document    json.RawMessage `json:"document"`
	Version     int             `json:"version"`
	ManagedBy   string          `json:"managedBy,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
	GroupIDs    []string        `json:"groupIds"`
	DeviceIDs   []string        `json:"deviceIds"`
}

// Command is a queued device command.
type Command struct {
	ID             string          `json:"id"`
	DeviceID       string          `json:"deviceId"`
	Type           string          `json:"type"`
	Params         json.RawMessage `json:"params"`
	Status         string          `json:"status"`
	NativeRequest  string          `json:"nativeRequest,omitempty"`
	NativeResponse string          `json:"nativeResponse,omitempty"`
	Error          string          `json:"error,omitempty"`
	CreatedBy      *string         `json:"createdBy,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
	SentAt         *time.Time      `json:"sentAt,omitempty"`
	CompletedAt    *time.Time      `json:"completedAt,omitempty"`
}

// AuditEntry is one audit log record.
type AuditEntry struct {
	ID       int64           `json:"id"`
	At       time.Time       `json:"at"`
	Actor    string          `json:"actor"`
	Action   string          `json:"action"`
	Target   string          `json:"target"`
	Details  json.RawMessage `json:"details"`
	RemoteIP string          `json:"remoteIp"`
}
