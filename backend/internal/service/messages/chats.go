package messages

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/user/whatsmeow-basileia/internal/infrastructure/whatsapp"
	"go.mau.fi/whatsmeow/types/events"
)

func init() {
	whatsapp.RegisterInstanceRef(`UPDATE labels SET instance_id = $1 WHERE instance_id = $2`)
	whatsapp.RegisterInstanceRef(`UPDATE chat_labels SET instance_id = $1 WHERE instance_id = $2`)
}

// Chat is a conversation derived from the message history.
type Chat struct {
	ChatJID       string    `json:"wa_chatid"`
	IsGroup       bool      `json:"wa_isGroup"`
	Name          string    `json:"name,omitempty"`
	LastMessage   string    `json:"wa_lastMessageTextVote,omitempty"`
	LastType      string    `json:"wa_lastMessageType,omitempty"`
	LastFromMe    bool      `json:"wa_lastMessageFromMe"`
	LastTimestamp time.Time `json:"wa_lastMsgTimestamp"`
	Messages      int       `json:"messages"`
	Unread        int       `json:"wa_unreadCount"`
	Labels        []string  `json:"wa_label"`
}

// Chats lists conversations, most recent first. search matches the chat id or contact name.
func (s *Store) Chats(ctx context.Context, instanceID, search string, isGroup *bool, limit, offset int) ([]Chat, int, error) {
	where := []string{"instance_id = $1", "status <> 'deleted'"}
	args := []interface{}{instanceID}
	if search != "" {
		args = append(args, "%"+strings.ToLower(search)+"%")
		n := strconv.Itoa(len(args))
		where = append(where, "(LOWER(chat_jid) LIKE $"+n+" OR LOWER(COALESCE(push_name, '')) LIKE $"+n+")")
	}
	if isGroup != nil {
		args = append(args, *isGroup)
		where = append(where, "is_group = $"+strconv.Itoa(len(args)))
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	clause := strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT chat_jid) FROM messages WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, `
		WITH ranked AS (
			SELECT chat_jid, is_group, text, type, from_me, timestamp, push_name, status,
			       ROW_NUMBER() OVER (PARTITION BY chat_jid ORDER BY timestamp DESC) AS rn,
			       COUNT(*) OVER (PARTITION BY chat_jid) AS total,
			       COUNT(*) FILTER (WHERE NOT from_me AND status = 'received') OVER (PARTITION BY chat_jid) AS unread,
			       MAX(push_name) FILTER (WHERE NOT from_me) OVER (PARTITION BY chat_jid) AS contact_name
			FROM messages WHERE `+clause+`
		)
		SELECT r.chat_jid, r.is_group, COALESCE(r.text, ''), r.type, r.from_me, r.timestamp, r.total, r.unread,
		       COALESCE(r.contact_name, ''),
		       COALESCE((SELECT array_agg(label_id) FROM chat_labels cl WHERE cl.instance_id = $1 AND cl.chat_jid = r.chat_jid), '{}')
		FROM ranked r WHERE r.rn = 1
		ORDER BY r.timestamp DESC
		LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Chat{}
	for rows.Next() {
		var c Chat
		var labels pq.StringArray
		if err := rows.Scan(&c.ChatJID, &c.IsGroup, &c.LastMessage, &c.LastType, &c.LastFromMe, &c.LastTimestamp, &c.Messages,
			&c.Unread, &c.Name, &labels); err != nil {
			return nil, 0, err
		}
		c.Labels = []string(labels)
		if c.Labels == nil {
			c.Labels = []string{}
		}
		out = append(out, c)
	}
	return out, total, rows.Err()
}

type Label struct {
	ID      string `json:"labelid"`
	Name    string `json:"name"`
	Color   int    `json:"color"`
	Deleted bool   `json:"deleted,omitempty"`
}

// HandleAppStateEvent keeps labels and chat-label associations in sync.
func (s *Store) HandleAppStateEvent(instanceID string, evt interface{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	switch v := evt.(type) {
	case *events.LabelEdit:
		if v.Action == nil {
			return
		}
		_, _ = s.db.ExecContext(ctx, `INSERT INTO labels (instance_id, id, name, color, deleted) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (instance_id, id) DO UPDATE SET name = EXCLUDED.name, color = EXCLUDED.color, deleted = EXCLUDED.deleted, updated_at = NOW()`,
			instanceID, v.LabelID, v.Action.GetName(), v.Action.GetColor(), v.Action.GetDeleted())
	case *events.LabelAssociationChat:
		if v.Action == nil {
			return
		}
		if v.Action.GetLabeled() {
			_, _ = s.db.ExecContext(ctx, `INSERT INTO chat_labels (instance_id, chat_jid, label_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
				instanceID, v.JID.ToNonAD().String(), v.LabelID)
		} else {
			_, _ = s.db.ExecContext(ctx, `DELETE FROM chat_labels WHERE instance_id = $1 AND chat_jid = $2 AND label_id = $3`,
				instanceID, v.JID.ToNonAD().String(), v.LabelID)
		}
	}
}

func (s *Store) Labels(ctx context.Context, instanceID string) ([]Label, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, color FROM labels WHERE instance_id = $1 AND NOT deleted ORDER BY id`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Label{}
	for rows.Next() {
		var l Label
		if err := rows.Scan(&l.ID, &l.Name, &l.Color); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) ChatLabels(ctx context.Context, instanceID, chat string) ([]string, error) {
	var labels pq.StringArray
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(array_agg(label_id), '{}') FROM chat_labels WHERE instance_id = $1 AND chat_jid = $2`,
		instanceID, chat).Scan(&labels)
	return []string(labels), err
}

// SetChatLabel records a label change made through the API (the app-state echo confirms it later).
func (s *Store) SetChatLabel(ctx context.Context, instanceID, chat, label string, on bool) {
	if on {
		_, _ = s.db.ExecContext(ctx, `INSERT INTO chat_labels (instance_id, chat_jid, label_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, instanceID, chat, label)
		return
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM chat_labels WHERE instance_id = $1 AND chat_jid = $2 AND label_id = $3`, instanceID, chat, label)
}

func (s *Store) UpsertLabel(ctx context.Context, instanceID string, l Label) {
	_, _ = s.db.ExecContext(ctx, `INSERT INTO labels (instance_id, id, name, color, deleted) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (instance_id, id) DO UPDATE SET name = EXCLUDED.name, color = EXCLUDED.color, deleted = EXCLUDED.deleted, updated_at = NOW()`,
		instanceID, l.ID, l.Name, l.Color, l.Deleted)
}
