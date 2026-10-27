package messaging

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	waBinary "go.mau.fi/whatsmeow/binary"
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
	extraNodes    []waBinary.Node
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

	var replyButtons []*waE2E.ButtonsMessage_Button
	for i, buttonText := range buttons {
		parts := splitChoice(buttonText)
		buttonName := parts[0]
		buttonId := fmt.Sprintf("btn_%d", i)
		if len(parts) > 1 {
			buttonId = parts[1]
		}
		replyButtons = append(replyButtons, &waE2E.ButtonsMessage_Button{
			ButtonID: proto.String(buttonId),
			ButtonText: &waE2E.ButtonsMessage_Button_ButtonText{
				DisplayText: proto.String(buttonName),
			},
			Type: waE2E.ButtonsMessage_Button_RESPONSE.Enum(),
		})
	}

	contentText := text
	if title != "" {
		contentText = "*" + title + "*\n\n" + text
	}

	buttonsMsg := &waE2E.ButtonsMessage{
		ContentText: proto.String(contentText),
		FooterText:  proto.String(footer),
		HeaderType:  waE2E.ButtonsMessage_EMPTY.Enum(),
		Buttons:     replyButtons,
	}

	btnMsgSecret := make([]byte, 32)
	_, _ = rand.Read(btnMsgSecret)

	b.msg = &waE2E.Message{
		DocumentWithCaptionMessage: &waE2E.FutureProofMessage{
			Message: &waE2E.Message{
				ButtonsMessage: buttonsMsg,
			},
		},
		MessageContextInfo: &waE2E.MessageContextInfo{
			MessageSecret: btnMsgSecret,
		},
	}

	b.extraNodes = []waBinary.Node{
		{
			Tag: "biz",
			Content: []waBinary.Node{{
				Tag: "interactive",
				Attrs: waBinary.Attrs{
					"type": "native_flow",
					"v":    "1",
				},
				Content: []waBinary.Node{{
					Tag: "native_flow",
					Attrs: waBinary.Attrs{
						"name": "quick_reply",
					},
				}},
			}},
		},
	}
	if !strings.Contains(b.jid.String(), "@g.us") {
		b.extraNodes = append(b.extraNodes, waBinary.Node{
			Tag:   "bot",
			Attrs: waBinary.Attrs{"biz_bot": "1"},
		})
	}

	return b
}

func (b *MessageBuilder) WithCopyButton(text string, footer string, title string, copyText string, copyCode string) *MessageBuilder {
	if copyText == "" {
		copyText = "Copy"
	}

	params := map[string]string{
		"display_text": copyText,
		"copy_code":    copyCode,
		"id":           copyCode,
	}

	paramsJSONBytes, _ := json.Marshal(params)
	paramsJSON := string(paramsJSONBytes)

	nfButtons := []*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
		{
			Name:             proto.String("cta_copy"),
			ButtonParamsJSON: proto.String(paramsJSON),
		},
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
				Buttons:           nfButtons,
				MessageVersion:    &msgVersion,
				MessageParamsJSON: proto.String(`{"from":"api","templateId":"` + copyCode + `"}`),
			},
		},
	}

	btnMsgSecret := make([]byte, 32)
	_, _ = rand.Read(btnMsgSecret)

	b.msg = &waE2E.Message{
		DocumentWithCaptionMessage: &waE2E.FutureProofMessage{
			Message: &waE2E.Message{
				InteractiveMessage: interactiveMsg,
			},
		},
		MessageContextInfo: &waE2E.MessageContextInfo{
			MessageSecret: btnMsgSecret,
		},
	}

	b.extraNodes = []waBinary.Node{
		{
			Tag: "biz",
			Content: []waBinary.Node{{
				Tag: "interactive",
				Attrs: waBinary.Attrs{
					"type": "native_flow",
					"v":    "1",
				},
				Content: []waBinary.Node{{
					Tag: "native_flow",
					Attrs: waBinary.Attrs{
						"name": "mixed",
					},
				}},
			}},
		},
	}
	if !strings.Contains(b.jid.String(), "@g.us") {
		b.extraNodes = append(b.extraNodes, waBinary.Node{
			Tag:   "bot",
			Attrs: waBinary.Attrs{"biz_bot": "1"},
		})
	}

	return b
}

func (b *MessageBuilder) WithPoll(question string, options []string, maxAnswer int) *MessageBuilder {
	if b.client != nil {
		b.msg = b.client.BuildPollCreation(question, options, maxAnswer)
	}
	return b
}

func (b *MessageBuilder) WithList(text, footer, title, buttonTextStr string, choices []string) *MessageBuilder {
	// Parse choices into list items
	var sections []*waE2E.ListMessage_Section
	var currentRows []*waE2E.ListMessage_Row
	currentSectionTitle := ""

	for i, choice := range choices {
		if strings.HasPrefix(choice, "[") && strings.HasSuffix(choice, "]") {
			if len(currentRows) > 0 {
				sections = append(sections, &waE2E.ListMessage_Section{
					Title: proto.String(currentSectionTitle),
					Rows:  currentRows,
				})
				currentRows = nil
			}
			currentSectionTitle = choice[1 : len(choice)-1]
			continue
		}

		parts := splitChoice(choice)
		rowTitle := parts[0]
		rowID := fmt.Sprintf("row_%d", i)
		rowDesc := ""

		if len(parts) > 1 {
			rowID = parts[1]
		}
		if len(parts) > 2 {
			rowDesc = parts[2]
		}

		row := &waE2E.ListMessage_Row{
			RowID: proto.String(rowID),
			Title: proto.String(rowTitle),
		}
		if rowDesc != "" {
			row.Description = proto.String(rowDesc)
		}
		currentRows = append(currentRows, row)
	}

	if len(currentRows) > 0 {
		sections = append(sections, &waE2E.ListMessage_Section{
			Title: proto.String(currentSectionTitle),
			Rows:  currentRows,
		})
	}

	if buttonTextStr == "" {
		buttonTextStr = "Menu"
	}

	listType := waE2E.ListMessage_SINGLE_SELECT
	listMessage := &waE2E.ListMessage{
		Title:       proto.String(title),
		Description: proto.String(text),
		FooterText:  proto.String(footer),
		ButtonText:  proto.String(buttonTextStr),
		ListType:    &listType,
		Sections:    sections,
	}

	btnMsgSecret := make([]byte, 32)
	_, _ = rand.Read(btnMsgSecret)

	b.msg = &waE2E.Message{
		DocumentWithCaptionMessage: &waE2E.FutureProofMessage{
			Message: &waE2E.Message{
				ListMessage: listMessage,
			},
		},
		MessageContextInfo: &waE2E.MessageContextInfo{
			MessageSecret: btnMsgSecret,
		},
	}

	b.extraNodes = []waBinary.Node{
		{
			Tag: "biz",
			Content: []waBinary.Node{{
				Tag: "list",
				Attrs: waBinary.Attrs{
					"v":    "2",
					"type": "single_select",
				},
			}},
		},
	}
	// Append biz_bot node if not sending to a group (for broader iOS rendering compatibility)
	if !strings.Contains(b.jid.String(), "@g.us") {
		b.extraNodes = append(b.extraNodes, waBinary.Node{
			Tag:   "bot",
			Attrs: waBinary.Attrs{"biz_bot": "1"},
		})
	}

	return b
}

func (b *MessageBuilder) WithMedia(resp whatsmeow.UploadResponse, mediaType whatsmeow.MediaType, mimeType, fileName, caption string) *MessageBuilder {
	switch mediaType {
	case whatsmeow.MediaImage:
		if mimeType == "" {
			mimeType = "image/jpeg"
		}
		b.msg.ImageMessage = &waE2E.ImageMessage{
			URL:           proto.String(resp.URL),
			DirectPath:    proto.String(resp.DirectPath),
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    proto.Uint64(uint64(resp.FileLength)),
			Mimetype:      proto.String(mimeType),
			Caption:       proto.String(caption),
		}
	case whatsmeow.MediaVideo:
		if mimeType == "" {
			mimeType = "video/mp4"
		}
		b.msg.VideoMessage = &waE2E.VideoMessage{
			URL:           proto.String(resp.URL),
			DirectPath:    proto.String(resp.DirectPath),
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    proto.Uint64(uint64(resp.FileLength)),
			Mimetype:      proto.String(mimeType),
			Caption:       proto.String(caption),
		}
	case whatsmeow.MediaDocument:
		if fileName == "" {
			fileName = "document"
		}
		b.msg.DocumentMessage = &waE2E.DocumentMessage{
			URL:           proto.String(resp.URL),
			DirectPath:    proto.String(resp.DirectPath),
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    proto.Uint64(uint64(resp.FileLength)),
			Mimetype:      proto.String(mimeType),
			FileName:      proto.String(fileName),
			Caption:       proto.String(caption),
		}
	case whatsmeow.MediaAudio:
		if mimeType == "" {
			mimeType = "audio/ogg; codecs=opus"
		}
		b.msg.AudioMessage = &waE2E.AudioMessage{
			URL:           proto.String(resp.URL),
			DirectPath:    proto.String(resp.DirectPath),
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    proto.Uint64(uint64(resp.FileLength)),
			Mimetype:      proto.String(mimeType),
			PTT:           proto.Bool(true), // Treating Audio as PTT by default for standard voice messages
		}
	}
	b.msg.Conversation = nil
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

func (b *MessageBuilder) WithContact(fullName, phone string) *MessageBuilder {
	vcard := fmt.Sprintf("BEGIN:VCARD\nVERSION:3.0\nN:;%v;;;\nFN:%v\nTEL;type=CELL;waid=%v:+%v\nEND:VCARD", fullName, fullName, phone, phone)
	b.msg.ContactMessage = &waE2E.ContactMessage{
		DisplayName: proto.String(fullName),
		Vcard:       proto.String(vcard),
	}
	b.msg.Conversation = nil
	return b
}

func (b *MessageBuilder) WithLocation(lat, long float64, name, address string) *MessageBuilder {
	b.msg.LocationMessage = &waE2E.LocationMessage{
		DegreesLatitude:  proto.Float64(lat),
		DegreesLongitude: proto.Float64(long),
		Name:             proto.String(name),
		Address:          proto.String(address),
	}
	b.msg.Conversation = nil
	return b
}

func (b *MessageBuilder) WithRequestLocationButton(text string, footer string, title string) *MessageBuilder {
	nfButtons := []*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
		{
			Name: proto.String("location_request_message"),
		},
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

	btnMsgSecret := make([]byte, 32)
	_, _ = rand.Read(btnMsgSecret)

	b.msg = &waE2E.Message{
		DocumentWithCaptionMessage: &waE2E.FutureProofMessage{
			Message: &waE2E.Message{
				InteractiveMessage: interactiveMsg,
			},
		},
		MessageContextInfo: &waE2E.MessageContextInfo{
			MessageSecret: btnMsgSecret,
		},
	}
	return b
}

func (b *MessageBuilder) Send(ctx context.Context) (string, error) {
	if b.client == nil {
		return "", fmt.Errorf("client is nil")
	}

	// Debug: Log full message details before sending
	debugf("\n\n====== OUTGOING MESSAGE BUILDER (DEBUG) ======\n")
	debugf("JID: %s\n", b.jid.String())
	debugf("Has MessageContextInfo: %v\n", b.msg.MessageContextInfo != nil)
	if b.msg.MessageContextInfo != nil {
		debugf("Has MessageSecret: %v (len=%d)\n",
			b.msg.MessageContextInfo.MessageSecret != nil,
			len(b.msg.MessageContextInfo.MessageSecret))
	}
	debugf("Extra Nodes Count: %d\n", len(b.extraNodes))
	if len(b.extraNodes) > 0 {
		for i, node := range b.extraNodes {
			debugf("Extra Node[%d]: Tag=%s, Attrs=%v, ContentLen=%d\n",
				i, node.Tag, node.Attrs, len(node.GetChildren()))
		}
	}
	debugf("Protobuf:\n%+v\n", b.msg)
	debugf("==========================================\n\n")

	var err error

	extra := whatsmeow.SendRequestExtra{}
	if len(b.extraNodes) > 0 {
		extra.AdditionalNodes = &b.extraNodes
		debugf("DEBUG: Setting AdditionalNodes (%d nodes)\n", len(b.extraNodes))
	} else {
		debugf("DEBUG: No AdditionalNodes set\n")
	}

	for i := 0; i <= b.retry; i++ {
		debugf("DEBUG: Attempt %d to send message\n", i+1)
		resp, sendErr := b.client.SendMessage(ctx, b.jid, b.msg, extra)
		if sendErr == nil {
			debugf("DEBUG: Message sent successfully, ID=%s\n", resp.ID)
			return resp.ID, nil
		}
		debugf("DEBUG: Send failed: %v\n", sendErr)
		err = sendErr
		time.Sleep(time.Duration(i) * time.Second)
	}

	return "", fmt.Errorf("failed to send message after retries: %w", err)
}
