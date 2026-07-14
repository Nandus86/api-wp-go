package whatsapp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	//"github.com/user/whatsmeow-basileia/pkg/logger"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
    waLog "go.mau.fi/whatsmeow/util/log"
    _ "github.com/lib/pq"
    "github.com/user/whatsmeow-basileia/internal/infrastructure/rabbitmq"
    "encoding/json"
)

type MultiClientManager struct {
	container     *sqlstore.Container
	clients       map[string]*whatsmeow.Client
	apiKeys       map[string]string // Maps apiKey -> deviceID
	clientIDs     map[*whatsmeow.Client]string // Maps client -> deviceID
	mu            sync.RWMutex
	dispatcher    *EventDispatcher
	instanceStore *InstanceStore
	rmqClient     *rabbitmq.Client
}

func NewMultiClientManager(dbDialect, dbAddress string, dispatcher *EventDispatcher, rmqClient *rabbitmq.Client) (*MultiClientManager, error) {
    dbLog := waLog.Stdout("Database", "DEBUG", true)
	container, err := sqlstore.New(context.Background(), dbDialect, dbAddress, dbLog)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

    instanceStore, err := NewInstanceStore(dbDialect, dbAddress)
    if err != nil {
        return nil, fmt.Errorf("failed to initialize instance store: %w", err)
    }

	manager := &MultiClientManager{
		container:     container,
		clients:       make(map[string]*whatsmeow.Client),
		apiKeys:       make(map[string]string),
		clientIDs:     make(map[*whatsmeow.Client]string),
		dispatcher:    dispatcher,
		instanceStore: instanceStore,
		rmqClient:     rmqClient,
	}

    // Load existing instances
    instances, err := instanceStore.GetAllInstances()
    if err != nil {
        return nil, fmt.Errorf("failed to get existing instances: %w", err)
    }

    // Initialize clients for paired instances
    for _, instance := range instances {
        if instance.APIKey != "" {
            manager.apiKeys[instance.APIKey] = instance.ID
        }

        if instance.JID != "" {
            jid, _ := types.ParseJID(instance.JID)
            device, err := container.GetDevice(context.Background(), jid)
            if err == nil && device != nil {
                client := whatsmeow.NewClient(device, waLog.Stdout("Client", "DEBUG", true))
                if instance.ProxyURI != "" {
                    err := client.SetProxyAddress(instance.ProxyURI)
                    if err != nil {
                        waLog.Stdout("Manager", "ERROR", true).Infof("Failed to set proxy for device %s: %v", instance.ID, err)
                    }
                }
                
                manager.clients[instance.ID] = client
                manager.clientIDs[client] = instance.ID
                
                // Wrap handleEvent to pass the deviceID dynamically
                client.AddEventHandler(func(evt interface{}) {
                    manager.mu.RLock()
                    currentID := manager.clientIDs[client]
                    manager.mu.RUnlock()
                    manager.handleEvent(currentID, evt)
                })
                go client.Connect()
            }
        }
    }

	return manager, nil
}

func (m *MultiClientManager) handleEvent(deviceID string, evt interface{}) {
	webhookUrl := ""
	apiKey := ""
	instanceName := deviceID
	inst, err := m.instanceStore.GetInstanceByID(deviceID)
	if err == nil && inst != nil {
		webhookUrl = inst.WebhookURL
		apiKey = inst.APIKey
		instanceName = inst.Name
	}

	switch v := evt.(type) {
	case *events.Message:
		// Record incoming message stat
		if !v.Info.IsFromMe {
			m.instanceStore.IncrementMessageStat(deviceID, "in")
		} else {
			m.instanceStore.IncrementMessageStat(deviceID, "out")
		}

		// Sniff incoming messages to capture the exact payload Basileapi uses
		if v.Message != nil && (v.Message.GetInteractiveMessage() != nil || v.Message.GetViewOnceMessage() != nil || v.Message.GetViewOnceMessageV2() != nil || v.Message.GetViewOnceMessageV2Extension() != nil || v.Message.GetTemplateMessage() != nil || v.Message.GetButtonsMessage() != nil || v.Message.GetListMessage() != nil) {
			waLog.Stdout("Sniffer", "INFO", true).Infof("\n\n====== INTERACTIVE MESSAGE SNIFFED ======\nJID: %s\nDeviceID: %d\nProtobuf:\n%+v\n==========================================\n\n", v.Info.Sender.String(), v.Info.Sender.Device, v.Message)
		}

		// Webhook forwarding
		if m.rmqClient != nil {
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
					"buttonOrListid":   "",
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

			envelope := map[string]interface{}{
				"webhook_url": webhookUrl,
				"payload":     bapiPayload,
			}

			body, err := json.Marshal(envelope)
			if err == nil {
				m.rmqClient.Publish(context.Background(), "webhook_events_queue", body)
			}
		}

	case *events.Receipt:
		if m.rmqClient != nil {
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

			body, err := json.Marshal(payload)
			if err == nil {
				m.rmqClient.Publish(context.Background(), "webhook_events_queue", body)
			}
		}

	case *events.ChatPresence:
		if m.rmqClient != nil {
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

			body, err := json.Marshal(payload)
			if err == nil {
				m.rmqClient.Publish(context.Background(), "webhook_events_queue", body)
			}
		}

	case *events.Presence:
		if m.rmqClient != nil {
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

			body, err := json.Marshal(payload)
			if err == nil {
				m.rmqClient.Publish(context.Background(), "webhook_events_queue", body)
			}
		}

	case *events.GroupInfo:
		if m.rmqClient != nil {
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

			body, err := json.Marshal(payload)
			if err == nil {
				m.rmqClient.Publish(context.Background(), "webhook_events_queue", body)
			}
		}

	case *events.CallOffer:
		if m.rmqClient != nil {
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

			body, err := json.Marshal(payload)
			if err == nil {
				m.rmqClient.Publish(context.Background(), "webhook_events_queue", body)
			}
		}
	}
	m.dispatcher.Dispatch(evt)
}



func (m *MultiClientManager) NewClientWithName(id, apiKey, name, webhookURL, proxyURI string) (string, string, *whatsmeow.Client, error) {
    m.mu.Lock()
    defer m.mu.Unlock()

    deviceID := id
    if deviceID == "" {
        u := make([]byte, 12)
        rand.Read(u)
        deviceID = hex.EncodeToString(u)
    }

    key := apiKey
    if key == "" {
        u := make([]byte, 16)
        rand.Read(u)
        u[8] = (u[8] | 0x80) & 0xBF
        u[6] = (u[6] | 0x40) & 0x4F
        key = fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:])
    }

    if _, exists := m.clients[deviceID]; exists {
        return "", "", nil, fmt.Errorf("client already exists")
    }

    err := m.instanceStore.CreateInstance(deviceID, key, name, webhookURL, proxyURI)
    if err != nil {
        return "", "", nil, fmt.Errorf("failed to save instance to db: %w", err)
    }

    device := m.container.NewDevice()
    client := whatsmeow.NewClient(device, waLog.Stdout("Client", "DEBUG", true))
    if proxyURI != "" {
        err := client.SetProxyAddress(proxyURI)
        if err != nil {
            waLog.Stdout("Manager", "ERROR", true).Infof("Failed to set proxy for device %s: %v", deviceID, err)
        }
    }
    m.clients[deviceID] = client
    m.apiKeys[key] = deviceID
    m.clientIDs[client] = deviceID

    // Wrap event handler to catch PAIRING success and save JID
    client.AddEventHandler(func(evt interface{}) {
        m.mu.RLock()
        currentID := m.clientIDs[client]
        m.mu.RUnlock()

        if v, ok := evt.(*events.PairSuccess); ok {
            m.instanceStore.UpdateInstanceJID(currentID, v.ID.String())
            waLog.Stdout("Manager", "INFO", true).Infof("Device %s successfully paired with JID %s", currentID, v.ID.String())
        }
        m.handleEvent(currentID, evt)
    })
    
    return deviceID, key, client, nil
}

func (m *MultiClientManager) GetClient(deviceID string) *whatsmeow.Client {
    m.mu.RLock()
    defer m.mu.RUnlock()

    if client, exists := m.clients[deviceID]; exists {
        return client
    }

    if id, exists := m.apiKeys[deviceID]; exists {
        if client, exists := m.clients[id]; exists {
            return client
        }
    }

    return nil
}

func (m *MultiClientManager) Connect(ctx context.Context, deviceID string) error {
    client := m.GetClient(deviceID)
    if client == nil {
        return fmt.Errorf("client not found")
    }
    return client.Connect()
}

func (m *MultiClientManager) GetQR(ctx context.Context, deviceID string) (<-chan string, error) {
    client := m.GetClient(deviceID)
    if client == nil {
         // Cannot auto-create here because we need a name
         return nil, fmt.Errorf("client not found and cannot be auto-created without a name")
    }

    if client.Store.ID != nil {
        return nil, fmt.Errorf("already logged in")
    }

    // Get the channel from whatsmeow which gives us QR events
    qrChan, _ := client.GetQRChannel(ctx)
    qrCodeChan := make(chan string)

    // Ensure connection is active and fresh to trigger immediate QR event
    if client.IsConnected() {
        client.Disconnect()
    }
    go client.Connect()

    go func() {
        defer close(qrCodeChan)
        waLog.Stdout("Manager", "INFO", true).Infof("Started QR stream for device %s", deviceID)
        
        for {
            select {
            case <-ctx.Done():
                waLog.Stdout("Manager", "INFO", true).Infof("Context done, stopping QR stream for %s", deviceID)
                return
            case evt, ok := <-qrChan:
                if !ok {
                    waLog.Stdout("Manager", "INFO", true).Infof("QR channel closed by whatsmeow for %s", deviceID)
                    return
                }
                if evt.Event == "code" {
                    waLog.Stdout("Manager", "INFO", true).Infof("Received QR code event for %s", deviceID)
                    // Non-blocking send or select with default to avoid hanging if consumer is slow
                    select {
                    case qrCodeChan <- evt.Code:
                    case <-ctx.Done():
                        return
                    }
                } else {
                    waLog.Stdout("Manager", "INFO", true).Infof("Received non-code QR event: %s for %s", evt.Event, deviceID)
                }
            }
        }
    }()

    return qrCodeChan, nil
}

// RenameInstance updates the custom name of an instance
func (m *MultiClientManager) RenameInstance(id, newName string) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    err := m.instanceStore.RenameInstance(id, newName)
    if err != nil {
         return fmt.Errorf("failed to rename instance in db: %w", err)
    }
    return nil
}

// UpdateInstanceWebhook updates the webhook url of an instance
func (m *MultiClientManager) UpdateInstanceWebhook(id, webhookURL string) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    err := m.instanceStore.UpdateInstanceWebhook(id, webhookURL)
    if err != nil {
         return fmt.Errorf("failed to update webhook in db: %w", err)
    }
    return nil
}

// UpdateInstanceProxy updates the proxy uri of an instance
func (m *MultiClientManager) UpdateInstanceProxy(id, proxyURI string) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    err := m.instanceStore.UpdateInstanceProxy(id, proxyURI)
    if err != nil {
         return fmt.Errorf("failed to update proxy in db: %w", err)
    }

    client, exists := m.clients[id]
    if exists {
        if proxyURI != "" {
            client.SetProxyAddress(proxyURI)
        } else {
            client.SetProxy(nil)
        }
    }

    return nil
}

// DeleteInstance removes an instance, disconnects its client, and drops it from the DB
func (m *MultiClientManager) DeleteInstance(id string) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    client, exists := m.clients[id]
    if exists {
        if client.IsConnected() {
            client.Disconnect()
        }
        client.Logout(context.Background())
        delete(m.clients, id)
        delete(m.clientIDs, client)
    }

    for key, val := range m.apiKeys {
        if val == id {
            delete(m.apiKeys, key)
            break
        }
    }

    err := m.instanceStore.DeleteInstance(id)
    if err != nil {
         return fmt.Errorf("failed to delete instance from db: %w", err)
    }
    return nil
}

// UpdateCredentials updates both Device ID and API Key in DB and in-memory maps
func (m *MultiClientManager) UpdateCredentials(oldID, newID, newAPIKey string) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    // 1. Update database
    err := m.instanceStore.UpdateCredentials(oldID, newID, newAPIKey)
    if err != nil {
        return err
    }

    // 2. Update memory mapping
    for key, val := range m.apiKeys {
        if val == oldID {
            delete(m.apiKeys, key)
            break
        }
    }
    m.apiKeys[newAPIKey] = newID

    if client, exists := m.clients[oldID]; exists {
        delete(m.clients, oldID)
        m.clients[newID] = client
        m.clientIDs[client] = newID
    }

    return nil
}

// GetMessageStats retrieves message statistics
func (m *MultiClientManager) GetMessageStats(instanceID string) ([]MessageStatGroup, error) {
    return m.instanceStore.GetMessageStats(instanceID)
}

// RecordMessageStat increments the message count for dash charts
func (m *MultiClientManager) RecordMessageStat(instanceID string, direction string) error {
    return m.instanceStore.IncrementMessageStat(instanceID, direction)
}

// ListInstances returns a list of all managed instances with metadata
func (m *MultiClientManager) ListInstances() []Instance {
    m.mu.RLock()
    defer m.mu.RUnlock()

    // Fetch latest from DB to get names
    dbInstances, err := m.instanceStore.GetAllInstances()
    if err != nil {
        waLog.Stdout("Manager", "ERROR", true).Infof("Failed to list instances from DB: %v", err)
        return nil
    }

    // Merge status from map
    for i, dbInst := range dbInstances {
        status := "offline" // Default status

        // If JID is empty, it means it has never successfully paired
        if dbInst.JID == "" {
            status = "unpaired"
        }

        client, exists := m.clients[dbInst.ID]
        if exists {
            if client.IsLoggedIn() {
                if client.IsConnected() {
                    status = "connected"
                } else {
                    status = "reconnecting"
                }
            } else {
                // Client exists but is not logged in (waiting for QR)
                status = "unpaired"
            }
        }
        // If client does not exist in memory, and JID was not empty, it remains "offline"
        // If client does not exist in memory, and JID was empty, it remains "unpaired"

        dbInstances[i].Status = status
    }
    
    return dbInstances
}

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

func (m *MultiClientManager) resolveLID(deviceID string, jid types.JID) types.JID {
	if jid.Server != "lid" {
		return jid
	}
	m.mu.RLock()
	client, ok := m.clients[deviceID]
	m.mu.RUnlock()
	if !ok || client == nil {
		return jid
	}
	pn, err := client.Store.LIDs.GetPNForLID(context.Background(), jid)
	if err == nil && !pn.IsEmpty() {
		return pn
	}
	return jid
}

