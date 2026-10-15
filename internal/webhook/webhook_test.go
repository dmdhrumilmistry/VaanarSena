package webhook

import (
	"testing"
	"time"
)

func TestSignAndVerify(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	body := []byte(`{"id":"1"}`)
	h := Sign("whsec_x", now, body)
	if err := Verify("whsec_x", h, body, now.Add(time.Minute), 5*time.Minute); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if Verify("whsec_y", h, body, now, 5*time.Minute) == nil {
		t.Error("wrong secret accepted")
	}
	if Verify("whsec_x", h, []byte(`{"id":"2"}`), now, 5*time.Minute) == nil {
		t.Error("modified body accepted")
	}
	if Verify("whsec_x", h, body, now.Add(time.Hour), 5*time.Minute) == nil {
		t.Error("stale timestamp accepted")
	}
	if Verify("whsec_x", "v1=abc", body, now, 5*time.Minute) == nil {
		t.Error("header without timestamp accepted")
	}
}

func TestBackoff(t *testing.T) {
	if Backoff(1) != 30*time.Second || Backoff(2) != time.Minute || Backoff(3) != 2*time.Minute {
		t.Errorf("backoff: %v %v %v", Backoff(1), Backoff(2), Backoff(3))
	}
	if Backoff(40) != 6*time.Hour {
		t.Errorf("backoff not capped: %v", Backoff(40))
	}
}

func TestValidateURL(t *testing.T) {
	for _, ok := range []string{"https://idp.example.com/hooks/vs", "http://10.0.0.5:8080/x"} {
		if err := ValidateURL(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "ftp://x/y", "https://", "https://user:pw@x/y", "/relative", "https://x/y#frag"} {
		if ValidateURL(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
