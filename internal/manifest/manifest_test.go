package manifest

import (
	"strings"
	"testing"
)

const sample = `
apiVersion: vaanarsena.io/v1
kind: Group
metadata: {name: outdated-iphones}
spec:
  kind: smart
  rules:
    match: all
    conditions:
      - {field: platform, op: eq, value: ios}
      - {field: osVersion, op: version_lt, value: "17.0"}
---
apiVersion: vaanarsena.io/v1
kind: Policy
metadata: {name: baseline, description: Every corporate device}
spec:
  priority: 10
  groups: [outdated-iphones]
  document:
    passcode: {required: true, minLength: 8}
    custom:
      windows:
        - {locUri: ./Device/Vendor/MSFT/Policy/Config/Experience/AllowCortana, format: int, data: "0"}
---
---
apiVersion: vaanarsena.io/v1
kind: Blueprint
metadata: {name: sales-onboarding}
spec:
  groups: [outdated-iphones]
  policies: [baseline]
  onEnroll:
    - {type: os_update}
`

func TestDecodeYAMLAndValidate(t *testing.T) {
	rs, err := Decode([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 3 {
		t.Fatalf("decoded %d resources, want 3 (empty documents skipped)", len(rs))
	}
	for _, r := range rs {
		if err := r.Validate(); err != nil {
			t.Errorf("%s %s: %v", r.Kind, r.Metadata.Name, err)
		}
	}
	ps, _ := rs[1].PolicySpec()
	if ps.Priority != 10 || ps.Document.Custom == nil || len(ps.Document.Custom.Windows) != 1 {
		t.Errorf("policy spec: %+v", ps)
	}
}

func TestDecodeJSONForms(t *testing.T) {
	one := `{"apiVersion":"vaanarsena.io/v1","kind":"Group","metadata":{"name":"a"},"spec":{"kind":"static"}}`
	for _, in := range []string{one, "[" + one + "," + strings.Replace(one, `"a"`, `"b"`, 1) + "]", `{"items":[` + one + `]}`} {
		rs, err := Decode([]byte(in))
		if err != nil || len(rs) == 0 {
			t.Errorf("decode %s: %v", in, err)
		}
	}
}

func TestRejects(t *testing.T) {
	bad := map[string]string{
		"unknown top-level field": "apiVersion: vaanarsena.io/v1\nkind: Group\nmetadata: {name: a}\nspec: {kind: static}\nstatus: {}\n",
		"wrong apiVersion":        "apiVersion: v2\nkind: Group\nmetadata: {name: a}\nspec: {kind: static}\n",
		"unknown kind":            "apiVersion: vaanarsena.io/v1\nkind: Device\nmetadata: {name: a}\nspec: {}\n",
		"bad name":                "apiVersion: vaanarsena.io/v1\nkind: Group\nmetadata: {name: '../x'}\nspec: {kind: static}\n",
		"static with rules":       "apiVersion: vaanarsena.io/v1\nkind: Group\nmetadata: {name: a}\nspec: {kind: static, rules: {match: all, conditions: [{field: platform, op: eq, value: ios}]}}\n",
		"smart without rules":     "apiVersion: vaanarsena.io/v1\nkind: Group\nmetadata: {name: a}\nspec: {kind: smart}\n",
		"policy typo":             "apiVersion: vaanarsena.io/v1\nkind: Policy\nmetadata: {name: a}\nspec: {document: {passcod: {}}}\n",
		"wipe onboarding":         "apiVersion: vaanarsena.io/v1\nkind: Blueprint\nmetadata: {name: a}\nspec: {onEnroll: [{type: wipe}]}\n",
		"empty blueprint":         "apiVersion: vaanarsena.io/v1\nkind: Blueprint\nmetadata: {name: a}\nspec: {groups: [x]}\n",
		"custom mdm payload":      "apiVersion: vaanarsena.io/v1\nkind: Policy\nmetadata: {name: a}\nspec: {document: {custom: {apple: [{payload: {PayloadType: com.apple.mdm}}]}}}\n",
		"bad windows uri":         "apiVersion: vaanarsena.io/v1\nkind: Policy\nmetadata: {name: a}\nspec: {document: {custom: {windows: [{locUri: Device/x}]}}}\n",
	}
	for name, in := range bad {
		rs, err := Decode([]byte(in))
		if err == nil {
			err = rs[0].Validate()
		}
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestEncodeRoundTrip(t *testing.T) {
	rs, err := Decode([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	y, err := EncodeYAML(rs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(y), "apiVersion: vaanarsena.io/v1\nkind: Group\n") {
		t.Errorf("unexpected key order:\n%s", y)
	}
	back, err := Decode(y)
	if err != nil || len(back) != len(rs) {
		t.Fatalf("round trip: %v (%d resources)", err, len(back))
	}
	for i := range rs {
		if canon(rs[i].Spec) != canon(back[i].Spec) {
			t.Errorf("resource %d spec changed in round trip", i)
		}
	}
}

func TestDecodeStripsBOM(t *testing.T) {
	bom := "\xEF\xBB\xBF"
	in := bom + "apiVersion: vaanarsena.io/v1\nkind: Group\nmetadata: {name: a}\nspec: {kind: static}\n---\n" +
		bom + "apiVersion: vaanarsena.io/v1\nkind: Group\nmetadata: {name: b}\nspec: {kind: static}\n"
	rs, err := Decode([]byte(in))
	if err != nil || len(rs) != 2 {
		t.Fatalf("decode with BOMs: %v (%d)", err, len(rs))
	}
}
