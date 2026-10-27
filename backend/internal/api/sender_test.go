package api

import (
	"testing"
	"time"

	"github.com/user/whatsmeow-basileia/internal/infrastructure/rabbitmq"
	"github.com/user/whatsmeow-basileia/internal/service/messaging"
)

func TestScheduleStart(t *testing.T) {
	now := time.Now()
	if d := scheduleStart(0).Sub(now); d < -time.Second || d > time.Second {
		t.Fatal("0 means now")
	}
	if d := scheduleStart(10).Sub(now); d < 9*time.Minute || d > 11*time.Minute {
		t.Fatalf("small values are minutes from now, got %v", d)
	}
	ms := now.Add(time.Hour).UnixMilli()
	if !scheduleStart(ms).Equal(time.UnixMilli(ms)) {
		t.Fatal("large values are epoch milliseconds")
	}
}

func TestSpread(t *testing.T) {
	start := time.Now()
	times := spread(5, start, 10, 30)
	if !times[0].Equal(start) {
		t.Fatal("first message goes at the start time")
	}
	for i := 1; i < len(times); i++ {
		gap := times[i].Sub(times[i-1])
		if gap < 10*time.Second || gap > 30*time.Second {
			t.Fatalf("gap %d out of range: %v", i, gap)
		}
	}
}

func TestToItem(t *testing.T) {
	at := time.Now()
	img := toItem(senderMessage{Number: "5511", Type: "image", File: "https://x/y.jpg", Text: "legenda"}, at)
	if img.Queue != rabbitmq.QueueSendMedia {
		t.Fatal("media goes to the media queue")
	}
	p := img.Payload.(messaging.SendMediaPayload)
	if p.MediaURL != "https://x/y.jpg" || p.Caption != "legenda" || p.Base64 != "" {
		t.Fatalf("unexpected media payload %+v", p)
	}

	b64 := toItem(senderMessage{Number: "5511", Type: "document", File: "data:application/pdf;base64,AAAA"}, at)
	if b64.Payload.(messaging.SendMediaPayload).Base64 == "" {
		t.Fatal("data URIs are sent as base64")
	}

	menu := toItem(senderMessage{Number: "5511", Type: "menu", Text: "Escolha", Choices: []string{"A", "B"}}, at)
	mp := menu.Payload.(messaging.SendMessagePayload)
	if menu.Queue != rabbitmq.QueueSendMessage || mp.Type != "list" || mp.Message != "Escolha" {
		t.Fatalf("unexpected menu payload %+v", mp)
	}
	if toItem(senderMessage{Number: "5511", Type: "text"}, at).Payload.(messaging.SendMessagePayload).Type != "" {
		t.Fatal("text maps to the default builder type")
	}
}
