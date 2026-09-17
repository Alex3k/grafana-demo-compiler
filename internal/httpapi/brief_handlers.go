package httpapi

import (
	"net/http"

	"github.com/Alex3k/grafana-demo-compiler/internal/application"
	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
)

func (s *Server) openBriefThread(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Focus domain.BriefFocus `json:"focus"`
	}
	if err := decodeJSON(r, &input); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid brief thread request", err)
		return
	}
	thread, err := s.briefFlow.OpenThread(r.Context(), r.PathValue("id"), input.Focus)
	if err != nil {
		s.writeFault(w, err)
		return
	}
	writeJSON(w, http.StatusOK, thread)
}

func (s *Server) createBriefThreadMessage(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "Streaming is unavailable", nil)
		return
	}
	var input struct {
		Content string `json:"content"`
	}
	if err := decodeJSON(r, &input); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid focused message request", err)
		return
	}
	turn, err := s.briefFlow.PrepareFocusedTurn(r.Context(), r.PathValue("id"), r.PathValue("threadID"), input.Content)
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
	_ = s.briefFlow.RunFocusedTurn(r.Context(), turn, sink)
}

func (s *Server) confirmBriefThread(w http.ResponseWriter, r *http.Request) {
	result, err := s.briefFlow.ConfirmThread(r.Context(), r.PathValue("id"), r.PathValue("threadID"))
	if err != nil {
		s.writeFault(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
