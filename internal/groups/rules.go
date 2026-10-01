// Package groups evaluates smart (dynamic) group rules against devices.
//
// A rule set is a tree:
//
//	{"match": "all", "conditions": [
//	   {"field": "platform", "op": "in", "value": ["ios", "ipados"]},
//	   {"field": "osVersion", "op": "version_lt", "value": "17.0"}
//	 ],
//	 "rules": [{"match": "any", "conditions": [...]}]}
//
// "match" is "all" (AND) or "any" (OR) over the conditions and nested rules.
package groups

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

// Rule is a node of the rule tree.
type Rule struct {
	Match      string      `json:"match" yaml:"match"`
	Conditions []Condition `json:"conditions,omitempty" yaml:"conditions,omitempty"`
	Rules      []Rule      `json:"rules,omitempty" yaml:"rules,omitempty"`
}

// Condition compares one device field with a value.
type Condition struct {
	Field string `json:"field" yaml:"field"`
	Op    string `json:"op" yaml:"op"`
	Value any    `json:"value,omitempty" yaml:"value,omitempty"`
}

// Fields usable in conditions besides facts.<path>.
var Fields = []string{
	"platform", "ownership", "status", "name", "serial", "model", "osVersion", "assignee",
	"compliant", "tags", "enrolledDays", "lastSeenDays",
}

// Ops lists supported operators.
var Ops = []string{
	"eq", "ne", "in", "not_in", "contains", "not_contains", "starts_with", "ends_with", "matches",
	"gt", "gte", "lt", "lte", "version_lt", "version_lte", "version_gt", "version_gte", "exists", "not_exists",
}

const maxDepth = 5

// Parse decodes and validates a rule set.
func Parse(raw []byte) (*Rule, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var r Rule
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("rules: %w", err)
	}
	return &r, r.Validate()
}

// Validate checks the tree and pre-compiles nothing; regexes are checked here
// so a bad pattern is rejected at save time, not at evaluation time.
func (r *Rule) Validate() error { return r.validate(0) }

func (r *Rule) validate(depth int) error {
	if depth > maxDepth {
		return fmt.Errorf("rules nest deeper than %d levels", maxDepth)
	}
	if r.Match != "all" && r.Match != "any" {
		return errors.New(`match must be "all" or "any"`)
	}
	if len(r.Conditions) == 0 && len(r.Rules) == 0 {
		return errors.New("a rule needs at least one condition or nested rule")
	}
	for i, c := range r.Conditions {
		if !validField(c.Field) {
			return fmt.Errorf("condition %d: unknown field %q (use one of %s, or facts.<path>)", i, c.Field, strings.Join(Fields, ", "))
		}
		if !contains(Ops, c.Op) {
			return fmt.Errorf("condition %d: unknown op %q", i, c.Op)
		}
		if c.Op == "matches" {
			s, ok := c.Value.(string)
			if !ok {
				return fmt.Errorf("condition %d: matches needs a string pattern", i)
			}
			if _, err := regexp.Compile(s); err != nil {
				return fmt.Errorf("condition %d: %w", i, err)
			}
		}
		if (c.Op == "in" || c.Op == "not_in") && !isList(c.Value) {
			return fmt.Errorf("condition %d: %s needs a list value", i, c.Op)
		}
		if c.Op != "exists" && c.Op != "not_exists" && c.Value == nil {
			return fmt.Errorf("condition %d: value is required for %s", i, c.Op)
		}
	}
	for i := range r.Rules {
		if err := r.Rules[i].validate(depth + 1); err != nil {
			return fmt.Errorf("nested rule %d: %w", i, err)
		}
	}
	return nil
}

func validField(f string) bool {
	return contains(Fields, f) || (strings.HasPrefix(f, "facts.") && len(f) > len("facts."))
}

// Matches evaluates the rule set against a device.
func (r *Rule) Matches(d *store.Device, now time.Time) bool {
	env := newEnv(d, now)
	return r.eval(env)
}

func (r *Rule) eval(env *env) bool {
	all := r.Match == "all"
	results := make([]bool, 0, len(r.Conditions)+len(r.Rules))
	for _, c := range r.Conditions {
		results = append(results, c.eval(env))
	}
	for i := range r.Rules {
		results = append(results, r.Rules[i].eval(env))
	}
	for _, ok := range results {
		if all && !ok {
			return false
		}
		if !all && ok {
			return true
		}
	}
	return all
}

type env struct {
	d     *store.Device
	now   time.Time
	facts map[string]any
}

func newEnv(d *store.Device, now time.Time) *env {
	e := &env{d: d, now: now}
	_ = json.Unmarshal(d.Facts, &e.facts)
	return e
}

// lookup returns the field value and whether it exists.
func (e *env) lookup(field string) (any, bool) {
	d := e.d
	switch field {
	case "platform":
		return d.Platform, true
	case "ownership":
		return d.Ownership, true
	case "status":
		return d.Status, true
	case "name":
		return d.Name, d.Name != ""
	case "serial":
		return d.Serial, d.Serial != ""
	case "model":
		return d.Model, d.Model != ""
	case "osVersion":
		return d.OSVersion, d.OSVersion != ""
	case "assignee":
		return d.Assignee, d.Assignee != ""
	case "compliant":
		if d.Compliant == nil {
			return nil, false
		}
		return *d.Compliant, true
	case "tags":
		out := make([]any, len(d.Tags))
		for i, t := range d.Tags {
			out[i] = t
		}
		return out, true
	case "enrolledDays":
		if d.EnrolledAt == nil {
			return nil, false
		}
		return math.Floor(e.now.Sub(*d.EnrolledAt).Hours() / 24), true
	case "lastSeenDays":
		if d.LastSeenAt == nil {
			return nil, false
		}
		return math.Floor(e.now.Sub(*d.LastSeenAt).Hours() / 24), true
	}
	if path, ok := strings.CutPrefix(field, "facts."); ok {
		var cur any = e.facts
		for _, part := range strings.Split(path, ".") {
			m, ok := cur.(map[string]any)
			if !ok {
				return nil, false
			}
			if cur, ok = m[part]; !ok {
				return nil, false
			}
		}
		return cur, cur != nil
	}
	return nil, false
}

func (c *Condition) eval(e *env) bool {
	v, ok := e.lookup(c.Field)
	switch c.Op {
	case "exists":
		return ok
	case "not_exists":
		return !ok
	}
	if !ok {
		// Missing fields only satisfy negative operators.
		return c.Op == "ne" || c.Op == "not_in" || c.Op == "not_contains"
	}
	switch c.Op {
	case "eq":
		return equal(v, c.Value)
	case "ne":
		return !equal(v, c.Value)
	case "in":
		return inList(v, c.Value)
	case "not_in":
		return !inList(v, c.Value)
	case "contains":
		return containsVal(v, c.Value)
	case "not_contains":
		return !containsVal(v, c.Value)
	case "starts_with":
		return strings.HasPrefix(strings.ToLower(str(v)), strings.ToLower(str(c.Value)))
	case "ends_with":
		return strings.HasSuffix(strings.ToLower(str(v)), strings.ToLower(str(c.Value)))
	case "matches":
		re, err := regexp.Compile(str(c.Value))
		return err == nil && re.MatchString(str(v))
	case "gt", "gte", "lt", "lte":
		a, okA := num(v)
		b, okB := num(c.Value)
		if !okA || !okB {
			return false
		}
		return cmpOp(c.Op, compareFloat(a, b))
	case "version_lt", "version_lte", "version_gt", "version_gte":
		return cmpOp(strings.TrimPrefix(c.Op, "version_"), CompareVersions(str(v), str(c.Value)))
	}
	return false
}

func cmpOp(op string, c int) bool {
	switch op {
	case "gt":
		return c > 0
	case "gte":
		return c >= 0
	case "lt":
		return c < 0
	case "lte":
		return c <= 0
	}
	return false
}

func compareFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// CompareVersions compares dotted versions numerically: "17.2" > "17.10" is
// false. Non-numeric prefixes like "Ubuntu 24.04" are skipped to the first
// digit; missing parts count as zero.
func CompareVersions(a, b string) int {
	pa, pb := versionParts(a), versionParts(b)
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

var digits = regexp.MustCompile(`\d+`)

func versionParts(s string) []int {
	i := strings.IndexAny(s, "0123456789")
	if i < 0 {
		return nil
	}
	s = s[i:]
	// Stop at the first space so "10.0.22631 Build 1" compares as 10.0.22631.
	if j := strings.IndexByte(s, ' '); j > 0 {
		s = s[:j]
	}
	var out []int
	for _, p := range digits.FindAllString(s, -1) {
		n, _ := strconv.Atoi(p)
		out = append(out, n)
	}
	return out
}

func str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		return fmt.Sprint(t)
	}
}

func num(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case string:
		f, err := strconv.ParseFloat(t, 64)
		return f, err == nil
	}
	return 0, false
}

// equal compares loosely: strings case-insensitively, numbers numerically,
// booleans against true/false strings.
func equal(a, b any) bool {
	if fa, ok := num(a); ok {
		if fb, ok := num(b); ok {
			return fa == fb
		}
	}
	if ba, ok := a.(bool); ok {
		switch bv := b.(type) {
		case bool:
			return ba == bv
		case string:
			return strconv.FormatBool(ba) == strings.ToLower(bv)
		}
		return false
	}
	return strings.EqualFold(str(a), str(b))
}

func isList(v any) bool { _, ok := v.([]any); return ok }

func inList(v, list any) bool {
	items, _ := list.([]any)
	for _, it := range items {
		if equal(v, it) {
			return true
		}
	}
	return false
}

// containsVal: list fields contain an element; strings contain a substring.
func containsVal(v, want any) bool {
	if items, ok := v.([]any); ok {
		return inList(want, items)
	}
	return strings.Contains(strings.ToLower(str(v)), strings.ToLower(str(want)))
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
