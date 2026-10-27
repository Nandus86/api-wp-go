package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/user/whatsmeow-basileia/internal/infrastructure/rabbitmq"
	"github.com/user/whatsmeow-basileia/internal/infrastructure/storage"
	"github.com/user/whatsmeow-basileia/internal/infrastructure/whatsapp"
	"github.com/user/whatsmeow-basileia/internal/service/messages"
	"github.com/user/whatsmeow-basileia/pkg/logger"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.uber.org/zap"
)

type Config struct {
	Workers        int
	MaxAttempts    int
	SimulateTyping bool
	RetentionDays  int
	MediaMaxBytes  int64
	ArchiveMedia   bool
}

// Observer receives send outcomes (used for metrics).
type Observer func(kind, outcome string)

// Service owns the outbound pipeline: jobs (Postgres) -> RabbitMQ -> workers -> WhatsApp.
type Service struct {
	jobs     *JobStore
	rmq      *rabbitmq.Client
	manager  *whatsapp.MultiClientManager
	history  *messages.Store
	pacer    *Pacer
	storage  *storage.Storage
	cfg      Config
	log      *zap.Logger
	http     *http.Client
	observer Observer
}

func NewService(jobs *JobStore, rmq *rabbitmq.Client, manager *whatsapp.MultiClientManager, history *messages.Store,
	pacer *Pacer, st *storage.Storage, cfg Config, log *zap.Logger) *Service {
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 5
	}
	return &Service{
		jobs: jobs, rmq: rmq, manager: manager, history: history, pacer: pacer, storage: st, cfg: cfg, log: log,
		http: &http.Client{Timeout: 2 * time.Minute},
	}
}

func (s *Service) Jobs() *JobStore          { return s.jobs }
func (s *Service) Pacer() *Pacer            { return s.pacer }
func (s *Service) History() *messages.Store { return s.history }
func (s *Service) SetObserver(o Observer)   { s.observer = o }
func (s *Service) observe(kind, out string) {
	if s.observer != nil {
		s.observer(kind, out)
	}
}

func (s *Service) Start(ctx context.Context) {
	if err := s.rmq.ConsumeN(rabbitmq.QueueSendMessage, s.cfg.Workers, s.handleSendMessage); err != nil {
		s.log.Error("Failed to start consuming send_message_queue", zap.Error(err))
	} else {
		s.log.Info("Started RabbitMQ worker for send_message_queue", zap.Int("workers", s.cfg.Workers))
	}
	if err := s.rmq.ConsumeN(rabbitmq.QueueSendMedia, s.cfg.Workers, s.handleSendMedia); err != nil {
		s.log.Error("Failed to start consuming send_media_queue", zap.Error(err))
	} else {
		s.log.Info("Started RabbitMQ worker for send_media_queue", zap.Int("workers", s.cfg.Workers))
	}
	go s.schedulerLoop(ctx)
	go s.purgeLoop(ctx)
}

// ---- Enqueueing ----

type EnqueueOptions struct {
	ScheduledAt time.Time
	BatchID     string
	Recipient   string
	TrackSource string
	TrackID     string
}

// Enqueue records a send job and publishes it. payload is any JSON-serialisable request;
// device_id and job_id are injected. On a database outage the message is still published
// (untracked), matching the previous behaviour; the returned job id is then empty.
func (s *Service) Enqueue(ctx context.Context, instanceID, queue string, payload interface{}, opt EnqueueOptions) (string, error) {
	body, job, err := s.prepare(instanceID, queue, payload, opt)
	if err != nil {
		return "", err
	}
	if err := s.jobs.insertJobs(ctx, s.jobs.db, []Job{*job}); err != nil {
		s.log.Error("Failed to record send job, publishing untracked", zap.Error(err))
		var m map[string]interface{}
		_ = json.Unmarshal(body, &m)
		delete(m, "job_id")
		body, _ = json.Marshal(m)
		return "", s.rmq.Publish(ctx, queue, body)
	}
	if job.Status == "queued" {
		if err := s.rmq.Publish(ctx, queue, body); err != nil {
			// The job is safe in Postgres: the scheduler publishes it once the broker is back.
			s.log.Warn("Broker unavailable, job will be published by the scheduler", zap.String("job", job.ID), zap.Error(err))
			s.jobs.ClearEnqueued(context.Background(), job.ID)
		}
	}
	return job.ID, nil
}

func (s *Service) prepare(instanceID, queue string, payload interface{}, opt EnqueueOptions) ([]byte, *Job, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, nil, fmt.Errorf("payload must be a JSON object: %w", err)
	}
	job := &Job{
		ID: newJobID(), InstanceID: instanceID, BatchID: opt.BatchID, Queue: queue, Status: "queued",
		MaxAttempts: s.cfg.MaxAttempts, ScheduledAt: time.Now(), Recipient: opt.Recipient,
		TrackSource: opt.TrackSource, TrackID: opt.TrackID,
	}
	if !opt.ScheduledAt.IsZero() && opt.ScheduledAt.After(time.Now().Add(time.Second)) {
		job.Status = "scheduled"
		job.ScheduledAt = opt.ScheduledAt
	}
	if job.Recipient == "" {
		for _, k := range []string{"number", "phone"} {
			if v, ok := m[k].(string); ok && v != "" {
				job.Recipient = v
				break
			}
		}
	}
	m["device_id"] = instanceID
	m["job_id"] = job.ID
	body, err := json.Marshal(m)
	if err != nil {
		return nil, nil, err
	}
	job.Payload = body
	return body, job, nil
}

// BatchItem is one message of a bulk campaign.
type BatchItem struct {
	Queue   string
	Payload interface{}
	Options EnqueueOptions
}

// EnqueueBatch stores a campaign and its jobs atomically; the scheduler sends them over time.
func (s *Service) EnqueueBatch(ctx context.Context, instanceID, info string, items []BatchItem) (*Batch, error) {
	b := &Batch{ID: newJobID(), InstanceID: instanceID, Info: info, Status: "scheduled", Total: len(items)}
	jobs := make([]Job, 0, len(items))
	for _, it := range items {
		it.Options.BatchID = b.ID
		_, job, err := s.prepare(instanceID, it.Queue, it.Payload, it.Options)
		if err != nil {
			return nil, err
		}
		// Every batch job goes through the scheduler so pausing works uniformly.
		job.Status = "scheduled"
		jobs = append(jobs, *job)
	}
	if err := s.jobs.CreateBatch(ctx, b, jobs); err != nil {
		return nil, err
	}
	return b, nil
}

// ---- Worker pipeline ----

type sendContext struct {
	instanceID string
	client     *whatsmeow.Client
	instance   *whatsapp.Instance
	job        *Job
}

type sendOutcome struct {
	msgID        string
	msg          *waE2E.Message
	chat         types.JID
	presenceOnly bool
	permanent    bool
	deferUntil   time.Time
	err          error
}

// runJob wraps a send with job claiming, connection checks and the retry policy.
// With a job, failures are retried through the job table (exponential backoff) and the
// queue message is always acknowledged. Without one (messages queued by older versions)
// a transient error is returned so RabbitMQ's retry queue handles it.
func (s *Service) runJob(jobID, deviceID string, send func(ctx context.Context, sc *sendContext) sendOutcome) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var job *Job
	if jobID != "" {
		var err error
		job, err = s.jobs.Claim(ctx, jobID, 6*time.Minute)
		if err != nil {
			return fmt.Errorf("claim job %s: %w", jobID, err)
		}
		if job == nil {
			return nil // cancelled, paused, already sent or owned by another worker
		}
		deviceID = job.InstanceID
	}

	instanceID, ok := s.manager.ResolveID(deviceID)
	if !ok {
		return s.finish(ctx, job, "", sendOutcome{err: permanentf("instance %q not found", deviceID), permanent: true})
	}
	client := s.manager.GetClient(instanceID)
	inst, _ := s.manager.GetInstance(instanceID)
	if client == nil || inst == nil {
		return s.finish(ctx, job, instanceID, sendOutcome{err: permanentf("instance %q not found", deviceID), permanent: true})
	}
	if !client.IsLoggedIn() && client.Store.ID == nil {
		return s.finish(ctx, job, instanceID, sendOutcome{err: permanentf("instance %q is not paired", instanceID), permanent: true})
	}
	if !client.IsConnected() {
		// Usually a short reconnect: retry later instead of dropping the message.
		return s.finish(ctx, job, instanceID, sendOutcome{err: errors.New("instance is not connected")})
	}

	out := send(ctx, &sendContext{instanceID: instanceID, client: client, instance: inst, job: job})
	return s.finish(ctx, job, instanceID, out)
}

func (s *Service) finish(ctx context.Context, job *Job, instanceID string, out sendOutcome) error {
	kind := "message"
	if job != nil && job.Queue == rabbitmq.QueueSendMedia {
		kind = "media"
	}
	if out.err == nil {
		if out.presenceOnly {
			if job != nil {
				_ = s.jobs.MarkSent(ctx, job.ID, "")
			}
			return nil
		}
		s.observe(kind, "sent")
		if job != nil {
			if err := s.jobs.MarkSent(ctx, job.ID, out.msgID); err != nil {
				s.log.Warn("Failed to mark job as sent", zap.String("job", job.ID), zap.Error(err))
			}
		}
		return nil
	}

	if !out.deferUntil.IsZero() && job != nil {
		s.observe(kind, "deferred")
		s.log.Info("Send deferred by pacing", zap.String("job", job.ID), zap.String("reason", out.err.Error()), zap.Time("until", out.deferUntil))
		return s.jobs.Defer(ctx, job.ID, out.err.Error(), out.deferUntil)
	}

	logger.PushLog("error", "Failed to send message: "+out.err.Error())
	if job == nil {
		s.observe(kind, "failed")
		s.log.Error("Failed to send untracked message", zap.String("instance", instanceID), zap.Error(out.err))
		if out.permanent {
			return nil
		}
		return out.err
	}
	if out.permanent || job.Attempts >= job.MaxAttempts {
		s.observe(kind, "failed")
		s.log.Error("Send job failed", zap.String("job", job.ID), zap.String("instance", instanceID), zap.Int("attempts", job.Attempts), zap.Error(out.err))
		return s.jobs.MarkFailed(ctx, job.ID, out.err.Error(), nil)
	}
	s.observe(kind, "retry")
	retryAt := time.Now().Add(jobBackoff(job.Attempts))
	s.log.Warn("Send job failed, will retry", zap.String("job", job.ID), zap.Int("attempt", job.Attempts), zap.Time("retry_at", retryAt), zap.Error(out.err))
	return s.jobs.MarkFailed(ctx, job.ID, out.err.Error(), &retryAt)
}

var jobBackoffSteps = []time.Duration{15 * time.Second, time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour}

func jobBackoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > len(jobBackoffSteps) {
		return jobBackoffSteps[len(jobBackoffSteps)-1]
	}
	return jobBackoffSteps[attempts-1]
}

// deliver applies pacing, typing delay, reply and mentions, sends the message and records it.
func (s *Service) deliver(ctx context.Context, sc *sendContext, b *MessageBuilder, opt SendOptions, textLen int) sendOutcome {
	chat := b.JID()
	instanceID := sc.instanceID

	cold := false
	if s.pacer.Enabled() && chat.Server == types.DefaultUserServer && s.history != nil {
		has, err := s.history.HasConversation(ctx, instanceID, chat.ToNonAD().String())
		cold = err == nil && !has
	}
	if d := s.pacer.Reserve(ctx, instanceID, sc.instance.PairedAt, cold); !d.Allowed {
		return sendOutcome{err: errors.New(d.Reason), deferUntil: d.RetryAt}
	}

	if opt.ReplyID != "" {
		b.WithContextInfo(s.quoteContext(ctx, instanceID, opt.ReplyID))
	}
	if opt.Mentions != "" {
		if jids := s.mentionJIDs(ctx, sc.client, chat, opt.Mentions); len(jids) > 0 {
			b.WithContextInfo(&waE2E.ContextInfo{MentionedJID: jids})
		}
	}

	s.waitBeforeSend(ctx, sc, chat, opt, textLen)

	sendCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	msgID, err := b.Send(sendCtx)
	if err != nil {
		s.pacer.Release(ctx, instanceID, cold)
		s.pacer.RecordFailure(ctx, instanceID)
		return sendOutcome{err: fmt.Errorf("send via WhatsApp: %w", err)}
	}
	s.pacer.RecordSuccess(ctx, instanceID)

	s.log.Info("Message sent successfully via Queue", zap.String("msgID", msgID), zap.String("to", chat.String()))
	logger.PushLog("info", "Message sent successfully to "+chat.User)
	s.manager.RecordMessageStat(instanceID, "out")
	jobID := ""
	if sc.job != nil {
		jobID = sc.job.ID
	}
	if s.history != nil {
		s.history.SaveOutgoing(instanceID, msgID, chat, b.Message(), jobID, time.Now())
	}
	return sendOutcome{msgID: msgID, msg: b.Message(), chat: chat}
}

// waitBeforeSend shows "typing..." for the requested delay, the simulated typing time,
// or sleeps the instance's random delay range.
func (s *Service) waitBeforeSend(ctx context.Context, sc *sendContext, chat types.JID, opt SendOptions, textLen int) {
	wait := time.Duration(opt.Delay) * time.Millisecond
	typing := wait > 0
	if wait == 0 && s.cfg.SimulateTyping && textLen > 0 {
		wait = time.Duration(textLen) * 40 * time.Millisecond
		if wait > 4*time.Second {
			wait = 4 * time.Second
		}
		if wait < 800*time.Millisecond {
			wait = 800 * time.Millisecond
		}
		typing = true
	}
	if wait == 0 && sc.instance.Settings.MsgDelayMax > 0 {
		lo, hi := sc.instance.Settings.MsgDelayMin, sc.instance.Settings.MsgDelayMax
		if lo > hi {
			lo, hi = hi, lo
		}
		wait = time.Duration(lo+rand.Intn(hi-lo+1)) * time.Millisecond
	}
	if wait <= 0 {
		return
	}
	if wait > 2*time.Minute {
		wait = 2 * time.Minute
	}
	if typing && chat.Server != types.BroadcastServer {
		_ = sc.client.SendChatPresence(ctx, chat, types.ChatPresenceComposing, types.ChatPresenceMediaText)
		defer sc.client.SendChatPresence(context.Background(), chat, types.ChatPresencePaused, types.ChatPresenceMediaText)
	}
	select {
	case <-time.After(wait):
	case <-ctx.Done():
	}
}

func (s *Service) quoteContext(ctx context.Context, instanceID, replyID string) *waE2E.ContextInfo {
	id := messages.NormalizeID(replyID)
	if s.history != nil {
		if m, err := s.history.Get(ctx, instanceID, id); err == nil {
			sender := types.EmptyJID
			if m.SenderJID != "" {
				sender, _ = types.ParseJID(m.SenderJID)
			}
			if m.FromMe {
				if c := s.manager.GetClient(instanceID); c != nil && c.Store.ID != nil {
					sender = c.Store.ID.ToNonAD()
				}
			}
			return QuoteContext(m.ID, sender, m.Raw())
		}
	}
	return QuoteContext(id, types.EmptyJID, nil)
}

func (s *Service) mentionJIDs(ctx context.Context, client *whatsmeow.Client, chat types.JID, mentions string) []string {
	var out []string
	if strings.EqualFold(strings.TrimSpace(mentions), "all") {
		if chat.Server != types.GroupServer {
			return nil
		}
		info, err := client.GetGroupInfo(ctx, chat)
		if err != nil {
			return nil
		}
		for _, p := range info.Participants {
			out = append(out, p.JID.ToNonAD().String())
		}
		return out
	}
	for _, n := range strings.Split(mentions, ",") {
		if jid, err := ParseJIDOrNumber(n); err == nil {
			out = append(out, jid.String())
		}
	}
	return out
}

var nonDigits = regexp.MustCompile(`\D+`)

// ParseJIDOrNumber accepts a full JID ("x@g.us", "x@s.whatsapp.net", "x@lid", "x@newsletter")
// or a phone number in any format ("+55 (11) 99999-9999").
func ParseJIDOrNumber(s string) (types.JID, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "@") {
		jid, err := types.ParseJID(s)
		if err != nil {
			return types.EmptyJID, permanentf("invalid JID %q: %v", s, err)
		}
		return jid, nil
	}
	digits := nonDigits.ReplaceAllString(s, "")
	if strings.Contains(s, "-") && len(digits) > 15 {
		// Legacy group id ("5511999999999-1600000000") without the @g.us suffix.
		return types.NewJID(strings.TrimSpace(s), types.GroupServer), nil
	}
	if len(digits) > 15 && strings.HasPrefix(digits, "120363") {
		return types.NewJID(digits, types.GroupServer), nil
	}
	if len(digits) < 8 || len(digits) > 15 {
		return types.EmptyJID, permanentf("invalid phone number %q", s)
	}
	return types.NewJID(digits, types.DefaultUserServer), nil
}

// ResolveRecipient turns a number or JID into the JID to send to. Phone numbers are
// checked against WhatsApp's directory first, which fixes the Brazilian 9th digit.
func ResolveRecipient(ctx context.Context, client *whatsmeow.Client, number string) (types.JID, error) {
	jid, err := ParseJIDOrNumber(number)
	if err != nil {
		return types.EmptyJID, err
	}
	if jid.Server != types.DefaultUserServer {
		return jid, nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, err := client.IsOnWhatsApp(lookupCtx, []string{"+" + jid.User})
	if err == nil && len(resp) > 0 && resp[0].IsIn {
		return resp[0].JID, nil
	}
	// Unverified: keep the previous behaviour and send to the number as given.
	return jid, nil
}

// ---- Background loops ----

func (s *Service) schedulerLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	tick := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		tick++
		if !s.rmq.IsReady() {
			continue
		}
		qctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		jobs, err := s.jobs.DueForEnqueue(qctx, 200)
		if err != nil {
			s.log.Warn("Job scheduler query failed", zap.Error(err))
		}
		for _, j := range jobs {
			if err := s.rmq.Publish(qctx, j.Queue, j.Payload); err != nil {
				s.jobs.ClearEnqueued(context.Background(), j.ID)
			}
		}
		if tick%5 == 0 {
			if err := s.jobs.RefreshBatches(qctx); err != nil {
				s.log.Warn("Batch refresh failed", zap.Error(err))
			}
		}
		cancel()
	}
}

func (s *Service) purgeLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		pctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		if n, err := s.jobs.Purge(pctx, 30); err == nil && n > 0 {
			s.log.Info("Purged old send jobs", zap.Int64("rows", n))
		}
		if s.history != nil && s.cfg.RetentionDays > 0 {
			if n, err := s.history.Purge(pctx, s.cfg.RetentionDays); err != nil {
				s.log.Warn("Message retention purge failed", zap.Error(err))
			} else if n > 0 {
				s.log.Info("Purged old messages", zap.Int64("rows", n))
			}
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
