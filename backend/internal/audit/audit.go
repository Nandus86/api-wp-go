// Package audit records who changed what through the management API.
// Message sends are not audited here: they have their own tables (jobs/messages).
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/user/whatsmeow-basileia/pkg/logger"
	"go.uber.org/zap"
)

type Entry struct {
	ID         int64                  `json:"id"`
	CreatedAt  time.Time              `json:"created_at"`
	ActorType  string                 `json:"actor_type"`
	ActorID    string                 `json:"actor_id,omitempty"`
	ActorName  string                 `json:"actor_name,omitempty"`
	Method     string                 `json:"method"`
	Path       string                 `json:"path"`
	Route      string                 `json:"route,omitempty"`
	InstanceID string                 `json:"instance_id,omitempty"`
	StatusCode int                    `json:"status_code"`
	IP         string                 `json:"ip,omitempty"`
	RequestID  string                 `json:"request_id,omitempty"`
	Details    map[string]interface{} `json:"details,omitempty"`
}

type Store struct {
	db    *sql.DB
	queue chan Entry
}

// NewStore starts a background writer so auditing never slows down API responses.
func NewStore(db *sql.DB) *Store {
	s := &Store{db: db, queue: make(chan Entry, 1000)}
	go s.writer()
	return s
}

func (s *Store) Record(e Entry) {
	select {
	case s.queue <- e:
	default:
		logger.Warn("Audit queue full, dropping entry", zap.String("path", e.Path))
	}
}

func (s *Store) writer() {
	for e := range s.queue {
		var details []byte
		if len(e.Details) > 0 {
			details, _ = json.Marshal(e.Details)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := s.db.ExecContext(ctx, `INSERT INTO audit_logs
			(actor_type, actor_id, actor_name, method, path, route, instance_id, status_code, ip, request_id, details)
			VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), $8, $9, $10, $11)`,
			e.ActorType, e.ActorID, e.ActorName, e.Method, e.Path, e.Route, e.InstanceID, e.StatusCode, e.IP, e.RequestID, nullJSON(details))
		cancel()
		if err != nil {
			logger.Warn("Failed to write audit entry", zap.Error(err))
		}
	}
}

func nullJSON(b []byte) interface{} {
	if len(b) == 0 {
		return nil
	}
	return b
}

type Filter struct {
	InstanceIDs []string // when non-empty, only these instances (scoped callers)
	InstanceID  string
	ActorID     string
	Method      string
	Limit       int
	Offset      int
}

func (s *Store) List(ctx context.Context, f Filter) ([]Entry, int, error) {
	var where []string
	var args []interface{}
	add := func(cond string, v interface{}) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(args))))
	}
	if f.InstanceID != "" {
		add("instance_id = ?", f.InstanceID)
	}
	if f.ActorID != "" {
		add("actor_id = ?", f.ActorID)
	}
	if f.Method != "" {
		add("method = ?", strings.ToUpper(f.Method))
	}
	if len(f.InstanceIDs) > 0 {
		args = append(args, pq.Array(f.InstanceIDs))
		where = append(where, "instance_id = ANY($"+strconv.Itoa(len(args))+")")
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_logs`+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := s.db.QueryContext(ctx, `SELECT id, created_at, actor_type, COALESCE(actor_id, ''), COALESCE(actor_name, ''),
		method, path, COALESCE(route, ''), COALESCE(instance_id, ''), status_code, COALESCE(ip, ''), COALESCE(request_id, ''), details
		FROM audit_logs`+clause+` ORDER BY id DESC LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		var details []byte
		if err := rows.Scan(&e.ID, &e.CreatedAt, &e.ActorType, &e.ActorID, &e.ActorName, &e.Method, &e.Path, &e.Route,
			&e.InstanceID, &e.StatusCode, &e.IP, &e.RequestID, &details); err != nil {
			return nil, 0, err
		}
		if len(details) > 0 {
			_ = json.Unmarshal(details, &e.Details)
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// Purge removes entries older than days (0 keeps everything).
func (s *Store) Purge(ctx context.Context, days int) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM audit_logs WHERE created_at < NOW() - make_interval(days => $1)`, days)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
