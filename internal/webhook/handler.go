// Package webhook receives and verifies GitHub webhook deliveries.
package webhook

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/google/go-github/v93/github"

	"go-gh/internal/events"
)

// Handler verifies the HMAC signature of each delivery and forwards relevant
// events to OnEvent.
type Handler struct {
	Path    string // e.g. /hook/<installID>
	Secret  []byte
	OnEvent func(events.Event)
	OnPing  func(hookID int64)
	Log     *slog.Logger
	Now     func() time.Time
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != h.Path {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 25<<20) // GitHub caps payloads at 25 MB
	payload, err := github.ValidatePayload(r, h.Secret)
	if err != nil {
		h.log().Warn("rejected webhook delivery", "err", err)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	typ := github.WebHookType(r)
	parsed, err := github.ParseWebHook(typ, payload)
	if err != nil {
		// Unknown event types are acknowledged so GitHub does not retry.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if p, ok := parsed.(*github.PingEvent); ok {
		if h.OnPing != nil {
			h.OnPing(p.GetHookID())
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	now := time.Now
	if h.Now != nil {
		now = h.Now
	}
	if ev, ok := events.FromPayload(parsed, events.Context{Source: events.SourceWebhook, Time: now()}); ok && h.OnEvent != nil {
		h.OnEvent(ev)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}
