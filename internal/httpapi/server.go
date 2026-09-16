package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Alex3k/grafana-demo-compiler/internal/chat"
	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
	deploymentguard "github.com/Alex3k/grafana-demo-compiler/internal/guard"
	"github.com/Alex3k/grafana-demo-compiler/internal/observability"
	"github.com/Alex3k/grafana-demo-compiler/internal/store"
)

type Server struct {
	store *store.Store
	chat  *chat.Service
	o11y  *observability.Runtime
	log   *slog.Logger
	web   fs.FS
}

func New(dataStore *store.Store, chatService *chat.Service, o11y *observability.Runtime, logger *slog.Logger, web fs.FS) http.Handler {
	server := &Server{store: dataStore, chat: chatService, o11y: o11y, log: logger, web: web}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", server.health)
	mux.HandleFunc("GET /api/sessions", server.listSessions)
	mux.HandleFunc("POST /api/sessions", server.createSession)
	mux.HandleFunc("GET /api/sessions/{id}", server.getSession)
	mux.HandleFunc("PATCH /api/sessions/{id}", server.renameSession)
	mux.HandleFunc("POST /api/sessions/{id}/messages", server.createMessage)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "API route not found"})
	})
	if web != nil {
		mux.Handle("/", spaHandler(web))
	}
	return recoverMiddleware(logger, requestLogMiddleware(logger, mux))
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	databaseStatus := "connected"
	if err := s.store.Ping(r.Context()); err != nil {
		databaseStatus = "error"
	}
	o11yStatus, o11yDetail := s.o11y.Status()
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"sqlite": map[string]string{"status": databaseStatus},
		"bedrock": map[string]any{
			"status":  ternary(s.chat.Configured(), "configured", "not_configured"),
			"modelId": s.chat.ModelID(),
		},
		"agentObservability": map[string]string{"status": o11yStatus, "detail": o11yDetail},
	})
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.store.ListSessions(r.Context())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not list sessions", err)
		return
	}
	writeJSON(w, http.StatusOK, sessions)
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Title string `json:"title"`
	}
	if err := decodeJSON(r, &input); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid session request", err)
		return
	}
	session, err := s.store.CreateSession(r.Context(), strings.TrimSpace(input.Title))
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not create session", err)
		return
	}
	writeJSON(w, http.StatusCreated, session)
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	session, err := s.store.GetSession(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "Session not found", err)
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not load session", err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) renameSession(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Title string `json:"title"`
	}
	if err := decodeJSON(r, &input); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid session request", err)
		return
	}
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" {
		s.writeError(w, http.StatusBadRequest, "A session title is required", nil)
		return
	}
	if err := s.store.RenameSession(r.Context(), r.PathValue("id"), input.Title); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		s.writeError(w, status, "Could not rename session", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

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
	input.Content = strings.TrimSpace(input.Content)
	if input.Content == "" {
		s.writeError(w, http.StatusBadRequest, "A message is required", nil)
		return
	}
	if utf8.RuneCountInString(input.Content) > 32_000 {
		s.writeError(w, http.StatusRequestEntityTooLarge, "Message exceeds 32,000 characters", nil)
		return
	}

	session, err := s.store.GetSession(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "Session not found", err)
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not load session", err)
		return
	}

	userMessage, err := s.store.CreateMessage(r.Context(), domain.Message{
		SessionID: session.ID,
		Role:      "user",
		Kind:      "message",
		Content:   input.Content,
		Status:    "complete",
	})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not save message", err)
		return
	}
	if session.Title == "Untitled demo" {
		title := suggestedTitle(input.Content)
		if renameErr := s.store.RenameSession(r.Context(), session.ID, title); renameErr == nil {
			session.Title = title
		}
	}
	if decision := deploymentguard.CheckDeploymentRequest(input.Content); decision.Blocked {
		s.handleBlockedDeployment(r.Context(), w, flusher, session, userMessage, decision)
		return
	}

	activity, _ := s.store.CreateMessage(r.Context(), domain.Message{
		SessionID: session.ID,
		Role:      "system",
		Kind:      "activity",
		Content:   "Understanding your demo request",
		Status:    "complete",
	})
	operation, err := s.store.CreateOperation(r.Context(), domain.Operation{
		SessionID: session.ID,
		Kind:      "bedrock_generation",
		Status:    "running",
		Summary:   "Waiting for the assistant",
	})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not start assistant operation", err)
		return
	}
	assistantMessage, err := s.store.CreateMessage(r.Context(), domain.Message{
		SessionID: session.ID,
		Role:      "assistant",
		Kind:      "message",
		Status:    "streaming",
	})
	if err != nil {
		_ = s.store.FinishOperation(r.Context(), operation.ID, "failed", "Assistant response failed", err.Error())
		s.writeError(w, http.StatusInternalServerError, "Could not start assistant response", err)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	writeEvent(w, flusher, "user_message", userMessage)
	writeEvent(w, flusher, "activity", activity)
	writeEvent(w, flusher, "message_started", assistantMessage)

	messages, err := s.store.ListMessages(r.Context(), session.ID)
	if err != nil {
		s.finishStreamWithError(r.Context(), w, flusher, operation, assistantMessage, "Could not reload conversation", err)
		return
	}
	var response strings.Builder
	result, err := s.chat.Stream(r.Context(), session, messages, func(delta string) error {
		response.WriteString(delta)
		if err := s.store.UpdateMessage(r.Context(), assistantMessage.ID, response.String(), "streaming"); err != nil {
			return err
		}
		return writeEvent(w, flusher, "delta", map[string]string{"messageId": assistantMessage.ID, "delta": delta})
	})
	if err != nil {
		s.finishStreamWithError(r.Context(), w, flusher, operation, assistantMessage, friendlyChatError(err), err)
		return
	}
	assistantMessage.Content = result.Text
	assistantMessage.Status = "complete"
	if err := s.store.UpdateMessage(r.Context(), assistantMessage.ID, result.Text, "complete"); err != nil {
		s.finishStreamWithError(r.Context(), w, flusher, operation, assistantMessage, "Could not save the completed response", err)
		return
	}
	_ = s.store.FinishOperation(r.Context(), operation.ID, "complete", "Assistant response complete", "")
	writeEvent(w, flusher, "message_completed", assistantMessage)
	s.updateLivingBrief(r.Context(), w, flusher, session, result.GenerationID)
}

func (s *Server) handleBlockedDeployment(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, session domain.Session, userMessage domain.Message, decision deploymentguard.Decision) {
	activity, _ := s.store.CreateMessage(ctx, domain.Message{
		SessionID: session.ID,
		Role:      "system",
		Kind:      "activity",
		Content:   "Local-only deployment guard blocked the remote application target",
		Status:    "complete",
	})
	operation, err := s.store.CreateOperation(ctx, domain.Operation{
		SessionID: session.ID,
		Kind:      "deployment_guard",
		Status:    "running",
		Summary:   "Checking the requested application deployment target",
	})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not record deployment guard outcome", err)
		return
	}
	content := fmt.Sprintf("Application deployment to %s is blocked by the MVP's local-only guard. I can build and run the application locally with Docker Compose and model the requested architecture or failure mode there. The required per-session Grafana Cloud stack and Grafana resources will still be created and managed through `gcx`.", decision.Target)
	assistantMessage, err := s.store.CreateMessage(ctx, domain.Message{
		SessionID: session.ID,
		Role:      "assistant",
		Kind:      "message",
		Content:   content,
		Status:    "complete",
	})
	if err != nil {
		_ = s.store.FinishOperation(ctx, operation.ID, "failed", "Could not save guard response", err.Error())
		s.writeError(w, http.StatusInternalServerError, "Could not save guard response", err)
		return
	}
	_ = s.store.FinishOperation(ctx, operation.ID, "complete", "Remote application deployment blocked", "")
	s.o11y.RecordDeploymentGuard(ctx, session.ID, decision.Target)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	_ = writeEvent(w, flusher, "user_message", userMessage)
	_ = writeEvent(w, flusher, "activity", activity)
	_ = writeEvent(w, flusher, "message_completed", assistantMessage)
	s.updateLivingBrief(ctx, w, flusher, session)
}

func (s *Server) updateLivingBrief(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, session domain.Session, parentGenerationIDs ...string) {
	activity, _ := s.store.CreateMessage(ctx, domain.Message{
		SessionID: session.ID,
		Role:      "system",
		Kind:      "activity",
		Content:   "Updating the living demo brief",
		Status:    "complete",
	})
	_ = writeEvent(w, flusher, "activity", activity)
	operation, err := s.store.CreateOperation(ctx, domain.Operation{
		SessionID: session.ID,
		Kind:      "living_brief_update",
		Status:    "running",
		Summary:   "Extracting decisions, narrative, and architecture",
	})
	if err != nil {
		s.log.Error("could not start living brief update", "session", session.ID, "error", err)
		return
	}
	messages, err := s.store.ListMessages(ctx, session.ID)
	if err == nil {
		var content domain.BriefContent
		var result chat.BriefResult
		result, err = s.chat.BuildBrief(ctx, session, messages, parentGenerationIDs...)
		if err == nil {
			content = result.Content
			if content.Acceptance.Accepted {
				evaluationActivity, _ := s.store.CreateMessage(ctx, domain.Message{
					SessionID: session.ID,
					Role:      "system",
					Kind:      "activity",
					Content:   "Evaluating the accepted plan against the demo requirement",
					Status:    "complete",
				})
				_ = writeEvent(w, flusher, "activity", evaluationActivity)
				var evaluation chat.EvaluationResult
				evaluation, err = s.chat.EvaluatePlan(ctx, session, content, messages, result.GenerationID)
				if err == nil {
					content.Acceptance.Evaluation = evaluation.Evaluation
				}
			}
		}
		if err == nil {
			var brief domain.LivingBrief
			brief, err = s.store.SaveBrief(ctx, session.ID, content)
			if err == nil {
				if content.Acceptance.Accepted && content.Acceptance.Evaluation.Result != "does_not_meet" {
					if stateErr := s.store.UpdateSessionState(ctx, session.ID, "Ready"); stateErr != nil {
						s.log.Error("could not mark accepted plan ready", "session", session.ID, "error", stateErr)
					} else {
						_ = writeEvent(w, flusher, "session_state", map[string]string{"state": "Ready"})
					}
				} else if session.State == "Ready" {
					if stateErr := s.store.UpdateSessionState(ctx, session.ID, "Draft"); stateErr != nil {
						s.log.Error("could not return revised plan to draft", "session", session.ID, "error", stateErr)
					} else {
						_ = writeEvent(w, flusher, "session_state", map[string]string{"state": "Draft"})
					}
				}
				_ = s.store.FinishOperation(ctx, operation.ID, "complete", "Living brief updated", "")
				_ = writeEvent(w, flusher, "brief_updated", brief)
				return
			}
		}
	}
	s.log.Error("living brief update failed", "session", session.ID, "error", err)
	_ = s.store.FinishOperation(ctx, operation.ID, "failed", "Living brief update failed", err.Error())
	failure, _ := s.store.CreateMessage(ctx, domain.Message{
		SessionID: session.ID,
		Role:      "system",
		Kind:      "activity",
		Content:   "The conversation is saved, but the living brief needs another pass",
		Status:    "complete",
	})
	_ = writeEvent(w, flusher, "activity", failure)
}

func (s *Server) finishStreamWithError(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, operation domain.Operation, message domain.Message, public string, err error) {
	s.log.Error("assistant response failed", "session", message.SessionID, "operation", operation.ID, "error", err)
	content := message.Content
	if content == "" {
		content = public
	}
	_ = s.store.UpdateMessage(ctx, message.ID, content, "failed")
	_ = s.store.FinishOperation(ctx, operation.ID, "failed", public, err.Error())
	_ = writeEvent(w, flusher, "error", map[string]string{"messageId": message.ID, "message": public})
}

func (s *Server) writeError(w http.ResponseWriter, status int, message string, err error) {
	if err != nil {
		s.log.Error(message, "error", err)
	}
	writeJSON(w, status, map[string]string{"error": message})
}

func decodeJSON(r *http.Request, destination any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeEvent(w http.ResponseWriter, flusher http.Flusher, event string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func suggestedTitle(content string) string {
	words := strings.Fields(content)
	if len(words) > 7 {
		words = words[:7]
	}
	title := strings.Join(words, " ")
	if utf8.RuneCountInString(title) > 56 {
		title = string([]rune(title)[:56])
	}
	if title == "" {
		return "Untitled demo"
	}
	return title
}

func friendlyChatError(err error) string {
	if errors.Is(err, chat.ErrNotConfigured) {
		return "Amazon Bedrock is not configured. Set AWS_REGION and BEDROCK_MODEL_ID, then restart the compiler."
	}
	if errors.Is(err, context.Canceled) {
		return "The response was interrupted. You can retry your message."
	}
	return "The assistant could not complete this response. Check the server log and retry."
}

func ternary(condition bool, yes, no string) string {
	if condition {
		return yes
	}
	return no
}

func requestLogMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started))
	})
}

func recoverMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("request panic", "path", r.URL.Path, "panic", recovered)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Unexpected server error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func spaHandler(files fs.FS) http.Handler {
	fileServer := http.FileServerFS(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			serveIndex(w, files)
			return
		}
		if _, err := fs.Stat(files, path); err != nil {
			serveIndex(w, files)
			return
		}
		r.URL.Path = "/" + path
		fileServer.ServeHTTP(w, r)
	})
}

func serveIndex(w http.ResponseWriter, files fs.FS) {
	content, err := fs.ReadFile(files, "index.html")
	if err != nil {
		http.Error(w, "UI is unavailable", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(content)
}
