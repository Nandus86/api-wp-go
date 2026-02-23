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
