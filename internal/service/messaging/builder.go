package messaging

import (
	"context"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

type MessageBuilder struct {
	client        *whatsmeow.Client
	jid           types.JID
	msg           *waE2E.Message
	retry         int
	isInteractive bool
}

func NewMessageBuilder(client *whatsmeow.Client, jidStr string) *MessageBuilder {
	jid, _ := types.ParseJID(jidStr)
	return &MessageBuilder{
		client: client,
		jid:    jid,
		msg:    &waE2E.Message{},
	}
}

func (b *MessageBuilder) WithText(text string) *MessageBuilder {
	b.msg.Conversation = proto.String(text)
	return b
}

func (b *MessageBuilder) WithButtons(text string, footer string, title string, buttons []string) *MessageBuilder {
	var nfButtons []*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton

	for i, btn := range buttons {
		parts := splitChoice(btn)
		displayText := parts[0]
		buttonID := generateID(i)
		if len(parts) > 1 {
			buttonID = parts[1]
		}

		paramsJSON := fmt.Sprintf(`{"display_text":"%s","id":"%s"}`, displayText, buttonID)
		nfButtons = append(nfButtons, &waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
			Name:             proto.String("quick_reply"),
			ButtonParamsJSON: proto.String(paramsJSON),
		})
	}

	msgVersion := int32(1)

	interactiveMsg := &waE2E.InteractiveMessage{
		Body: &waE2E.InteractiveMessage_Body{
			Text: proto.String(text),
		},
		Footer: &waE2E.InteractiveMessage_Footer{
			Text: proto.String(footer),
		},
		Header: &waE2E.InteractiveMessage_Header{
			Title:              proto.String(title),
			HasMediaAttachment: proto.Bool(false),
		},
		InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{
			NativeFlowMessage: &waE2E.InteractiveMessage_NativeFlowMessage{
				Buttons:        nfButtons,
				MessageVersion: &msgVersion,
			},
		},
	}

	b.msg.Conversation = nil
	b.msg.InteractiveMessage = interactiveMsg
	return b
}

func (b *MessageBuilder) WithList(text string, footer string, title string, buttonText string, choices []string) *MessageBuilder {
	// Interactive messages blocked by WhatsApp for non-business accounts / unstable
	// Using rich text format with Unicode box drawing for visual appeal (Working solution)

	listText := fmt.Sprintf("╔═══ *%s* ═══╗\n\n%s\n", title, text)

	sectionCount := 0
	for i, choice := range choices {
		if len(choice) > 2 && choice[0] == '[' && choice[len(choice)-1] == ']' {
			// Section header with visual separator
			if sectionCount > 0 {
				listText += "\n"
			}
			listText += fmt.Sprintf("├─ *%s*\n", choice[1:len(choice)-1])
			sectionCount++
			continue
		}

		parts := splitChoice(choice)
		rowTitle := parts[0]
		rowDesc := ""
		if len(parts) > 2 {
			rowDesc = parts[2]
		}

		// Numbered list item
		if rowDesc != "" {
			listText += fmt.Sprintf("│  %d. *%s*\n│     _%s_\n", i+1, rowTitle, rowDesc)
		} else {
			listText += fmt.Sprintf("│  %d. %s\n", i+1, rowTitle)
		}
	}

	listText += fmt.Sprintf("\n╚═══════════════╝\n_%s_", footer)

	b.msg.Conversation = proto.String(listText)
	return b
}

func splitChoice(choice string) []string {
	// Simple split by pipe
	var parts []string
	current := ""
	for _, char := range choice {
		if char == '|' {
			parts = append(parts, current)
			current = ""
		} else {
			current += string(char)
		}
	}
	parts = append(parts, current)
	return parts
}

func generateID(i int) string {
	return fmt.Sprintf("id_%d", i)
}

func (b *MessageBuilder) Send(ctx context.Context) (string, error) {
	if b.client == nil {
		return "", fmt.Errorf("client is nil")
	}

	fmt.Printf("\n\n====== OUTGOING MESSAGE BUILDER ======\nJID: %s\nProtobuf:\n%+v\n==========================================\n\n", b.jid.String(), b.msg)

	var err error

	for i := 0; i <= b.retry; i++ {
		resp, sendErr := b.client.SendMessage(ctx, b.jid, b.msg)
		if sendErr == nil {
			return resp.ID, nil
		}
		err = sendErr
		time.Sleep(time.Duration(i) * time.Second)
	}

	return "", fmt.Errorf("failed to send message after retries: %w", err)
}
