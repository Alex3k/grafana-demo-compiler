package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/Alex3k/grafana-demo-compiler/internal/chat"
	"github.com/Alex3k/grafana-demo-compiler/internal/gcxtool"
	"github.com/grafana/agento11y/go/agento11y"
)

func (s *Server) listGCXActions(w http.ResponseWriter, r *http.Request) {
	session, err := s.store.GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeError(w, 404, "Session not found", err)
		return
	}
	actions, err := s.store.ListGCXActions(r.Context(), session.ID)
	if err != nil {
		s.writeError(w, 500, "Could not load Grafana actions", err)
		return
	}
	writeJSON(w, 200, map[string]any{"stack": chat.SessionStack(session), "actions": actions})
}

func (s *Server) decideGCXAction(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Decision string `json:"decision"`
	}
	if err := decodeJSON(r, &input); err != nil || (input.Decision != "approve" && input.Decision != "reject") {
		writeJSON(w, 400, map[string]string{"error": "Choose approve or reject"})
		return
	}
	session, err := s.store.GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeError(w, 404, "Session not found", err)
		return
	}
	status := "running"
	if input.Decision == "reject" {
		status = "rejected"
	}
	a, err := s.store.ClaimGCXAction(r.Context(), session.ID, r.PathValue("actionID"), status)
	if err != nil {
		s.writeError(w, 409, "This action is no longer pending; refresh its status", err)
		return
	}
	if status == "rejected" {
		writeJSON(w, 200, a)
		return
	}
	ctx := r.Context()
	var finish func()
	if s.o11y.Configured() {
		var rec *agento11y.ToolExecutionRecorder
		ctx, rec = s.o11y.Client.StartToolExecution(ctx, agento11y.ToolExecutionStart{ToolName: "run_gcx_approved", ToolCallID: a.ID, ToolType: "function", ConversationID: session.ID, ConversationTitle: session.Title, IncludeContent: true})
		finish = func() { rec.SetResult(agento11y.ToolExecutionEnd{Arguments: a.Request, Result: a}); rec.End() }
		defer finish()
	}
	if a.Stack != chat.SessionStack(session) {
		a.Status = "failed"
		a.Output = "Session stack changed; ask for a new proposal."
	} else {
		a.Output, err = gcxtool.Execute(ctx, a.Stack, a.Request)
		a.Status = "complete"
		if err != nil {
			a.Status = "failed"
			a.Output += "\n" + gcxtool.Redact(err.Error()) + "\nExecution may have partially succeeded. Inspect the resource before requesting another write."
		}
	}
	persist, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.store.FinishGCXAction(persist, a.ID, a.Status, a.Output); err != nil {
		s.writeError(w, 500, "Could not record result; inspect Grafana before retrying", err)
		return
	}
	writeJSON(w, 200, a)
}
