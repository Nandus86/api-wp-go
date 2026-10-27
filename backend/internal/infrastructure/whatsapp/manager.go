package whatsapp

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/user/whatsmeow-basileia/internal/infrastructure/rabbitmq"
	"github.com/user/whatsmeow-basileia/pkg/logger"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
	"go.uber.org/zap"
)

var (
	ErrClientNotFound = errors.New("client not found")
	ErrNotLoggedIn    = errors.New("instance is not logged in")
	ErrAlreadyPaired  = errors.New("already logged in")
)

type Options struct {
	DB         *sql.DB
	Dialect    string
	WALogLevel string
	Debug      bool
}

type MultiClientManager struct {
	container     *sqlstore.Container
	clients       map[string]*whatsmeow.Client
	apiKeys       map[string]string            // Maps apiKey -> deviceID
	clientIDs     map[*whatsmeow.Client]string // Maps client -> deviceID
	instances     map[string]*Instance         // In-memory cache of instance metadata (by ID)
	pairing       map[string]PairingState      // Latest QR / pairing code per instance
	mu            sync.RWMutex
	dispatcher    *EventDispatcher
	instanceStore *InstanceStore
	rmqClient     *rabbitmq.Client
	hooks         Hooks
	waLogLevel    string
	debug         bool
}

func NewMultiClientManager(opts Options, dispatcher *EventDispatcher, rmqClient *rabbitmq.Client) (*MultiClientManager, error) {
	if opts.WALogLevel == "" {
		opts.WALogLevel = "INFO"
	}
	container := sqlstore.NewWithDB(opts.DB, opts.Dialect, waLog.Stdout("Database", opts.WALogLevel, true))
	if err := container.Upgrade(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to upgrade whatsmeow store: %w", err)
	}

	manager := &MultiClientManager{
		container:     container,
		clients:       make(map[string]*whatsmeow.Client),
		apiKeys:       make(map[string]string),
		clientIDs:     make(map[*whatsmeow.Client]string),
		instances:     make(map[string]*Instance),
		pairing:       make(map[string]PairingState),
		dispatcher:    dispatcher,
		instanceStore: NewInstanceStore(opts.DB),
		rmqClient:     rmqClient,
		waLogLevel:    opts.WALogLevel,
		debug:         opts.Debug,
	}
	return manager, nil
}

// SetHooks installs the callbacks used to fan events out (webhooks, realtime, message store).
// Must be called before Start.
func (m *MultiClientManager) SetHooks(h Hooks) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hooks = h
}

// Start loads every instance from the database, creates its client and connects the paired ones.
// Unpaired instances also get a client so a QR code can be requested right after a restart.
func (m *MultiClientManager) Start(ctx context.Context) error {
	instances, err := m.instanceStore.GetAllInstances()
	if err != nil {
		return fmt.Errorf("failed to get existing instances: %w", err)
	}

	for i := range instances {
		instance := instances[i]
		var device *store.Device
		if instance.JID != "" {
			jid, perr := types.ParseJID(instance.JID)
			if perr == nil {
				device, err = m.container.GetDevice(ctx, jid)
				if err != nil {
					logger.Error("Failed to load device from store", zap.String("instance", instance.ID), zap.Error(err))
				}
			}
			if device == nil {
				// The session was removed from the store (e.g. logged out while we were down).
				logger.Warn("Paired session not found in store, marking instance as unpaired", zap.String("instance", instance.ID))
				_ = m.instanceStore.UpdateInstanceJID(instance.ID, "")
				instance.JID = ""
				instance.PairedAt = nil
			}
		}
		if device == nil {
			device = m.container.NewDevice()
		}

		m.mu.Lock()
		client := m.newClientLocked(instance.ID, device, instance.ProxyURI)
		m.instances[instance.ID] = &instance
		if instance.APIKey != "" {
			m.apiKeys[instance.APIKey] = instance.ID
		}
		m.mu.Unlock()

		if instance.JID != "" {
			go func(id string, c *whatsmeow.Client) {
				if err := c.Connect(); err != nil {
					logger.Error("Failed to connect instance", zap.String("instance", id), zap.Error(err))
				}
			}(instance.ID, client)
		}
	}
	logger.Info("WhatsApp manager started", zap.Int("instances", len(instances)))
	return nil
}

// newClientLocked creates a whatsmeow client for a device and wires its event handler.
// Caller must hold m.mu.
func (m *MultiClientManager) newClientLocked(id string, device *store.Device, proxyURI string) *whatsmeow.Client {
	client := whatsmeow.NewClient(device, waLog.Stdout("Client", m.waLogLevel, true))
	client.EnableAutoReconnect = true
	if proxyURI != "" {
		if err := client.SetProxyAddress(proxyURI); err != nil {
			logger.Error("Failed to set proxy for device", zap.String("instance", id), zap.Error(err))
		}
	}
	m.clients[id] = client
	m.clientIDs[client] = id

	// Wrap handleEvent to pass the deviceID dynamically (it can change via UpdateCredentials)
	client.AddEventHandler(func(evt interface{}) {
		m.mu.RLock()
		currentID, ok := m.clientIDs[client]
		m.mu.RUnlock()
		if !ok {
			return // client was replaced or deleted
		}
		m.handleEvent(currentID, client, evt)
	})
	return client
}

// resetClient replaces the client of an instance with a fresh, unpaired one.
// Used after a logout, when the old device store is no longer valid.
func (m *MultiClientManager) resetClient(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.clients[id]
	proxy := ""
	if inst := m.instances[id]; inst != nil {
		proxy = inst.ProxyURI
	}
	if ok {
		old.RemoveEventHandlers()
		delete(m.clientIDs, old)
		go old.Disconnect()
	}
	m.newClientLocked(id, m.container.NewDevice(), proxy)
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
		key = GenerateAPIKey()
	}

	if _, exists := m.clients[deviceID]; exists {
		return "", "", nil, fmt.Errorf("client already exists")
	}
	if owner, exists := m.apiKeys[key]; exists && owner != deviceID {
		return "", "", nil, fmt.Errorf("api_key already in use by another instance")
	}

	err := m.instanceStore.CreateInstance(deviceID, key, name, webhookURL, proxyURI)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to save instance to db: %w", err)
	}

	client := m.newClientLocked(deviceID, m.container.NewDevice(), proxyURI)
	m.apiKeys[key] = deviceID
	if inst, err := m.instanceStore.GetInstanceByID(deviceID); err == nil {
		m.instances[deviceID] = inst
	} else {
		m.instances[deviceID] = &Instance{ID: deviceID, Name: name, WebhookURL: webhookURL, ProxyURI: proxyURI, APIKey: key, CreatedAt: time.Now()}
	}

	return deviceID, key, client, nil
}

// GenerateAPIKey returns a random UUIDv4-formatted token (same format as before).
func GenerateAPIKey() string {
	u := make([]byte, 16)
	rand.Read(u)
	u[8] = (u[8] | 0x80) & 0xBF
	u[6] = (u[6] | 0x40) & 0x4F
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:])
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

// ResolveID maps an instance ID or instance api_key to the canonical instance ID.
func (m *MultiClientManager) ResolveID(idOrKey string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.instances[idOrKey]; ok {
		return idOrKey, true
	}
	if id, ok := m.apiKeys[idOrKey]; ok {
		return id, true
	}
	return "", false
}

// InstanceByAPIKey returns the instance owning an api_key (used for authentication).
func (m *MultiClientManager) InstanceByAPIKey(key string) (*Instance, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.apiKeys[key]
	if !ok {
		return nil, false
	}
	inst, ok := m.instances[id]
	if !ok {
		return nil, false
	}
	cp := *inst
	return &cp, true
}

// GetInstance returns a copy of the cached instance metadata.
func (m *MultiClientManager) GetInstance(id string) (*Instance, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst, ok := m.instances[id]
	if !ok {
		return nil, false
	}
	cp := *inst
	return &cp, true
}

func (m *MultiClientManager) refreshInstance(id string) {
	inst, err := m.instanceStore.GetInstanceByID(id)
	if err != nil {
		return
	}
	m.mu.Lock()
	m.instances[id] = inst
	m.mu.Unlock()
}

func (m *MultiClientManager) Connect(ctx context.Context, deviceID string) error {
	client := m.GetClient(deviceID)
	if client == nil {
		return ErrClientNotFound
	}
	if client.IsConnected() {
		return nil
	}
	return client.Connect()
}

// Disconnect closes the websocket but keeps the session (it can reconnect without QR).
func (m *MultiClientManager) Disconnect(deviceID string) error {
	client := m.GetClient(deviceID)
	if client == nil {
		return ErrClientNotFound
	}
	client.Disconnect()
	return nil
}

// Logout unlinks the device from the phone and resets the instance to unpaired.
func (m *MultiClientManager) Logout(ctx context.Context, deviceID string) error {
	id, ok := m.ResolveID(deviceID)
	if !ok {
		return ErrClientNotFound
	}
	client := m.GetClient(id)
	if client != nil && client.Store.ID != nil {
		if err := client.Logout(ctx); err != nil {
			logger.Warn("Logout request failed, resetting locally", zap.String("instance", id), zap.Error(err))
			_ = m.container.DeleteDevice(ctx, client.Store)
		}
	}
	_ = m.instanceStore.UpdateInstanceJID(id, "")
	m.refreshInstance(id)
	m.resetClient(id)
	return nil
}

func (m *MultiClientManager) GetQR(ctx context.Context, deviceID string) (<-chan string, error) {
	client := m.GetClient(deviceID)
	if client == nil {
		// Cannot auto-create here because we need a name
		return nil, fmt.Errorf("client not found and cannot be auto-created without a name")
	}

	if client.Store.ID != nil {
		return nil, ErrAlreadyPaired
	}

	// A QR channel can only be opened on a disconnected client, so restart any login
	// attempt already in progress (e.g. the QR dialog was reopened).
	if client.IsConnected() {
		client.Disconnect()
	}

	// Get the channel from whatsmeow which gives us QR events
	qrChan, err := client.GetQRChannel(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open QR channel: %w", err)
	}
	qrCodeChan := make(chan string)

	go client.Connect()

	go func() {
		defer close(qrCodeChan)
		logger.Debug("Started QR stream", zap.String("instance", deviceID))

		for {
			select {
			case <-ctx.Done():
				// Keep draining so whatsmeow never blocks on a full channel after we leave.
				go drainQR(qrChan)
				return
			case evt, ok := <-qrChan:
				if !ok {
					return
				}
				if evt.Event == "code" {
					m.setPairing(deviceID, PairingState{QRCode: evt.Code, ExpiresAt: time.Now().Add(evt.Timeout)})
					select {
					case qrCodeChan <- evt.Code:
					case <-ctx.Done():
						go drainQR(qrChan)
						return
					}
				} else {
					logger.Debug("Received non-code QR event", zap.String("event", evt.Event), zap.String("instance", deviceID))
				}
			}
		}
	}()

	return qrCodeChan, nil
}

func drainQR(ch <-chan whatsmeow.QRChannelItem) {
	for range ch {
	}
}

// GetQROnce connects (if needed) and returns the first QR code, without a stream.
func (m *MultiClientManager) GetQROnce(ctx context.Context, deviceID string) (string, error) {
	// The QR channel must outlive this request: it keeps emitting new codes for ~160s.
	qrCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	ch, err := m.GetQR(qrCtx, deviceID)
	if err != nil {
		cancel()
		return "", err
	}
	select {
	case code, ok := <-ch:
		if !ok {
			cancel()
			return "", fmt.Errorf("QR channel closed before a code was generated")
		}
		// Keep the stream alive in the background so the phone can still scan this code.
		go func() {
			for range ch {
			}
			cancel()
		}()
		return code, nil
	case <-ctx.Done():
		cancel()
		return "", ctx.Err()
	case <-time.After(30 * time.Second):
		cancel()
		return "", fmt.Errorf("timed out waiting for QR code")
	}
}

// PairCode links the instance using a phone-number pairing code instead of a QR code.
func (m *MultiClientManager) PairCode(ctx context.Context, deviceID, phone string) (string, error) {
	client := m.GetClient(deviceID)
	if client == nil {
		return "", ErrClientNotFound
	}
	if client.Store.ID != nil {
		return "", ErrAlreadyPaired
	}
	// PairPhone requires an active login websocket; opening the QR channel and waiting
	// for the first code guarantees that. The channel lives as long as the pairing window.
	if client.IsConnected() {
		client.Disconnect()
	}
	qrCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	qrChan, err := client.GetQRChannel(qrCtx)
	if err != nil {
		cancel()
		return "", fmt.Errorf("failed to open QR channel: %w", err)
	}
	if err := client.Connect(); err != nil {
		cancel()
		return "", fmt.Errorf("failed to connect: %w", err)
	}

	select {
	case evt, ok := <-qrChan:
		if !ok || evt.Event != "code" {
			cancel()
			return "", fmt.Errorf("unexpected QR channel state: %v", evt.Event)
		}
	case <-ctx.Done():
		cancel()
		return "", ctx.Err()
	case <-time.After(30 * time.Second):
		cancel()
		return "", fmt.Errorf("timed out waiting for login websocket")
	}
	go func() {
		drainQR(qrChan)
		cancel()
	}()

	code, err := client.PairPhone(ctx, phone, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
	if err != nil {
		return "", fmt.Errorf("pair phone: %w", err)
	}
	m.setPairing(deviceID, PairingState{PairCode: code, ExpiresAt: time.Now().Add(160 * time.Second)})
	return code, nil
}

// PairingState is the latest QR code or pairing code generated for an instance.
type PairingState struct {
	QRCode    string    `json:"qrcode,omitempty"`
	PairCode  string    `json:"paircode,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (m *MultiClientManager) setPairing(id string, st PairingState) {
	if canonical, ok := m.ResolveID(id); ok {
		id = canonical
	}
	m.mu.Lock()
	m.pairing[id] = st
	m.mu.Unlock()
}

// Pairing returns the pending QR/pairing code of an unpaired instance (if still valid).
func (m *MultiClientManager) Pairing(id string) (PairingState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st, ok := m.pairing[id]
	if !ok || time.Now().After(st.ExpiresAt) {
		return PairingState{}, false
	}
	if c := m.clients[id]; c != nil && c.Store.ID != nil {
		return PairingState{}, false
	}
	return st, true
}

// RenameInstance updates the custom name of an instance
func (m *MultiClientManager) RenameInstance(id, newName string) error {
	if err := m.instanceStore.RenameInstance(id, newName); err != nil {
		return fmt.Errorf("failed to rename instance in db: %w", err)
	}
	m.refreshInstance(id)
	return nil
}

// UpdateInstanceWebhook updates the webhook url of an instance
func (m *MultiClientManager) UpdateInstanceWebhook(id, webhookURL string) error {
	if err := m.instanceStore.UpdateInstanceWebhook(id, webhookURL); err != nil {
		return fmt.Errorf("failed to update webhook in db: %w", err)
	}
	m.refreshInstance(id)
	return nil
}

// UpdateInstanceProxy updates the proxy uri of an instance
func (m *MultiClientManager) UpdateInstanceProxy(id, proxyURI string) error {
	if err := m.instanceStore.UpdateInstanceProxy(id, proxyURI); err != nil {
		return fmt.Errorf("failed to update proxy in db: %w", err)
	}
	m.refreshInstance(id)

	m.mu.RLock()
	client, exists := m.clients[id]
	m.mu.RUnlock()
	if exists {
		if proxyURI != "" {
			if err := client.SetProxyAddress(proxyURI); err != nil {
				return fmt.Errorf("invalid proxy: %w", err)
			}
		} else {
			client.SetProxy(nil)
		}
	}

	return nil
}

// UpdateSettings replaces the behaviour settings of an instance.
func (m *MultiClientManager) UpdateSettings(id string, settings InstanceSettings) error {
	if err := m.instanceStore.UpdateSettings(id, settings); err != nil {
		return fmt.Errorf("failed to update settings: %w", err)
	}
	m.refreshInstance(id)
	if settings.AlwaysOnline {
		if client := m.GetClient(id); client != nil && client.IsLoggedIn() {
			_ = client.SendPresence(context.Background(), types.PresenceAvailable)
		}
	}
	return nil
}

// DeleteInstance removes an instance, disconnects its client, and drops it from the DB
func (m *MultiClientManager) DeleteInstance(id string) error {
	m.mu.Lock()
	client, exists := m.clients[id]
	if exists {
		client.RemoveEventHandlers()
		delete(m.clients, id)
		delete(m.clientIDs, client)
	}
	for key, val := range m.apiKeys {
		if val == id {
			delete(m.apiKeys, key)
		}
	}
	delete(m.instances, id)
	m.mu.Unlock()

	if exists {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		if client.Store.ID != nil {
			if err := client.Logout(ctx); err != nil {
				_ = m.container.DeleteDevice(ctx, client.Store)
			}
		}
		cancel()
		client.Disconnect()
	}

	if err := m.instanceStore.DeleteInstance(id); err != nil {
		return fmt.Errorf("failed to delete instance from db: %w", err)
	}
	return nil
}

// UpdateCredentials updates both Device ID and API Key in DB and in-memory maps
func (m *MultiClientManager) UpdateCredentials(oldID, newID, newAPIKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if oldID != newID {
		if _, taken := m.instances[newID]; taken {
			return fmt.Errorf("instance id %q already exists", newID)
		}
	}
	if owner, taken := m.apiKeys[newAPIKey]; taken && owner != oldID {
		return fmt.Errorf("api_key already in use by another instance")
	}

	// 1. Update database
	err := m.instanceStore.UpdateCredentials(oldID, newID, newAPIKey)
	if err != nil {
		return err
	}

	// 2. Update memory mapping
	for key, val := range m.apiKeys {
		if val == oldID {
			delete(m.apiKeys, key)
		}
	}
	m.apiKeys[newAPIKey] = newID

	if client, exists := m.clients[oldID]; exists {
		delete(m.clients, oldID)
		m.clients[newID] = client
		m.clientIDs[client] = newID
	}
	if inst, exists := m.instances[oldID]; exists {
		delete(m.instances, oldID)
		inst.ID = newID
		inst.APIKey = newAPIKey
		m.instances[newID] = inst
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

// liveStatus derives the connection status shown by the API (same values as before).
func liveStatus(inst *Instance, client *whatsmeow.Client) string {
	status := "offline" // Default status

	// If JID is empty, it means it has never successfully paired
	if inst.JID == "" {
		status = "unpaired"
	}

	if client != nil {
		if client.IsLoggedIn() {
			if client.IsConnected() {
				status = "connected"
			} else {
				status = "reconnecting"
			}
		} else if client.Store.ID == nil {
			// Client exists but is not logged in (waiting for QR)
			status = "unpaired"
		} else if client.IsConnected() {
			status = "connecting"
		}
	}
	return status
}

// ListInstances returns a list of all managed instances with metadata
func (m *MultiClientManager) ListInstances() []Instance {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Instance, 0, len(m.instances))
	for _, inst := range m.instances {
		cp := *inst
		cp.Status = liveStatus(inst, m.clients[inst.ID])
		out = append(out, cp)
	}
	sortInstances(out)
	return out
}

// InstanceStatus returns the live status of a single instance.
func (m *MultiClientManager) InstanceStatus(id string) (*Instance, *whatsmeow.Client, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst, ok := m.instances[id]
	if !ok {
		return nil, nil, false
	}
	cp := *inst
	client := m.clients[id]
	cp.Status = liveStatus(inst, client)
	return &cp, client, true
}

// ConnectedCount returns how many instances currently have a live, logged-in connection.
func (m *MultiClientManager) ConnectedCount() (connected, total int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for id := range m.instances {
		total++
		if c := m.clients[id]; c != nil && c.IsConnected() && c.IsLoggedIn() {
			connected++
		}
	}
	return
}

// Shutdown disconnects every client (sessions are kept, they reconnect on next boot).
func (m *MultiClientManager) Shutdown() {
	m.mu.RLock()
	clients := make([]*whatsmeow.Client, 0, len(m.clients))
	for _, c := range m.clients {
		clients = append(clients, c)
	}
	m.mu.RUnlock()

	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Add(1)
		go func(c *whatsmeow.Client) {
			defer wg.Done()
			c.Disconnect()
		}(c)
	}
	wg.Wait()
}

func (m *MultiClientManager) Container() *sqlstore.Container { return m.container }

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

// ResolveLID is the exported form used by services outside this package.
func (m *MultiClientManager) ResolveLID(deviceID string, jid types.JID) types.JID {
	return m.resolveLID(deviceID, jid)
}
