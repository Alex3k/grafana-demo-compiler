package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Alex3k/grafana-demo-compiler/internal/chat"
	"github.com/Alex3k/grafana-demo-compiler/internal/deployment"
	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
	"github.com/Alex3k/grafana-demo-compiler/internal/observability"
	"github.com/Alex3k/grafana-demo-compiler/internal/store"
)

type Server struct {
	store         *store.Store
	chat          *chat.Service
	o11y          *observability.Runtime
	log           *slog.Logger
	web           fs.FS
	prototypeRoot string
	deployment    *deployment.Runner
}

func New(dataStore *store.Store, chatService *chat.Service, o11y *observability.Runtime, logger *slog.Logger, prototypeRoot string, web fs.FS) http.Handler {
	server := &Server{store: dataStore, chat: chatService, o11y: o11y, log: logger, web: web, prototypeRoot: prototypeRoot, deployment: deployment.New()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", server.health)
	mux.HandleFunc("GET /api/sessions", server.listSessions)
	mux.HandleFunc("POST /api/sessions", server.createSession)
	mux.HandleFunc("GET /api/sessions/{id}", server.getSession)
	mux.HandleFunc("PATCH /api/sessions/{id}", server.renameSession)
	mux.HandleFunc("POST /api/sessions/{id}/messages", server.createMessage)
	mux.HandleFunc("POST /api/sessions/{id}/prototypes", server.createPrototype)
	mux.HandleFunc("POST /api/sessions/{id}/deployments", server.createDeployment)
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
	return recoverMiddleware(logger, requestLogMiddleware(logger, mux))
}

func (s *Server) createDeployment(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Target string `json:"target"`
		Region string `json:"region"`
	}
	if err := decodeJSON(r, &input); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid deployment request", err)
		return
	}
	input.Target = strings.TrimSpace(strings.ToLower(input.Target))
	input.Region = strings.TrimSpace(input.Region)
	if input.Target != "local" {
		s.writeError(w, http.StatusUnprocessableEntity, "This MVP only deploys application services locally with Docker Compose", nil)
		return
	}
	if input.Region == "" {
		s.writeError(w, http.StatusBadRequest, "Grafana Cloud region is required", nil)
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
	prototype := latestCompletePrototype(session.Prototypes)
	if prototype == nil {
		s.writeError(w, http.StatusConflict, "Generate and validate a prototype before deploying it", nil)
		return
	}
	for _, existing := range session.Deployments {
		if existing.Status == "provisioning" || existing.Status == "needs_token" || existing.Status == "starting" || existing.Status == "running" || existing.Status == "verifying" || existing.Status == "verified" {
			s.writeError(w, http.StatusConflict, "This session already has an active local deployment", nil)
			return
		}
	}
	shortID := session.ID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	stackSlug := "democompiler" + strings.ToLower(shortID)
	stackName := session.Title + " demo " + shortID
	item, err := s.store.CreateDeployment(r.Context(), domain.Deployment{
		SessionID: session.ID, PrototypeIterationID: prototype.ID,
		Target: "local", Region: input.Region,
		StackName: stackName, StackSlug: stackSlug, Status: "provisioning",
		Progress: []string{"Deployment accepted: local Docker Compose only"},
	})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not create deployment", err)
		return
	}
	go s.runStackProvisioning(item)
	writeJSON(w, http.StatusAccepted, item)
}

func latestCompletePrototype(iterations []domain.PrototypeIteration) *domain.PrototypeIteration {
	for index := range iterations {
		if iterations[index].Status == "complete" {
			return &iterations[index]
		}
	}
	return nil
}

func (s *Server) runStackProvisioning(item domain.Deployment) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	appendProgress := func(message string) {
		if strings.TrimSpace(message) == "" || (len(item.Progress) > 0 && item.Progress[len(item.Progress)-1] == message) {
			return
		}
		item.Progress = append(item.Progress, message)
		persistCtx, cancelPersist := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelPersist()
		if err := s.store.UpdateDeployment(persistCtx, item); err != nil {
			s.log.Error("save deployment progress", "sessionId", item.SessionID, "deploymentId", item.ID, "error", err)
		}
	}
	stack, err := s.deployment.Provision(ctx, item.Region, item.StackName, item.StackSlug, appendProgress)
	if err != nil {
		item.Status = "failed"
		item.Error = err.Error()
		appendProgress("Stack provisioning stopped: " + actionableGCXError(err))
	} else {
		item.StackURL, item.OTLPEndpoint, item.InstanceID = stack.URL, stack.OTLPEndpoint, stack.InstanceID
		item.Status = "needs_token"
		appendProgress("Grafana Cloud stack is ready; an OTLP access-policy token is required")
	}
	persistCtx, cancelPersist := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPersist()
	if err := s.store.UpdateDeployment(persistCtx, item); err != nil {
		s.log.Error("finish stack provisioning", "sessionId", item.SessionID, "deploymentId", item.ID, "error", err)
	}
}

func (s *Server) configureDeploymentToken(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(r, &input); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid token request", err)
		return
	}
	item, err := s.store.GetDeployment(r.Context(), r.PathValue("id"), r.PathValue("deploymentID"))
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "Deployment not found", err)
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not load deployment", err)
		return
	}
	if item.Status != "needs_token" {
		s.writeError(w, http.StatusConflict, "This deployment is not waiting for a telemetry token", nil)
		return
	}
	if strings.TrimSpace(input.Token) == "" {
		s.writeError(w, http.StatusBadRequest, "An OTLP access-policy token is required", nil)
		return
	}
	session, err := s.store.GetSession(r.Context(), item.SessionID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not load deployment session", err)
		return
	}
	var iteration *domain.PrototypeIteration
	for index := range session.Prototypes {
		if session.Prototypes[index].ID == item.PrototypeIterationID {
			iteration = &session.Prototypes[index]
			break
		}
	}
	if iteration == nil || !pathWithin(s.prototypeRoot, iteration.RootPath) {
		s.writeError(w, http.StatusConflict, "The deployment prototype path is unavailable", nil)
		return
	}
	item.Status = "starting"
	item.Error = ""
	item.Progress = append(item.Progress, "Telemetry token received securely; it will not be stored in the session database")
	if item.OTLPEndpoint == "" {
		item.Progress = append(item.Progress, "Resolving Grafana Cloud OTLP connection details")
	}
	claimCtx, cancelClaim := context.WithTimeout(context.Background(), 3*time.Second)
	claimed, err := s.store.ClaimDeploymentStart(claimCtx, item)
	cancelClaim()
	if err != nil {
		input.Token = ""
		s.writeError(w, http.StatusInternalServerError, "Could not start local deployment", err)
		return
	}
	if !claimed {
		input.Token = ""
		s.writeError(w, http.StatusConflict, "This deployment is already being configured", nil)
		return
	}
	if item.OTLPEndpoint == "" {
		resolveCtx, cancelResolve := context.WithTimeout(context.Background(), 30*time.Second)
		endpoint, resolveErr := s.deployment.ResolveOTLP(resolveCtx, item.StackSlug, input.Token)
		cancelResolve()
		if resolveErr != nil {
			input.Token = ""
			item.Status = "needs_token"
			item.Error = "Could not read this stack's OTLP connection details. Check that the token includes stacks:read and retry."
			item.Progress = append(item.Progress, item.Error)
			persistCtx, cancelPersist := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancelPersist()
			if err := s.store.UpdateDeployment(persistCtx, item); err != nil {
				s.log.Error("save OTLP resolution failure", "sessionId", item.SessionID, "deploymentId", item.ID, "error", err)
			}
			s.writeError(w, http.StatusBadGateway, "Could not read this stack's OTLP connection details. Check the Cloud Access Policy token and retry.", resolveErr)
			return
		}
		item.OTLPEndpoint = endpoint
		item.Progress = append(item.Progress, "Grafana Cloud OTLP connection details resolved")
	}
	persistCtx, cancelPersist := context.WithTimeout(context.Background(), 3*time.Second)
	err = s.store.UpdateDeployment(persistCtx, item)
	cancelPersist()
	if err != nil {
		item.Status = "needs_token"
		item.Error = "Could not save the Grafana Cloud connection details. Retry the token when the deployment is ready."
		restoreCtx, cancelRestore := context.WithTimeout(context.Background(), 3*time.Second)
		restoreErr := s.store.UpdateDeployment(restoreCtx, item)
		cancelRestore()
		if restoreErr != nil {
			s.log.Error("restore deployment after connection save failure", "sessionId", item.SessionID, "deploymentId", item.ID, "error", restoreErr)
		}
		s.writeError(w, http.StatusInternalServerError, "Could not start local deployment", err)
		return
	}
	go s.runLocalDeployment(item, iteration.RootPath, input.Token)
	input.Token = ""
	writeJSON(w, http.StatusAccepted, item)
}

func (s *Server) runLocalDeployment(item domain.Deployment, root, token string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	appendProgress := func(message string) {
		if strings.TrimSpace(message) == "" || (len(item.Progress) > 0 && item.Progress[len(item.Progress)-1] == message) {
			return
		}
		item.Progress = append(item.Progress, message)
		persistCtx, cancelPersist := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelPersist()
		_ = s.store.UpdateDeployment(persistCtx, item)
	}
	err := s.deployment.StartLocal(ctx, root, item.StackSlug, item.OTLPEndpoint, item.InstanceID, token, item.SessionID, appendProgress)
	token = ""
	if err != nil {
		item.Status = "failed"
		item.Error = err.Error()
		appendProgress("Local deployment failed: " + err.Error())
	} else {
		item.Status = "running"
		appendProgress("Local services are running; telemetry verification is the next step")
		_ = s.store.SetSessionState(context.Background(), item.SessionID, "Running")
	}
	persistCtx, cancelPersist := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPersist()
	if err := s.store.UpdateDeployment(persistCtx, item); err != nil {
		s.log.Error("finish local deployment", "sessionId", item.SessionID, "deploymentId", item.ID, "error", err)
	}
}

func actionableGCXError(err error) string {
	message := err.Error()
	if strings.Contains(strings.ToLower(message), "expired") || strings.Contains(message, "gcx cloud login") {
		return "gcx Cloud login has expired. Run `gcx cloud login`, then retry deployment."
	}
	return message
}

func pathWithin(root, candidate string) bool {
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	candidatePath, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(rootPath, candidatePath)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (s *Server) createPrototype(w http.ResponseWriter, r *http.Request) {
	session, err := s.store.GetSession(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "Session not found", err)
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not load session", err)
		return
	}
	if session.Brief == nil || !session.Brief.Content.PrototypeOffer.Ready {
		s.writeError(w, http.StatusConflict, "The living brief does not yet contain a prototype offer", nil)
		return
	}
	if len(session.Prototypes) > 0 && session.Prototypes[0].Status == "generating" {
		s.writeError(w, http.StatusConflict, "A prototype iteration is already running", nil)
		return
	}
	iteration, err := s.store.CreatePrototypeIteration(r.Context(), session.ID, session.Brief.Version)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not create prototype iteration", err)
		return
	}
	shortID := iteration.ID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	iteration.RootPath = filepath.Join(s.prototypeRoot, session.ID, "iterations", fmt.Sprintf("%03d-%s", iteration.Number, shortID))
	iteration.Progress = append(iteration.Progress, "Queued for the background builder")
	if err := s.store.UpdatePrototypeProgress(r.Context(), iteration); err != nil {
		iteration.Status = "failed"
		iteration.Error = err.Error()
		_ = s.store.FinishPrototypeIteration(r.Context(), iteration)
		s.writeError(w, http.StatusInternalServerError, "Could not prepare prototype generation", err)
		return
	}
	operation, err := s.store.CreateOperation(r.Context(), domain.Operation{
		SessionID: session.ID,
		Kind:      "prototype_generation",
		Status:    "running",
		Summary:   fmt.Sprintf("Building prototype iteration %d", iteration.Number),
	})
	if err != nil {
		iteration.Status = "failed"
		iteration.Error = err.Error()
		_ = s.store.FinishPrototypeIteration(r.Context(), iteration)
		s.writeError(w, http.StatusInternalServerError, "Could not start prototype generation", err)
		return
	}
	_, _ = s.store.CreateMessage(r.Context(), domain.Message{
		SessionID: session.ID, Role: "system", Kind: "activity",
		Content: fmt.Sprintf("Building local prototype iteration %d", iteration.Number), Status: "complete",
	})
	go s.runPrototype(session, iteration, operation)
	writeJSON(w, http.StatusAccepted, iteration)
}

func (s *Server) runPrototype(session domain.Session, iteration domain.PrototypeIteration, operation domain.Operation) {
	buildCtx, cancelBuild := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancelBuild()
	appendProgress := func(message string) {
		if strings.TrimSpace(message) == "" || iteration.Progress[len(iteration.Progress)-1] == message {
			return
		}
		iteration.Progress = append(iteration.Progress, message)
		if len(iteration.Progress) > 100 {
			iteration.Progress = iteration.Progress[len(iteration.Progress)-100:]
		}
		persistCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.store.UpdatePrototypeProgress(persistCtx, iteration); err != nil {
			s.log.Error("save prototype progress", "sessionId", session.ID, "iterationId", iteration.ID, "error", err)
		}
	}
	result, buildErr := s.chat.BuildPrototype(buildCtx, session, iteration.RootPath, appendProgress)
	iteration.Summary = result.Summary
	iteration.Artifacts = result.Artifacts
	iteration.Checks = result.Checks
	persistCtx, cancelPersist := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPersist()
	if buildErr != nil {
		iteration.Status = "failed"
		iteration.Error = buildErr.Error()
		appendProgress("Build failed: " + buildErr.Error())
		s.log.Error("prototype generation failed", "sessionId", session.ID, "iterationId", iteration.ID, "error", buildErr)
		_ = s.store.FinishOperation(persistCtx, operation.ID, "failed", "Prototype generation failed", buildErr.Error())
	} else if failedPrototypeChecks(result.Checks) {
		iteration.Status = "failed"
		iteration.Error = "Prototype validation failed"
		appendProgress("Prototype validation failed")
		_ = s.store.FinishOperation(persistCtx, operation.ID, "failed", "Prototype validation failed", iteration.Error)
	} else {
		iteration.Status = "complete"
		appendProgress("Prototype generated and validated")
		_ = s.store.FinishOperation(persistCtx, operation.ID, "complete", "Prototype generated and validated", "")
		_ = s.store.SetSessionState(persistCtx, session.ID, "Generated")
	}
	if err := s.store.FinishPrototypeIteration(persistCtx, iteration); err != nil {
		s.log.Error("save prototype iteration", "sessionId", session.ID, "iterationId", iteration.ID, "error", err)
		iteration.Status = "failed"
		iteration.Error = "Could not save prototype result"
	}
	completion := fmt.Sprintf("Prototype iteration %d %s", iteration.Number, iteration.Status)
	_, _ = s.store.CreateMessage(persistCtx, domain.Message{SessionID: session.ID, Role: "system", Kind: "activity", Content: completion, Status: "complete"})
}

func failedPrototypeChecks(checks []domain.PrototypeCheck) bool {
	if len(checks) == 0 {
		return true
	}
	for _, check := range checks {
		if check.Status != "passed" {
			return true
		}
	}
	return false
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

func (s *Server) openBriefThread(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Focus domain.BriefFocus `json:"focus"`
	}
	if err := decodeJSON(r, &input); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid brief thread request", err)
		return
	}
	if err := validateBriefFocus(&input.Focus); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if _, err := s.store.GetSession(r.Context(), r.PathValue("id")); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		s.writeError(w, status, "Session not found", err)
		return
	}
	thread, err := s.store.OpenBriefThread(r.Context(), r.PathValue("id"), input.Focus)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not open focused brief chat", err)
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
	if err != nil {
		s.writeError(w, http.StatusNotFound, "Session not found", err)
		return
	}
	thread, err := s.store.GetBriefThread(r.Context(), session.ID, r.PathValue("threadID"))
	if err != nil {
		s.writeError(w, http.StatusNotFound, "Focused brief chat not found", err)
		return
	}
	if thread.State != "draft" {
		s.writeError(w, http.StatusConflict, "This focused brief chat is already confirmed", nil)
		return
	}
	userMessage, err := s.store.CreateBriefThreadMessage(r.Context(), thread.ID, domain.Message{SessionID: session.ID, Role: "user", Kind: "message", Content: input.Content, Status: "complete"})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not save focused message", err)
		return
	}
	if err := s.store.UpdateBriefThreadCandidate(r.Context(), session.ID, thread.ID, ""); err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not reset the proposed brief value", err)
		return
	}
	activity, _ := s.store.CreateBriefThreadMessage(r.Context(), thread.ID, domain.Message{SessionID: session.ID, Role: "system", Kind: "activity", Content: "Refining " + thread.Focus.Label, Status: "complete"})
	assistantMessage, err := s.store.CreateBriefThreadMessage(r.Context(), thread.ID, domain.Message{SessionID: session.ID, Role: "assistant", Kind: "message", Status: "streaming"})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not start focused response", err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	_ = writeEvent(w, flusher, "user_message", userMessage)
	_ = writeEvent(w, flusher, "activity", activity)
	_ = writeEvent(w, flusher, "message_started", assistantMessage)
	_ = writeEvent(w, flusher, "candidate_updated", map[string]string{"value": ""})

	messages, err := s.store.ListBriefThreadMessages(r.Context(), thread.ID)
	if err != nil {
		s.finishBriefThreadStreamWithError(r.Context(), w, flusher, assistantMessage, "Could not reload focused conversation", err)
		return
	}
	var response strings.Builder
	result, err := s.chat.StreamFocused(r.Context(), session, messages, thread.Focus, func(delta string) error {
		response.WriteString(delta)
		if err := s.store.UpdateBriefThreadMessage(r.Context(), assistantMessage.ID, response.String(), "streaming"); err != nil {
			return err
		}
		return writeEvent(w, flusher, "delta", map[string]string{"messageId": assistantMessage.ID, "delta": delta})
	})
	if err != nil {
		s.finishBriefThreadStreamWithError(r.Context(), w, flusher, assistantMessage, friendlyChatError(err), err)
		return
	}
	assistantMessage.Content = result.Text
	assistantMessage.Status = "complete"
	candidate, ok := focusedCandidate(result.Text)
	if !ok {
		s.finishBriefThreadStreamWithError(r.Context(), w, flusher, assistantMessage, "The focused response did not include a proposed brief value. Please retry your message.", errors.New("focused response missing proposed brief value"))
		return
	}
	if err := s.store.UpdateBriefThreadMessage(r.Context(), assistantMessage.ID, result.Text, "complete"); err != nil {
		s.finishBriefThreadStreamWithError(r.Context(), w, flusher, assistantMessage, "Could not save the focused response", err)
		return
	}
	if err := s.store.UpdateBriefThreadCandidate(r.Context(), session.ID, thread.ID, candidate); err != nil {
		s.finishBriefThreadStreamWithError(r.Context(), w, flusher, assistantMessage, "Could not save the proposed brief value", err)
		return
	}
	_ = writeEvent(w, flusher, "message_completed", assistantMessage)
	_ = writeEvent(w, flusher, "candidate_updated", map[string]string{"value": candidate})
	_ = writeEvent(w, flusher, "turn_completed", map[string]string{"threadId": thread.ID})
}

func (s *Server) confirmBriefThread(w http.ResponseWriter, r *http.Request) {
	session, err := s.store.GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeError(w, http.StatusNotFound, "Session not found", err)
		return
	}
	thread, err := s.store.GetBriefThread(r.Context(), session.ID, r.PathValue("threadID"))
	if err != nil {
		s.writeError(w, http.StatusNotFound, "Focused brief chat not found", err)
		return
	}
	if thread.State != "draft" {
		s.writeError(w, http.StatusConflict, "This focused brief chat is already confirmed", nil)
		return
	}
	if strings.TrimSpace(thread.CandidateValue) == "" {
		s.writeError(w, http.StatusBadRequest, "Discuss this topic until there is a proposed brief value before confirming it", nil)
		return
	}
	if session.Brief == nil {
		s.writeError(w, http.StatusConflict, "The living brief is not ready for a focused confirmation", nil)
		return
	}
	content := session.Brief.Content
	if !applyBriefTopic(&content, thread.Focus.Label, thread.CandidateValue) {
		s.writeError(w, http.StatusUnprocessableEntity, "The confirmed topic could not be matched in the living brief", nil)
		return
	}
	brief, err := s.store.ApplyBriefThread(r.Context(), session.ID, thread.ID, content)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not save the confirmed topic", err)
		return
	}
	thread.State = "confirmed"
	activity, _ := s.store.CreateMessage(r.Context(), domain.Message{SessionID: session.ID, Role: "system", Kind: "activity", Content: "Confirmed brief topic: " + thread.Focus.Label, Status: "complete"})
	writeJSON(w, http.StatusOK, map[string]any{"thread": thread, "brief": brief, "activity": activity})
}

func (s *Server) updateLivingBrief(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, session domain.Session, parentGenerationIDs ...string) {
	defer func() {
		_ = writeEvent(w, flusher, "turn_completed", map[string]string{"sessionId": session.ID})
	}()
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

func (s *Server) finishBriefThreadStreamWithError(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, message domain.Message, public string, err error) {
	s.log.Error("focused assistant response failed", "session", message.SessionID, "message", message.ID, "error", err)
	content := message.Content
	if content == "" {
		content = public
	}
	_ = s.store.UpdateBriefThreadMessage(ctx, message.ID, content, "failed")
	_ = writeEvent(w, flusher, "error", map[string]string{"messageId": message.ID, "message": public})
}

func validateBriefFocus(focus *domain.BriefFocus) error {
	focus.Label = strings.TrimSpace(focus.Label)
	focus.Value = strings.TrimSpace(focus.Value)
	if focus.Label == "" || utf8.RuneCountInString(focus.Label) > 120 || utf8.RuneCountInString(focus.Value) > 8_000 {
		return errors.New("Invalid brief topic context")
	}
	if focus.Status != "proposed" && focus.Status != "confirmed" {
		return errors.New("Brief topic must be proposed or confirmed")
	}
	return nil
}

func focusedCandidate(response string) (string, bool) {
	const heading = "### Proposed brief value"
	index := strings.LastIndex(response, heading)
	if index < 0 {
		return "", false
	}
	value := strings.TrimSpace(response[index+len(heading):])
	if value == "" || utf8.RuneCountInString(value) > 8_000 {
		return "", false
	}
	return value, true
}

func applyBriefTopic(content *domain.BriefContent, label, value string) bool {
	core := map[string]*domain.BriefItem{
		"Audience":            &content.Audience,
		"Company":             &content.Company,
		"Outcome":             &content.Outcome,
		"Stakes":              &content.Stakes,
		"Scenario":            &content.Scenario,
		"Journey":             &content.Journey,
		"Simulation boundary": &content.Scope.SimulationBoundary,
	}
	if item := core[label]; item != nil {
		item.Value = value
		item.Status = "confirmed"
		return true
	}
	section, name, found := strings.Cut(label, ": ")
	if !found {
		return false
	}
	collections := map[string]*[]domain.BriefItem{
		"Proof points":          &content.ProofPoints,
		"Included scope":        &content.Scope.Included,
		"Deliberately excluded": &content.Scope.Excluded,
		"Services":              &content.Services,
		"Telemetry":             &content.Telemetry,
		"Grafana resources":     &content.GrafanaResources,
	}
	items := collections[section]
	if items == nil {
		return false
	}
	for index := range *items {
		if strings.EqualFold((*items)[index].Name, name) {
			(*items)[index].Value = value
			(*items)[index].Status = "confirmed"
			return true
		}
	}
	return false
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
