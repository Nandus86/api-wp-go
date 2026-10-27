package messaging

import (
	"fmt"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// Debug enables the verbose protobuf dumps (DEBUG_MESSAGES=true).
var Debug bool

func debugf(format string, args ...interface{}) {
	if Debug {
		fmt.Printf(format, args...)
	}
}

// Message exposes the built protobuf (stored in the message history after sending).
func (b *MessageBuilder) Message() *waE2E.Message { return b.msg }

// JID returns the recipient.
func (b *MessageBuilder) JID() types.JID { return b.jid }

// WithContextInfo attaches reply/mention metadata to whatever message was built.
// A plain Conversation is upgraded to an ExtendedTextMessage, which is the only text
// type that can carry a ContextInfo.
func (b *MessageBuilder) WithContextInfo(ci *waE2E.ContextInfo) *MessageBuilder {
	if ci == nil {
		return b
	}
	m := b.msg
	switch {
	case m.Conversation != nil:
		m.ExtendedTextMessage = &waE2E.ExtendedTextMessage{Text: m.Conversation, ContextInfo: ci}
		m.Conversation = nil
	case m.ExtendedTextMessage != nil:
		m.ExtendedTextMessage.ContextInfo = mergeContext(m.ExtendedTextMessage.ContextInfo, ci)
	case m.ImageMessage != nil:
		m.ImageMessage.ContextInfo = mergeContext(m.ImageMessage.ContextInfo, ci)
	case m.VideoMessage != nil:
		m.VideoMessage.ContextInfo = mergeContext(m.VideoMessage.ContextInfo, ci)
	case m.DocumentMessage != nil:
		m.DocumentMessage.ContextInfo = mergeContext(m.DocumentMessage.ContextInfo, ci)
	case m.AudioMessage != nil:
		m.AudioMessage.ContextInfo = mergeContext(m.AudioMessage.ContextInfo, ci)
	case m.ContactMessage != nil:
		m.ContactMessage.ContextInfo = mergeContext(m.ContactMessage.ContextInfo, ci)
	case m.LocationMessage != nil:
		m.LocationMessage.ContextInfo = mergeContext(m.LocationMessage.ContextInfo, ci)
	}
	return b
}

func mergeContext(dst, src *waE2E.ContextInfo) *waE2E.ContextInfo {
	if dst == nil {
		return src
	}
	if src.StanzaID != nil {
		dst.StanzaID = src.StanzaID
		dst.Participant = src.Participant
		dst.QuotedMessage = src.QuotedMessage
	}
	if len(src.MentionedJID) > 0 {
		dst.MentionedJID = src.MentionedJID
	}
	return dst
}

// QuoteContext builds the ContextInfo used to reply to a stored message.
func QuoteContext(id string, sender types.JID, quoted *waE2E.Message) *waE2E.ContextInfo {
	ci := &waE2E.ContextInfo{StanzaID: proto.String(id), QuotedMessage: quoted}
	if !sender.IsEmpty() {
		ci.Participant = proto.String(sender.ToNonAD().String())
	}
	return ci
}
