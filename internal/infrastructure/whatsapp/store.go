package whatsapp

import (
    "database/sql"
    _ "github.com/lib/pq"
)

type Instance struct {
    ID     string `json:"id"`
    Name   string `json:"name"`
    JID    string `json:"jid"`
    Status string `json:"status"`
}

type InstanceStore struct {
    db *sql.DB
}

func NewInstanceStore(dbDialect, dbAddress string) (*InstanceStore, error) {
    db, err := sql.Open(dbDialect, dbAddress)
    if err != nil {
        return nil, err
    }

    _, err = db.Exec(`CREATE TABLE IF NOT EXISTS instances (
        id VARCHAR PRIMARY KEY,
        name VARCHAR NOT NULL,
        jid VARCHAR
    )`)
    if err != nil {
        return nil, err
    }

    _, err = db.Exec(`CREATE TABLE IF NOT EXISTS message_stats (
        id SERIAL PRIMARY KEY,
        instance_id VARCHAR NOT NULL,
        timestamp TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
        direction VARCHAR NOT NULL, -- 'in' or 'out'
        count INT DEFAULT 1
    )`)
    if err != nil {
        return nil, err
    }

    return &InstanceStore{db: db}, nil
}

func (s *InstanceStore) CreateInstance(id, name string) error {
    _, err := s.db.Exec(`INSERT INTO instances (id, name, jid) VALUES ($1, $2, '')`, id, name)
    return err
}

func (s *InstanceStore) UpdateInstanceJID(id, jid string) error {
    _, err := s.db.Exec(`UPDATE instances SET jid = $1 WHERE id = $2`, jid, id)
    return err
}

func (s *InstanceStore) RenameInstance(id, name string) error {
    _, err := s.db.Exec(`UPDATE instances SET name = $1 WHERE id = $2`, name, id)
    return err
}

func (s *InstanceStore) GetInstanceByID(id string) (*Instance, error) {
    row := s.db.QueryRow(`SELECT id, name, jid FROM instances WHERE id = $1`, id)
    var i Instance
    err := row.Scan(&i.ID, &i.Name, &i.JID)
    if err != nil {
        return nil, err
    }
    return &i, nil
}

func (s *InstanceStore) GetAllInstances() ([]Instance, error) {
    rows, err := s.db.Query(`SELECT id, name, jid FROM instances`)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var instances []Instance
    for rows.Next() {
        var i Instance
        if err := rows.Scan(&i.ID, &i.Name, &i.JID); err != nil {
            return nil, err
        }
        instances = append(instances, i)
    }
    return instances, nil
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
