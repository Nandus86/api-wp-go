package whatsapp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"

	//"github.com/user/whatsmeow-basileia/pkg/logger"
	"go.mau.fi/whatsmeow"
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
            inst, err := m.instanceStore.GetInstanceByID(deviceID)
            webhookUrl := ""
            if err == nil && inst != nil {
                webhookUrl = inst.WebhookURL
            }

            payload := map[string]interface{}{
                "event": "messages.upsert",
                "instance_id": deviceID,
                "webhook_url": webhookUrl,
                "data": map[string]interface{}{
                    "message": map[string]interface{}{
                        "conversation": v.Message.GetConversation(),
                    },
                    "key": map[string]interface{}{
                        "remoteJid": v.Info.Chat.String(),
                        "fromMe": v.Info.IsFromMe,
                        "id": v.Info.ID,
                    },
                    "pushName": v.Info.PushName,
                    "timestamp": v.Info.Timestamp.Unix(),
                },
            }

            if v.Message.GetExtendedTextMessage() != nil {
                 payload["data"].(map[string]interface{})["message"].(map[string]interface{})["conversation"] = v.Message.GetExtendedTextMessage().GetText()
            }

            body, err := json.Marshal(payload)
            if err == nil {
                m.rmqClient.Publish(context.Background(), "webhook_events_queue", body)
            }
        }
	case *events.Receipt:
        // Also trace delivered/sent if needed, but usually we just track messages sent directly in the worker or when events.Message fromMe=true arrives.
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
