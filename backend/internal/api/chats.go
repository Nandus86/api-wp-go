package api

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/user/whatsmeow-basileia/internal/service/messages"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// chatTarget decodes {"number": ...} plus extra fields and resolves the chat JID.
func (h *Handler) chatTarget(w http.ResponseWriter, r *http.Request, body interface{}, number func() string) (string, *whatsmeow.Client, types.JID, bool) {
	if err := decodeJSON(r, body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return "", nil, types.EmptyJID, false
	}
	jid, err := parseJID(number())
	if err != nil {
		writeError(w, http.StatusBadRequest, "number: "+err.Error())
		return "", nil, types.EmptyJID, false
	}
	id, client, ok := h.clientFor(w, r, "")
	return id, client, jid, ok
}

// lastMessageKey returns the newest stored message of a chat, needed by some app-state patches.
func (h *Handler) lastMessageKey(ctx context.Context, instanceID string, client *whatsmeow.Client, chat types.JID) (time.Time, *waCommon.MessageKey) {
	m, err := h.history.LastInChat(ctx, instanceID, chat.String())
	if err != nil {
		return time.Now(), nil
	}
	key := &waCommon.MessageKey{
		RemoteJID: proto.String(chat.String()),
		FromMe:    proto.Bool(m.FromMe),
		ID:        proto.String(m.ID),
	}
	if chat.Server == types.GroupServer && !m.FromMe && m.SenderJID != "" {
		key.Participant = proto.String(m.SenderJID)
	}
	return m.Timestamp, key
}

type chatBlockInput struct {
	Number string `json:"number"`
	Block  bool   `json:"block"`
}

// UazChatBlock blocks or unblocks a contact.
// @Summary Block contact (UAZAPI)
// @Tags uazapi-chats
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body chatBlockInput true "number, block"
// @Success 200 {object} types.Blocklist
// @Router /chat/block [post]
func (h *Handler) UazChatBlock(w http.ResponseWriter, r *http.Request) {
	var in chatBlockInput
	_, client, jid, ok := h.chatTarget(w, r, &in, func() string { return in.Number })
	if !ok {
		return
	}
	action := events.BlocklistChangeActionUnblock
	if in.Block {
		action = events.BlocklistChangeActionBlock
	}
	list, err := client.UpdateBlocklist(r.Context(), jid, action)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// UazChatBlocklist lists blocked contacts.
// @Summary Blocklist (UAZAPI)
// @Tags uazapi-chats
// @Produce json
// @Param token header string true "Instance Token"
// @Success 200 {object} types.Blocklist
// @Router /chat/blocklist [get]
func (h *Handler) UazChatBlocklist(w http.ResponseWriter, r *http.Request) {
	_, client, ok := h.clientFor(w, r, "")
	if !ok {
		return
	}
	list, err := client.GetBlocklist(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type chatToggleInput struct {
	Number  string `json:"number"`
	Archive bool   `json:"archive"`
	Read    bool   `json:"read"`
	Pin     bool   `json:"pin"`
}

func (h *Handler) sendPatch(w http.ResponseWriter, r *http.Request, client *whatsmeow.Client, patch appstate.PatchInfo, result map[string]interface{}) {
	if err := client.SendAppState(r.Context(), patch); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	result["status"] = "success"
	writeJSON(w, http.StatusOK, result)
}

// UazChatArchive archives or unarchives a chat.
// @Summary Archive chat (UAZAPI)
// @Tags uazapi-chats
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body chatToggleInput true "number, archive"
// @Success 200 {object} map[string]interface{}
// @Router /chat/archive [post]
func (h *Handler) UazChatArchive(w http.ResponseWriter, r *http.Request) {
	var in chatToggleInput
	id, client, jid, ok := h.chatTarget(w, r, &in, func() string { return in.Number })
	if !ok {
		return
	}
	ts, key := h.lastMessageKey(r.Context(), id, client, jid)
	h.sendPatch(w, r, client, appstate.BuildArchive(jid, in.Archive, ts, key), map[string]interface{}{"chat": jid.String(), "archived": in.Archive})
}

// UazChatRead marks a whole chat as read or unread.
// @Summary Mark chat read/unread (UAZAPI)
// @Tags uazapi-chats
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body chatToggleInput true "number, read"
// @Success 200 {object} map[string]interface{}
// @Router /chat/read [post]
func (h *Handler) UazChatRead(w http.ResponseWriter, r *http.Request) {
	var in chatToggleInput
	id, client, jid, ok := h.chatTarget(w, r, &in, func() string { return in.Number })
	if !ok {
		return
	}
	ts, key := h.lastMessageKey(r.Context(), id, client, jid)
	h.sendPatch(w, r, client, appstate.BuildMarkChatAsRead(jid, in.Read, ts, key), map[string]interface{}{"chat": jid.String(), "read": in.Read})
}

// UazChatPin pins or unpins a chat.
// @Summary Pin chat (UAZAPI)
// @Tags uazapi-chats
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body chatToggleInput true "number, pin"
// @Success 200 {object} map[string]interface{}
// @Router /chat/pin [post]
func (h *Handler) UazChatPin(w http.ResponseWriter, r *http.Request) {
	var in chatToggleInput
	_, client, jid, ok := h.chatTarget(w, r, &in, func() string { return in.Number })
	if !ok {
		return
	}
	h.sendPatch(w, r, client, appstate.BuildPin(jid, in.Pin), map[string]interface{}{"chat": jid.String(), "pinned": in.Pin})
}

type chatMuteInput struct {
	Number      string `json:"number"`
	MuteEndTime int    `json:"muteEndTime"`
}

// UazChatMute mutes a chat: 0 unmute, 8 hours, 168 one week, -1 forever (any other value = hours).
// @Summary Mute chat (UAZAPI)
// @Tags uazapi-chats
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body chatMuteInput true "number, muteEndTime"
// @Success 200 {object} map[string]interface{}
// @Router /chat/mute [post]
func (h *Handler) UazChatMute(w http.ResponseWriter, r *http.Request) {
	var in chatMuteInput
	_, client, jid, ok := h.chatTarget(w, r, &in, func() string { return in.Number })
	if !ok {
		return
	}
	var patch appstate.PatchInfo
	switch {
	case in.MuteEndTime == 0:
		patch = appstate.BuildMute(jid, false, 0)
	case in.MuteEndTime < 0:
		patch = appstate.BuildMute(jid, true, 0) // forever
	default:
		patch = appstate.BuildMute(jid, true, time.Duration(in.MuteEndTime)*time.Hour)
	}
	h.sendPatch(w, r, client, patch, map[string]interface{}{"chat": jid.String(), "muteEndTime": in.MuteEndTime})
}

type chatEphemeralInput struct {
	Number   string      `json:"number"`
	Duration interface{} `json:"duration"`
}

// UazChatEphemeral sets disappearing messages for a chat (0/off, 1d, 7d, 90d).
// @Summary Chat disappearing messages (UAZAPI)
// @Tags uazapi-chats
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body chatEphemeralInput true "number, duration"
// @Success 200 {object} map[string]interface{}
// @Router /chat/ephemeral [post]
func (h *Handler) UazChatEphemeral(w http.ResponseWriter, r *http.Request) {
	var in chatEphemeralInput
	_, client, jid, ok := h.chatTarget(w, r, &in, func() string { return in.Number })
	if !ok {
		return
	}
	d, err := parseEphemeral(in.Duration)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := client.SetDisappearingTimer(r.Context(), jid, d, time.Now()); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "chat": jid.String(), "seconds": int(d.Seconds())})
}

type chatCheckInput struct {
	Numbers []string `json:"numbers"`
}

// UazChatCheck checks which numbers have WhatsApp (and returns the canonical JID).
// @Summary Check numbers on WhatsApp (UAZAPI)
// @Tags uazapi-chats
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body chatCheckInput true "numbers"
// @Success 200 {array} map[string]interface{}
// @Router /chat/check [post]
func (h *Handler) UazChatCheck(w http.ResponseWriter, r *http.Request) {
	var in chatCheckInput
	if err := decodeJSON(r, &in); err != nil || len(in.Numbers) == 0 || len(in.Numbers) > 500 {
		writeError(w, http.StatusBadRequest, "numbers (1-500) is required")
		return
	}
	_, client, ok := h.clientFor(w, r, "")
	if !ok {
		return
	}
	phones := make([]string, 0, len(in.Numbers))
	for _, n := range in.Numbers {
		jid, err := parseJID(n)
		if err != nil || jid.Server != types.DefaultUserServer {
			continue
		}
		phones = append(phones, "+"+jid.User)
	}
	resp, err := client.IsOnWhatsApp(r.Context(), phones)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	out := make([]map[string]interface{}, 0, len(resp))
	for _, x := range resp {
		item := map[string]interface{}{"query": x.Query, "isInWhatsapp": x.IsIn, "jid": x.JID.String()}
		if x.VerifiedName != nil && x.VerifiedName.Details != nil {
			item["verifiedName"] = x.VerifiedName.Details.GetVerifiedName()
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, out)
}

type chatDetailsInput struct {
	Number  string `json:"number"`
	Preview bool   `json:"preview"`
}

// UazChatDetails returns contact info, status, profile picture and labels of a chat.
// @Summary Chat details (UAZAPI)
// @Tags uazapi-chats
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body chatDetailsInput true "number"
// @Success 200 {object} map[string]interface{}
// @Router /chat/details [post]
func (h *Handler) UazChatDetails(w http.ResponseWriter, r *http.Request) {
	var in chatDetailsInput
	id, client, jid, ok := h.chatTarget(w, r, &in, func() string { return in.Number })
	if !ok {
		return
	}
	out := map[string]interface{}{"wa_chatid": jid.String(), "wa_isGroup": jid.Server == types.GroupServer}
	if jid.Server == types.DefaultUserServer {
		if info, err := client.GetUserInfo(r.Context(), []types.JID{jid}); err == nil {
			if u, ok := info[jid]; ok {
				out["status"] = u.Status
				out["verifiedName"] = u.VerifiedName
				out["devices"] = len(u.Devices)
				if !u.LID.IsEmpty() {
					out["lid"] = u.LID.String()
				}
			}
		}
		if c, err := client.Store.Contacts.GetContact(r.Context(), jid); err == nil && c.Found {
			out["wa_contactName"] = c.FullName
			out["wa_name"] = c.PushName
			out["businessName"] = c.BusinessName
		}
	} else if jid.Server == types.GroupServer {
		if info, err := client.GetGroupInfo(r.Context(), jid); err == nil {
			out["wa_name"] = info.Name
			out["group"] = info
		}
	}
	if pic, err := client.GetProfilePictureInfo(r.Context(), jid, &whatsmeow.GetProfilePictureParams{Preview: in.Preview}); err == nil && pic != nil {
		out["image"] = pic.URL
		out["imageId"] = pic.ID
	}
	if labels, err := h.history.ChatLabels(r.Context(), id, jid.String()); err == nil {
		out["wa_label"] = labels
	}
	writeJSON(w, http.StatusOK, out)
}

type chatDeleteInput struct {
	Number             string `json:"number"`
	DeleteChatDB       bool   `json:"deleteChatDB"`
	DeleteMessagesDB   bool   `json:"deleteMessagesDB"`
	DeleteChatWhatsApp bool   `json:"deleteChatWhatsApp"`
	ClearChatWhatsApp  bool   `json:"clearChatWhatsApp"`
}

// UazChatDelete deletes a chat from the local history and/or from WhatsApp.
// @Summary Delete chat (UAZAPI)
// @Tags uazapi-chats
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body chatDeleteInput true "number and what to delete"
// @Success 200 {object} map[string]interface{}
// @Router /chat/delete [post]
func (h *Handler) UazChatDelete(w http.ResponseWriter, r *http.Request) {
	var in chatDeleteInput
	id, client, jid, ok := h.chatTarget(w, r, &in, func() string { return in.Number })
	if !ok {
		return
	}
	out := map[string]interface{}{"chat": jid.String()}
	if in.DeleteChatWhatsApp || in.ClearChatWhatsApp {
		ts, key := h.lastMessageKey(r.Context(), id, client, jid)
		if err := client.SendAppState(r.Context(), appstate.BuildDeleteChat(jid, ts, key, true)); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		out["whatsapp"] = "deleted"
	}
	if in.DeleteChatDB || in.DeleteMessagesDB {
		n, err := h.history.DeleteChat(r.Context(), id, jid.String())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out["deleted_messages"] = n
	}
	out["status"] = "success"
	writeJSON(w, http.StatusOK, out)
}

type chatFindInput struct {
	Search    string `json:"wa_contactName"`
	ChatID    string `json:"wa_chatid"`
	IsGroup   *bool  `json:"wa_isGroup"`
	Limit     int    `json:"limit"`
	Offset    int    `json:"offset"`
	Operator  string `json:"operator"`
	Sort      string `json:"sort"`
	LeadField string `json:"name"`
}

// UazChatFind lists conversations from the message history (most recent first).
// @Summary Find chats (UAZAPI)
// @Description Supported filters: wa_chatid, wa_contactName/name (substring), wa_isGroup, limit, offset.
// @Tags uazapi-chats
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body chatFindInput false "Filters"
// @Success 200 {object} map[string]interface{}
// @Router /chat/find [post]
func (h *Handler) UazChatFind(w http.ResponseWriter, r *http.Request) {
	var in chatFindInput
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	id, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}
	search := in.Search
	if search == "" {
		search = in.LeadField
	}
	if in.ChatID != "" {
		search = in.ChatID
	}
	chats, total, err := h.history.Chats(r.Context(), id, search, in.IsGroup, in.Limit, in.Offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"chats": chats, "total": total})
}

type chatLabelsInput struct {
	Number        string   `json:"number"`
	LabelIDs      []string `json:"labelids"`
	AddLabelID    string   `json:"add_labelid"`
	RemoveLabelID string   `json:"remove_labelid"`
}

// UazChatLabels sets, adds or removes WhatsApp Business labels of a chat.
// @Summary Chat labels (UAZAPI)
// @Tags uazapi-chats
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body chatLabelsInput true "number and labelids / add_labelid / remove_labelid"
// @Success 200 {object} map[string]interface{}
// @Router /chat/labels [post]
func (h *Handler) UazChatLabels(w http.ResponseWriter, r *http.Request) {
	var in chatLabelsInput
	id, client, jid, ok := h.chatTarget(w, r, &in, func() string { return in.Number })
	if !ok {
		return
	}
	chat := jid.String()
	add := map[string]bool{}
	remove := map[string]bool{}
	if in.LabelIDs != nil {
		current, _ := h.history.ChatLabels(r.Context(), id, chat)
		want := map[string]bool{}
		for _, l := range in.LabelIDs {
			want[l] = true
			add[l] = true
		}
		for _, l := range current {
			if !want[l] {
				remove[l] = true
			}
			delete(add, l)
		}
	}
	if in.AddLabelID != "" {
		add[in.AddLabelID] = true
	}
	if in.RemoveLabelID != "" {
		remove[in.RemoveLabelID] = true
	}
	for l := range add {
		if err := client.SendAppState(r.Context(), appstate.BuildLabelChat(jid, l, true)); err != nil {
			writeError(w, http.StatusBadGateway, "add label "+l+": "+err.Error())
			return
		}
		h.history.SetChatLabel(r.Context(), id, chat, l, true)
	}
	for l := range remove {
		if err := client.SendAppState(r.Context(), appstate.BuildLabelChat(jid, l, false)); err != nil {
			writeError(w, http.StatusBadGateway, "remove label "+l+": "+err.Error())
			return
		}
		h.history.SetChatLabel(r.Context(), id, chat, l, false)
	}
	labels, _ := h.history.ChatLabels(r.Context(), id, chat)
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "chat": chat, "labels": labels})
}

type labelEditInput struct {
	LabelID string `json:"labelid"`
	Name    string `json:"name"`
	Color   int    `json:"color"`
	Delete  bool   `json:"delete"`
}

// UazLabelEdit creates, edits or deletes a label (WhatsApp Business).
// @Summary Edit label (UAZAPI)
// @Tags uazapi-chats
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body labelEditInput true "Label"
// @Success 200 {object} map[string]interface{}
// @Router /label/edit [post]
func (h *Handler) UazLabelEdit(w http.ResponseWriter, r *http.Request) {
	var in labelEditInput
	if err := decodeJSON(r, &in); err != nil || in.LabelID == "" {
		writeError(w, http.StatusBadRequest, "labelid is required")
		return
	}
	id, client, ok := h.clientFor(w, r, "")
	if !ok {
		return
	}
	if err := client.SendAppState(r.Context(), appstate.BuildLabelEdit(in.LabelID, in.Name, int32(in.Color), in.Delete)); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	h.history.UpsertLabel(r.Context(), id, messages.Label{ID: in.LabelID, Name: in.Name, Color: in.Color, Deleted: in.Delete})
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "labelid": in.LabelID})
}

// UazLabels lists labels synced from WhatsApp.
// @Summary List labels (UAZAPI)
// @Tags uazapi-chats
// @Produce json
// @Param token header string true "Instance Token"
// @Success 200 {array} messages.Label
// @Router /labels [get]
func (h *Handler) UazLabels(w http.ResponseWriter, r *http.Request) {
	id, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}
	labels, err := h.history.Labels(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, labels)
}

type contactOut struct {
	JID          string `json:"jid"`
	Name         string `json:"contactName,omitempty"`
	FirstName    string `json:"contact_FirstName,omitempty"`
	PushName     string `json:"pushName,omitempty"`
	BusinessName string `json:"businessName,omitempty"`
}

func (h *Handler) contacts(w http.ResponseWriter, r *http.Request) ([]contactOut, bool) {
	_, client, ok := h.clientFor(w, r, "")
	if !ok {
		return nil, false
	}
	all, err := client.Store.Contacts.GetAllContacts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil, false
	}
	out := make([]contactOut, 0, len(all))
	for jid, c := range all {
		out = append(out, contactOut{JID: jid.String(), Name: c.FullName, FirstName: c.FirstName, PushName: c.PushName, BusinessName: c.BusinessName})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name+out[i].PushName) < strings.ToLower(out[j].Name+out[j].PushName)
	})
	return out, true
}

// UazContacts lists the address book synced from the phone.
// @Summary Contacts (UAZAPI)
// @Tags uazapi-chats
// @Produce json
// @Param token header string true "Instance Token"
// @Success 200 {array} contactOut
// @Router /contacts [get]
func (h *Handler) UazContacts(w http.ResponseWriter, r *http.Request) {
	if list, ok := h.contacts(w, r); ok {
		writeJSON(w, http.StatusOK, list)
	}
}

type contactsListInput struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// UazContactsList lists contacts with paging.
// @Summary Contacts paged (UAZAPI)
// @Tags uazapi-chats
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body contactsListInput false "Paging"
// @Success 200 {object} map[string]interface{}
// @Router /contacts/list [post]
func (h *Handler) UazContactsList(w http.ResponseWriter, r *http.Request) {
	var in contactsListInput
	if r.ContentLength != 0 {
		_ = decodeJSON(r, &in)
	}
	list, ok := h.contacts(w, r)
	if !ok {
		return
	}
	total := len(list)
	if in.Offset > 0 {
		if in.Offset >= len(list) {
			list = list[:0]
		} else {
			list = list[in.Offset:]
		}
	}
	if in.Limit > 0 && in.Limit < len(list) {
		list = list[:in.Limit]
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"contacts": list, "total": total})
}
