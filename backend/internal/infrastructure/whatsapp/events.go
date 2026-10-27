package whatsapp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/user/whatsmeow-basileia/pkg/logger"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
)

// OutboundEvent is an event ready to be delivered to webhooks and realtime subscribers.
// Payload is the exact JSON body POSTed to webhooks (kept identical to previous versions).
type OutboundEvent struct {
	InstanceID string
	Event      string
	WebhookURL string // legacy per-instance URL (may be empty)
	Payload    map[string]interface{}
}

// Hooks lets services outside this package observe WhatsApp traffic without import cycles.
type Hooks struct {
	// Emit receives every outbound event. When nil, events go straight to webhook_events_queue.
	Emit      func(OutboundEvent)
	OnMessage func(instanceID string, client *whatsmeow.Client, evt *events.Message)
	OnReceipt func(instanceID string, evt *events.Receipt)
	// OnStatus is called when the connection state of an instance changes.
	OnStatus func(instanceID, state string)
}

func (m *MultiClientManager) emit(ev OutboundEvent) {
	m.mu.RLock()
	emit := m.hooks.Emit
	m.mu.RUnlock()
	if emit != nil {
		emit(ev)
		return
	}
	if m.rmqClient == nil {
		return
	}
	envelope := map[string]interface{}{
		"instance_id": ev.InstanceID,
		"event":       ev.Event,
		"webhook_url": ev.WebhookURL,
		"payload":     ev.Payload,
	}
	body, err := json.Marshal(envelope)
	if err == nil {
		m.rmqClient.Publish(context.Background(), "webhook_events_queue", body)
	}
}

func (m *MultiClientManager) handleEvent(deviceID string, client *whatsmeow.Client, evt interface{}) {
	webhookUrl := ""
	apiKey := ""
	instanceName := deviceID
	var settings InstanceSettings
	inst, ok := m.GetInstance(deviceID)
	if ok {
		settings = inst.Settings
		webhookUrl = inst.WebhookURL
		apiKey = inst.APIKey
		instanceName = inst.Name
	}

	// Connection lifecycle (connected, logged out, banned...) is handled separately.
	if m.handleLifecycle(deviceID, client, inst, evt) {
		m.dispatcher.Dispatch(evt)
		return
	}

	skipWebhook := false
	m.mu.RLock()
	hooks := m.hooks
	m.mu.RUnlock()

	switch v := evt.(type) {
	case *events.Message:
		// Record incoming message stat
		if !v.Info.IsFromMe {
			m.instanceStore.IncrementMessageStat(deviceID, "in")
		} else {
			m.instanceStore.IncrementMessageStat(deviceID, "out")
		}

		if hooks.OnMessage != nil {
			hooks.OnMessage(deviceID, client, v)
		}
		if settings.ReadMessages && !v.Info.IsFromMe {
			go client.MarkRead(context.Background(), []types.MessageID{v.Info.ID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender)
		}
		if (settings.IgnoreGroups && v.Info.IsGroup) || (settings.IgnoreStatus && v.Info.Chat.String() == types.StatusBroadcastJID.String()) {
			skipWebhook = true
		}

		// Sniff incoming messages to capture the exact payload Basileapi uses
		if m.debug && v.Message != nil && (v.Message.GetInteractiveMessage() != nil || v.Message.GetViewOnceMessage() != nil || v.Message.GetViewOnceMessageV2() != nil || v.Message.GetViewOnceMessageV2Extension() != nil || v.Message.GetTemplateMessage() != nil || v.Message.GetButtonsMessage() != nil || v.Message.GetListMessage() != nil) {
			waLog.Stdout("Sniffer", "INFO", true).Infof("\n\n====== INTERACTIVE MESSAGE SNIFFED ======\nJID: %s\nDeviceID: %d\nProtobuf:\n%+v\n==========================================\n\n", v.Info.Sender.String(), v.Info.Sender.Device, v.Message)
		}

		// Webhook forwarding
		if !skipWebhook {
			msgType, content, _, _, _ := parseMessageContent(v.Message)

			var verifiedName string
			if v.Info.VerifiedName != nil && v.Info.VerifiedName.Details != nil {
				verifiedName = v.Info.VerifiedName.Details.GetVerifiedName()
			}

			senderJID := m.resolveLID(deviceID, v.Info.Sender)
			chatJID := m.resolveLID(deviceID, v.Info.Chat)

			var senderLid string
			if v.Info.Sender.Server == "lid" {
				senderLid = v.Info.Sender.ToNonAD().String()
			}
			var chatLid string
			if v.Info.Chat.Server == "lid" {
				chatLid = v.Info.Chat.ToNonAD().String()
			}

			// Extract text content reliably
			textContent := ""
			if val, ok := content["text"].(string); ok {
				textContent = val
			} else if val, ok := content["conversation"].(string); ok {
				textContent = val
			}

			// Map message types to BasileAPI types
			bapiType := "text"
			buttonOrListid := ""

			if msgType == "imageMessage" {
				bapiType = "image"
			} else if msgType == "videoMessage" {
				bapiType = "video"
			} else if msgType == "audioMessage" {
				bapiType = "audio"
			} else if msgType == "documentMessage" {
				bapiType = "document"
			} else if msgType == "contactMessage" {
				bapiType = "contact"
			} else if msgType == "locationMessage" {
				bapiType = "location"
			} else if resp := v.Message.GetListResponseMessage(); resp != nil {
				bapiType = "list_response"
				buttonOrListid = resp.GetSingleSelectReply().GetSelectedRowID()
				textContent = resp.GetTitle()
			} else if resp := v.Message.GetTemplateButtonReplyMessage(); resp != nil {
				bapiType = "template_button_reply"
				buttonOrListid = resp.GetSelectedID()
				textContent = resp.GetSelectedDisplayText()
			} else if resp := v.Message.GetInteractiveResponseMessage(); resp != nil {
				if nf := resp.GetNativeFlowResponseMessage(); nf != nil {
					bapiType = "native_flow_response"

					var params map[string]interface{}
					if err := json.Unmarshal([]byte(nf.GetParamsJSON()), &params); err == nil {
						if id, ok := params["id"].(string); ok {
							buttonOrListid = id
						}
					}
					textContent = nf.GetName()
				}
			}

			bapiPayload := map[string]interface{}{
				"BaseUrl":   "https://api.basileia.com",
				"EventType": "messages",
				"chat": map[string]interface{}{
					"chatbot_agentResetMemoryAt": 0,
					"chatbot_disableUntil":       0,
					"chatbot_lastTriggerAt":      0,
					"chatbot_lastTrigger_id":     "",
					"id":                         fmt.Sprintf("r%x", time.Now().UnixNano()),
					"image":                      "",
					"imagePreview":               "",
					"lead_assignedAttendant_id":  "",
					"lead_email":                 "",
					"lead_field01":               "",
					"lead_field02":               "",
					"lead_field03":               "",
					"lead_field04":               "",
					"lead_field05":               "",
					"lead_field06":               "",
					"lead_field07":               "",
					"lead_field08":               "",
					"lead_field09":               "",
					"lead_field10":               "",
					"lead_field11":               "",
					"lead_field12":               "",
					"lead_field13":               "",
					"lead_field14":               "",
					"lead_field15":               "",
					"lead_field16":               "",
					"lead_field17":               "",
					"lead_field18":               "",
					"lead_field19":               "",
					"lead_field20":               "",
					"lead_fullName":              v.Info.PushName,
					"lead_isTicketOpen":          false,
					"lead_kanbanOrder":           0,
					"lead_name":                  v.Info.PushName,
					"lead_notes":                 "",
					"lead_personalid":            "",
					"lead_status":                "",
					"lead_tags":                  []string{},
					"name":                       v.Info.PushName,
					"owner":                      senderJID.User,
					"phone":                      "+" + senderJID.User,
					"wa_archived":                false,
					"wa_chatid":                  chatJID.String(),
					"wa_chatlid":                 chatLid,
					"wa_contactName":             v.Info.PushName,
					"wa_ephemeralExpiration":     0,
					"wa_fastid":                  senderJID.User + ":" + chatJID.User,
					"wa_isBlocked":               false,
					"wa_isGroup":                 v.Info.IsGroup,
					"wa_isGroup_admin":           false,
					"wa_isGroup_announce":        false,
					"wa_isGroup_community":       false,
					"wa_isGroup_member":          false,
					"wa_isPinned":                false,
					"wa_label":                   []string{},
					"wa_lastMessageSender":       senderLid,
					"wa_lastMessageTextVote":     textContent,
					"wa_lastMessageType":         "Conversation",
					"wa_lastMsgTimestamp":        v.Info.Timestamp.UnixMilli(),
					"wa_muteEndTime":             0,
					"wa_name":                    verifiedName,
					"wa_notes":                   "",
					"wa_unreadCount":             1,
				},
				"chatSource":   "updated",
				"instanceName": instanceName,
				"message": map[string]interface{}{
					"buttonOrListid":   buttonOrListid,
					"chatid":           chatJID.String(),
					"chatlid":          chatLid,
					"content":          textContent,
					"convertOptions":   "",
					"edited":           "",
					"fromMe":           v.Info.IsFromMe,
					"groupName":        "",
					"id":               senderJID.User + ":" + v.Info.ID,
					"isGroup":          v.Info.IsGroup,
					"mediaType":        v.Info.MediaType,
					"messageTimestamp": v.Info.Timestamp.UnixMilli(),
					"messageType":      "Conversation",
					"messageid":        v.Info.ID,
					"owner":            senderJID.User,
					"pinned":           false,
					"quoted":           "",
					"reaction":         "",
					"sender":           senderLid,
					"senderName":       v.Info.PushName,
					"sender_lid":       senderLid,
					"sender_pn":        senderJID.String(),
					"source":           "web",
					"status":           "",
					"text":             textContent,
					"track_id":         "",
					"track_source":     "",
					"type":             bapiType,
					"vote":             "",
					"wasSentByApi":     false,
				},
				"owner": senderJID.User,
				"token": apiKey,
			}

			m.emit(OutboundEvent{InstanceID: deviceID, Event: "messages", WebhookURL: webhookUrl, Payload: bapiPayload})
		}

	case *events.Receipt:
		if hooks.OnReceipt != nil {
			hooks.OnReceipt(deviceID, v)
		}
		if !skipWebhook {
			var msgIDs []string
			for _, id := range v.MessageIDs {
				msgIDs = append(msgIDs, string(id))
			}

			chatJID := m.resolveLID(deviceID, v.Chat)
			senderJID := m.resolveLID(deviceID, v.Sender)

			var senderLid string
			if v.Sender.Server == "lid" {
				senderLid = v.Sender.ToNonAD().String()
			}
			var chatLid string
			if v.Chat.Server == "lid" {
				chatLid = v.Chat.ToNonAD().String()
			}

			payload := map[string]interface{}{
				"event":       "receipts.update",
				"instance_id": deviceID,
				"api_key":     apiKey,
				"webhook_url": webhookUrl,
				"data": map[string]interface{}{
					"chatId":     chatJID.String(),
					"sender":     senderJID.String(),
					"senderLid":  senderLid,
					"chatLid":    chatLid,
					"messageIds": msgIDs,
					"type":       string(v.Type),
					"timestamp":  v.Timestamp.Unix(),
				},
			}

			m.emit(OutboundEvent{InstanceID: deviceID, Event: payload["event"].(string), WebhookURL: webhookUrl, Payload: payload})
		}

	case *events.ChatPresence:
		if !skipWebhook {
			chatJID := m.resolveLID(deviceID, v.Chat)
			senderJID := m.resolveLID(deviceID, v.Sender)

			var senderLid string
			if v.Sender.Server == "lid" {
				senderLid = v.Sender.ToNonAD().String()
			}
			var chatLid string
			if v.Chat.Server == "lid" {
				chatLid = v.Chat.ToNonAD().String()
			}

			payload := map[string]interface{}{
				"event":       "presence.update",
				"instance_id": deviceID,
				"api_key":     apiKey,
				"webhook_url": webhookUrl,
				"data": map[string]interface{}{
					"chatId":    chatJID.String(),
					"sender":    senderJID.String(),
					"senderLid": senderLid,
					"chatLid":   chatLid,
					"state":     string(v.State),
					"media":     string(v.Media),
				},
			}

			m.emit(OutboundEvent{InstanceID: deviceID, Event: payload["event"].(string), WebhookURL: webhookUrl, Payload: payload})
		}

	case *events.Presence:
		if !skipWebhook {
			fromJID := m.resolveLID(deviceID, v.From)

			var senderLid string
			if v.From.Server == "lid" {
				senderLid = v.From.ToNonAD().String()
			}

			payload := map[string]interface{}{
				"event":       "presence.update",
				"instance_id": deviceID,
				"api_key":     apiKey,
				"webhook_url": webhookUrl,
				"data": map[string]interface{}{
					"sender":      fromJID.String(),
					"senderLid":   senderLid,
					"unavailable": v.Unavailable,
					"lastSeen":    v.LastSeen.Format(time.RFC3339),
				},
			}

			m.emit(OutboundEvent{InstanceID: deviceID, Event: payload["event"].(string), WebhookURL: webhookUrl, Payload: payload})
		}

	case *events.GroupInfo:
		if !skipWebhook {
			groupJID := m.resolveLID(deviceID, v.JID)
			var senderStr string
			if v.Sender != nil {
				senderStr = m.resolveLID(deviceID, *v.Sender).String()
			}

			var senderLid string
			if v.Sender != nil && v.Sender.Server == "lid" {
				senderLid = v.Sender.ToNonAD().String()
			}

			data := map[string]interface{}{
				"groupId":   groupJID.String(),
				"sender":    senderStr,
				"senderLid": senderLid,
				"timestamp": v.Timestamp.Unix(),
			}
			if v.Name != nil {
				data["name"] = v.Name.Name
			}
			if v.Topic != nil {
				data["topic"] = v.Topic.Topic
			}

			payload := map[string]interface{}{
				"event":       "groups.update",
				"instance_id": deviceID,
				"api_key":     apiKey,
				"webhook_url": webhookUrl,
				"data":        data,
			}

			m.emit(OutboundEvent{InstanceID: deviceID, Event: payload["event"].(string), WebhookURL: webhookUrl, Payload: payload})
		}

	case *events.CallOffer:
		if settings.RejectCalls {
			go m.rejectCall(client, v, settings.RejectCallMessage)
		}
		if !skipWebhook {
			fromJID := m.resolveLID(deviceID, v.From)

			var senderLid string
			if v.From.Server == "lid" {
				senderLid = v.From.ToNonAD().String()
			}

			payload := map[string]interface{}{
				"event":       "calls.offer",
				"instance_id": deviceID,
				"api_key":     apiKey,
				"webhook_url": webhookUrl,
				"data": map[string]interface{}{
					"callId":    v.CallID,
					"sender":    fromJID.String(),
					"senderLid": senderLid,
					"timestamp": v.Timestamp.Unix(),
				},
			}

			m.emit(OutboundEvent{InstanceID: deviceID, Event: payload["event"].(string), WebhookURL: webhookUrl, Payload: payload})
		}
	}
	m.dispatcher.Dispatch(evt)
}

// handleLifecycle processes connection/session events. It returns true when evt was one of them.
func (m *MultiClientManager) handleLifecycle(deviceID string, client *whatsmeow.Client, inst *Instance, evt interface{}) bool {
	state := ""
	reason := ""
	switch v := evt.(type) {
	case *events.PairSuccess:
		_ = m.instanceStore.UpdateInstanceJID(deviceID, v.ID.String())
		logger.Info("Device successfully paired", zap.String("instance", deviceID), zap.String("jid", v.ID.String()))
		state = "paired"
	case *events.PairError:
		state, reason = "pair_error", v.Error.Error()
	case *events.Connected:
		_ = m.instanceStore.RecordConnection(deviceID, true, "")
		state = "open"
		if inst != nil && inst.Settings.AlwaysOnline {
			go client.SendPresence(context.Background(), types.PresenceAvailable)
		}
	case *events.Disconnected:
		_ = m.instanceStore.RecordConnection(deviceID, false, "")
		state = "close"
	case *events.KeepAliveTimeout:
		state, reason = "keepalive_timeout", fmt.Sprintf("%d failed keepalives", v.ErrorCount)
	case *events.KeepAliveRestored:
		state = "open"
	case *events.LoggedOut:
		reason = v.Reason.String()
		_ = m.instanceStore.RecordConnection(deviceID, false, "logged out: "+reason)
		_ = m.instanceStore.UpdateInstanceJID(deviceID, "")
		state = "logged_out"
		logger.Warn("Instance logged out", zap.String("instance", deviceID), zap.String("reason", reason))
		// The device store is gone: swap in a fresh client so a new QR can be requested.
		go m.resetClient(deviceID)
	case *events.StreamReplaced:
		_ = m.instanceStore.RecordConnection(deviceID, false, "stream replaced by another connection")
		state, reason = "replaced", "stream replaced by another connection"
	case *events.TemporaryBan:
		reason = v.String()
		_ = m.instanceStore.RecordConnection(deviceID, false, "temporary ban: "+reason)
		state = "banned"
		logger.Error("Instance temporarily banned", zap.String("instance", deviceID), zap.String("reason", reason))
	case *events.ConnectFailure:
		reason = fmt.Sprintf("%s %s", v.Reason.String(), v.Message)
		_ = m.instanceStore.RecordConnection(deviceID, false, "connect failure: "+reason)
		state = "failure"
	case *events.ClientOutdated:
		reason = "client outdated, update whatsmeow"
		_ = m.instanceStore.RecordConnection(deviceID, false, reason)
		state = "failure"
		logger.Error("WhatsApp reports the client is outdated", zap.String("instance", deviceID))
	default:
		return false
	}

	m.refreshInstance(deviceID)
	if fresh, ok := m.GetInstance(deviceID); ok {
		inst = fresh
	}
	m.mu.RLock()
	onStatus := m.hooks.OnStatus
	m.mu.RUnlock()
	if onStatus != nil {
		onStatus(deviceID, state)
	}

	webhookURL, apiKey := "", ""
	if inst != nil {
		webhookURL, apiKey = inst.WebhookURL, inst.APIKey
	}
	jid := ""
	if client != nil && client.Store.ID != nil {
		jid = client.Store.ID.ToNonAD().String()
	}
	m.emit(OutboundEvent{
		InstanceID: deviceID,
		Event:      "connection.update",
		WebhookURL: webhookURL,
		Payload: map[string]interface{}{
			"event":       "connection.update",
			"instance_id": deviceID,
			"api_key":     apiKey,
			"webhook_url": webhookURL,
			"data": map[string]interface{}{
				"state":     state,
				"reason":    reason,
				"jid":       jid,
				"timestamp": time.Now().Unix(),
			},
		},
	})
	return true
}

func (m *MultiClientManager) rejectCall(client *whatsmeow.Client, call *events.CallOffer, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := client.RejectCall(ctx, call.From, call.CallID); err != nil {
		logger.Warn("Failed to reject call", zap.Error(err))
		return
	}
	if message != "" {
		to := call.From.ToNonAD()
		if _, err := client.SendMessage(ctx, to, &waE2E.Message{Conversation: proto.String(message)}); err != nil {
			logger.Warn("Failed to send call rejection message", zap.Error(err))
		}
	}
}

func sortInstances(list []Instance) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].CreatedAt.Equal(list[j].CreatedAt) {
			return list[i].ID < list[j].ID
		}
		return list[i].CreatedAt.Before(list[j].CreatedAt)
	})
}
