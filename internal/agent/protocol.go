// Package agent is the server side of the VaanarSena Linux agent protocol, and
// defines the wire types shared with cmd/vaanarsena-agent.
//
//	POST /agent/v1/enroll   {token, csr, ...}           -> certificate
//	POST /agent/v1/checkin  (mTLS) {facts, results}     -> {commands, policy}
package agent

import (
	"encoding/json"

	"github.com/dmdhrumilmistry/VaanarSena/internal/policy"
)

// EnrollRequest is sent once with an enrollment token.
type EnrollRequest struct {
	Token     string `json:"token"`
	CSR       []byte `json:"csr"` // DER PKCS#10
	MachineID string `json:"machineId"`
	Hostname  string `json:"hostname"`
	OS        string `json:"os"`
	OSVersion string `json:"osVersion"`
}

// EnrollResponse carries the agent's identity.
type EnrollResponse struct {
	DeviceID         string `json:"deviceId"`
	CertificatePEM   string `json:"certificate"`
	CACertificatePEM string `json:"caCertificate"`
	CheckinSeconds   int    `json:"checkinSeconds"`
}

// Result reports the outcome of one command.
type Result struct {
	CommandID string `json:"commandId"`
	OK        bool   `json:"ok"`
	Output    string `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Compliance is the agent's evaluation of the policy it was given.
type Compliance struct {
	Compliant bool     `json:"compliant"`
	Issues    []string `json:"issues,omitempty"`
}

// InventoryItem is one installed package or systemd service.
type InventoryItem struct {
	Name       string            `json:"name"`
	Identifier string            `json:"identifier,omitempty"`
	Version    string            `json:"version,omitempty"`
	Publisher  string            `json:"publisher,omitempty"`
	Source     string            `json:"source"`          // dpkg rpm pacman apk flatpak snap systemd
	State      string            `json:"state,omitempty"` // services: running stopped failed
	Details    map[string]string `json:"details,omitempty"`
}

// Inventory is the software the agent found. It is sent on the first check-in,
// when Hash changes, at least daily and after a refresh, and never by an
// agent in personal mode. A nil list leaves what the server has untouched
// (the agent found no tool able to produce it).
type Inventory struct {
	Apps     []InventoryItem `json:"apps,omitempty"`
	Services []InventoryItem `json:"services,omitempty"`
	// Hash identifies the content, so the agent can tell when it changed.
	Hash string `json:"hash,omitempty"`
}

// CheckinRequest is sent on every poll.
type CheckinRequest struct {
	Facts      map[string]any `json:"facts"`
	Results    []Result       `json:"results,omitempty"`
	Compliance *Compliance    `json:"compliance,omitempty"`
	// PolicyVersion is the hash of the last policy applied.
	PolicyVersion string `json:"policyVersion,omitempty"`
	// Inventory is present only when there is something new to report.
	Inventory *Inventory `json:"inventory,omitempty"`
}

// Command is a queued command delivered to the agent.
type Command struct {
	ID     string          `json:"id"`
	Type   string          `json:"type"`
	Params json.RawMessage `json:"params"`
}

// CheckinResponse returns work for the agent.
type CheckinResponse struct {
	Commands       []Command        `json:"commands"`
	Policy         *policy.Document `json:"policy,omitempty"`
	PolicyVersion  string           `json:"policyVersion"`
	CheckinSeconds int              `json:"checkinSeconds"`
	// Personal tells the agent to run in inventory-only mode.
	Personal bool `json:"personal"`
}
