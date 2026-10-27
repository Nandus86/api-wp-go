package messaging

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
)

var ErrJobNotFound = errors.New("job not found")

// Job is one tracked send. Status lifecycle:
//
//	scheduled -> queued -> sending -> sent
//	                         \-> retrying -> queued ... -> failed
//	scheduled/queued/retrying -> cancelled | paused
type Job struct {
	ID          string          `json:"id"`
	InstanceID  string          `json:"instance_id"`
	BatchID     string          `json:"batch_id,omitempty"`
	Queue       string          `json:"queue"`
	Payload     json.RawMessage `json:"payload,omitempty" swaggertype:"object"`
	Recipient   string          `json:"recipient,omitempty"`
	Status      string          `json:"status"`
	Attempts    int             `json:"attempts"`
	MaxAttempts int             `json:"max_attempts"`
	ScheduledAt time.Time       `json:"scheduled_at"`
	WAMessageID string          `json:"message_id,omitempty"`
	LastError   string          `json:"last_error,omitempty"`
	TrackSource string          `json:"track_source,omitempty"`
	TrackID     string          `json:"track_id,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	SentAt      *time.Time      `json:"sent_at,omitempty"`
}

type Batch struct {
	ID         string         `json:"id"`
	InstanceID string         `json:"instance_id"`
	Info       string         `json:"info,omitempty"`
	Status     string         `json:"status"`
	Total      int            `json:"total"`
	Counts     map[string]int `json:"counts"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

type JobStore struct {
	db *sql.DB
}

func NewJobStore(db *sql.DB) *JobStore { return &JobStore{db: db} }

func newJobID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

const jobColumns = `id, instance_id, COALESCE(batch_id, ''), queue, payload, COALESCE(recipient, ''), status, attempts, max_attempts,
	scheduled_at, COALESCE(wa_message_id, ''), COALESCE(last_error, ''), COALESCE(track_source, ''), COALESCE(track_id, ''),
	created_at, updated_at, sent_at`

func scanJob(row interface{ Scan(...any) error }) (*Job, error) {
	var j Job
	var payload []byte
	var sent sql.NullTime
	if err := row.Scan(&j.ID, &j.InstanceID, &j.BatchID, &j.Queue, &payload, &j.Recipient, &j.Status, &j.Attempts, &j.MaxAttempts,
		&j.ScheduledAt, &j.WAMessageID, &j.LastError, &j.TrackSource, &j.TrackID, &j.CreatedAt, &j.UpdatedAt, &sent); err != nil {
		return nil, err
	}
	j.Payload = payload
	if sent.Valid {
		j.SentAt = &sent.Time
	}
	return &j, nil
}

// insertJobs writes jobs in one statement. Jobs due now are stamped as enqueued
// because the caller publishes them right away.
func (s *JobStore) insertJobs(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, jobs []Job) error {
	if len(jobs) == 0 {
		return nil
	}
	const cols = 11
	var sb strings.Builder
	sb.WriteString(`INSERT INTO message_jobs (id, instance_id, batch_id, queue, payload, recipient, status, max_attempts, scheduled_at,
		track_source, track_id, enqueued_at) VALUES `)
	args := make([]interface{}, 0, len(jobs)*cols)
	now := time.Now()
	for i, j := range jobs {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString("(")
		for k := 1; k <= cols; k++ {
			if k > 1 {
				sb.WriteString(",")
			}
			sb.WriteString("$" + strconv.Itoa(len(args)+k))
		}
		if j.Status == "queued" && !j.ScheduledAt.After(now) {
			sb.WriteString(", NOW())")
		} else {
			sb.WriteString(", NULL)")
		}
		args = append(args, j.ID, j.InstanceID, nullable(j.BatchID), j.Queue, []byte(j.Payload), nullable(j.Recipient), j.Status,
			j.MaxAttempts, j.ScheduledAt, nullable(j.TrackSource), nullable(j.TrackID))
	}
	_, err := q.ExecContext(ctx, sb.String(), args...)
	return err
}

func nullable(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func (s *JobStore) Get(ctx context.Context, id string) (*Job, error) {
	j, err := scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM message_jobs WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrJobNotFound
	}
	return j, err
}

// Claim atomically moves a job to "sending" for this worker. It returns nil when the job
// was cancelled/paused, already sent, not due yet or is being processed elsewhere.
func (s *JobStore) Claim(ctx context.Context, id string, lease time.Duration) (*Job, error) {
	j, err := scanJob(s.db.QueryRowContext(ctx, `UPDATE message_jobs
		SET status = 'sending', attempts = attempts + 1, locked_until = NOW() + make_interval(secs => $2), updated_at = NOW()
		WHERE id = $1
		  AND (status IN ('queued', 'retrying', 'scheduled') OR (status = 'sending' AND locked_until < NOW()))
		  AND scheduled_at <= NOW() + INTERVAL '2 seconds'
		RETURNING `+jobColumns, id, lease.Seconds()))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return j, err
}

func (s *JobStore) MarkSent(ctx context.Context, id, waID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE message_jobs SET status = 'sent', wa_message_id = $2, sent_at = NOW(), updated_at = NOW(),
		locked_until = NULL, last_error = NULL WHERE id = $1`, id, waID)
	return err
}

// MarkFailed schedules a retry at retryAt, or fails the job permanently when retryAt is nil.
func (s *JobStore) MarkFailed(ctx context.Context, id, reason string, retryAt *time.Time) error {
	if retryAt == nil {
		_, err := s.db.ExecContext(ctx, `UPDATE message_jobs SET status = 'failed', last_error = $2, locked_until = NULL, updated_at = NOW()
			WHERE id = $1`, id, reason)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE message_jobs SET status = 'retrying', last_error = $2, scheduled_at = $3, enqueued_at = NULL,
		locked_until = NULL, updated_at = NOW() WHERE id = $1`, id, reason, *retryAt)
	return err
}

// Defer pushes a job to a later time without consuming an attempt (send pacing).
func (s *JobStore) Defer(ctx context.Context, id, reason string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE message_jobs SET status = 'scheduled', attempts = GREATEST(attempts - 1, 0), last_error = $2,
		scheduled_at = $3, enqueued_at = NULL, locked_until = NULL, updated_at = NOW() WHERE id = $1`, id, reason, at)
	return err
}

// DueForEnqueue promotes due jobs to "queued" and returns them for publishing. It also picks
// up jobs whose queue message was lost (queued for too long) or whose worker crashed.
func (s *JobStore) DueForEnqueue(ctx context.Context, limit int) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `UPDATE message_jobs SET status = 'queued', enqueued_at = NOW(), updated_at = NOW()
		WHERE id IN (
			SELECT id FROM message_jobs
			WHERE (status IN ('scheduled', 'retrying') AND scheduled_at <= NOW())
			   OR (status = 'queued' AND scheduled_at <= NOW() AND (enqueued_at IS NULL OR enqueued_at < NOW() - INTERVAL '10 minutes'))
			   OR (status = 'sending' AND locked_until < NOW())
			ORDER BY scheduled_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		) RETURNING `+jobColumns, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

func (s *JobStore) ClearEnqueued(ctx context.Context, id string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE message_jobs SET enqueued_at = NULL WHERE id = $1 AND status = 'queued'`, id)
}

type JobFilter struct {
	InstanceID string
	BatchID    string
	Status     []string
	Limit      int
	Offset     int
}

func (s *JobStore) List(ctx context.Context, f JobFilter) ([]Job, int, error) {
	where := []string{"instance_id = $1"}
	args := []interface{}{f.InstanceID}
	if f.BatchID != "" {
		args = append(args, f.BatchID)
		where = append(where, "batch_id = $"+strconv.Itoa(len(args)))
	}
	if len(f.Status) > 0 {
		args = append(args, pq.Array(f.Status))
		where = append(where, "status = ANY($"+strconv.Itoa(len(args))+")")
	}
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 100
	}
	clause := " WHERE " + strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM message_jobs`+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobColumns+` FROM message_jobs`+clause+
		` ORDER BY scheduled_at, created_at LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *j)
	}
	return out, total, rows.Err()
}

// CancelPending cancels jobs that were not sent yet (all of an instance or of one batch).
func (s *JobStore) CancelPending(ctx context.Context, instanceID, batchID string) (int64, error) {
	q := `UPDATE message_jobs SET status = 'cancelled', updated_at = NOW()
		WHERE instance_id = $1 AND status IN ('scheduled', 'queued', 'retrying', 'paused')`
	args := []interface{}{instanceID}
	if batchID != "" {
		q += ` AND batch_id = $2`
		args = append(args, batchID)
	}
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ---- Batches ----

func (s *JobStore) CreateBatch(ctx context.Context, b *Batch, jobs []Job) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO message_batches (id, instance_id, info, status, total) VALUES ($1, $2, $3, 'scheduled', $4)`,
		b.ID, b.InstanceID, nullable(b.Info), len(jobs)); err != nil {
		return err
	}
	for start := 0; start < len(jobs); start += 500 {
		end := start + 500
		if end > len(jobs) {
			end = len(jobs)
		}
		if err := s.insertJobs(ctx, tx, jobs[start:end]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *JobStore) ListBatches(ctx context.Context, instanceID, status string) ([]Batch, error) {
	q := `SELECT b.id, b.instance_id, COALESCE(b.info, ''), b.status, b.total, b.created_at, b.updated_at,
		COALESCE(jsonb_object_agg(j.status, j.n) FILTER (WHERE j.status IS NOT NULL), '{}'::jsonb)
		FROM message_batches b
		LEFT JOIN (SELECT batch_id, status, COUNT(*) AS n FROM message_jobs WHERE batch_id IS NOT NULL GROUP BY batch_id, status) j
		  ON j.batch_id = b.id
		WHERE b.instance_id = $1`
	args := []interface{}{instanceID}
	if status != "" {
		q += ` AND b.status = $2`
		args = append(args, status)
	}
	rows, err := s.db.QueryContext(ctx, q+` GROUP BY b.id ORDER BY b.created_at DESC LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Batch{}
	for rows.Next() {
		var b Batch
		var counts []byte
		if err := rows.Scan(&b.ID, &b.InstanceID, &b.Info, &b.Status, &b.Total, &b.CreatedAt, &b.UpdatedAt, &counts); err != nil {
			return nil, err
		}
		b.Counts = map[string]int{}
		_ = json.Unmarshal(counts, &b.Counts)
		out = append(out, b)
	}
	return out, rows.Err()
}

// SetBatchState pauses, resumes or deletes a batch. Returns false when it does not exist.
func (s *JobStore) SetBatchState(ctx context.Context, instanceID, batchID, action string) (bool, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM message_batches WHERE id = $1 AND instance_id = $2)`, batchID, instanceID).Scan(&exists); err != nil || !exists {
		return false, err
	}
	var stmts []string
	switch action {
	case "stop", "pause":
		stmts = []string{
			`UPDATE message_jobs SET status = 'paused', updated_at = NOW() WHERE batch_id = $1 AND status IN ('scheduled', 'queued', 'retrying')`,
			`UPDATE message_batches SET status = 'paused', updated_at = NOW() WHERE id = $1`,
		}
	case "continue", "resume":
		// Paused jobs resume from now on, keeping their relative spacing.
		stmts = []string{
			`UPDATE message_jobs j SET status = 'scheduled', enqueued_at = NULL, updated_at = NOW(),
				scheduled_at = NOW() + (j.scheduled_at - m.first)
			 FROM (SELECT MIN(scheduled_at) AS first FROM message_jobs WHERE batch_id = $1 AND status = 'paused') m
			 WHERE j.batch_id = $1 AND j.status = 'paused'`,
			`UPDATE message_batches SET status = 'running', updated_at = NOW() WHERE id = $1`,
		}
	case "delete":
		stmts = []string{`DELETE FROM message_batches WHERE id = $1`}
	default:
		return true, errors.New("action must be stop, continue or delete")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return true, err
	}
	defer tx.Rollback()
	for _, q := range stmts {
		if _, err := tx.ExecContext(ctx, q, batchID); err != nil {
			return true, err
		}
	}
	return true, tx.Commit()
}

// RefreshBatches moves batches to running/done according to their jobs.
func (s *JobStore) RefreshBatches(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE message_batches b SET status = 'running', updated_at = NOW()
		WHERE b.status = 'scheduled' AND EXISTS (SELECT 1 FROM message_jobs j WHERE j.batch_id = b.id AND j.status IN ('sending', 'sent', 'failed'))`)
	if err != nil {
		return err
	}
	// Campaigns whose every job was cancelled end as cancelled, the others as done.
	_, err = s.db.ExecContext(ctx, `UPDATE message_batches b SET status = 'cancelled', updated_at = NOW()
		WHERE b.status IN ('scheduled', 'running', 'paused')
		  AND EXISTS (SELECT 1 FROM message_jobs j WHERE j.batch_id = b.id)
		  AND NOT EXISTS (SELECT 1 FROM message_jobs j WHERE j.batch_id = b.id AND j.status <> 'cancelled')`)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE message_batches b SET status = 'done', updated_at = NOW()
		WHERE b.status IN ('scheduled', 'running')
		  AND NOT EXISTS (SELECT 1 FROM message_jobs j WHERE j.batch_id = b.id AND j.status IN ('scheduled', 'queued', 'sending', 'retrying', 'paused'))`)
	return err
}

// ClearDone deletes finished batches older than hours (UAZAPI /sender/cleardone).
func (s *JobStore) ClearDone(ctx context.Context, instanceID string, hours int) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM message_batches WHERE instance_id = $1 AND status IN ('done', 'cancelled')
		AND updated_at < NOW() - make_interval(hours => $2)`, instanceID, hours)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *JobStore) ClearAllBatches(ctx context.Context, instanceID string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM message_batches WHERE instance_id = $1`, instanceID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Purge removes finished standalone jobs older than days.
func (s *JobStore) Purge(ctx context.Context, days int) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM message_jobs WHERE batch_id IS NULL AND status IN ('sent', 'failed', 'cancelled')
		AND updated_at < NOW() - make_interval(days => $1)`, days)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
