// Package messages keeps the message history (inbound and outbound) and delivery status.
package messages

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/user/whatsmeow-basileia/internal/infrastructure/storage"
	"github.com/user/whatsmeow-basileia/internal/infrastructure/whatsapp"
	"github.com/user/whatsmeow-basileia/pkg/logger"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var ErrNotFound = errors.New("message not found")

func init() {
	whatsapp.RegisterInstanceRef(`UPDATE messages SET instance_id = $1 WHERE instance_id = $2`)
	whatsapp.RegisterInstanceRef(`UPDATE message_jobs SET instance_id = $1 WHERE instance_id = $2`)
	whatsapp.RegisterInstanceRef(`UPDATE message_batches SET instance_id = $1 WHERE instance_id = $2`)
}

type Message struct {
	InstanceID string                 `json:"instance_id"`
	ID         string                 `json:"id"`
	ChatJID    string                 `json:"chat_jid"`
	SenderJID  string                 `json:"sender_jid,omitempty"`
	FromMe     bool                   `json:"from_me"`
	IsGroup    bool                   `json:"is_group"`
	Type       string                 `json:"type"`
	Text       string                 `json:"text,omitempty"`
	PushName   string                 `json:"push_name,omitempty"`
	Content    map[string]interface{} `json:"content,omitempty"`
	MediaKey   string                 `json:"media_key,omitempty"`
	MediaMime  string                 `json:"media_mime,omitempty"`
	Status     string                 `json:"status"`
	SentByAPI  bool                   `json:"sent_by_api"`
	JobID      string                 `json:"job_id,omitempty"`
	Edited     bool                   `json:"edited"`
	Timestamp  time.Time              `json:"timestamp"`
	HasRaw     bool                   `json:"has_media_source"`
	raw        []byte
}

// Raw decodes the stored protobuf (nil when not available).
func (m *Message) Raw() *waE2E.Message {
	if len(m.raw) == 0 {
		return nil
	}
	var msg waE2E.Message
	if err := proto.Unmarshal(m.raw, &msg); err != nil {
		return nil
	}
	return &msg
}

type Options struct {
	ArchiveMedia  bool
	MediaMaxBytes int64
}

type Store struct {
	db      *sql.DB
	storage *storage.Storage
	opts    Options
	writes  chan row
	media   chan mediaJob
}

type row struct {
	m       Message
	content []byte
}

type mediaJob struct {
	instanceID string
	id         string
	client     *whatsmeow.Client
	msg        *waE2E.Message
	mime       string
	ts         time.Time
}

func NewStore(db *sql.DB, st *storage.Storage, opts Options) *Store {
	s := &Store{db: db, storage: st, opts: opts, writes: make(chan row, 5000), media: make(chan mediaJob, 500)}
	go s.writer()
	for i := 0; i < 4; i++ {
		go s.mediaWorker()
	}
	return s
}

// statusRank lets receipts only move a message forward (delivered never overwrites read).
const statusRankSQL = `CASE %s WHEN 'failed' THEN 0 WHEN 'received' THEN 0 WHEN 'pending' THEN 1 WHEN 'sent' THEN 2
	WHEN 'delivered' THEN 3 WHEN 'read' THEN 4 WHEN 'played' THEN 5 WHEN 'deleted' THEN 9 ELSE 0 END`

func rank(col string) string { return fmt.Sprintf(statusRankSQL, col) }

// StatusFromReceipt maps a whatsmeow receipt type to a message status ("" = ignore).
func StatusFromReceipt(t types.ReceiptType) string {
	switch t {
	case types.ReceiptTypeDelivered:
		return "delivered"
	case types.ReceiptTypeRead, types.ReceiptTypeReadSelf:
		return "read"
	case types.ReceiptTypePlayed, types.ReceiptTypePlayedSelf:
		return "played"
	}
	return ""
}

func textOf(content map[string]interface{}) string {
	for _, k := range []string{"text", "conversation", "caption", "contentText", "description", "displayName", "name"} {
		if v, ok := content[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func mimeOf(content map[string]interface{}) string {
	if v, ok := content["mimetype"].(string); ok {
		return v
	}
	return ""
}

func isMedia(t string) bool {
	switch t {
	case "imageMessage", "videoMessage", "audioMessage", "documentMessage", "stickerMessage":
		return true
	}
	return false
}

func buildRow(instanceID, id string, chat, sender types.JID, fromMe, isGroup bool, pushName string, msg *waE2E.Message, ts time.Time, status string) row {
	msgType, content, ctxInfo, reaction, poll := whatsapp.ParseMessageContent(msg)
	if msgType == "unknown" {
		if t := protoType(msg); t != "" {
			msgType = t
		}
	}
	if content == nil {
		content = map[string]interface{}{}
	}
	if ctxInfo != nil {
		content["contextInfo"] = ctxInfo
	}
	if reaction != nil {
		content["reaction"] = reaction
	}
	if poll != nil {
		content["poll"] = poll
	}
	m := Message{
		InstanceID: instanceID, ID: id, ChatJID: chat.ToNonAD().String(), FromMe: fromMe, IsGroup: isGroup,
		Type: msgType, Text: textOf(content), PushName: pushName, MediaMime: mimeOf(content), Status: status, Timestamp: ts,
	}
	if !sender.IsEmpty() {
		m.SenderJID = sender.ToNonAD().String()
	}
	// Only media messages need the protobuf later (download, re-upload); quoting uses it too,
	// so keep it for every message under 64 KiB.
	if msg != nil {
		if b, err := proto.Marshal(msg); err == nil && (len(b) < 64<<10 || isMedia(msgType)) {
			m.raw = b
		}
	}
	cb, _ := json.Marshal(content)
	return row{m: m, content: cb}
}

// SaveIncoming records a message event (also used for messages sent from the phone).
func (s *Store) SaveIncoming(instanceID string, client *whatsmeow.Client, evt *events.Message) {
	if evt.Message == nil {
		return
	}
	if evt.Message.GetProtocolMessage() != nil {
		s.handleProtocol(instanceID, evt)
		return
	}
	if !hasContent(evt.Message) {
		return // e.g. a bare sender-key distribution message
	}
	status := "received"
	if evt.Info.IsFromMe {
		status = "sent"
	}
	r := buildRow(instanceID, evt.Info.ID, evt.Info.Chat, evt.Info.Sender, evt.Info.IsFromMe, evt.Info.IsGroup, evt.Info.PushName, evt.Message, evt.Info.Timestamp, status)
	s.enqueue(r)

	if s.opts.ArchiveMedia && s.storage.Enabled() && isMedia(r.m.Type) {
		select {
		case s.media <- mediaJob{instanceID: instanceID, id: evt.Info.ID, client: client, msg: evt.Message, mime: r.m.MediaMime, ts: evt.Info.Timestamp}:
		default:
			logger.Warn("Media archive queue full, skipping", zap.String("message", evt.Info.ID))
		}
	}
}

func hasContent(m *waE2E.Message) bool {
	return protoType(m) != ""
}

// protoType returns the name of the first populated content field of a message
// (e.g. "listResponseMessage"), ignoring envelope-only fields.
func protoType(m *waE2E.Message) string {
	if m == nil {
		return ""
	}
	name := ""
	m.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		switch fd.JSONName() {
		case "messageContextInfo", "senderKeyDistributionMessage":
			return true
		}
		name = fd.JSONName()
		return false
	})
	return name
}

// handleProtocol applies edits and revokes that arrive as protocol messages.
func (s *Store) handleProtocol(instanceID string, evt *events.Message) {
	pm := evt.Message.GetProtocolMessage()
	if pm == nil || pm.GetKey() == nil {
		return
	}
	target := pm.GetKey().GetID()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	switch pm.GetType() {
	case waE2E.ProtocolMessage_REVOKE:
		_ = s.MarkDeleted(ctx, instanceID, target)
	case waE2E.ProtocolMessage_MESSAGE_EDIT:
		if edited := pm.GetEditedMessage(); edited != nil {
			_, content, _, _, _ := whatsapp.ParseMessageContent(edited)
			_ = s.MarkEdited(ctx, instanceID, target, textOf(content))
		}
	}
}

// SaveOutgoing records a message sent through the API.
func (s *Store) SaveOutgoing(instanceID, id string, chat types.JID, msg *waE2E.Message, jobID string, ts time.Time) {
	r := buildRow(instanceID, id, chat, types.EmptyJID, true, chat.Server == types.GroupServer, "", msg, ts, "sent")
	r.m.SentByAPI = true
	r.m.JobID = jobID
	s.enqueue(r)
}

func (s *Store) enqueue(r row) {
	select {
	case s.writes <- r:
	default:
		logger.Warn("Message history queue full, dropping row", zap.String("message", r.m.ID))
	}
}

// writer batches inserts so a burst of messages costs a few round-trips, not one each.
func (s *Store) writer() {
	batch := make([]row, 0, 100)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := s.insert(batch); err != nil {
			logger.Error("Failed to store message history", zap.Int("rows", len(batch)), zap.Error(err))
		}
		batch = batch[:0]
	}
	for {
		select {
		case r := <-s.writes:
			batch = append(batch, r)
			if len(batch) >= 100 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (s *Store) insert(rows []row) error {
	// The same message can appear twice in one batch (e.g. history sync); keep the last.
	dedup := map[string]int{}
	var unique []row
	for _, r := range rows {
		k := r.m.InstanceID + "\x00" + r.m.ID
		if i, ok := dedup[k]; ok {
			unique[i] = r
			continue
		}
		dedup[k] = len(unique)
		unique = append(unique, r)
	}

	var sb strings.Builder
	sb.WriteString(`INSERT INTO messages (instance_id, id, chat_jid, sender_jid, from_me, is_group, type, text, push_name, content, raw,
		media_mime, status, sent_by_api, job_id, timestamp) VALUES `)
	args := make([]interface{}, 0, len(unique)*16)
	for i, r := range unique {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString("(")
		for j := 1; j <= 16; j++ {
			if j > 1 {
				sb.WriteString(",")
			}
			sb.WriteString("$" + strconv.Itoa(len(args)+j))
		}
		sb.WriteString(")")
		m := r.m
		args = append(args, m.InstanceID, m.ID, m.ChatJID, nullStr(m.SenderJID), m.FromMe, m.IsGroup, m.Type, nullStr(m.Text),
			nullStr(m.PushName), r.content, m.raw, nullStr(m.MediaMime), m.Status, m.SentByAPI, nullStr(m.JobID), m.Timestamp)
	}
	sb.WriteString(` ON CONFLICT (instance_id, id) DO UPDATE SET
		content = EXCLUDED.content, raw = COALESCE(EXCLUDED.raw, messages.raw), text = COALESCE(EXCLUDED.text, messages.text),
		job_id = COALESCE(messages.job_id, EXCLUDED.job_id), sent_by_api = messages.sent_by_api OR EXCLUDED.sent_by_api,
		updated_at = NOW()`)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := s.db.ExecContext(ctx, sb.String(), args...)
	return err
}

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// UpdateStatus moves messages forward to status (never backwards).
func (s *Store) UpdateStatus(ctx context.Context, instanceID string, ids []string, status string) error {
	if status == "" || len(ids) == 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET status = $3, updated_at = NOW()
		WHERE instance_id = $1 AND id = ANY($2) AND `+rank("status")+` < `+rank("$3::varchar"),
		instanceID, pq.Array(ids), status)
	return err
}

func (s *Store) MarkDeleted(ctx context.Context, instanceID, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET status = 'deleted', updated_at = NOW() WHERE instance_id = $1 AND id = $2`, instanceID, id)
	return err
}

func (s *Store) MarkEdited(ctx context.Context, instanceID, id, text string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET edited = TRUE, text = $3, updated_at = NOW() WHERE instance_id = $1 AND id = $2`, instanceID, id, text)
	return err
}

const messageColumns = `instance_id, id, chat_jid, COALESCE(sender_jid, ''), from_me, is_group, type, COALESCE(text, ''),
	COALESCE(push_name, ''), content, COALESCE(media_key, ''), COALESCE(media_mime, ''), status, sent_by_api, COALESCE(job_id, ''),
	edited, timestamp, raw`

func scan(row interface{ Scan(...any) error }) (*Message, error) {
	var m Message
	var content []byte
	if err := row.Scan(&m.InstanceID, &m.ID, &m.ChatJID, &m.SenderJID, &m.FromMe, &m.IsGroup, &m.Type, &m.Text, &m.PushName,
		&content, &m.MediaKey, &m.MediaMime, &m.Status, &m.SentByAPI, &m.JobID, &m.Edited, &m.Timestamp, &m.raw); err != nil {
		return nil, err
	}
	if len(content) > 0 {
		_ = json.Unmarshal(content, &m.Content)
	}
	m.HasRaw = len(m.raw) > 0
	return &m, nil
}

// Get looks a message up by id. UAZAPI-style ids ("owner:msgid") are accepted too.
func (s *Store) Get(ctx context.Context, instanceID, id string) (*Message, error) {
	id = NormalizeID(id)
	m, err := scan(s.db.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM messages WHERE instance_id = $1 AND id = $2`, instanceID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return m, err
}

// NormalizeID strips the "owner:" prefix used in webhook payloads ("5511999:3EB0...").
func NormalizeID(id string) string {
	id = strings.TrimSpace(id)
	if i := strings.LastIndex(id, ":"); i >= 0 {
		return id[i+1:]
	}
	return id
}

type Filter struct {
	ChatJID string
	ID      string
	JobID   string
	Before  *time.Time
	Limit   int
	Offset  int
}

func (s *Store) Find(ctx context.Context, instanceID string, f Filter) ([]Message, int, error) {
	where := []string{"instance_id = $1"}
	args := []interface{}{instanceID}
	add := func(cond string, v interface{}) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(args))))
	}
	if f.ChatJID != "" {
		add("chat_jid = ?", f.ChatJID)
	}
	if f.ID != "" {
		add("id = ?", NormalizeID(f.ID))
	}
	if f.JobID != "" {
		add("job_id = ?", f.JobID)
	}
	if f.Before != nil {
		add("timestamp < ?", *f.Before)
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 50
	}
	clause := " WHERE " + strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages`+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := s.db.QueryContext(ctx, `SELECT `+messageColumns+` FROM messages`+clause+
		` ORDER BY timestamp DESC LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		m, err := scan(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *m)
	}
	return out, total, rows.Err()
}

// HasConversation reports whether any message was exchanged with chat (used by send pacing).
func (s *Store) HasConversation(ctx context.Context, instanceID, chat string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM messages WHERE instance_id = $1 AND chat_jid = $2)`, instanceID, chat).Scan(&exists)
	return exists, err
}

// LastInChat returns the newest message of a chat (needed by archive/read app-state patches).
func (s *Store) LastInChat(ctx context.Context, instanceID, chat string) (*Message, error) {
	m, err := scan(s.db.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM messages WHERE instance_id = $1 AND chat_jid = $2
		ORDER BY timestamp DESC LIMIT 1`, instanceID, chat))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return m, err
}

func (s *Store) DeleteChat(ctx context.Context, instanceID, chat string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM messages WHERE instance_id = $1 AND chat_jid = $2`, instanceID, chat)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) SetMediaKey(ctx context.Context, instanceID, id, key, mime string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET media_key = $3, media_mime = COALESCE(NULLIF($4, ''), media_mime), updated_at = NOW()
		WHERE instance_id = $1 AND id = $2`, instanceID, id, key, mime)
	return err
}

// Purge deletes messages older than days (0 keeps everything) together with archived media.
func (s *Store) Purge(ctx context.Context, days int) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	if s.storage.Enabled() {
		rows, err := s.db.QueryContext(ctx, `SELECT media_key FROM messages WHERE media_key IS NOT NULL AND timestamp < NOW() - make_interval(days => $1) LIMIT 5000`, days)
		if err == nil {
			for rows.Next() {
				var k string
				if rows.Scan(&k) == nil {
					_ = s.storage.Delete(ctx, k)
				}
			}
			rows.Close()
		}
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM messages WHERE timestamp < NOW() - make_interval(days => $1)`, days)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) mediaWorker() {
	for j := range s.media {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		data, err := j.client.DownloadAny(ctx, j.msg)
		if err != nil {
			cancel()
			logger.Warn("Failed to download media for archiving", zap.String("message", j.id), zap.Error(err))
			continue
		}
		if s.opts.MediaMaxBytes > 0 && int64(len(data)) > s.opts.MediaMaxBytes {
			cancel()
			continue
		}
		key, err := s.ArchiveBytes(ctx, j.instanceID, j.id, data, j.mime, j.ts)
		cancel()
		if err != nil {
			logger.Warn("Failed to archive media", zap.String("message", j.id), zap.Error(err))
			continue
		}
		_ = key
	}
}

// ArchiveBytes stores media in object storage and links it to the message.
func (s *Store) ArchiveBytes(ctx context.Context, instanceID, id string, data []byte, mime string, ts time.Time) (string, error) {
	if !s.storage.Enabled() {
		return "", storage.ErrDisabled
	}
	key := path.Join("messages", instanceID, ts.UTC().Format("2006/01/02"), id+ExtFor(mime))
	if err := s.storage.Put(ctx, key, data, mime); err != nil {
		return "", err
	}
	// The history row may still be in the write buffer; retry briefly so the link is not lost.
	for i := 0; i < 5; i++ {
		res, err := s.db.ExecContext(ctx, `UPDATE messages SET media_key = $3, updated_at = NOW() WHERE instance_id = $1 AND id = $2`, instanceID, id, key)
		if err != nil {
			return key, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			break
		}
		time.Sleep(time.Second)
	}
	return key, nil
}

// ExtFor returns a file extension for common WhatsApp mime types.
func ExtFor(mime string) string {
	mime = strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0]))
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	case "audio/ogg":
		return ".ogg"
	case "audio/mpeg":
		return ".mp3"
	case "audio/mp4", "audio/aac":
		return ".m4a"
	case "application/pdf":
		return ".pdf"
	}
	return ""
}

func (s *Store) Storage() *storage.Storage { return s.storage }
