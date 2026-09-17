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
	"sync"
	"time"

	"github.com/Alex3k/grafana-demo-compiler/internal/application"
	"github.com/Alex3k/grafana-demo-compiler/internal/chat"
	"github.com/Alex3k/grafana-demo-compiler/internal/observability"
	"github.com/Alex3k/grafana-demo-compiler/internal/store"
)

type Server struct {
	store      *store.Store
	chat       *chat.Service
	chatFlow   *application.ChatService
	briefFlow  *application.BriefService
	prototype  *application.PrototypeService
	deployment *application.DeploymentService
	o11y       *observability.Runtime
	log        *slog.Logger
	web        fs.FS
	mutationMu sync.RWMutex
}

func New(
	dataStore *store.Store,
	chatService *chat.Service,
	chatFlow *application.ChatService,
	briefFlow *application.BriefService,
	prototypeFlow *application.PrototypeService,
	deploymentFlow *application.DeploymentService,
	o11y *observability.Runtime,
	logger *slog.Logger,
	web fs.FS,
) http.Handler {
	server := &Server{
		store: dataStore, chat: chatService, chatFlow: chatFlow, briefFlow: briefFlow,
		prototype: prototypeFlow, deployment: deploymentFlow, o11y: o11y, log: logger, web: web,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sessions/{id}/revisions", server.listRevisions)
	mux.HandleFunc("POST /api/sessions/{id}/revisions/{revisionID}", server.decideRevision)
	if chatService != nil {
		chatService.SetGCXStore(dataStore)
	}
	mux.HandleFunc("GET /api/sessions/{id}/gcx-actions", server.listGCXActions)
	mux.HandleFunc("POST /api/sessions/{id}/gcx-actions/{actionID}", server.decideGCXAction)
	mux.HandleFunc("GET /api/health", server.health)
	mux.HandleFunc("GET /api/sessions", server.listSessions)
	mux.HandleFunc("POST /api/sessions", server.createSession)
	mux.HandleFunc("GET /api/sessions/{id}", server.getSession)
	mux.HandleFunc("GET /api/sessions/{id}/context-usage", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"usage": server.chat.ContextUsage(r.PathValue("id"))})
	})
	mux.HandleFunc("PATCH /api/sessions/{id}", server.renameSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", server.deleteSession)
	mux.HandleFunc("POST /api/sessions/{id}/messages", server.createMessage)
	mux.HandleFunc("POST /api/sessions/{id}/prototypes", server.createPrototype)
	mux.HandleFunc("POST /api/sessions/{id}/deployments", server.createDeployment)
	mux.HandleFunc("POST /api/sessions/{id}/stack", server.createStack)
	mux.HandleFunc("POST /api/sessions/{id}/deployments/{deploymentID}/token", server.configureDeploymentToken)
	mux.HandleFunc("POST /api/sessions/{id}/brief-threads", server.openBriefThread)
	mux.HandleFunc("POST /api/sessions/{id}/brief-threads/{threadID}/messages", server.createBriefThreadMessage)
	mux.HandleFunc("POST /api/sessions/{id}/brief-threads/{threadID}/confirm", server.confirmBriefThread)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "API route not found"})
	})
	if web != nil {
		mux.Handle("/", spaHandler(web))
	}
	return recoverMiddleware(logger, requestLogMiddleware(logger, server.sessionMutationGate(mux)))
}

func (s *Server) sessionMutationGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/sessions/") {
			if r.Method == http.MethodDelete {
				s.mutationMu.Lock()
				defer s.mutationMu.Unlock()
			} else {
				s.mutationMu.RLock()
				defer s.mutationMu.RUnlock()
				id := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/")[0]
				deleting, err := s.store.SessionDeleting(r.Context(), id)
				if err != nil {
					s.writeError(w, 500, "Could not check session", err)
					return
				}
				if deleting {
					writeJSON(w, 409, map[string]string{"error": "Session cleanup is pending. Retry deleting the session."})
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Confirm bool `json:"confirm"`
	}
	if err := decodeJSON(r, &input); err != nil || !input.Confirm {
		writeJSON(w, 400, map[string]string{"error": "Explicit confirmation is required to permanently delete the session and its resources"})
		return
	}
	if s.deployment == nil {
		writeJSON(w, 503, map[string]string{"error": "Cleanup is unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	err := s.deployment.DeleteSession(ctx, s.store, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.log.Error("session deletion failed", "error", err)
		writeJSON(w, 409, map[string]string{"error": "Cleanup did not complete. Wait for active work to finish and check gcx Cloud authentication and Docker, then retry deletion. The session remains available; some resources may already have been removed."})
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

func (s *Server) writeFault(w http.ResponseWriter, err error) {
	var appFault *application.Fault
	if !errors.As(err, &appFault) {
		s.writeError(w, http.StatusInternalServerError, "Unexpected server error", err)
		return
	}
	status := map[application.FaultCode]int{
		application.FaultInvalid:       http.StatusBadRequest,
		application.FaultTooLarge:      http.StatusRequestEntityTooLarge,
		application.FaultNotFound:      http.StatusNotFound,
		application.FaultConflict:      http.StatusConflict,
		application.FaultUnprocessable: http.StatusUnprocessableEntity,
		application.FaultBadGateway:    http.StatusBadGateway,
		application.FaultInternal:      http.StatusInternalServerError,
	}[appFault.Code]
	if status == 0 {
		status = http.StatusInternalServerError
	}
	s.writeError(w, status, appFault.Public, appFault.Cause)
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
