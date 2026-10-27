package api

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

// groupListCache avoids hitting WhatsApp on every /group/list call ("force" bypasses it).
type groupListCache struct {
	mu      sync.Mutex
	entries map[string]groupCacheEntry
}

type groupCacheEntry struct {
	at     time.Time
	groups []*types.GroupInfo
}

var groupCache = &groupListCache{entries: map[string]groupCacheEntry{}}

func (c *groupListCache) get(id string) ([]*types.GroupInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[id]
	if !ok || time.Since(e.at) > time.Minute {
		return nil, false
	}
	return e.groups, true
}

func (c *groupListCache) set(id string, groups []*types.GroupInfo) {
	c.mu.Lock()
	c.entries[id] = groupCacheEntry{at: time.Now(), groups: groups}
	c.mu.Unlock()
}

func (c *groupListCache) invalidate(id string) {
	c.mu.Lock()
	delete(c.entries, id)
	c.mu.Unlock()
}

type groupCreateInput struct {
	Name         string   `json:"name"`
	Participants []string `json:"participants"`
}

// UazGroupCreate creates a group.
// @Summary Create group (UAZAPI)
// @Tags uazapi-groups
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body groupCreateInput true "Group"
// @Success 200 {object} map[string]interface{}
// @Router /group/create [post]
func (h *Handler) UazGroupCreate(w http.ResponseWriter, r *http.Request) {
	var in groupCreateInput
	if err := decodeJSON(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	id, client, ok := h.clientFor(w, r, "")
	if !ok {
		return
	}
	participants, err := parseJIDs(in.Participants)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	info, err := client.CreateGroup(r.Context(), whatsmeow.ReqCreateGroup{Name: strings.TrimSpace(in.Name), Participants: participants})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	groupCache.invalidate(id)
	writeJSON(w, http.StatusOK, info)
}

type groupInfoInput struct {
	GroupJID                string `json:"groupjid"`
	GetInviteLink           bool   `json:"getInviteLink"`
	GetRequestsParticipants bool   `json:"getRequestsParticipants"`
}

// UazGroupInfo returns group metadata (optionally with invite link and join requests).
// @Summary Group info (UAZAPI)
// @Tags uazapi-groups
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body groupInfoInput true "Group"
// @Success 200 {object} map[string]interface{}
// @Router /group/info [post]
func (h *Handler) UazGroupInfo(w http.ResponseWriter, r *http.Request) {
	var in groupInfoInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	jid, err := parseGroupJID(in.GroupJID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	_, client, ok := h.clientFor(w, r, "")
	if !ok {
		return
	}
	info, err := client.GetGroupInfo(r.Context(), jid)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	out := map[string]interface{}{"group": info}
	if in.GetInviteLink {
		if link, err := client.GetGroupInviteLink(r.Context(), jid, false); err == nil {
			out["invite_link"] = link
		} else {
			out["invite_link_error"] = err.Error()
		}
	}
	if in.GetRequestsParticipants {
		if reqs, err := client.GetGroupRequestParticipants(r.Context(), jid); err == nil {
			out["requests"] = reqs
		} else {
			out["requests_error"] = err.Error()
		}
	}
	writeJSON(w, http.StatusOK, out)
}

type inviteInput struct {
	InviteCode string `json:"invitecode"`
}

func inviteCode(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// UazGroupInviteInfo resolves an invite code/link without joining.
// @Summary Invite info (UAZAPI)
// @Tags uazapi-groups
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body inviteInput true "Invite code or https://chat.whatsapp.com/... link"
// @Success 200 {object} map[string]interface{}
// @Router /group/inviteInfo [post]
func (h *Handler) UazGroupInviteInfo(w http.ResponseWriter, r *http.Request) {
	var in inviteInput
	if err := decodeJSON(r, &in); err != nil || in.InviteCode == "" {
		writeError(w, http.StatusBadRequest, "invitecode is required")
		return
	}
	_, client, ok := h.clientFor(w, r, "")
	if !ok {
		return
	}
	info, err := client.GetGroupInfoFromLink(r.Context(), inviteCode(in.InviteCode))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// UazGroupJoin joins a group by invite code/link.
// @Summary Join group (UAZAPI)
// @Tags uazapi-groups
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body inviteInput true "Invite"
// @Success 200 {object} map[string]interface{}
// @Router /group/join [post]
func (h *Handler) UazGroupJoin(w http.ResponseWriter, r *http.Request) {
	var in inviteInput
	if err := decodeJSON(r, &in); err != nil || in.InviteCode == "" {
		writeError(w, http.StatusBadRequest, "invitecode is required")
		return
	}
	id, client, ok := h.clientFor(w, r, "")
	if !ok {
		return
	}
	jid, err := client.JoinGroupWithLink(r.Context(), inviteCode(in.InviteCode))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	groupCache.invalidate(id)
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "groupjid": jid.String()})
}

type groupJIDInput struct {
	GroupJID string `json:"groupjid"`
}

// UazGroupLeave leaves a group.
// @Summary Leave group (UAZAPI)
// @Tags uazapi-groups
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body groupJIDInput true "Group"
// @Success 200 {object} map[string]interface{}
// @Router /group/leave [post]
func (h *Handler) UazGroupLeave(w http.ResponseWriter, r *http.Request) {
	h.groupAction(w, r, func(r *http.Request, client *whatsmeow.Client, jid types.JID, _ map[string]interface{}) (interface{}, error) {
		return nil, client.LeaveGroup(r.Context(), jid)
	})
}

// UazGroupResetInvite revokes the invite link and returns a new one.
// @Summary Reset invite link (UAZAPI)
// @Tags uazapi-groups
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body groupJIDInput true "Group"
// @Success 200 {object} map[string]interface{}
// @Router /group/resetInviteCode [post]
func (h *Handler) UazGroupResetInvite(w http.ResponseWriter, r *http.Request) {
	h.groupAction(w, r, func(r *http.Request, client *whatsmeow.Client, jid types.JID, _ map[string]interface{}) (interface{}, error) {
		link, err := client.GetGroupInviteLink(r.Context(), jid, true)
		return map[string]string{"invite_link": link}, err
	})
}

// groupAction decodes {"groupjid": ...} plus any extra fields and runs fn on the group.
func (h *Handler) groupAction(w http.ResponseWriter, r *http.Request, fn func(*http.Request, *whatsmeow.Client, types.JID, map[string]interface{}) (interface{}, error)) {
	var body map[string]interface{}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	raw, _ := body["groupjid"].(string)
	jid, err := parseGroupJID(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, client, ok := h.clientFor(w, r, "")
	if !ok {
		return
	}
	result, err := fn(r, client, jid, body)
	if err != nil {
		if _, bad := err.(badInput); bad {
			writeError(w, http.StatusBadRequest, err.Error())
		} else {
			writeError(w, http.StatusBadGateway, err.Error())
		}
		return
	}
	groupCache.invalidate(id)
	out := map[string]interface{}{"status": "success", "groupjid": jid.String()}
	if result != nil {
		out["result"] = result
	}
	if info, err := client.GetGroupInfo(r.Context(), jid); err == nil {
		out["group"] = info
	}
	writeJSON(w, http.StatusOK, out)
}

type badInput string

func (b badInput) Error() string { return string(b) }

func boolField(body map[string]interface{}, key string) (bool, error) {
	v, ok := body[key].(bool)
	if !ok {
		return false, badInput(key + " (boolean) is required")
	}
	return v, nil
}

func stringField(body map[string]interface{}, key string) (string, error) {
	v, ok := body[key].(string)
	if !ok {
		return "", badInput(key + " (string) is required")
	}
	return v, nil
}

// UazGroupUpdateAnnounce toggles "only admins can send messages".
// @Summary Announce mode (UAZAPI)
// @Tags uazapi-groups
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body map[string]interface{} true "groupjid, announce"
// @Success 200 {object} map[string]interface{}
// @Router /group/updateAnnounce [post]
func (h *Handler) UazGroupUpdateAnnounce(w http.ResponseWriter, r *http.Request) {
	h.groupAction(w, r, func(r *http.Request, c *whatsmeow.Client, jid types.JID, b map[string]interface{}) (interface{}, error) {
		v, err := boolField(b, "announce")
		if err != nil {
			return nil, err
		}
		return nil, c.SetGroupAnnounce(r.Context(), jid, v)
	})
}

// UazGroupUpdateLocked toggles "only admins can edit group info".
// @Summary Locked mode (UAZAPI)
// @Tags uazapi-groups
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body map[string]interface{} true "groupjid, locked"
// @Success 200 {object} map[string]interface{}
// @Router /group/updateLocked [post]
func (h *Handler) UazGroupUpdateLocked(w http.ResponseWriter, r *http.Request) {
	h.groupAction(w, r, func(r *http.Request, c *whatsmeow.Client, jid types.JID, b map[string]interface{}) (interface{}, error) {
		v, err := boolField(b, "locked")
		if err != nil {
			return nil, err
		}
		return nil, c.SetGroupLocked(r.Context(), jid, v)
	})
}

// UazGroupUpdateJoinApproval toggles admin approval for new members.
// @Summary Join approval (UAZAPI)
// @Tags uazapi-groups
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body map[string]interface{} true "groupjid, IsJoinApprovalRequired"
// @Success 200 {object} map[string]interface{}
// @Router /group/updateJoinApproval [post]
func (h *Handler) UazGroupUpdateJoinApproval(w http.ResponseWriter, r *http.Request) {
	h.groupAction(w, r, func(r *http.Request, c *whatsmeow.Client, jid types.JID, b map[string]interface{}) (interface{}, error) {
		v, err := boolField(b, "IsJoinApprovalRequired")
		if err != nil {
			return nil, err
		}
		return nil, c.SetGroupJoinApprovalMode(r.Context(), jid, v)
	})
}

// UazGroupUpdateMemberAddMode sets who can add members (admin_add or all_member_add).
// @Summary Member add mode (UAZAPI)
// @Tags uazapi-groups
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body map[string]interface{} true "groupjid, MemberAddMode"
// @Success 200 {object} map[string]interface{}
// @Router /group/updateMemberAddMode [post]
func (h *Handler) UazGroupUpdateMemberAddMode(w http.ResponseWriter, r *http.Request) {
	h.groupAction(w, r, func(r *http.Request, c *whatsmeow.Client, jid types.JID, b map[string]interface{}) (interface{}, error) {
		v, err := stringField(b, "MemberAddMode")
		if err != nil {
			return nil, err
		}
		mode := types.GroupMemberAddMode(v)
		if mode != types.GroupMemberAddModeAdmin && mode != types.GroupMemberAddModeAllMember {
			return nil, badInput("MemberAddMode must be admin_add or all_member_add")
		}
		return nil, c.SetGroupMemberAddMode(r.Context(), jid, mode)
	})
}

// UazGroupUpdateName renames a group.
// @Summary Group name (UAZAPI)
// @Tags uazapi-groups
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body map[string]interface{} true "groupjid, name"
// @Success 200 {object} map[string]interface{}
// @Router /group/updateName [post]
func (h *Handler) UazGroupUpdateName(w http.ResponseWriter, r *http.Request) {
	h.groupAction(w, r, func(r *http.Request, c *whatsmeow.Client, jid types.JID, b map[string]interface{}) (interface{}, error) {
		v, err := stringField(b, "name")
		if err != nil || strings.TrimSpace(v) == "" {
			return nil, badInput("name is required")
		}
		return nil, c.SetGroupName(r.Context(), jid, strings.TrimSpace(v))
	})
}

// UazGroupUpdateDescription changes the group description.
// @Summary Group description (UAZAPI)
// @Tags uazapi-groups
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body map[string]interface{} true "groupjid, description"
// @Success 200 {object} map[string]interface{}
// @Router /group/updateDescription [post]
func (h *Handler) UazGroupUpdateDescription(w http.ResponseWriter, r *http.Request) {
	h.groupAction(w, r, func(r *http.Request, c *whatsmeow.Client, jid types.JID, b map[string]interface{}) (interface{}, error) {
		v, err := stringField(b, "description")
		if err != nil {
			return nil, err
		}
		return nil, c.SetGroupDescription(r.Context(), jid, v)
	})
}

// UazGroupUpdateImage changes the group picture ("remove" deletes it). JPEG is required.
// @Summary Group picture (UAZAPI)
// @Tags uazapi-groups
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body map[string]interface{} true "groupjid, image (URL, base64 or remove)"
// @Success 200 {object} map[string]interface{}
// @Router /group/updateImage [post]
func (h *Handler) UazGroupUpdateImage(w http.ResponseWriter, r *http.Request) {
	h.groupAction(w, r, func(r *http.Request, c *whatsmeow.Client, jid types.JID, b map[string]interface{}) (interface{}, error) {
		v, err := stringField(b, "image")
		if err != nil {
			return nil, err
		}
		var data []byte
		if !strings.EqualFold(v, "remove") && !strings.EqualFold(v, "delete") {
			if data, err = h.loadImage(r.Context(), v); err != nil {
				return nil, badInput(err.Error())
			}
		}
		pictureID, err := c.SetGroupPhoto(r.Context(), jid, data)
		return map[string]string{"picture_id": pictureID}, err
	})
}

// UazGroupEphemeral sets disappearing messages (0/off, 1d, 7d, 90d).
// @Summary Group disappearing messages (UAZAPI)
// @Tags uazapi-groups
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body map[string]interface{} true "groupjid, duration"
// @Success 200 {object} map[string]interface{}
// @Router /group/ephemeral [post]
func (h *Handler) UazGroupEphemeral(w http.ResponseWriter, r *http.Request) {
	h.groupAction(w, r, func(r *http.Request, c *whatsmeow.Client, jid types.JID, b map[string]interface{}) (interface{}, error) {
		d, err := parseEphemeral(b["duration"])
		if err != nil {
			return nil, badInput(err.Error())
		}
		return nil, c.SetDisappearingTimer(r.Context(), jid, d, time.Now())
	})
}

// UazGroupUpdateParticipants adds, removes, promotes, demotes, approves or rejects members.
// @Summary Manage participants (UAZAPI)
// @Tags uazapi-groups
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body map[string]interface{} true "groupjid, action, participants"
// @Success 200 {object} map[string]interface{}
// @Router /group/updateParticipants [post]
func (h *Handler) UazGroupUpdateParticipants(w http.ResponseWriter, r *http.Request) {
	h.groupAction(w, r, func(r *http.Request, c *whatsmeow.Client, jid types.JID, b map[string]interface{}) (interface{}, error) {
		action, err := stringField(b, "action")
		if err != nil {
			return nil, err
		}
		rawList, _ := b["participants"].([]interface{})
		var list []string
		for _, p := range rawList {
			if s, ok := p.(string); ok {
				list = append(list, s)
			}
		}
		if len(list) == 0 {
			return nil, badInput("participants is required")
		}
		jids, err := parseJIDs(list)
		if err != nil {
			return nil, badInput(err.Error())
		}
		switch strings.ToLower(action) {
		case "add", "remove", "promote", "demote":
			return c.UpdateGroupParticipants(r.Context(), jid, jids, whatsmeow.ParticipantChange(strings.ToLower(action)))
		case "approve", "reject":
			return c.UpdateGroupRequestParticipants(r.Context(), jid, jids, whatsmeow.ParticipantRequestChange(strings.ToLower(action)))
		}
		return nil, badInput("action must be add, remove, promote, demote, approve or reject")
	})
}

type groupListInput struct {
	Limit          int    `json:"limit"`
	Offset         int    `json:"offset"`
	Search         string `json:"search"`
	Force          bool   `json:"force"`
	NoParticipants bool   `json:"noParticipants"`
}

// UazGroupList lists joined groups (GET with ?force&noparticipants, or POST with paging/search).
// @Summary List groups (UAZAPI)
// @Tags uazapi-groups
// @Produce json
// @Param token header string true "Instance Token"
// @Param force query bool false "Bypass the 60s cache"
// @Param noparticipants query bool false "Omit participant lists"
// @Success 200 {object} map[string]interface{}
// @Router /group/list [get]
func (h *Handler) UazGroupList(w http.ResponseWriter, r *http.Request) {
	in := groupListInput{Limit: 1000}
	if r.Method == http.MethodPost {
		if r.ContentLength != 0 {
			if err := decodeJSON(r, &in); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body")
				return
			}
		}
	} else {
		q := r.URL.Query()
		in.Force, _ = strconv.ParseBool(q.Get("force"))
		in.NoParticipants, _ = strconv.ParseBool(q.Get("noparticipants"))
	}
	id, client, ok := h.clientFor(w, r, "")
	if !ok {
		return
	}
	groups, cached := groupCache.get(id)
	if !cached || in.Force {
		var err error
		if groups, err = client.GetJoinedGroups(r.Context()); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		groupCache.set(id, groups)
	}
	out := make([]types.GroupInfo, 0, len(groups))
	search := strings.ToLower(strings.TrimSpace(in.Search))
	for _, g := range groups {
		if search != "" && !strings.Contains(strings.ToLower(g.Name), search) && !strings.Contains(g.JID.String(), search) {
			continue
		}
		cp := *g
		if in.NoParticipants {
			cp.Participants = nil
		}
		out = append(out, cp)
	}
	total := len(out)
	if in.Offset > 0 && in.Offset < len(out) {
		out = out[in.Offset:]
	} else if in.Offset >= len(out) && in.Offset > 0 {
		out = out[:0]
	}
	if in.Limit > 0 && in.Limit < len(out) {
		out = out[:in.Limit]
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"groups": out, "total": total})
}

// UazCommunityCreate creates a community.
// @Summary Create community (UAZAPI)
// @Tags uazapi-groups
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body nameInput true "Community name"
// @Success 200 {object} map[string]interface{}
// @Router /community/create [post]
func (h *Handler) UazCommunityCreate(w http.ResponseWriter, r *http.Request) {
	var in nameInput
	if err := decodeJSON(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	id, client, ok := h.clientFor(w, r, "")
	if !ok {
		return
	}
	info, err := client.CreateGroup(r.Context(), whatsmeow.ReqCreateGroup{
		Name: strings.TrimSpace(in.Name), GroupParent: types.GroupParent{IsParent: true},
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	groupCache.invalidate(id)
	writeJSON(w, http.StatusOK, info)
}

type communityEditInput struct {
	Community string   `json:"community"`
	Action    string   `json:"action"`
	GroupJIDs []string `json:"groupjids"`
}

// UazCommunityEditGroups links or unlinks groups to a community.
// @Summary Community groups (UAZAPI)
// @Tags uazapi-groups
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body communityEditInput true "community, action (add/remove), groupjids"
// @Success 200 {object} map[string]interface{}
// @Router /community/editgroups [post]
func (h *Handler) UazCommunityEditGroups(w http.ResponseWriter, r *http.Request) {
	var in communityEditInput
	if err := decodeJSON(r, &in); err != nil || len(in.GroupJIDs) == 0 {
		writeError(w, http.StatusBadRequest, "community, action and groupjids are required")
		return
	}
	parent, err := parseGroupJID(in.Community)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, client, ok := h.clientFor(w, r, "")
	if !ok {
		return
	}
	results := map[string]string{}
	for _, g := range in.GroupJIDs {
		child, err := parseGroupJID(g)
		if err != nil {
			results[g] = err.Error()
			continue
		}
		switch strings.ToLower(in.Action) {
		case "add":
			err = client.LinkGroup(r.Context(), parent, child)
		case "remove":
			err = client.UnlinkGroup(r.Context(), parent, child)
		default:
			writeError(w, http.StatusBadRequest, "action must be add or remove")
			return
		}
		if err != nil {
			results[g] = err.Error()
		} else {
			results[g] = "success"
		}
	}
	groupCache.invalidate(id)
	writeJSON(w, http.StatusOK, map[string]interface{}{"community": parent.String(), "results": results})
}
