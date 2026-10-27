package webhook

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/user/whatsmeow-basileia/internal/infrastructure/whatsapp"
)

var ErrNotFound = errors.New("not found")

func init() {
	// Deliveries are not a foreign key (they outlive deleted instances), so they follow
	// instance id renames explicitly.
	whatsapp.RegisterInstanceRef(`UPDATE webhook_deliveries SET instance_id = $1 WHERE instance_id = $2`)
}

// Webhook is a registered delivery target.
type Webhook struct {
	ID              string            `json:"id"`
	InstanceID      *string           `json:"instance_id"`
	URL             string            `json:"url"`
	Events          []string          `json:"events"`
	ExcludeMessages []string          `json:"exclude_messages"`
	Secret          string            `json:"secret,omitempty"`
	HasSecret       bool              `json:"has_secret"`
	Headers         map[string]string `json:"headers"`
	Enabled         bool              `json:"enabled"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// Delivery is one outbox row.
type Delivery struct {
	ID             int64      `json:"id"`
	WebhookID      string     `json:"webhook_id,omitempty"`
	InstanceID     string     `json:"instance_id,omitempty"`
	Event          string     `json:"event"`
	URL            string     `json:"url"`
	Body           string     `json:"body,omitempty"`
	Status         string     `json:"status"`
	Attempts       int        `json:"attempts"`
	MaxAttempts    int        `json:"max_attempts"`
	NextAttemptAt  time.Time  `json:"next_attempt_at"`
	LastStatusCode *int       `json:"last_status_code,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
	DurationMS     *int       `json:"duration_ms,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DeliveredAt    *time.Time `json:"delivered_at,omitempty"`
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

const webhookColumns = `id, instance_id, url, events, exclude_messages, COALESCE(secret, ''), headers, enabled, created_at, updated_at`

func scanWebhook(row interface{ Scan(...any) error }) (*Webhook, error) {
	var w Webhook
	var inst sql.NullString
	var events, excl pq.StringArray
	var headers []byte
	if err := row.Scan(&w.ID, &inst, &w.URL, &events, &excl, &w.Secret, &headers, &w.Enabled, &w.CreatedAt, &w.UpdatedAt); err != nil {
		return nil, err
	}
	if inst.Valid {
		w.InstanceID = &inst.String
	}
	w.Events = nonNil(events)
	w.ExcludeMessages = nonNil(excl)
	w.Headers = map[string]string{}
	_ = json.Unmarshal(headers, &w.Headers)
	w.HasSecret = w.Secret != ""
	return &w, nil
}

func nonNil(a []string) []string {
	if a == nil {
		return []string{}
	}
	return a
}

// ListWebhooks returns webhooks of an instance; instanceID "" returns global ones,
// "*" returns all of them.
func (s *Store) ListWebhooks(ctx context.Context, instanceID string) ([]Webhook, error) {
	q := `SELECT ` + webhookColumns + ` FROM webhooks`
	var args []interface{}
	switch instanceID {
	case "*":
	case "":
		q += ` WHERE instance_id IS NULL`
	default:
		q += ` WHERE instance_id = $1`
		args = append(args, instanceID)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY created_at`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Webhook{}
	for rows.Next() {
		w, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	return out, rows.Err()
}

// targetsFor returns enabled webhooks that apply to an instance (its own + global ones).
func (s *Store) targetsFor(ctx context.Context, instanceID string) ([]Webhook, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+webhookColumns+` FROM webhooks
		WHERE enabled AND (instance_id IS NULL OR instance_id = $1)`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Webhook
	for rows.Next() {
		w, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	return out, rows.Err()
}

func (s *Store) GetWebhook(ctx context.Context, id string) (*Webhook, error) {
	w, err := scanWebhook(s.db.QueryRowContext(ctx, `SELECT `+webhookColumns+` FROM webhooks WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return w, err
}

func (s *Store) CreateWebhook(ctx context.Context, w *Webhook) error {
	if w.ID == "" {
		w.ID = newID()
	}
	headers, _ := json.Marshal(nonNilMap(w.Headers))
	_, err := s.db.ExecContext(ctx, `INSERT INTO webhooks (id, instance_id, url, events, exclude_messages, secret, headers, enabled)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8)`,
		w.ID, w.InstanceID, w.URL, pq.Array(nonNil(w.Events)), pq.Array(nonNil(w.ExcludeMessages)), w.Secret, headers, w.Enabled)
	return err
}

func (s *Store) UpdateWebhook(ctx context.Context, w *Webhook) error {
	headers, _ := json.Marshal(nonNilMap(w.Headers))
	res, err := s.db.ExecContext(ctx, `UPDATE webhooks SET url = $2, events = $3, exclude_messages = $4, secret = NULLIF($5, ''),
		headers = $6, enabled = $7, updated_at = NOW() WHERE id = $1`,
		w.ID, w.URL, pq.Array(nonNil(w.Events)), pq.Array(nonNil(w.ExcludeMessages)), w.Secret, headers, w.Enabled)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteWebhook(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM webhooks WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

type newDelivery struct {
	WebhookID  string
	InstanceID string
	Event      string
	URL        string
	Body       string
}

// insertDeliveries writes the outbox rows in a single statement and marks them enqueued.
func (s *Store) insertDeliveries(ctx context.Context, maxAttempts int, ds []newDelivery) ([]int64, error) {
	if len(ds) == 0 {
		return nil, nil
	}
	var sb strings.Builder
	sb.WriteString(`INSERT INTO webhook_deliveries (webhook_id, instance_id, event, url, body, max_attempts, enqueued_at) VALUES `)
	args := make([]interface{}, 0, len(ds)*6)
	for i, d := range ds {
		if i > 0 {
			sb.WriteString(", ")
		}
		n := len(args)
		sb.WriteString("(NULLIF($" + strconv.Itoa(n+1) + ", ''), NULLIF($" + strconv.Itoa(n+2) + ", ''), $" + strconv.Itoa(n+3) +
			", $" + strconv.Itoa(n+4) + ", $" + strconv.Itoa(n+5) + ", $" + strconv.Itoa(n+6) + ", NOW())")
		args = append(args, d.WebhookID, d.InstanceID, d.Event, d.URL, d.Body, maxAttempts)
	}
	sb.WriteString(" RETURNING id")
	rows, err := s.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type claimed struct {
	ID          int64
	WebhookID   string
	InstanceID  string
	Event       string
	URL         string
	Body        string
	Attempts    int
	MaxAttempts int
}

// claim atomically takes a due delivery for this worker. It returns nil when the row
// was already delivered, is not due yet or is being processed by someone else.
func (s *Store) claim(ctx context.Context, id int64, lease time.Duration) (*claimed, error) {
	var c claimed
	err := s.db.QueryRowContext(ctx, `UPDATE webhook_deliveries
		SET status = 'delivering', attempts = attempts + 1, locked_until = NOW() + make_interval(secs => $2),
		    updated_at = NOW()
		WHERE id = $1
		  AND (status IN ('pending', 'retrying') OR (status = 'delivering' AND locked_until < NOW()))
		  AND next_attempt_at <= NOW() + INTERVAL '1 second'
		RETURNING id, COALESCE(webhook_id, ''), COALESCE(instance_id, ''), event, url, body, attempts, max_attempts`,
		id, lease.Seconds()).Scan(&c.ID, &c.WebhookID, &c.InstanceID, &c.Event, &c.URL, &c.Body, &c.Attempts, &c.MaxAttempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) markDelivered(ctx context.Context, id int64, status int, dur time.Duration) error {
	_, err := s.db.ExecContext(ctx, `UPDATE webhook_deliveries SET status = 'delivered', delivered_at = NOW(), updated_at = NOW(),
		locked_until = NULL, last_status_code = $2, last_error = NULL, duration_ms = $3 WHERE id = $1`, id, status, dur.Milliseconds())
	return err
}

// markFailed schedules a retry at next, or parks the row as failed when next is nil.
func (s *Store) markFailed(ctx context.Context, id int64, status int, errMsg string, dur time.Duration, next *time.Time) error {
	var code interface{}
	if status > 0 {
		code = status
	}
	if next == nil {
		_, err := s.db.ExecContext(ctx, `UPDATE webhook_deliveries SET status = 'failed', updated_at = NOW(), locked_until = NULL,
			last_status_code = $2, last_error = $3, duration_ms = $4 WHERE id = $1`, id, code, errMsg, dur.Milliseconds())
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE webhook_deliveries SET status = 'retrying', updated_at = NOW(), locked_until = NULL,
		enqueued_at = NULL, next_attempt_at = $5, last_status_code = $2, last_error = $3, duration_ms = $4 WHERE id = $1`,
		id, code, errMsg, dur.Milliseconds(), *next)
	return err
}

// dueForEnqueue picks due rows that are not already sitting in the queue and stamps them.
func (s *Store) dueForEnqueue(ctx context.Context, limit int) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `UPDATE webhook_deliveries SET enqueued_at = NOW()
		WHERE id IN (
			SELECT id FROM webhook_deliveries
			WHERE ((status IN ('pending', 'retrying') AND next_attempt_at <= NOW())
			    OR (status = 'delivering' AND locked_until < NOW()))
			  AND (enqueued_at IS NULL OR enqueued_at < NOW() - INTERVAL '2 minutes')
			ORDER BY next_attempt_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		) RETURNING id`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type DeliveryFilter struct {
	InstanceIDs []string // scope restriction (empty = all)
	InstanceID  string
	Status      string
	Event       string
	WebhookID   string
	Limit       int
	Offset      int
	WithBody    bool
}

func (s *Store) ListDeliveries(ctx context.Context, f DeliveryFilter) ([]Delivery, int, error) {
	var where []string
	var args []interface{}
	add := func(cond string, v interface{}) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(args))))
	}
	if f.InstanceID != "" {
		add("instance_id = ?", f.InstanceID)
	}
	if len(f.InstanceIDs) > 0 {
		add("instance_id = ANY(?)", pq.Array(f.InstanceIDs))
	}
	if f.Status != "" {
		add("status = ?", f.Status)
	}
	if f.Event != "" {
		add("event = ?", f.Event)
	}
	if f.WebhookID != "" {
		add("webhook_id = ?", f.WebhookID)
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 50
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM webhook_deliveries`+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	bodyCol := "''"
	if f.WithBody {
		bodyCol = "body"
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := s.db.QueryContext(ctx, `SELECT id, COALESCE(webhook_id, ''), COALESCE(instance_id, ''), event, url, `+bodyCol+`, status,
		attempts, max_attempts, next_attempt_at, last_status_code, COALESCE(last_error, ''), duration_ms, created_at, updated_at, delivered_at
		FROM webhook_deliveries`+clause+` ORDER BY id DESC LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *d)
	}
	return out, total, rows.Err()
}

func scanDelivery(row interface{ Scan(...any) error }) (*Delivery, error) {
	var d Delivery
	var code, dur sql.NullInt64
	var delivered sql.NullTime
	if err := row.Scan(&d.ID, &d.WebhookID, &d.InstanceID, &d.Event, &d.URL, &d.Body, &d.Status, &d.Attempts, &d.MaxAttempts,
		&d.NextAttemptAt, &code, &d.LastError, &dur, &d.CreatedAt, &d.UpdatedAt, &delivered); err != nil {
		return nil, err
	}
	if code.Valid {
		c := int(code.Int64)
		d.LastStatusCode = &c
	}
	if dur.Valid {
		v := int(dur.Int64)
		d.DurationMS = &v
	}
	if delivered.Valid {
		d.DeliveredAt = &delivered.Time
	}
	return &d, nil
}

func (s *Store) GetDelivery(ctx context.Context, id int64) (*Delivery, error) {
	d, err := scanDelivery(s.db.QueryRowContext(ctx, `SELECT id, COALESCE(webhook_id, ''), COALESCE(instance_id, ''), event, url, body, status,
		attempts, max_attempts, next_attempt_at, last_status_code, COALESCE(last_error, ''), duration_ms, created_at, updated_at, delivered_at
		FROM webhook_deliveries WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// Redrive resets failed (or any non-delivering) rows so they are delivered again from scratch.
// It returns the ids that were reset.
func (s *Store) Redrive(ctx context.Context, ids []int64, instanceIDs []string, status string, maxAttempts int) ([]int64, error) {
	var where []string
	var args []interface{}
	args = append(args, maxAttempts)
	if len(ids) > 0 {
		args = append(args, pq.Array(ids))
		where = append(where, "id = ANY($"+strconv.Itoa(len(args))+")")
	}
	if len(instanceIDs) > 0 {
		args = append(args, pq.Array(instanceIDs))
		where = append(where, "instance_id = ANY($"+strconv.Itoa(len(args))+")")
	}
	if status == "" {
		status = "failed"
	}
	args = append(args, status)
	where = append(where, "status = $"+strconv.Itoa(len(args)))
	where = append(where, "status <> 'delivering'")

	rows, err := s.db.QueryContext(ctx, `UPDATE webhook_deliveries SET status = 'pending', attempts = 0, max_attempts = $1,
		next_attempt_at = NOW(), locked_until = NULL, enqueued_at = NOW(), updated_at = NOW(), delivered_at = NULL
		WHERE `+strings.Join(where, " AND ")+` RETURNING id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Stats counts deliveries per status (optionally for a set of instances).
func (s *Store) Stats(ctx context.Context, instanceIDs []string) (map[string]int, error) {
	q := `SELECT status, COUNT(*) FROM webhook_deliveries`
	var args []interface{}
	if len(instanceIDs) > 0 {
		q += ` WHERE instance_id = ANY($1)`
		args = append(args, pq.Array(instanceIDs))
	}
	rows, err := s.db.QueryContext(ctx, q+` GROUP BY status`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{"pending": 0, "delivering": 0, "retrying": 0, "delivered": 0, "failed": 0}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

// Purge deletes delivered rows older than deliveredDays and failed rows older than failedDays.
func (s *Store) Purge(ctx context.Context, deliveredDays, failedDays int) (int64, error) {
	var total int64
	if deliveredDays > 0 {
		res, err := s.db.ExecContext(ctx, `DELETE FROM webhook_deliveries WHERE status = 'delivered' AND created_at < NOW() - make_interval(days => $1)`, deliveredDays)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	if failedDays > 0 {
		res, err := s.db.ExecContext(ctx, `DELETE FROM webhook_deliveries WHERE status = 'failed' AND created_at < NOW() - make_interval(days => $1)`, failedDays)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

func (s *Store) secretFor(ctx context.Context, webhookID string) (string, map[string]string) {
	if webhookID == "" {
		return "", nil
	}
	w, err := s.GetWebhook(ctx, webhookID)
	if err != nil {
		return "", nil
	}
	return w.Secret, w.Headers
}
