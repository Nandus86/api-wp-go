package api

import (
	"net/http"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

type newsletterInput struct {
	ID                string `json:"id"`
	JID               string `json:"jid"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	Picture           string `json:"picture"`
	Key               string `json:"key"`
	Count             int    `json:"count"`
	BeforeID          int    `json:"beforeid"`
	AfterID           int    `json:"afterid"`
	Since             int64  `json:"since"`
	ServerIDs         []int  `json:"serverids"`
	ServerID          int    `json:"serverid"`
	Reaction          string `json:"reaction"`
	ReactionMessageID string `json:"reactionmessageid"`
}

// newsletterCall decodes the body, resolves the channel JID and runs fn.
func (h *Handler) newsletterCall(w http.ResponseWriter, r *http.Request, needJID bool, fn func(*http.Request, *whatsmeow.Client, types.JID, newsletterInput) (interface{}, error)) {
	var in newsletterInput
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	var jid types.JID
	if needJID {
		var err error
		if jid, err = parseNewsletterJID(in.ID, in.JID); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	_, client, ok := h.clientFor(w, r, "")
	if !ok {
		return
	}
	out, err := fn(r, client, jid, in)
	if err != nil {
		if _, bad := err.(badInput); bad {
			writeError(w, http.StatusBadRequest, err.Error())
		} else {
			writeError(w, http.StatusBadGateway, err.Error())
		}
		return
	}
	if out == nil {
		out = map[string]interface{}{"status": "success", "jid": jid.String()}
	}
	writeJSON(w, http.StatusOK, out)
}

// UazNewsletterCreate creates a channel.
// @Summary Create channel (UAZAPI)
// @Tags uazapi-newsletter
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body newsletterInput true "name, description, picture"
// @Success 200 {object} map[string]interface{}
// @Router /newsletter/create [post]
func (h *Handler) UazNewsletterCreate(w http.ResponseWriter, r *http.Request) {
	h.newsletterCall(w, r, false, func(r *http.Request, c *whatsmeow.Client, _ types.JID, in newsletterInput) (interface{}, error) {
		if strings.TrimSpace(in.Name) == "" {
			return nil, badInput("name is required")
		}
		params := whatsmeow.CreateNewsletterParams{Name: in.Name, Description: in.Description}
		if in.Picture != "" {
			pic, err := h.loadImage(r.Context(), in.Picture)
			if err != nil {
				return nil, badInput(err.Error())
			}
			params.Picture = pic
		}
		return c.CreateNewsletter(r.Context(), params)
	})
}

// UazNewsletterList lists subscribed channels.
// @Summary List channels (UAZAPI)
// @Tags uazapi-newsletter
// @Produce json
// @Param token header string true "Instance Token"
// @Success 200 {array} map[string]interface{}
// @Router /newsletter/list [get]
func (h *Handler) UazNewsletterList(w http.ResponseWriter, r *http.Request) {
	h.newsletterCall(w, r, false, func(r *http.Request, c *whatsmeow.Client, _ types.JID, _ newsletterInput) (interface{}, error) {
		return c.GetSubscribedNewsletters(r.Context())
	})
}

// UazNewsletterInfo returns channel metadata.
// @Summary Channel info (UAZAPI)
// @Tags uazapi-newsletter
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body newsletterInput true "id or jid"
// @Success 200 {object} map[string]interface{}
// @Router /newsletter/info [post]
func (h *Handler) UazNewsletterInfo(w http.ResponseWriter, r *http.Request) {
	h.newsletterCall(w, r, true, func(r *http.Request, c *whatsmeow.Client, jid types.JID, _ newsletterInput) (interface{}, error) {
		return c.GetNewsletterInfo(r.Context(), jid)
	})
}

// UazNewsletterLink resolves a channel invite key/link.
// @Summary Channel by invite (UAZAPI)
// @Tags uazapi-newsletter
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body newsletterInput true "key"
// @Success 200 {object} map[string]interface{}
// @Router /newsletter/link [post]
func (h *Handler) UazNewsletterLink(w http.ResponseWriter, r *http.Request) {
	h.newsletterCall(w, r, false, func(r *http.Request, c *whatsmeow.Client, _ types.JID, in newsletterInput) (interface{}, error) {
		key := inviteCode(in.Key)
		if key == "" {
			return nil, badInput("key is required")
		}
		return c.GetNewsletterInfoWithInvite(r.Context(), key)
	})
}

// UazNewsletterFollow follows a channel.
// @Summary Follow channel (UAZAPI)
// @Tags uazapi-newsletter
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body newsletterInput true "id or jid"
// @Success 200 {object} map[string]interface{}
// @Router /newsletter/follow [post]
func (h *Handler) UazNewsletterFollow(w http.ResponseWriter, r *http.Request) {
	h.newsletterCall(w, r, true, func(r *http.Request, c *whatsmeow.Client, jid types.JID, _ newsletterInput) (interface{}, error) {
		return nil, c.FollowNewsletter(r.Context(), jid)
	})
}

// UazNewsletterUnfollow unfollows a channel.
// @Summary Unfollow channel (UAZAPI)
// @Tags uazapi-newsletter
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body newsletterInput true "id or jid"
// @Success 200 {object} map[string]interface{}
// @Router /newsletter/unfollow [post]
func (h *Handler) UazNewsletterUnfollow(w http.ResponseWriter, r *http.Request) {
	h.newsletterCall(w, r, true, func(r *http.Request, c *whatsmeow.Client, jid types.JID, _ newsletterInput) (interface{}, error) {
		return nil, c.UnfollowNewsletter(r.Context(), jid)
	})
}

// UazNewsletterSubscribe subscribes to live updates (reactions/views) of a channel.
// @Summary Subscribe to channel updates (UAZAPI)
// @Tags uazapi-newsletter
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body newsletterInput true "id or jid"
// @Success 200 {object} map[string]interface{}
// @Router /newsletter/subscribe [post]
func (h *Handler) UazNewsletterSubscribe(w http.ResponseWriter, r *http.Request) {
	h.newsletterCall(w, r, true, func(r *http.Request, c *whatsmeow.Client, jid types.JID, _ newsletterInput) (interface{}, error) {
		d, err := c.NewsletterSubscribeLiveUpdates(r.Context(), jid)
		return map[string]interface{}{"status": "success", "jid": jid.String(), "duration_seconds": int(d.Seconds())}, err
	})
}

func (h *Handler) newsletterMute(w http.ResponseWriter, r *http.Request, mute bool) {
	h.newsletterCall(w, r, true, func(r *http.Request, c *whatsmeow.Client, jid types.JID, _ newsletterInput) (interface{}, error) {
		return map[string]interface{}{"status": "success", "jid": jid.String(), "muted": mute}, c.NewsletterToggleMute(r.Context(), jid, mute)
	})
}

// UazNewsletterMute mutes a channel.
// @Summary Mute channel (UAZAPI)
// @Tags uazapi-newsletter
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body newsletterInput true "id or jid"
// @Success 200 {object} map[string]interface{}
// @Router /newsletter/mute [post]
func (h *Handler) UazNewsletterMute(w http.ResponseWriter, r *http.Request) {
	h.newsletterMute(w, r, true)
}

// UazNewsletterUnmute unmutes a channel.
// @Summary Unmute channel (UAZAPI)
// @Tags uazapi-newsletter
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body newsletterInput true "id or jid"
// @Success 200 {object} map[string]interface{}
// @Router /newsletter/unmute [post]
func (h *Handler) UazNewsletterUnmute(w http.ResponseWriter, r *http.Request) {
	h.newsletterMute(w, r, false)
}

// UazNewsletterMessages lists channel messages.
// @Summary Channel messages (UAZAPI)
// @Tags uazapi-newsletter
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body newsletterInput true "id or jid, count, beforeid"
// @Success 200 {array} map[string]interface{}
// @Router /newsletter/messages [post]
func (h *Handler) UazNewsletterMessages(w http.ResponseWriter, r *http.Request) {
	h.newsletterCall(w, r, true, func(r *http.Request, c *whatsmeow.Client, jid types.JID, in newsletterInput) (interface{}, error) {
		if in.Count <= 0 {
			in.Count = 20
		}
		return c.GetNewsletterMessages(r.Context(), jid, &whatsmeow.GetNewsletterMessagesParams{
			Count: in.Count, Before: types.MessageServerID(in.BeforeID),
		})
	})
}

// UazNewsletterUpdates lists recent updates (reactions/views) of channel messages.
// @Summary Channel updates (UAZAPI)
// @Tags uazapi-newsletter
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body newsletterInput true "id or jid, count, since, afterid"
// @Success 200 {array} map[string]interface{}
// @Router /newsletter/updates [post]
func (h *Handler) UazNewsletterUpdates(w http.ResponseWriter, r *http.Request) {
	h.newsletterCall(w, r, true, func(r *http.Request, c *whatsmeow.Client, jid types.JID, in newsletterInput) (interface{}, error) {
		if in.Count <= 0 {
			in.Count = 20
		}
		params := &whatsmeow.GetNewsletterUpdatesParams{Count: in.Count, After: types.MessageServerID(in.AfterID)}
		if in.Since > 0 {
			params.Since = unixAuto(in.Since)
		}
		return c.GetNewsletterMessageUpdates(r.Context(), jid, params)
	})
}

// UazNewsletterViewed marks channel messages as viewed.
// @Summary Mark channel messages viewed (UAZAPI)
// @Tags uazapi-newsletter
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body newsletterInput true "id or jid, serverids"
// @Success 200 {object} map[string]interface{}
// @Router /newsletter/viewed [post]
func (h *Handler) UazNewsletterViewed(w http.ResponseWriter, r *http.Request) {
	h.newsletterCall(w, r, true, func(r *http.Request, c *whatsmeow.Client, jid types.JID, in newsletterInput) (interface{}, error) {
		if len(in.ServerIDs) == 0 {
			return nil, badInput("serverids is required")
		}
		ids := make([]types.MessageServerID, len(in.ServerIDs))
		for i, v := range in.ServerIDs {
			ids[i] = types.MessageServerID(v)
		}
		return nil, c.NewsletterMarkViewed(r.Context(), jid, ids)
	})
}

// UazNewsletterReaction reacts to a channel message.
// @Summary React to channel message (UAZAPI)
// @Tags uazapi-newsletter
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body newsletterInput true "id or jid, serverid, reaction"
// @Success 200 {object} map[string]interface{}
// @Router /newsletter/reaction [post]
func (h *Handler) UazNewsletterReaction(w http.ResponseWriter, r *http.Request) {
	h.newsletterCall(w, r, true, func(r *http.Request, c *whatsmeow.Client, jid types.JID, in newsletterInput) (interface{}, error) {
		if in.ServerID == 0 {
			return nil, badInput("serverid is required")
		}
		msgID := types.MessageID(in.ReactionMessageID)
		if msgID == "" {
			msgID = c.GenerateMessageID()
		}
		return nil, c.NewsletterSendReaction(r.Context(), jid, types.MessageServerID(in.ServerID), in.Reaction, msgID)
	})
}
