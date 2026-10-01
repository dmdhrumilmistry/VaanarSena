package groups

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

func device() *store.Device {
	seen := time.Now().Add(-10 * 24 * time.Hour)
	enrolled := time.Now().Add(-40 * 24 * time.Hour)
	ok := false
	return &store.Device{
		Platform: "ios", Ownership: "corporate", Status: "enrolled", Name: "Alice iPhone", Model: "iPhone15,2",
		OSVersion: "16.7.2", Assignee: "alice@eng.example.com", Tags: []string{"sales", "emea"}, Compliant: &ok,
		LastSeenAt: &seen, EnrolledAt: &enrolled,
		Facts: json.RawMessage(`{"apple":{"BatteryLevel":0.42,"IsSupervised":true},"linux":{"diskEncrypted":false}}`),
	}
}

func mustRule(t *testing.T, js string) *Rule {
	t.Helper()
	r, err := Parse([]byte(js))
	if err != nil {
		t.Fatalf("parse %s: %v", js, err)
	}
	return r
}

func TestConditions(t *testing.T) {
	d := device()
	cases := map[string]bool{
		`{"field":"platform","op":"eq","value":"IOS"}`:                     true,
		`{"field":"platform","op":"in","value":["android","ios"]}`:         true,
		`{"field":"platform","op":"not_in","value":["ios"]}`:               false,
		`{"field":"osVersion","op":"version_lt","value":"17.0"}`:           true,
		`{"field":"osVersion","op":"version_gte","value":"16.7.10"}`:       false,
		`{"field":"assignee","op":"ends_with","value":"@ENG.example.com"}`: true,
		`{"field":"model","op":"matches","value":"^iPhone1[45],"}`:         true,
		`{"field":"tags","op":"contains","value":"sales"}`:                 true,
		`{"field":"tags","op":"not_contains","value":"vip"}`:               true,
		`{"field":"compliant","op":"eq","value":false}`:                    true,
		`{"field":"lastSeenDays","op":"gt","value":7}`:                     true,
		`{"field":"enrolledDays","op":"lte","value":30}`:                   false,
		`{"field":"facts.apple.BatteryLevel","op":"lt","value":0.5}`:       true,
		`{"field":"facts.apple.IsSupervised","op":"eq","value":true}`:      true,
		`{"field":"facts.linux.diskEncrypted","op":"eq","value":"false"}`:  true,
		`{"field":"facts.windows.missing","op":"exists"}`:                  false,
		`{"field":"facts.windows.missing","op":"not_exists"}`:              true,
		`{"field":"serial","op":"eq","value":"X"}`:                         false, // empty serial is "missing"
		`{"field":"serial","op":"ne","value":"X"}`:                         true,
	}
	for cond, want := range cases {
		r := mustRule(t, `{"match":"all","conditions":[`+cond+`]}`)
		if got := r.Matches(d, time.Now()); got != want {
			t.Errorf("%s = %v, want %v", cond, got, want)
		}
	}
}

func TestNestedAnyAll(t *testing.T) {
	r := mustRule(t, `{"match":"all","conditions":[{"field":"ownership","op":"eq","value":"corporate"}],
		"rules":[{"match":"any","conditions":[{"field":"platform","op":"eq","value":"android"},{"field":"tags","op":"contains","value":"emea"}]}]}`)
	if !r.Matches(device(), time.Now()) {
		t.Error("nested any should match via the emea tag")
	}
	d := device()
	d.Tags = nil
	if r.Matches(d, time.Now()) {
		t.Error("nested any should fail without the tag")
	}
}

func TestValidation(t *testing.T) {
	bad := []string{
		`{"match":"some","conditions":[{"field":"platform","op":"eq","value":"ios"}]}`,
		`{"match":"all"}`,
		`{"match":"all","conditions":[{"field":"nope","op":"eq","value":1}]}`,
		`{"match":"all","conditions":[{"field":"platform","op":"like","value":"x"}]}`,
		`{"match":"all","conditions":[{"field":"name","op":"matches","value":"("}]}`,
		`{"match":"all","conditions":[{"field":"platform","op":"in","value":"ios"}]}`,
		`{"match":"all","conditions":[{"field":"platform","op":"eq"}]}`,
		`{"match":"all","conditions":[{"field":"platform","op":"eq","value":"ios","extra":1}]}`,
	}
	for _, b := range bad {
		if _, err := Parse([]byte(b)); err == nil {
			t.Errorf("accepted %s", b)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"17.2", "17.10", -1}, {"10.0.22631", "10.0.19045", 1}, {"Ubuntu 24.04.1 LTS", "24.04", 1},
		{"14", "14.0.0", 0}, {"", "1", -1},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
