package policy

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/smallstep/pkcs7"
	"howett.net/plist"
)

// Custom carries raw, platform-native payloads for settings the neutral model
// does not cover. They are passed through after the neutral translation, so
// they win on conflict.
type Custom struct {
	// Scope is "corporate" (default) or "all". Raw payloads can do anything a
	// platform allows, so they reach personal devices only when asked to.
	Scope   string         `json:"scope,omitempty" yaml:"scope,omitempty"`
	Apple   []ApplePayload `json:"apple,omitempty" yaml:"apple,omitempty"`
	Windows []WindowsNode  `json:"windows,omitempty" yaml:"windows,omitempty"`
	Android map[string]any `json:"android,omitempty" yaml:"android,omitempty"`
}

// ApplePayload is either one payload dictionary (Payload, which must carry a
// PayloadType) or a complete .mobileconfig (Mobileconfig, base64; signed or
// unsigned), whose PayloadContent is unpacked into the policy profile.
type ApplePayload struct {
	Payload      map[string]any `json:"payload,omitempty" yaml:"payload,omitempty"`
	Mobileconfig string         `json:"mobileconfig,omitempty" yaml:"mobileconfig,omitempty"`
}

// WindowsNode is one OMA-DM operation on a CSP node.
type WindowsNode struct {
	LocURI string `json:"locUri" yaml:"locUri"`
	// Op is Replace (default), Add, Exec or Delete.
	Op     string `json:"op,omitempty" yaml:"op,omitempty"`
	Format string `json:"format,omitempty" yaml:"format,omitempty"` // int, chr, bool, xml, b64
	Data   string `json:"data,omitempty" yaml:"data,omitempty"`
}

// AppliesTo reports whether the custom block targets a device.
func (c *Custom) AppliesTo(personal bool) bool {
	return c != nil && (!personal || c.Scope == "all")
}

func (c *Custom) validate() error {
	var errs []error
	if c.Scope != "" && c.Scope != "corporate" && c.Scope != "all" {
		errs = append(errs, errors.New(`custom.scope must be "corporate" or "all"`))
	}
	for i, p := range c.Apple {
		switch {
		case p.Payload != nil && p.Mobileconfig != "":
			errs = append(errs, fmt.Errorf("custom.apple[%d]: set payload or mobileconfig, not both", i))
		case p.Payload != nil:
			if t, _ := p.Payload["PayloadType"].(string); t == "" {
				errs = append(errs, fmt.Errorf("custom.apple[%d].payload needs a PayloadType", i))
			}
			if t, _ := p.Payload["PayloadType"].(string); t == "com.apple.mdm" || t == "Configuration" {
				errs = append(errs, fmt.Errorf("custom.apple[%d]: %s payloads cannot be nested in a policy profile", i, t))
			}
		case p.Mobileconfig != "":
			if _, err := ExpandMobileconfig(p.Mobileconfig); err != nil {
				errs = append(errs, fmt.Errorf("custom.apple[%d].mobileconfig: %w", i, err))
			}
		default:
			errs = append(errs, fmt.Errorf("custom.apple[%d]: payload or mobileconfig is required", i))
		}
	}
	for i, n := range c.Windows {
		if !strings.HasPrefix(n.LocURI, "./") {
			errs = append(errs, fmt.Errorf("custom.windows[%d].locUri must start with ./", i))
		}
		switch n.Op {
		case "", "Replace", "Add", "Exec", "Delete":
		default:
			errs = append(errs, fmt.Errorf("custom.windows[%d].op must be Replace, Add, Exec or Delete", i))
		}
		switch n.Format {
		case "", "int", "chr", "bool", "xml", "b64", "node":
		default:
			errs = append(errs, fmt.Errorf("custom.windows[%d].format must be int, chr, bool, xml, b64 or node", i))
		}
	}
	for k := range c.Android {
		// Fields the server owns: overriding them would break management.
		if k == "statusReportingSettings" || k == "name" || k == "version" {
			errs = append(errs, fmt.Errorf("custom.android.%s is managed by VaanarSena", k))
		}
	}
	return errors.Join(errs...)
}

// ExpandMobileconfig decodes a base64 .mobileconfig (optionally CMS signed)
// and returns its payload dictionaries.
func ExpandMobileconfig(b64 string) ([]map[string]any, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(b64), ""))
	if err != nil {
		return nil, fmt.Errorf("not base64: %w", err)
	}
	if p7, err := pkcs7.Parse(raw); err == nil && len(p7.Content) > 0 {
		raw = p7.Content // signed profile: the signature is the sender's, we re-sign
	}
	var prof struct {
		PayloadType    string
		PayloadContent []map[string]any
	}
	if _, err := plist.Unmarshal(raw, &prof); err != nil {
		return nil, fmt.Errorf("not a property list: %w", err)
	}
	if prof.PayloadType != "Configuration" {
		return nil, errors.New("not a configuration profile (PayloadType must be Configuration)")
	}
	if len(prof.PayloadContent) == 0 {
		return nil, errors.New("profile has no payloads")
	}
	for _, p := range prof.PayloadContent {
		if t, _ := p["PayloadType"].(string); t == "com.apple.mdm" {
			return nil, errors.New("profile contains an MDM payload; enrollment profiles cannot be deployed as policy")
		}
	}
	return prof.PayloadContent, nil
}

// applePayloads flattens custom Apple entries into payload dictionaries.
func (c *Custom) applePayloads() []map[string]any {
	var out []map[string]any
	for _, p := range c.Apple {
		if p.Payload != nil {
			cp := map[string]any{}
			for k, v := range p.Payload {
				cp[k] = v
			}
			out = append(out, cp)
			continue
		}
		pls, err := ExpandMobileconfig(p.Mobileconfig)
		if err != nil {
			continue // validated at save time
		}
		out = append(out, pls...)
	}
	return out
}

func mergeCustom(dst, src *Custom) *Custom {
	if src == nil {
		return dst
	}
	if dst == nil {
		dst = &Custom{}
	}
	// The broadest scope wins only if the later policy asks for it.
	if src.Scope != "" {
		dst.Scope = src.Scope
	}
	dst.Apple = append(dst.Apple, src.Apple...)
	idx := map[string]int{}
	for i, n := range dst.Windows {
		idx[n.LocURI] = i
	}
	for _, n := range src.Windows {
		if i, ok := idx[n.LocURI]; ok {
			dst.Windows[i] = n
			continue
		}
		idx[n.LocURI] = len(dst.Windows)
		dst.Windows = append(dst.Windows, n)
	}
	if len(src.Android) > 0 && dst.Android == nil {
		dst.Android = map[string]any{}
	}
	for k, v := range src.Android {
		dst.Android[k] = v
	}
	return dst
}
