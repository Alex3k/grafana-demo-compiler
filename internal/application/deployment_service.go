package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Alex3k/grafana-demo-compiler/internal/deployment"
	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
	"github.com/Alex3k/grafana-demo-compiler/internal/store"
)

type DeploymentStore interface {
	GetSession(context.Context, string) (domain.Session, error)
	CreateDeployment(context.Context, domain.Deployment) (domain.Deployment, error)
	GetDeployment(context.Context, string, string) (domain.Deployment, error)
	ListLocalCleanupCandidates(context.Context) ([]domain.Deployment, error)
	UpdateDeployment(context.Context, domain.Deployment) error
	ClaimDeploymentStart(context.Context, domain.Deployment) (bool, error)
	SetSessionState(context.Context, string, string) error
}

type DeploymentRunner interface {
	Provision(context.Context, string, string, string, func(string)) (deployment.Stack, error)
	ResolveOTLP(context.Context, string, string) (string, error)
	StopLocal(context.Context, string, string, func(string)) error
	StartLocal(context.Context, string, string, string, string, string, string, func(string)) error
}

type DeploymentService struct {
	store   DeploymentStore
	runner  DeploymentRunner
	log     *slog.Logger
	root    string
	localMu sync.Mutex
}

func NewDeploymentService(dataStore DeploymentStore, runner DeploymentRunner, logger *slog.Logger, prototypeRoot string) *DeploymentService {
	if logger == nil {
		logger = slog.Default()
	}
	return &DeploymentService{store: dataStore, runner: runner, log: logger, root: prototypeRoot}
}

// Start validates a local-only deployment request, records it, and detaches
// Grafana Cloud stack provisioning from the caller's context.
func (s *DeploymentService) Start(ctx context.Context, sessionID, target, region string) (domain.Deployment, *Fault) {
	target = strings.TrimSpace(strings.ToLower(target))
	region = strings.TrimSpace(region)
	if target != "local" {
		return domain.Deployment{}, &Fault{Code: FaultUnprocessable, Public: "This MVP only deploys application services locally with Docker Compose"}
	}
	if region == "" {
		return domain.Deployment{}, &Fault{Code: FaultInvalid, Public: "Grafana Cloud region is required"}
	}
	session, err := s.store.GetSession(ctx, sessionID)
	if errors.Is(err, store.ErrNotFound) {
		return domain.Deployment{}, &Fault{Code: FaultNotFound, Public: "Session not found", Cause: err}
	}
	if err != nil {
		return domain.Deployment{}, &Fault{Code: FaultInternal, Public: "Could not load session", Cause: err}
	}
	prototype := latestCompletedPrototype(session.Prototypes)
	if prototype == nil {
		return domain.Deployment{}, &Fault{Code: FaultConflict, Public: "Generate and validate a prototype before deploying it"}
	}
	for _, existing := range session.Deployments {
		switch existing.Status {
		case "provisioning", "needs_token", "starting", "verifying":
			return domain.Deployment{}, &Fault{Code: FaultConflict, Public: "This session already has an active local deployment"}
		}
	}
	shortID := session.ID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	item, err := s.store.CreateDeployment(ctx, domain.Deployment{
		SessionID: session.ID, PrototypeIterationID: prototype.ID,
		Target: "local", Region: region,
		StackName: session.Title + " demo " + shortID,
		StackSlug: "democompiler" + strings.ToLower(shortID),
		Status:    "provisioning",
		Progress:  []string{"Deployment accepted: local Docker Compose only"},
	})
	if err != nil {
		return domain.Deployment{}, &Fault{Code: FaultInternal, Public: "Could not create deployment", Cause: err}
	}
	go s.provision(item)
	return item, nil
}

// ConfigureToken claims a waiting deployment, resolves missing connection
// details, and starts the local application asynchronously. The token is only
// passed in memory to the runner and is never included in persisted state.
func (s *DeploymentService) ConfigureToken(ctx context.Context, sessionID, deploymentID, token string) (domain.Deployment, *Fault) {
	item, err := s.store.GetDeployment(ctx, sessionID, deploymentID)
	if errors.Is(err, store.ErrNotFound) {
		return domain.Deployment{}, &Fault{Code: FaultNotFound, Public: "Deployment not found", Cause: err}
	}
	if err != nil {
		return domain.Deployment{}, &Fault{Code: FaultInternal, Public: "Could not load deployment", Cause: err}
	}
	if item.Status != "needs_token" {
		return domain.Deployment{}, &Fault{Code: FaultConflict, Public: "This deployment is not waiting for a telemetry token"}
	}
	if strings.TrimSpace(token) == "" {
		return domain.Deployment{}, &Fault{Code: FaultInvalid, Public: "An OTLP access-policy token is required"}
	}
	session, err := s.store.GetSession(ctx, item.SessionID)
	if err != nil {
		return domain.Deployment{}, &Fault{Code: FaultInternal, Public: "Could not load deployment session", Cause: err}
	}
	var iteration *domain.PrototypeIteration
	for index := range session.Prototypes {
		if session.Prototypes[index].ID == item.PrototypeIterationID {
			iteration = &session.Prototypes[index]
			break
		}
	}
	if iteration == nil || !pathWithinRoot(s.root, iteration.RootPath) {
		return domain.Deployment{}, &Fault{Code: FaultConflict, Public: "The deployment prototype path is unavailable"}
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
		return domain.Deployment{}, &Fault{Code: FaultInternal, Public: "Could not start local deployment", Cause: err}
	}
	if !claimed {
		return domain.Deployment{}, &Fault{Code: FaultConflict, Public: "This deployment is already being configured"}
	}

	if item.OTLPEndpoint == "" {
		resolveCtx, cancelResolve := context.WithTimeout(context.Background(), 30*time.Second)
		endpoint, resolveErr := s.runner.ResolveOTLP(resolveCtx, item.StackSlug, token)
		cancelResolve()
		if resolveErr != nil {
			item.Status = "needs_token"
			item.Error = "Could not read this stack's OTLP connection details. Check that the token includes stacks:read and retry."
			item.Progress = append(item.Progress, item.Error)
			persistCtx, cancelPersist := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancelPersist()
			if err := s.store.UpdateDeployment(persistCtx, item); err != nil {
				s.log.Error("save OTLP resolution failure", "sessionId", item.SessionID, "deploymentId", item.ID, "error", err)
			}
			return domain.Deployment{}, &Fault{Code: FaultBadGateway, Public: "Could not read this stack's OTLP connection details. Check the Cloud Access Policy token and retry.", Cause: resolveErr}
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
		return domain.Deployment{}, &Fault{Code: FaultInternal, Public: "Could not start local deployment", Cause: err}
	}
	go s.startLocal(item, iteration.RootPath, token)
	return item, nil
}

func (s *DeploymentService) provision(item domain.Deployment) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	appendProgress := s.deploymentProgressAppender(&item)
	stack, err := s.runner.Provision(ctx, item.Region, item.StackName, item.StackSlug, appendProgress)
	if err != nil {
		item.Status = "failed"
		item.Error = err.Error()
		appendProgress("Stack provisioning stopped: " + actionableDeploymentError(err))
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

func (s *DeploymentService) startLocal(item domain.Deployment, root, token string) {
	s.localMu.Lock()
	defer s.localMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	appendProgress := s.deploymentProgressAppender(&item)
	err := s.stopActiveLocalDeployment(ctx, item.ID, appendProgress)
	if err == nil {
		err = s.runner.StartLocal(ctx, root, item.StackSlug, item.OTLPEndpoint, item.InstanceID, token, item.SessionID, appendProgress)
	}
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

func (s *DeploymentService) stopActiveLocalDeployment(ctx context.Context, nextDeploymentID string, progress func(string)) error {
	active, err := s.store.ListLocalCleanupCandidates(ctx)
	if err != nil {
		return fmt.Errorf("list active local deployments: %w", err)
	}
	sort.SliceStable(active, func(left, right int) bool {
		return localCleanupPriority(active[left].Status) < localCleanupPriority(active[right].Status)
	})
	stoppedSessions := make(map[string]bool)
	for _, existing := range active {
		if existing.ID == nextDeploymentID {
			continue
		}
		if !stoppedSessions[existing.SessionID] {
			session, err := s.store.GetSession(ctx, existing.SessionID)
			if err != nil {
				return fmt.Errorf("load active local deployment: %w", err)
			}
			var root string
			for _, iteration := range session.Prototypes {
				if iteration.ID == existing.PrototypeIterationID {
					root = iteration.RootPath
					break
				}
			}
			if root == "" || !pathWithinRoot(s.root, root) {
				return errors.New("active local deployment path is unavailable")
			}
			progress("Stopping the previously running local demo")
			if err := s.runner.StopLocal(ctx, root, existing.SessionID, progress); err != nil {
				return fmt.Errorf("stop previous local deployment: %w", err)
			}
			if err := s.store.SetSessionState(ctx, existing.SessionID, "Generated"); err != nil {
				return fmt.Errorf("update stopped deployment session: %w", err)
			}
			stoppedSessions[existing.SessionID] = true
		}
		existing.Status = "interrupted"
		existing.Error = ""
		existing.Progress = append(existing.Progress, "Local services stopped before starting another demo; Grafana Cloud stack preserved")
		if err := s.store.UpdateDeployment(ctx, existing); err != nil {
			return fmt.Errorf("record stopped local deployment: %w", err)
		}
	}
	return nil
}

func localCleanupPriority(status string) int {
	switch status {
	case "running", "verifying", "verified":
		return 0
	default:
		return 1
	}
}

func (s *DeploymentService) deploymentProgressAppender(item *domain.Deployment) func(string) {
	return func(message string) {
		if strings.TrimSpace(message) == "" || (len(item.Progress) > 0 && item.Progress[len(item.Progress)-1] == message) {
			return
		}
		item.Progress = append(item.Progress, message)
		persistCtx, cancelPersist := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelPersist()
		if err := s.store.UpdateDeployment(persistCtx, *item); err != nil {
			s.log.Error("save deployment progress", "sessionId", item.SessionID, "deploymentId", item.ID, "error", err)
		}
	}
}

func latestCompletedPrototype(iterations []domain.PrototypeIteration) *domain.PrototypeIteration {
	for index := range iterations {
		if iterations[index].Status == "complete" {
			return &iterations[index]
		}
	}
	return nil
}

func actionableDeploymentError(err error) string {
	message := err.Error()
	if strings.Contains(strings.ToLower(message), "expired") || strings.Contains(message, "gcx cloud login") {
		return "gcx Cloud login has expired. Run `gcx cloud login`, then retry deployment."
	}
	return message
}

func pathWithinRoot(root, candidate string) bool {
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
