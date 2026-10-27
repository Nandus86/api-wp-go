package messages

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

func TestNormalizeID(t *testing.T) {
	for in, want := range map[string]string{
		"3EB0ABC":               "3EB0ABC",
		"5511999999999:3EB0ABC": "3EB0ABC",
		" 3EB0ABC ":             "3EB0ABC",
	} {
		if got := NormalizeID(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestStatusFromReceipt(t *testing.T) {
	cases := map[types.ReceiptType]string{
		types.ReceiptTypeDelivered: "delivered",
		types.ReceiptTypeRead:      "read",
		types.ReceiptTypeReadSelf:  "read",
		types.ReceiptTypePlayed:    "played",
		types.ReceiptTypeRetry:     "",
		types.ReceiptTypeSender:    "",
	}
	for in, want := range cases {
		if got := StatusFromReceipt(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestBuildRowTypes(t *testing.T) {
	chat := types.NewJID("5511999999999", types.DefaultUserServer)

	text := buildRow("i", "1", chat, chat, false, false, "Ana", &waE2E.Message{Conversation: proto.String("olá")}, nowUTC(), "received")
	if text.m.Type != "conversation" || text.m.Text != "olá" || len(text.m.raw) == 0 {
		t.Fatalf("unexpected text row %+v", text.m)
	}

	list := buildRow("i", "2", chat, chat, false, false, "", &waE2E.Message{
		ListResponseMessage: &waE2E.ListResponseMessage{Title: proto.String("Opção 1")},
	}, nowUTC(), "received")
	if list.m.Type != "listResponseMessage" {
		t.Fatalf("types unknown to the parser must keep their protobuf name, got %q", list.m.Type)
	}

	if hasContent(&waE2E.Message{SenderKeyDistributionMessage: &waE2E.SenderKeyDistributionMessage{}}) {
		t.Fatal("a bare sender key distribution message is not content")
	}
}

func TestExtFor(t *testing.T) {
	for mime, want := range map[string]string{
		"image/jpeg":             ".jpg",
		"audio/ogg; codecs=opus": ".ogg",
		"application/pdf":        ".pdf",
		"application/x-unknown":  "",
	} {
		if got := ExtFor(mime); got != want {
			t.Errorf("%s: got %q want %q", mime, got, want)
		}
	}
}

func nowUTC() time.Time { return time.Now().UTC() }
