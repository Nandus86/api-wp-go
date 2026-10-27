package messaging

import (
	"context"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

func TestParseJIDOrNumber(t *testing.T) {
	cases := []struct {
		in, want string
		err      bool
	}{
		{"5511999999999", "5511999999999@s.whatsapp.net", false},
		{"+55 (11) 99999-9999", "5511999999999@s.whatsapp.net", false},
		{"120363025246125486@g.us", "120363025246125486@g.us", false},
		{"5511999999999-1600000000", "5511999999999-1600000000@g.us", false},
		{"120363025246125486", "120363025246125486@g.us", false},
		{"123456789@lid", "123456789@lid", false},
		{"120363000000000000@newsletter", "120363000000000000@newsletter", false},
		{"123", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := ParseJIDOrNumber(c.in)
		if c.err {
			if err == nil {
				t.Errorf("%q: expected error, got %s", c.in, got)
			} else if !isPermanent(err) {
				t.Errorf("%q: invalid numbers must be permanent errors", c.in)
			}
			continue
		}
		if err != nil || got.String() != c.want {
			t.Errorf("%q: got %s (%v), want %s", c.in, got, err, c.want)
		}
	}
}

func TestJobBackoff(t *testing.T) {
	if jobBackoff(0) != 15*time.Second || jobBackoff(1) != 15*time.Second {
		t.Fatal("first retry after 15s")
	}
	if jobBackoff(3) != 5*time.Minute {
		t.Fatalf("third retry after 5m, got %v", jobBackoff(3))
	}
	if jobBackoff(99) != time.Hour {
		t.Fatal("backoff is capped at 1h")
	}
}

func TestPacerDailyCapBySessionAge(t *testing.T) {
	p := NewPacer(PacingConfig{Enabled: true, Schedule: []int{10, 20, 40}}, nil)
	now := time.Now()
	day := func(d int) *time.Time { t := now.Add(-time.Duration(d)*24*time.Hour - time.Minute); return &t }
	if p.DailyCap(nil, now) != 10 {
		t.Fatal("brand new session gets the first step")
	}
	if p.DailyCap(day(1), now) != 20 || p.DailyCap(day(2), now) != 40 || p.DailyCap(day(30), now) != 40 {
		t.Fatal("cap must follow the schedule and stay at the last value")
	}
}

func TestPacerReserveAndBreaker(t *testing.T) {
	ctx := context.Background()
	p := NewPacer(PacingConfig{Enabled: true, Schedule: []int{3}, ColdCap: 1, FailBreaker: 2, BreakerPause: time.Minute}, nil)

	if !p.Reserve(ctx, "i", nil, true).Allowed {
		t.Fatal("first cold send allowed")
	}
	if d := p.Reserve(ctx, "i", nil, true); d.Allowed || d.RetryAt.IsZero() {
		t.Fatal("second cold send must hit the cold cap")
	}
	if !p.Reserve(ctx, "i", nil, false).Allowed || !p.Reserve(ctx, "i", nil, false).Allowed {
		t.Fatal("warm sends allowed up to the daily cap")
	}
	if p.Reserve(ctx, "i", nil, false).Allowed {
		t.Fatal("daily cap of 3 reached")
	}
	p.Release(ctx, "i", false)
	if !p.Reserve(ctx, "i", nil, false).Allowed {
		t.Fatal("released reservation can be reused")
	}

	q := NewPacer(PacingConfig{Enabled: true, Schedule: []int{100}, FailBreaker: 2, BreakerPause: time.Minute}, nil)
	q.RecordFailure(ctx, "j")
	if !q.Reserve(ctx, "j", nil, false).Allowed {
		t.Fatal("one failure does not trip the breaker")
	}
	q.RecordFailure(ctx, "j")
	if d := q.Reserve(ctx, "j", nil, false); d.Allowed || d.RetryAt.Before(time.Now()) {
		t.Fatal("breaker must pause the instance")
	}

	off := NewPacer(PacingConfig{Enabled: false, Schedule: []int{1}}, nil)
	for i := 0; i < 5; i++ {
		if !off.Reserve(ctx, "k", nil, true).Allowed {
			t.Fatal("disabled pacer never blocks")
		}
	}
}

func TestWithContextInfoUpgradesText(t *testing.T) {
	b := &MessageBuilder{msg: &waE2E.Message{Conversation: proto.String("hi")}}
	b.WithContextInfo(QuoteContext("ABC", types.NewJID("5511", types.DefaultUserServer), nil))
	m := b.Message()
	if m.Conversation != nil || m.ExtendedTextMessage == nil || m.ExtendedTextMessage.GetText() != "hi" {
		t.Fatal("conversation must become an extended text message")
	}
	ci := m.ExtendedTextMessage.GetContextInfo()
	if ci.GetStanzaID() != "ABC" || ci.GetParticipant() != "5511@s.whatsapp.net" {
		t.Fatalf("unexpected context %+v", ci)
	}
	b.WithContextInfo(&waE2E.ContextInfo{MentionedJID: []string{"1@s.whatsapp.net"}})
	if got := m.ExtendedTextMessage.GetContextInfo(); got.GetStanzaID() != "ABC" || len(got.MentionedJID) != 1 {
		t.Fatal("mentions must merge with the existing reply context")
	}
}
