package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

// The shipped examples are documentation; keep them valid.
func TestExamplesValidate(t *testing.T) {
	files, err := filepath.Glob("../../examples/manifests/*.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no example manifests found: %v", err)
	}
	kinds := map[string]int{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		rs, err := Decode(data)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, r := range rs {
			if err := r.Validate(); err != nil {
				t.Errorf("%s: %s %q: %v", f, r.Kind, r.Metadata.Name, err)
			}
			kinds[r.Kind]++
		}
	}
	for _, k := range []string{KindGroup, KindPolicy, KindBlueprint} {
		if kinds[k] == 0 {
			t.Errorf("examples contain no %s", k)
		}
	}
}
