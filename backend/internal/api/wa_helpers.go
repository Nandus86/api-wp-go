package api

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/user/whatsmeow-basileia/internal/service/messaging"
	"go.mau.fi/whatsmeow/types"
)

func parseJID(s string) (types.JID, error) {
	return messaging.ParseJIDOrNumber(s)
}

func parseJIDs(list []string) ([]types.JID, error) {
	out := make([]types.JID, 0, len(list))
	for _, s := range list {
		jid, err := parseJID(s)
		if err != nil {
			return nil, err
		}
		out = append(out, jid)
	}
	return out, nil
}

func parseGroupJID(s string) (types.JID, error) {
	jid, err := parseJID(s)
	if err != nil {
		return types.EmptyJID, err
	}
	if jid.Server != types.GroupServer {
		return types.EmptyJID, fmt.Errorf("%q is not a group JID (expected ...@g.us)", s)
	}
	return jid, nil
}

func parseNewsletterJID(id, jid string) (types.JID, error) {
	v := strings.TrimSpace(jid)
	if v == "" {
		v = strings.TrimSpace(id)
	}
	if v == "" {
		return types.EmptyJID, fmt.Errorf("id or jid is required")
	}
	if !strings.Contains(v, "@") {
		v += "@" + types.NewsletterServer
	}
	return types.ParseJID(v)
}

// loadImage reads an image given as a data URI, raw base64, an http(s) URL or a media: reference.
func (h *Handler) loadImage(ctx context.Context, src string) ([]byte, error) {
	src = strings.TrimSpace(src)
	switch {
	case src == "":
		return nil, fmt.Errorf("image is required")
	case strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://"):
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
		if err != nil {
			return nil, err
		}
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			return nil, fmt.Errorf("download image: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("download image: HTTP %d", resp.StatusCode)
		}
		return io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	case strings.HasPrefix(src, messaging.MediaRefPrefix):
		data, _, err := h.storage.Get(ctx, strings.TrimPrefix(src, messaging.MediaRefPrefix))
		return data, err
	default:
		if i := strings.Index(src, ";base64,"); i != -1 {
			src = src[i+8:]
		}
		return base64.StdEncoding.DecodeString(src)
	}
}

// unixAuto converts a unix timestamp given in seconds or milliseconds.
func unixAuto(v int64) time.Time {
	if v > 100_000_000_000 {
		return time.UnixMilli(v)
	}
	return time.Unix(v, 0)
}

// parseEphemeral accepts 0/off, 1d, 7d, 90d, or a number of seconds.
func parseEphemeral(v interface{}) (time.Duration, error) {
	var s string
	switch t := v.(type) {
	case nil:
		return 0, fmt.Errorf("duration is required")
	case float64:
		s = strconv.FormatInt(int64(t), 10)
	case string:
		s = strings.ToLower(strings.TrimSpace(t))
	default:
		return 0, fmt.Errorf("invalid duration")
	}
	switch s {
	case "0", "off", "":
		return 0, nil
	case "1d", "24h":
		return 24 * time.Hour, nil
	case "7d":
		return 7 * 24 * time.Hour, nil
	case "90d":
		return 90 * 24 * time.Hour, nil
	}
	secs, err := strconv.Atoi(s)
	if err != nil || secs < 0 {
		return 0, fmt.Errorf("duration must be 0/off, 1d, 7d, 90d or seconds")
	}
	return time.Duration(secs) * time.Second, nil
}
