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
	inst, err := m.instanceStore.GetInstanceByID(deviceID)
	if err == nil && inst != nil {
		webhookUrl = inst.WebhookURL
		apiKey = inst.APIKey
	}

	switch v := evt.(type) {
	case *events.Message:
		// Record incoming message stat
		if !v.Info.IsFromMe {
			m.instanceStore.IncrementMessageStat(deviceID, "in")
		} else {
			m.instanceStore.IncrementMessageStat(deviceID, "out")
		}

		// Sniff incoming messages to capture the exact payload Uazapi uses
		if v.Message != nil && (v.Message.GetInteractiveMessage() != nil || v.Message.GetViewOnceMessage() != nil || v.Message.GetViewOnceMessageV2() != nil || v.Message.GetViewOnceMessageV2Extension() != nil || v.Message.GetTemplateMessage() != nil || v.Message.GetButtonsMessage() != nil || v.Message.GetListMessage() != nil) {
			waLog.Stdout("Sniffer", "INFO", true).Infof("\n\n====== INTERACTIVE MESSAGE SNIFFED ======\nJID: %s\nDeviceID: %d\nProtobuf:\n%+v\n==========================================\n\n", v.Info.Sender.String(), v.Info.Sender.Device, v.Message)
		}

		// Webhook forwarding
		if m.rmqClient != nil {
			msgType, content, contextInfo, reaction, poll := parseMessageContent(v.Message)

			var verifiedName string
			if v.Info.VerifiedName != nil && v.Info.VerifiedName.Details != nil {
				verifiedName = v.Info.VerifiedName.Details.GetVerifiedName()
			}

			senderJID := m.resolveLID(deviceID, v.Info.Sender)
			chatJID := m.resolveLID(deviceID, v.Info.Chat)

			payload := map[string]interface{}{
				"event":       "messages.upsert",
				"instance_id": deviceID,
				"api_key":     apiKey,
				"webhook_url": webhookUrl,
				"data": map[string]interface{}{
					"key": map[string]interface{}{
						"id":          v.Info.ID,
						"remoteJid":   chatJID.String(),
						"fromMe":      v.Info.IsFromMe,
						"participant": senderJID.String(),
					},
					"pushName":     v.Info.PushName,
					"timestamp":    v.Info.Timestamp.Unix(),
					"messageType":  msgType,
					"message":      content,
					"status": map[string]interface{}{
						"isEphemeral": v.IsEphemeral,
						"isViewOnce":  v.IsViewOnce || v.IsViewOnceV2,
						"isEdit":      v.IsEdit,
					},
					"isGroup":      v.Info.IsGroup,
					"device":       int(v.Info.Sender.Device),
					"senderPhone":  senderJID.User,
					"chatPhone":    chatJID.User,
					"senderBare":   senderJID.ToNonAD().String(),
					"chatBare":     chatJID.ToNonAD().String(),
					"verifiedName": verifiedName,
					"multicast":    v.Info.Multicast,
					"mediaType":    v.Info.MediaType,
					"contextInfo":  contextInfo,
					"reaction":     reaction,
					"poll":         poll,
					"raw":          v.Message,
				},
			}

			body, err := json.Marshal(payload)
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

			payload := map[string]interface{}{
				"event":       "receipts.update",
				"instance_id": deviceID,
				"api_key":     apiKey,
				"webhook_url": webhookUrl,
				"data": map[string]interface{}{
					"chatId":     chatJID.String(),
					"sender":     senderJID.String(),
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

			payload := map[string]interface{}{
				"event":       "presence.update",
				"instance_id": deviceID,
				"api_key":     apiKey,
				"webhook_url": webhookUrl,
				"data": map[string]interface{}{
					"chatId": chatJID.String(),
					"sender": senderJID.String(),
					"state":  string(v.State),
					"media":  string(v.Media),
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

			payload := map[string]interface{}{
				"event":       "presence.update",
				"instance_id": deviceID,
				"api_key":     apiKey,
				"webhook_url": webhookUrl,
				"data": map[string]interface{}{
					"sender":      fromJID.String(),
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

			data := map[string]interface{}{
				"groupId":   groupJID.String(),
				"sender":    senderStr,
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

			payload := map[string]interface{}{
				"event":       "calls.offer",
				"instance_id": deviceID,
				"api_key":     apiKey,
				"webhook_url": webhookUrl,
				"data": map[string]interface{}{
					"callId":    v.CallID,
					"sender":    fromJID.String(),
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

