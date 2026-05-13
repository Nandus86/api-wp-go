package whatsapp

import (
	"context"
	"crypto/rand"
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
	container *sqlstore.Container
	clients   map[string]*whatsmeow.Client
	mu        sync.RWMutex
    dispatcher *EventDispatcher
    instanceStore *InstanceStore
    rmqClient *rabbitmq.Client
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
		container: container,
		clients:   make(map[string]*whatsmeow.Client),
        dispatcher: dispatcher,
        instanceStore: instanceStore,
        rmqClient: rmqClient,
	}

    // Register Webhook Event Forwarder
    manager.registerWebhookForwarder()

    // Load existing instances
    instances, err := instanceStore.GetAllInstances()
    if err != nil {
        return nil, fmt.Errorf("failed to get existing instances: %w", err)
    }

    // Initialize clients for paired instances
    for _, instance := range instances {
        if instance.JID != "" {
            jid, _ := types.ParseJID(instance.JID)
            device, err := container.GetDevice(context.Background(), jid)
            if err == nil && device != nil {
                client := whatsmeow.NewClient(device, waLog.Stdout("Client", "DEBUG", true))
                // Attach device ID to client context or pass it somehow if needed, but here we can just capture it
                manager.clients[instance.ID] = client
                
                // Wrap handleEvent to pass the deviceID
                client.AddEventHandler(func(evt interface{}) {
                    manager.handleEvent(instance.ID, evt)
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
	case *events.Receipt:
        // Also trace delivered/sent if needed, but usually we just track messages sent directly in the worker or when events.Message fromMe=true arrives.
	}
    m.dispatcher.Dispatch(evt)
}

// registerWebhookForwarder listens to internal dispatcher events, wraps them
// in a standard JSON format, and publishes them to RabbitMQ for webhook delivery.
func (m *MultiClientManager) registerWebhookForwarder() {
    if m.rmqClient == nil {
        return
    }

    m.dispatcher.Register(&events.Message{}, func(evt interface{}) {
        msg, ok := evt.(*events.Message)
        if !ok {
            return
        }

        // Extremely simplified payload for the n8n webhook
        payload := map[string]interface{}{
            "event": "messages.upsert",
            "data": map[string]interface{}{
                "message": map[string]interface{}{
                    "conversation": msg.Message.GetConversation(),
                },
                "key": map[string]interface{}{
                    "remoteJid": msg.Info.Chat.String(),
                    "fromMe": msg.Info.IsFromMe,
                    "id": msg.Info.ID,
                },
                "pushName": msg.Info.PushName,
                "timestamp": msg.Info.Timestamp.Unix(),
            },
        }

        if msg.Message.GetExtendedTextMessage() != nil {
             payload["data"].(map[string]interface{})["message"].(map[string]interface{})["conversation"] = msg.Message.GetExtendedTextMessage().GetText()
        }

        body, err := json.Marshal(payload)
        if err == nil {
            m.rmqClient.Publish(context.Background(), "webhook_events_queue", body)
        }
    })
    
    // We can add more events like Receipt, Presence, etc here over time
}

func (m *MultiClientManager) NewClientWithName(name string) (string, *whatsmeow.Client, error) {
    m.mu.Lock()
    defer m.mu.Unlock()

    u := make([]byte, 16)
    rand.Read(u)
    u[8] = (u[8] | 0x80) & 0xBF
    u[6] = (u[6] | 0x40) & 0x4F
    deviceID := fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:])

    if _, exists := m.clients[deviceID]; exists {
        return "", nil, fmt.Errorf("client already exists")
    }

    err := m.instanceStore.CreateInstance(deviceID, name)
    if err != nil {
        return "", nil, fmt.Errorf("failed to save instance to db: %w", err)
    }

    device := m.container.NewDevice()
    client := whatsmeow.NewClient(device, waLog.Stdout("Client", "DEBUG", true))
    m.clients[deviceID] = client

    // Wrap event handler to catch PAIRING success and save JID
    client.AddEventHandler(func(evt interface{}) {
        if v, ok := evt.(*events.PairSuccess); ok {
            m.instanceStore.UpdateInstanceJID(deviceID, v.ID.String())
            waLog.Stdout("Manager", "INFO", true).Infof("Device %s successfully paired with JID %s", deviceID, v.ID.String())
        }
        m.handleEvent(deviceID, evt)
    })
    
    return deviceID, client, nil
}

func (m *MultiClientManager) GetClient(deviceID string) *whatsmeow.Client {
    m.mu.RLock()
    defer m.mu.RUnlock()
    // Note: This logic assumes deviceID is the JID string from sqlstore
    // Real implementation needs a mapping if deviceID is custom
    return m.clients[deviceID]
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
    }

    err := m.instanceStore.DeleteInstance(id)
    if err != nil {
         return fmt.Errorf("failed to delete instance from db: %w", err)
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
            if client.IsConnected() {
                status = "connected"
            } else if client.IsLoggedIn() { // Client exists and has logged in before, but is not currently connected
                status = "reconnecting"
            } else {
                // Client exists but is not connected and not logged in (e.g., store is empty)
                status = "unpaired"
            }
        }
        // If client does not exist in memory, and JID was not empty, it remains "offline"
        // If client does not exist in memory, and JID was empty, it remains "unpaired"

        dbInstances[i].Status = status
    }
    
    return dbInstances
}
