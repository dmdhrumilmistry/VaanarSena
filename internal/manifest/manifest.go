// Package manifest implements declarative configuration: Policy, Group and
// Blueprint resources described in YAML or JSON, applied idempotently by name.
//
//	apiVersion: vaanarsena.io/v1
//	kind: Group
//	metadata:
//	  name: outdated-iphones
//	spec:
//	  kind: smart
//	  rules:
//	    match: all
//	    conditions:
//	      - {field: platform, op: eq, value: ios}
//	      - {field: osVersion, op: version_lt, value: "17.0"}
//
// The same code path backs the console and the REST API, so a manifest and a
// click produce identical state.
package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dmdhrumilmistry/VaanarSena/internal/blueprint"
	"github.com/dmdhrumilmistry/VaanarSena/internal/groups"
	"github.com/dmdhrumilmistry/VaanarSena/internal/policy"
)

// APIVersion is the only supported manifest version.
const APIVersion = "vaanarsena.io/v1"

// Kinds, in dependency order: groups first, because policies and blueprints
// target them; policies before blueprints, because blueprints reference them.
const (
	KindGroup     = "Group"
	KindPolicy    = "Policy"
	KindBlueprint = "Blueprint"
)

var kindOrder = map[string]int{KindGroup: 0, KindPolicy: 1, KindBlueprint: 2}

// Resource is one manifest document.
type Resource struct {
	APIVersion string          `json:"apiVersion" yaml:"apiVersion"`
	Kind       string          `json:"kind" yaml:"kind"`
	Metadata   Metadata        `json:"metadata" yaml:"metadata"`
	Spec       json.RawMessage `json:"spec" yaml:"-"`
}

// Metadata identifies a resource.
type Metadata struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// GroupSpec is the spec of a Group.
type GroupSpec struct {
	// Kind is "static" (default) or "smart".
	Kind  string       `json:"kind,omitempty"`
	Rules *groups.Rule `json:"rules,omitempty"`
	// Members of a static group, by device ID or serial number. When the
	// field is omitted, membership is left as it is (managed elsewhere).
	Members *Members `json:"members,omitempty"`
}

// Members selects devices for a static group.
type Members struct {
	IDs     []string `json:"ids,omitempty"`
	Serials []string `json:"serials,omitempty"`
}

// PolicySpec is the spec of a Policy.
type PolicySpec struct {
	Priority int              `json:"priority,omitempty"`
	Groups   []string         `json:"groups,omitempty"`
	Document *policy.Document `json:"document"`
}

// BlueprintSpec is the spec of a Blueprint.
type BlueprintSpec struct {
	Priority int      `json:"priority,omitempty"`
	Groups   []string `json:"groups,omitempty"`
	blueprint.Spec
}

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

var nameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9 ._-]{0,127}$`)

// Decode parses YAML (multi-document) or JSON (one object, an array, or
// {"items": [...]}) into resources. Unknown fields are rejected everywhere.
func Decode(data []byte) ([]Resource, error) {
	// Windows editors and PowerShell redirection add UTF-8 byte order marks,
	// at the start of the file or of concatenated documents.
	data = bytes.ReplaceAll(data, utf8BOM, nil)
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("manifest is empty")
	}
	var raws []json.RawMessage
	switch trimmed[0] {
	case '[':
		if err := json.Unmarshal(trimmed, &raws); err != nil {
			return nil, err
		}
	case '{':
		var probe struct {
			Items []json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal(trimmed, &probe); err == nil && probe.Items != nil {
			raws = probe.Items
		} else {
			raws = []json.RawMessage{trimmed}
		}
	default:
		dec := yaml.NewDecoder(bytes.NewReader(data))
		for {
			var doc any
			err := dec.Decode(&doc)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("yaml: %w", err)
			}
			if doc == nil {
				continue // empty document between separators
			}
			b, err := json.Marshal(normalize(doc))
			if err != nil {
				return nil, fmt.Errorf("yaml: %w", err)
			}
			raws = append(raws, b)
		}
	}
	out := make([]Resource, 0, len(raws))
	for i, raw := range raws {
		var r Resource
		if err := strictJSON(raw, &r); err != nil {
			return nil, fmt.Errorf("document %d: %w", i+1, err)
		}
		out = append(out, r)
	}
	return out, nil
}

// normalize converts YAML-decoded values into JSON-encodable ones (yaml.v3
// yields map[string]any for string-keyed maps, but nested any-keyed maps can
// appear for non-string keys).
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = normalize(val)
		}
		return t
	case map[any]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[fmt.Sprint(k)] = normalize(val)
		}
		return m
	case []any:
		for i := range t {
			t[i] = normalize(t[i])
		}
		return t
	}
	return v
}

func strictJSON(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// Validate checks a resource in isolation: shape, name and spec contents.
// References between resources are checked by the applier.
func (r *Resource) Validate() error {
	if r.APIVersion != APIVersion {
		return fmt.Errorf("apiVersion must be %s", APIVersion)
	}
	if !nameRE.MatchString(r.Metadata.Name) {
		return errors.New("metadata.name must be 1-128 characters: letters, digits, space, dot, dash, underscore")
	}
	if len(r.Spec) == 0 {
		return errors.New("spec is required")
	}
	switch r.Kind {
	case KindGroup:
		_, err := r.GroupSpec()
		return err
	case KindPolicy:
		_, err := r.PolicySpec()
		return err
	case KindBlueprint:
		_, err := r.BlueprintSpec()
		return err
	}
	return fmt.Errorf("unknown kind %q (expected Group, Policy or Blueprint)", r.Kind)
}

// GroupSpec decodes and validates a Group spec.
func (r *Resource) GroupSpec() (*GroupSpec, error) {
	var s GroupSpec
	if err := strictJSON(r.Spec, &s); err != nil {
		return nil, err
	}
	switch s.Kind {
	case "", "static":
		s.Kind = "static"
		if s.Rules != nil {
			return nil, errors.New("rules are only allowed on smart groups (set kind: smart)")
		}
	case "smart":
		if s.Rules == nil {
			return nil, errors.New("smart groups need rules")
		}
		if err := s.Rules.Validate(); err != nil {
			return nil, err
		}
		if s.Members != nil {
			return nil, errors.New("smart group membership comes from rules; remove members")
		}
	default:
		return nil, errors.New(`spec.kind must be "static" or "smart"`)
	}
	return &s, nil
}

// PolicySpec decodes and validates a Policy spec.
func (r *Resource) PolicySpec() (*PolicySpec, error) {
	var s PolicySpec
	if err := strictJSON(r.Spec, &s); err != nil {
		return nil, err
	}
	if s.Document == nil {
		return nil, errors.New("spec.document is required")
	}
	// Round-trip through Parse to apply the same strict checks as the API.
	b, _ := json.Marshal(s.Document)
	if _, err := policy.Parse(b); err != nil {
		return nil, err
	}
	if s.Priority == 0 {
		s.Priority = 100
	}
	return &s, nil
}

// BlueprintSpec decodes and validates a Blueprint spec.
func (r *Resource) BlueprintSpec() (*BlueprintSpec, error) {
	var s BlueprintSpec
	if err := strictJSON(r.Spec, &s); err != nil {
		return nil, err
	}
	if err := s.Spec.Validate(); err != nil {
		return nil, err
	}
	if s.Priority == 0 {
		s.Priority = 100
	}
	return &s, nil
}

// key identifies a resource within a manifest set.
func (r *Resource) key() string { return r.Kind + "/" + strings.ToLower(r.Metadata.Name) }

// EncodeYAML renders resources as a multi-document YAML stream.
func EncodeYAML(rs []Resource) ([]byte, error) {
	var buf bytes.Buffer
	for i, r := range rs {
		if i > 0 {
			buf.WriteString("---\n")
		}
		var spec any
		if err := json.Unmarshal(r.Spec, &spec); err != nil {
			return nil, err
		}
		doc := map[string]any{"apiVersion": r.APIVersion, "kind": r.Kind, "metadata": r.Metadata, "spec": spec}
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(orderedDoc(doc)); err != nil {
			return nil, err
		}
		_ = enc.Close()
	}
	return buf.Bytes(), nil
}

// orderedDoc emits apiVersion, kind, metadata, spec in that order.
func orderedDoc(doc map[string]any) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode}
	for _, k := range []string{"apiVersion", "kind", "metadata", "spec"} {
		var v yaml.Node
		_ = v.Encode(doc[k])
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, &v)
	}
	return n
}
