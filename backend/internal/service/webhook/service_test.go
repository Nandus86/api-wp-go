package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func TestSign(t *testing.T) {
	body := []byte(`{"event":"messages"}`)
	got := Sign("s3cret", "1700000000", body)

	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write([]byte("1700000000." + string(body)))
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	if Sign("other", "1700000000", body) == got {
		t.Fatal("different secrets must produce different signatures")
	}
	if Sign("s3cret", "1700000001", body) == got {
		t.Fatal("the timestamp must be part of the signature")
	}
}

func TestBackoffGrowsAndIsBounded(t *testing.T) {
	prev := time.Duration(0)
	for attempt := 1; attempt <= len(backoffSteps); attempt++ {
		base := backoffSteps[attempt-1]
		for i := 0; i < 20; i++ {
			d := Backoff(attempt)
			if d < base-base/10 || d > base+base/10 {
				t.Fatalf("attempt %d: %v outside ±10%% of %v", attempt, d, base)
			}
		}
		if base <= prev {
			t.Fatalf("backoff must grow: step %d (%v) <= %v", attempt, base, prev)
		}
		prev = base
	}
	last := backoffSteps[len(backoffSteps)-1]
	if d := Backoff(100); d > last+last/10 {
		t.Fatalf("backoff must be capped, got %v", d)
	}
	if d := Backoff(0); d > backoffSteps[0]+backoffSteps[0]/10 {
		t.Fatalf("attempt 0 behaves like 1, got %v", d)
	}
}

func TestMatchesEvents(t *testing.T) {
	cases := []struct {
		events []string
		event  string
		want   bool
	}{
		{nil, "messages", true},
		{[]string{}, "presence.update", true},
		{[]string{"messages"}, "messages", true},
		{[]string{"messages"}, "receipts.update", false},
		{[]string{"messages_update"}, "receipts.update", true}, // UAZAPI alias
		{[]string{"connection"}, "connection.update", true},
		{[]string{"call"}, "calls.offer", true},
		{[]string{"*"}, "groups.update", true},
	}
	for _, c := range cases {
		if got := Matches(c.events, nil, c.event, []byte(`{}`)); got != c.want {
			t.Errorf("Matches(%v, %s) = %v, want %v", c.events, c.event, got, c.want)
		}
	}
}

func TestMatchesExcludeMessages(t *testing.T) {
	group := []byte(`{"message":{"fromMe":false,"isGroup":true,"wasSentByApi":false}}`)
	mine := []byte(`{"message":{"fromMe":true,"isGroup":false,"wasSentByApi":true}}`)

	if Matches(nil, []string{"isGroupYes"}, "messages", group) {
		t.Error("isGroupYes must drop group messages")
	}
	if !Matches(nil, []string{"isGroupYes"}, "messages", mine) {
		t.Error("isGroupYes must keep direct messages")
	}
	if Matches(nil, []string{"fromMeYes"}, "messages", mine) {
		t.Error("fromMeYes must drop own messages")
	}
	if Matches(nil, []string{"wasSentByApi"}, "messages", mine) {
		t.Error("wasSentByApi must drop API sends")
	}
	if !Matches(nil, []string{"isGroupYes"}, "receipts.update", group) {
		t.Error("message filters only apply to the messages event")
	}
}

func TestValidateURL(t *testing.T) {
	for _, ok := range []string{"http://n8n:5678/webhook/x", "https://example.com/hook?a=1"} {
		if err := ValidateURL(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "example.com", "ftp://x/y", "http://", "/relative"} {
		if err := ValidateURL(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}
