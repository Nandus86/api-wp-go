package whatsapp

import (
	"database/sql"
	"encoding/json"
	"time"
)

// InstanceSettings are per-instance behaviour toggles stored as JSONB.
type InstanceSettings struct {
	RejectCalls       bool   `json:"reject_calls"`
	RejectCallMessage string `json:"reject_call_message,omitempty"`
	AlwaysOnline      bool   `json:"always_online"`
	ReadMessages      bool   `json:"read_messages"`
	IgnoreGroups      bool   `json:"ignore_groups"`
	IgnoreStatus      bool   `json:"ignore_status"`
	// Delay range (ms) applied before each queued send; 0 disables it.
	MsgDelayMin int `json:"msg_delay_min"`
	MsgDelayMax int `json:"msg_delay_max"`
}

type Instance struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	JID        string `json:"jid"`
	Status     string `json:"status"`
	WebhookURL string `json:"webhook_url"`
	ProxyURI   string `json:"proxy_uri"`
	APIKey     string `json:"api_key"`

	CreatedAt          time.Time        `json:"created_at"`
	PairedAt           *time.Time       `json:"paired_at,omitempty"`
	LastConnectedAt    *time.Time       `json:"last_connected_at,omitempty"`
	LastDisconnectedAt *time.Time       `json:"last_disconnected_at,omitempty"`
	LastError          string           `json:"last_error,omitempty"`
	Settings           InstanceSettings `json:"settings"`
}

type InstanceStore struct {
	db *sql.DB
}

// NewInstanceStore wraps the shared pool. The schema is owned by the migrations package.
func NewInstanceStore(db *sql.DB) *InstanceStore {
	return &InstanceStore{db: db}
}

const instanceColumns = `id, name, COALESCE(jid, ''), COALESCE(webhook_url, ''), COALESCE(proxy_uri, ''), COALESCE(api_key, ''),
	created_at, paired_at, last_connected_at, last_disconnected_at, COALESCE(last_error, ''), settings`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanInstance(row rowScanner) (*Instance, error) {
	var i Instance
	var paired, connected, disconnected sql.NullTime
	var settings []byte
	if err := row.Scan(&i.ID, &i.Name, &i.JID, &i.WebhookURL, &i.ProxyURI, &i.APIKey,
		&i.CreatedAt, &paired, &connected, &disconnected, &i.LastError, &settings); err != nil {
		return nil, err
	}
	if paired.Valid {
		i.PairedAt = &paired.Time
	}
	if connected.Valid {
		i.LastConnectedAt = &connected.Time
	}
	if disconnected.Valid {
		i.LastDisconnectedAt = &disconnected.Time
	}
	if len(settings) > 0 {
		_ = json.Unmarshal(settings, &i.Settings)
	}
	return &i, nil
}

func (s *InstanceStore) CreateInstance(id, apiKey, name, webhookURL, proxyURI string) error {
	_, err := s.db.Exec(`INSERT INTO instances (id, name, jid, webhook_url, proxy_uri, api_key) VALUES ($1, $2, '', $3, $4, $5)`, id, name, webhookURL, proxyURI, apiKey)
	return err
}

func (s *InstanceStore) UpdateInstanceWebhook(id, webhookURL string) error {
	_, err := s.db.Exec(`UPDATE instances SET webhook_url = $1, updated_at = NOW() WHERE id = $2`, webhookURL, id)
	return err
}

func (s *InstanceStore) UpdateInstanceProxy(id, proxyURI string) error {
	_, err := s.db.Exec(`UPDATE instances SET proxy_uri = $1, updated_at = NOW() WHERE id = $2`, proxyURI, id)
	return err
}

// UpdateInstanceJID stores the paired JID. An empty jid marks the instance as unpaired.
func (s *InstanceStore) UpdateInstanceJID(id, jid string) error {
	if jid == "" {
		_, err := s.db.Exec(`UPDATE instances SET jid = '', paired_at = NULL, updated_at = NOW() WHERE id = $1`, id)
		return err
	}
	_, err := s.db.Exec(`UPDATE instances SET jid = $1, paired_at = COALESCE(paired_at, NOW()), updated_at = NOW() WHERE id = $2`, jid, id)
	return err
}

func (s *InstanceStore) RenameInstance(id, name string) error {
	_, err := s.db.Exec(`UPDATE instances SET name = $1, updated_at = NOW() WHERE id = $2`, name, id)
	return err
}

func (s *InstanceStore) UpdateSettings(id string, settings InstanceSettings) error {
	b, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE instances SET settings = $1, updated_at = NOW() WHERE id = $2`, b, id)
	return err
}

// RecordConnection tracks connection lifecycle timestamps and the last error message.
func (s *InstanceStore) RecordConnection(id string, connected bool, lastError string) error {
	if connected {
		_, err := s.db.Exec(`UPDATE instances SET last_connected_at = NOW(), last_error = NULL WHERE id = $1`, id)
		return err
	}
	_, err := s.db.Exec(`UPDATE instances SET last_disconnected_at = NOW(), last_error = NULLIF($2, '') WHERE id = $1`, id, lastError)
	return err
}

func (s *InstanceStore) GetInstanceByID(id string) (*Instance, error) {
	return scanInstance(s.db.QueryRow(`SELECT `+instanceColumns+` FROM instances WHERE id = $1`, id))
}

func (s *InstanceStore) GetInstanceByIDOrAPIKey(idOrApiKey string) (*Instance, error) {
	return scanInstance(s.db.QueryRow(`SELECT `+instanceColumns+` FROM instances WHERE id = $1 OR api_key = $1 LIMIT 1`, idOrApiKey))
}

func (s *InstanceStore) GetAllInstances() ([]Instance, error) {
	rows, err := s.db.Query(`SELECT ` + instanceColumns + ` FROM instances ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var instances []Instance
	for rows.Next() {
		i, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		instances = append(instances, *i)
	}
	return instances, rows.Err()
}

func (s *InstanceStore) UpdateCredentials(oldID, newID, newAPIKey string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Update every table that references the instance id if we changed the ID
	if oldID != newID {
		for _, q := range instanceRefUpdates {
			if _, err = tx.Exec(q, newID, oldID); err != nil {
				return err
			}
		}
	}

	// 2. Update the instance table
	_, err = tx.Exec(`UPDATE instances SET id = $1, api_key = $2, updated_at = NOW() WHERE id = $3`, newID, newAPIKey, oldID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// instanceRefUpdates re-points rows owned by an instance when its id changes.
var instanceRefUpdates = []string{
	`UPDATE message_stats SET instance_id = $1 WHERE instance_id = $2`,
}

// RegisterInstanceRef lets other stores (messages, webhooks, jobs...) join the id rename.
func RegisterInstanceRef(query string) {
	instanceRefUpdates = append(instanceRefUpdates, query)
}

func (s *InstanceStore) DeleteInstance(id string) error {
	_, err := s.db.Exec(`DELETE FROM instances WHERE id = $1`, id)
	return err
}

type MessageStatGroup struct {
	Hour      string `json:"hour"`
	Direction string `json:"direction"`
	Count     int    `json:"count"`
}

func (s *InstanceStore) IncrementMessageStat(instanceID string, direction string) error {
	// direction: "in" or "out"
	// Insert a new record for this message event.
	_, err := s.db.Exec(`INSERT INTO message_stats (instance_id, direction, count) VALUES ($1, $2, 1)`, instanceID, direction)
	return err
}

func (s *InstanceStore) GetMessageStats(instanceID string) ([]MessageStatGroup, error) {
	// Get last 24 hours of stats grouped by hour
	query := `
        SELECT to_char(date_trunc('hour', timestamp), 'YYYY-MM-DD"T"HH24:MI:SS"Z"') as hour, direction, SUM(count) as count
        FROM message_stats
        WHERE timestamp >= NOW() - INTERVAL '24 HOURS'
    `
	var args []interface{}
	if instanceID != "" && instanceID != "all" {
		query += ` AND instance_id = $1 `
		args = append(args, instanceID)
	}
	query += ` GROUP BY 1, 2 ORDER BY 1 ASC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []MessageStatGroup
	for rows.Next() {
		var st MessageStatGroup
		if err := rows.Scan(&st.Hour, &st.Direction, &st.Count); err != nil {
			return nil, err
		}
		stats = append(stats, st)
	}
	return stats, nil
}

// DB exposes the shared pool (used by readiness probes).
func (s *InstanceStore) DB() *sql.DB { return s.db }
