package httpapi

import (
	"net/http"

	"github.com/Alex3k/grafana-demo-compiler/internal/application"
)

func (s *Server) createMessage(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "Streaming is unavailable", nil)
		return
	}
	var input struct {
		Content string `json:"content"`
	}
	if err := decodeJSON(r, &input); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid message request", err)
		return
	}
	turn, err := s.chatFlow.PrepareTurn(r.Context(), r.PathValue("id"), input.Content)
	if err != nil {
		s.writeFault(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	sink := func(event application.Event) error {
		return writeEvent(w, flusher, event.Name, event.Data)
	}
	// Streaming failures have already been persisted and emitted to the client.
	_ = s.chatFlow.RunTurn(r.Context(), turn, sink)
}
