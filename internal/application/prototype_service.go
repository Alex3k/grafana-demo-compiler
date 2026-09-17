package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/Alex3k/grafana-demo-compiler/internal/chat"
	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
	"github.com/Alex3k/grafana-demo-compiler/internal/store"
)

type PrototypeStore interface {
	GetSession(context.Context, string) (domain.Session, error)
	CreatePrototypeIteration(context.Context, string, int) (domain.PrototypeIteration, error)
	UpdatePrototypeProgress(context.Context, domain.PrototypeIteration) error
	FinishPrototypeIteration(context.Context, domain.PrototypeIteration) error
	CreateOperation(context.Context, domain.Operation) (domain.Operation, error)
	FinishOperation(context.Context, string, string, string, string) error
	CreateMessage(context.Context, domain.Message) (domain.Message, error)
	SetSessionState(context.Context, string, string) error
}

type PrototypeBuilder interface {
	BuildPrototype(context.Context, domain.Session, string, func(string)) (chat.PrototypeResult, error)
}

type PrototypeService struct {
	store PrototypeStore
	build PrototypeBuilder
	log   *slog.Logger
	root  string
}

func NewPrototypeService(dataStore PrototypeStore, builder PrototypeBuilder, logger *slog.Logger, root string) *PrototypeService {
	if logger == nil {
		logger = slog.Default()
	}
	return &PrototypeService{store: dataStore, build: builder, log: logger, root: root}
}

// Start records a new iteration and detaches generation from the caller's
// context. The returned iteration is the accepted, queued representation.
func (s *PrototypeService) Start(ctx context.Context, sessionID string) (domain.PrototypeIteration, *Fault) {
	session, err := s.store.GetSession(ctx, sessionID)
	if errors.Is(err, store.ErrNotFound) {
		return domain.PrototypeIteration{}, &Fault{Code: FaultNotFound, Public: "Session not found", Cause: err}
	}
	if err != nil {
		return domain.PrototypeIteration{}, &Fault{Code: FaultInternal, Public: "Could not load session", Cause: err}
	}
	if session.Brief == nil || !session.Brief.Content.PrototypeOffer.Ready {
		return domain.PrototypeIteration{}, &Fault{Code: FaultConflict, Public: "The living brief does not yet contain a prototype offer"}
	}
	if len(session.Prototypes) > 0 && session.Prototypes[0].Status == "generating" {
		return domain.PrototypeIteration{}, &Fault{Code: FaultConflict, Public: "A prototype iteration is already running"}
	}

	iteration, err := s.store.CreatePrototypeIteration(ctx, session.ID, session.Brief.Version)
	if err != nil {
		return domain.PrototypeIteration{}, &Fault{Code: FaultInternal, Public: "Could not create prototype iteration", Cause: err}
	}
	shortID := iteration.ID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	iteration.RootPath = filepath.Join(s.root, session.ID, "iterations", fmt.Sprintf("%03d-%s", iteration.Number, shortID))
	iteration.Progress = append(iteration.Progress, "Queued for the background builder")
	if err := s.store.UpdatePrototypeProgress(ctx, iteration); err != nil {
		iteration.Status = "failed"
		iteration.Error = err.Error()
		_ = s.store.FinishPrototypeIteration(ctx, iteration)
		return domain.PrototypeIteration{}, &Fault{Code: FaultInternal, Public: "Could not prepare prototype generation", Cause: err}
	}

	operation, err := s.store.CreateOperation(ctx, domain.Operation{
		SessionID: session.ID,
		Kind:      "prototype_generation",
		Status:    "running",
		Summary:   fmt.Sprintf("Building prototype iteration %d", iteration.Number),
	})
	if err != nil {
		iteration.Status = "failed"
		iteration.Error = err.Error()
		_ = s.store.FinishPrototypeIteration(ctx, iteration)
		return domain.PrototypeIteration{}, &Fault{Code: FaultInternal, Public: "Could not start prototype generation", Cause: err}
	}
	_, _ = s.store.CreateMessage(ctx, domain.Message{
		SessionID: session.ID,
		Role:      "system",
		Kind:      "activity",
		Content:   fmt.Sprintf("Building local prototype iteration %d", iteration.Number),
		Status:    "complete",
	})

	go s.run(session, iteration, operation)
	return iteration, nil
}

func (s *PrototypeService) run(session domain.Session, iteration domain.PrototypeIteration, operation domain.Operation) {
	buildCtx, cancelBuild := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancelBuild()
	appendProgress := func(message string) {
		if strings.TrimSpace(message) == "" || (len(iteration.Progress) > 0 && iteration.Progress[len(iteration.Progress)-1] == message) {
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

	result, buildErr := s.build.BuildPrototype(buildCtx, session, iteration.RootPath, appendProgress)
	iteration.Summary = result.Summary
	iteration.Artifacts = result.Artifacts
	iteration.Checks = result.Checks
	persistCtx, cancelPersist := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPersist()
	switch {
	case buildErr != nil:
		iteration.Status = "failed"
		iteration.Error = buildErr.Error()
		appendProgress("Build failed: " + buildErr.Error())
		s.log.Error("prototype generation failed", "sessionId", session.ID, "iterationId", iteration.ID, "error", buildErr)
		_ = s.store.FinishOperation(persistCtx, operation.ID, "failed", "Prototype generation failed", buildErr.Error())
	case prototypeChecksFailed(result.Checks):
		iteration.Status = "failed"
		iteration.Error = "Prototype validation failed"
		appendProgress("Prototype validation failed")
		_ = s.store.FinishOperation(persistCtx, operation.ID, "failed", "Prototype validation failed", iteration.Error)
	default:
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
	_, _ = s.store.CreateMessage(persistCtx, domain.Message{
		SessionID: session.ID, Role: "system", Kind: "activity", Content: completion, Status: "complete",
	})
}

func prototypeChecksFailed(checks []domain.PrototypeCheck) bool {
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
