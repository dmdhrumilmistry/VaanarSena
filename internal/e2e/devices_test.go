package e2e

import (
	"testing"

	"github.com/dmdhrumilmistry/VaanarSena/internal/agent"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

// TestReclassifyingOwnershipReappliesConfiguration checks that changing a
// device's ownership re-evaluates smart groups (ownership is a rule field) and
// pushes the policy again, since translators treat personal devices
// differently.
func TestReclassifyingOwnershipReappliesConfiguration(t *testing.T) {
	e := setup(t)
	e.call("POST", "/api/v1/apply", `
apiVersion: vaanarsena.io/v1
kind: Group
metadata: {name: company-owned}
spec:
  kind: smart
  rules:
    match: all
    conditions:
      - {field: ownership, op: eq, value: corporate}
---
apiVersion: vaanarsena.io/v1
kind: Policy
metadata: {name: block-usb}
spec:
  groups: [company-owned]
  document:
    restrictions: {usbStorage: false}
`, 200, nil)

	a := e.enrollAgent("personal", "reclass-1")
	facts := map[string]any{"hostname": "reclass-1"}
	first := a.checkin(facts, nil)
	if first.Policy != nil && first.Policy.Restrictions != nil {
		t.Fatalf("personal device got the company-owned policy: %+v", first.Policy)
	}
	quiet := a.checkin(facts, ackAll(first.Commands))
	a.checkin(facts, ackAll(quiet.Commands))

	e.call("PATCH", "/api/v1/devices/"+a.id, `{"ownership":"corporate"}`, 200, nil)
	cr := a.checkin(facts, nil)
	if !types(cr.Commands)["apply_policy"] {
		t.Errorf("no policy push after reclassification: %v", types(cr.Commands))
	}
	if cr.Personal {
		t.Error("agent still told it is personal")
	}
	if cr.Policy == nil || cr.Policy.Restrictions == nil || cr.Policy.Restrictions.USBStorage == nil || *cr.Policy.Restrictions.USBStorage {
		t.Errorf("reclassified device did not join the smart group's policy: %+v", cr.Policy)
	}
}

func TestDeviceComplianceFilter(t *testing.T) {
	e := setup(t)
	bad := e.enrollAgent("corporate", "noncompliant-1")
	good := e.enrollAgent("corporate", "compliant-1")
	unknown := e.enrollAgent("corporate", "unknown-1")
	bad.checkinWith(agent.CheckinRequest{Facts: map[string]any{"hostname": "b"}, Compliance: &agent.Compliance{Compliant: false, Issues: []string{"x"}}})
	good.checkinWith(agent.CheckinRequest{Facts: map[string]any{"hostname": "g"}, Compliance: &agent.Compliance{Compliant: true}})
	unknown.checkin(map[string]any{"hostname": "u"}, nil)

	for filter, want := range map[string]string{"false": bad.id, "true": good.id, "unknown": unknown.id} {
		var out struct {
			Devices []store.Device
			Total   int
		}
		e.call("GET", "/api/v1/devices?compliant="+filter, "", 200, &out)
		if out.Total != 1 || len(out.Devices) != 1 || out.Devices[0].ID != want {
			t.Errorf("compliant=%s: total %d, devices %+v", filter, out.Total, out.Devices)
		}
	}
	e.call("GET", "/api/v1/devices?compliant=maybe", "", 400, nil)
}
