package api

import (
	"strings"
	"testing"
	"time"
)

func TestParseEphemeral(t *testing.T) {
	cases := []struct {
		in   interface{}
		want time.Duration
		err  bool
	}{
		{"off", 0, false},
		{"0", 0, false},
		{float64(0), 0, false},
		{"1d", 24 * time.Hour, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"90d", 90 * 24 * time.Hour, false},
		{float64(86400), 24 * time.Hour, false},
		{"86400", 24 * time.Hour, false},
		{nil, 0, true},
		{"soon", 0, true},
		{true, 0, true},
	}
	for _, c := range cases {
		got, err := parseEphemeral(c.in)
		if (err != nil) != c.err || got != c.want {
			t.Errorf("%v: got %v, %v", c.in, got, err)
		}
	}
}

func TestInviteCode(t *testing.T) {
	for in, want := range map[string]string{
		"AbC123":                            "AbC123",
		"https://chat.whatsapp.com/AbC123":  "AbC123",
		" https://whatsapp.com/channel/XyZ": "XyZ",
	} {
		if got := inviteCode(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestParseNewsletterJID(t *testing.T) {
	jid, err := parseNewsletterJID("120363000000000000", "")
	if err != nil || jid.String() != "120363000000000000@newsletter" {
		t.Fatalf("id without server: %v %v", jid, err)
	}
	jid, err = parseNewsletterJID("ignored", "120363000000000001@newsletter")
	if err != nil || jid.String() != "120363000000000001@newsletter" {
		t.Fatalf("jid wins over id: %v %v", jid, err)
	}
	if _, err := parseNewsletterJID("", ""); err == nil {
		t.Fatal("empty must fail")
	}
}

func TestParseGroupJID(t *testing.T) {
	if _, err := parseGroupJID("120363025246125486@g.us"); err != nil {
		t.Fatal(err)
	}
	if _, err := parseGroupJID("5511999999999"); err == nil {
		t.Fatal("a phone number is not a group")
	}
}

func TestUnixAuto(t *testing.T) {
	if !unixAuto(1700000000).Equal(time.Unix(1700000000, 0)) {
		t.Fatal("seconds")
	}
	if !unixAuto(1700000000123).Equal(time.UnixMilli(1700000000123)) {
		t.Fatal("milliseconds")
	}
}

func TestQRDataURL(t *testing.T) {
	if u := qrDataURL("2@abc,def,ghi"); !strings.HasPrefix(u, "data:image/png;base64,") || len(u) < 200 {
		t.Fatalf("unexpected data url %q", u[:40])
	}
}
