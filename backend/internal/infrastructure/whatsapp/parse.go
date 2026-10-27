package whatsapp

import (
	"go.mau.fi/whatsmeow/proto/waE2E"
)

func extractContextInfo(ctx *waE2E.ContextInfo) map[string]interface{} {
	if ctx == nil {
		return nil
	}
	res := make(map[string]interface{})
	if ctx.StanzaID != nil {
		res["quotedMessageId"] = ctx.GetStanzaID()
	}
	if ctx.Participant != nil {
		res["quotedParticipant"] = ctx.GetParticipant()
	}
	if ctx.QuotedMessage != nil {
		if ctx.QuotedMessage.Conversation != nil {
			res["quotedMessage"] = ctx.QuotedMessage.GetConversation()
		} else if ctx.QuotedMessage.ExtendedTextMessage != nil {
			res["quotedMessage"] = ctx.QuotedMessage.GetExtendedTextMessage().GetText()
		}
	}
	if len(ctx.MentionedJID) > 0 {
		res["mentionedJids"] = ctx.MentionedJID
	}
	return res
}

func parseMessageContent(msg *waE2E.Message) (
	msgType string,
	content map[string]interface{},
	contextInfo map[string]interface{},
	reaction map[string]interface{},
	poll map[string]interface{},
) {
	if msg == nil {
		return "empty", nil, nil, nil, nil
	}

	content = make(map[string]interface{})

	if msg.Conversation != nil {
		content["conversation"] = msg.GetConversation()
		return "conversation", content, nil, nil, nil
	}

	if msg.ExtendedTextMessage != nil {
		ext := msg.GetExtendedTextMessage()
		content["text"] = ext.GetText()
		if ext.ContextInfo != nil {
			contextInfo = extractContextInfo(ext.ContextInfo)
		}
		return "extendedTextMessage", content, contextInfo, nil, nil
	}

	if msg.ImageMessage != nil {
		img := msg.GetImageMessage()
		content["caption"] = img.GetCaption()
		content["mimetype"] = img.GetMimetype()
		content["fileLength"] = img.GetFileLength()
		content["url"] = img.GetURL()
		if img.ContextInfo != nil {
			contextInfo = extractContextInfo(img.ContextInfo)
		}
		return "imageMessage", content, contextInfo, nil, nil
	}

	if msg.VideoMessage != nil {
		vid := msg.GetVideoMessage()
		content["caption"] = vid.GetCaption()
		content["mimetype"] = vid.GetMimetype()
		content["fileLength"] = vid.GetFileLength()
		content["url"] = vid.GetURL()
		content["gifPlayback"] = vid.GetGifPlayback()
		if vid.ContextInfo != nil {
			contextInfo = extractContextInfo(vid.ContextInfo)
		}
		return "videoMessage", content, contextInfo, nil, nil
	}

	if msg.AudioMessage != nil {
		aud := msg.GetAudioMessage()
		content["mimetype"] = aud.GetMimetype()
		content["fileLength"] = aud.GetFileLength()
		content["url"] = aud.GetURL()
		content["ptt"] = aud.GetPTT()
		if aud.ContextInfo != nil {
			contextInfo = extractContextInfo(aud.ContextInfo)
		}
		return "audioMessage", content, contextInfo, nil, nil
	}

	if msg.DocumentMessage != nil {
		doc := msg.GetDocumentMessage()
		content["title"] = doc.GetTitle()
		content["fileName"] = doc.GetFileName()
		content["mimetype"] = doc.GetMimetype()
		content["fileLength"] = doc.GetFileLength()
		content["url"] = doc.GetURL()
		if doc.ContextInfo != nil {
			contextInfo = extractContextInfo(doc.ContextInfo)
		}
		return "documentMessage", content, contextInfo, nil, nil
	}

	if msg.StickerMessage != nil {
		stk := msg.GetStickerMessage()
		content["mimetype"] = stk.GetMimetype()
		content["fileLength"] = stk.GetFileLength()
		content["url"] = stk.GetURL()
		if stk.ContextInfo != nil {
			contextInfo = extractContextInfo(stk.ContextInfo)
		}
		return "stickerMessage", content, contextInfo, nil, nil
	}

	if msg.LocationMessage != nil {
		loc := msg.GetLocationMessage()
		content["latitude"] = loc.GetDegreesLatitude()
		content["longitude"] = loc.GetDegreesLongitude()
		content["name"] = loc.GetName()
		content["address"] = loc.GetAddress()
		if loc.ContextInfo != nil {
			contextInfo = extractContextInfo(loc.ContextInfo)
		}
		return "locationMessage", content, contextInfo, nil, nil
	}

	if msg.ContactMessage != nil {
		cnt := msg.GetContactMessage()
		content["displayName"] = cnt.GetDisplayName()
		content["vcard"] = cnt.GetVcard()
		if cnt.ContextInfo != nil {
			contextInfo = extractContextInfo(cnt.ContextInfo)
		}
		return "contactMessage", content, contextInfo, nil, nil
	}

	if msg.ReactionMessage != nil {
		react := msg.GetReactionMessage()
		reaction = make(map[string]interface{})
		reaction["text"] = react.GetText()
		if react.Key != nil {
			reaction["targetMessageId"] = react.Key.GetID()
		}
		return "reactionMessage", nil, nil, reaction, nil
	}

	if msg.PollCreationMessage != nil {
		pollObj := msg.GetPollCreationMessage()
		poll = make(map[string]interface{})
		poll["question"] = pollObj.GetName()
		var options []string
		for _, opt := range pollObj.GetOptions() {
			options = append(options, opt.GetOptionName())
		}
		poll["options"] = options
		if pollObj.ContextInfo != nil {
			contextInfo = extractContextInfo(pollObj.ContextInfo)
		}
		return "pollCreationMessage", nil, contextInfo, nil, poll
	}

	if msg.PollUpdateMessage != nil {
		vote := msg.GetPollUpdateMessage()
		poll = make(map[string]interface{})
		if vote.PollCreationMessageKey != nil {
			poll["targetPollId"] = vote.PollCreationMessageKey.GetID()
		}
		return "pollUpdateMessage", nil, nil, nil, poll
	}

	if msg.ButtonsMessage != nil {
		btn := msg.GetButtonsMessage()
		content["contentText"] = btn.GetContentText()
		content["footerText"] = btn.GetFooterText()
		if btn.ContextInfo != nil {
			contextInfo = extractContextInfo(btn.ContextInfo)
		}
		return "buttonsMessage", content, contextInfo, nil, nil
	}

	if msg.ListMessage != nil {
		lst := msg.GetListMessage()
		content["title"] = lst.GetTitle()
		content["description"] = lst.GetDescription()
		content["buttonText"] = lst.GetButtonText()
		if lst.ContextInfo != nil {
			contextInfo = extractContextInfo(lst.ContextInfo)
		}
		return "listMessage", content, contextInfo, nil, nil
	}

	return "unknown", nil, nil, nil, nil
}
