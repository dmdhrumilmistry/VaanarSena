// Package blueprint defines blueprints: a reusable bundle of configuration and
// onboarding assigned to groups. A blueprint can reference named policies,
// carry its own inline policy (including custom payloads) and list onboarding
// steps that run once on every device that comes into scope.
package blueprint

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dmdhrumilmistry/VaanarSena/internal/command"
	"github.com/dmdhrumilmistry/VaanarSena/internal/policy"
)

// Spec is the blueprint body.
type Spec struct {
	// Policies are names of existing policies to apply.
	Policies []string `json:"policies,omitempty" yaml:"policies,omitempty"`
	// Policy is an inline policy document.
	Policy *policy.Document `json:"policy,omitempty" yaml:"policy,omitempty"`
	// OnEnroll steps run once per device when it first falls in scope (at
	// enrollment, or later when it joins a targeted group).
	OnEnroll []Step `json:"onEnroll,omitempty" yaml:"onEnroll,omitempty"`
}

// Step is one onboarding command.
type Step struct {
	Type   string          `json:"type" yaml:"type"`
	Params json.RawMessage `json:"params,omitempty" yaml:"params,omitempty"`
}

// Steps that would destroy or un-manage a device make no sense as onboarding.
var forbidden = map[string]bool{command.Wipe: true, command.Retire: true}

// Parse decodes and validates a spec, rejecting unknown fields.
func Parse(raw []byte) (*Spec, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var s Spec
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("blueprint: %w", err)
	}
	return &s, s.Validate()
}

// Validate checks steps and the inline policy.
func (s *Spec) Validate() error {
	var errs []error
	if s.Policy != nil {
		if err := s.Policy.Validate(); err != nil {
			errs = append(errs, err)
		}
	}
	seen := map[string]bool{}
	for _, name := range s.Policies {
		if name == "" || seen[name] {
			errs = append(errs, fmt.Errorf("policies: empty or duplicate name %q", name))
		}
		seen[name] = true
	}
	for i, st := range s.OnEnroll {
		if _, ok := command.Lookup(st.Type); !ok {
			errs = append(errs, fmt.Errorf("onEnroll[%d]: unknown command %q", i, st.Type))
			continue
		}
		if forbidden[st.Type] {
			errs = append(errs, fmt.Errorf("onEnroll[%d]: %s cannot be an onboarding step", i, st.Type))
		}
		if _, err := command.ParseParams(st.Params); err != nil {
			errs = append(errs, fmt.Errorf("onEnroll[%d].params: %w", i, err))
		}
	}
	if s.Policy == nil && len(s.Policies) == 0 && len(s.OnEnroll) == 0 {
		errs = append(errs, errors.New("blueprint is empty: set policies, policy or onEnroll"))
	}
	return errors.Join(errs...)
}
