package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// Version is overridden at build time with -ldflags "-X .../internal/api.Version=x.y.z".
var Version = "0.0.8"

var startedAt = time.Now()

var errNotConnected = errors.New("not connected")

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError keeps the JSON shape {"error": "..."} for new endpoints. Legacy endpoints
// keep using http.Error so existing clients see the same plain-text bodies.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeJSON(r *http.Request, v interface{}) error {
	dec := json.NewDecoder(r.Body)
	return dec.Decode(v)
}
