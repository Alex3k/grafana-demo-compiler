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
	ClaimStackConnect(context.Context, string) (bool, error)
	ClaimGrafanaStack(context.Context, domain.GrafanaStack) (domain.GrafanaStack, bool, error)
	UpdateGrafanaStack(context.Context, domain.GrafanaStack) error
	GetSession(context.Context, string) (domain.Session, error)
	CreateDeployment(context.Context, domain.Deployment) (domain.Deployment, error)
	GetDeployment(context.Context, string, string) (domain.Deployment, error)
	ListLocalCleanupCandidates(context.Context) ([]domain.Deployment, error)
	UpdateDeployment(context.Context, domain.Deployment) error
	ClaimDeploymentStart(context.Context, domain.Deployment) (bool, error)
	SetSessionState(context.Context, string, string) error
}

type DeploymentRunner interface {
	ConnectStack(context.Context, string, string, func(string)) error
	Provision(context.Context, string, string, string, func(string)) (deployment.Stack, error)
	ResolveOTLP(context.Context, string, string) (string, error)
	StopLocal(context.Context, string, string, func(string)) error
	PreflightLocal(context.Context, string) error
	StartLocal(context.Context, string, string, string, string, string, string, string, func(string)) ([]string, error)
	VerifyTelemetry(context.Context, string, string, []string, time.Time, func(string)) error
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

// Start attaches a local application deployment to the session's ready stack.
func (s *DeploymentService) Start(ctx context.Context, sessionID, target, region string) (domain.Deployment, *Fault) {
	target = strings.TrimSpace(strings.ToLower(target))
	if target != "local" {
		return domain.Deployment{}, &Fault{Code: FaultUnprocessable, Public: "This MVP only deploys application services locally with Docker Compose"}
	}
	session, err := s.store.GetSession(ctx, sessionID)
	if errors.Is(err, store.ErrNotFound) {
		return domain.Deployment{}, &Fault{Code: FaultNotFound, Public: "Session not found", Cause: err}
	}
	if err != nil {
		return domain.Deployment{}, &Fault{Code: FaultInternal, Public: "Could not load session", Cause: err}
	}
	stack := session.GrafanaStack
	if stack == nil || stack.Status != "ready" {
		return domain.Deployment{}, &Fault{Code: FaultConflict, Public: "Create a Grafana Cloud stack and wait for it to be ready before deploying"}
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
	item, err := s.store.CreateDeployment(ctx, domain.Deployment{
		SessionID: session.ID, PrototypeIterationID: prototype.ID,
		Target: "local", Region: stack.Region,
		StackName: stack.StackName, StackSlug: stack.StackSlug,
		StackURL: stack.StackURL, OTLPEndpoint: stack.OTLPEndpoint, InstanceID: stack.InstanceID,
		Status:   "needs_token",
		Progress: []string{"Using the session's Grafana Cloud stack; an OTLP access-policy token is required"},
	})
	if err != nil {
		return domain.Deployment{}, &Fault{Code: FaultInternal, Public: "Could not create deployment", Cause: err}
	}
	return item, nil
}

// CreateStack provisions Grafana independently of prototype generation.
func (s *DeploymentService) CreateStack(ctx context.Context, sessionID, region string) (domain.GrafanaStack, *Fault) {
	region = strings.TrimSpace(region)
	if region == "" {
		return domain.GrafanaStack{}, &Fault{Code: FaultInvalid, Public: "Grafana Cloud region is required"}
	}
	session, err := s.store.GetSession(ctx, sessionID)
	if errors.Is(err, store.ErrNotFound) {
		return domain.GrafanaStack{}, &Fault{Code: FaultNotFound, Public: "Session not found", Cause: err}
	}
	if err != nil {
		return domain.GrafanaStack{}, &Fault{Code: FaultInternal, Public: "Could not load session", Cause: err}
	}
	if session.GrafanaStack != nil && session.GrafanaStack.Status != "failed" {
		return domain.GrafanaStack{}, &Fault{Code: FaultConflict, Public: "This session already has a Grafana Cloud stack"}
	}
	shortID := session.ID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	item, claimed, err := s.store.ClaimGrafanaStack(ctx, domain.GrafanaStack{
		SessionID: session.ID, Region: region, StackName: session.Title + " demo " + shortID,
		StackSlug: "democompiler" + strings.ToLower(shortID), Status: "provisioning",
		Progress: []string{"Grafana Cloud stack creation accepted"},
	})
	if err != nil {
		return domain.GrafanaStack{}, &Fault{Code: FaultInternal, Public: "Could not create Grafana Cloud stack", Cause: err}
	}
	if !claimed {
		return domain.GrafanaStack{}, &Fault{Code: FaultConflict, Public: "This session already has a Grafana Cloud stack or is being deleted"}
	}
	go s.provision(item)
	return item, nil
}

func (s *DeploymentService) ConnectStack(ctx context.Context, sessionID string) (domain.GrafanaStack, *Fault) {
	session, err := s.store.GetSession(ctx, sessionID)
	if errors.Is(err, store.ErrNotFound) {
		return domain.GrafanaStack{}, &Fault{Code: FaultNotFound, Public: "Session not found"}
	}
	if err != nil {
		return domain.GrafanaStack{}, &Fault{Code: FaultInternal, Public: "Could not load session", Cause: err}
	}
	if session.GrafanaStack == nil {
		return domain.GrafanaStack{}, &Fault{Code: FaultConflict, Public: "Create the Grafana stack before connecting"}
	}
	claimed, err := s.store.ClaimStackConnect(ctx, sessionID)
	if err != nil {
		return domain.GrafanaStack{}, &Fault{Code: FaultInternal, Public: "Could not start Grafana connection", Cause: err}
	}
	if !claimed {
		return domain.GrafanaStack{}, &Fault{Code: FaultConflict, Public: "Grafana stack is busy or being deleted"}
	}
	item := *session.GrafanaStack
	item.Status, item.Error = "awaiting_auth", ""
	go s.connectStack(item)
	return item, nil
}

func (s *DeploymentService) connectStack(item domain.GrafanaStack) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	progress := func(message string) {
		item.Progress = append(item.Progress, message)
		persistCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.store.UpdateGrafanaStack(persistCtx, item); err != nil {
			s.log.Error("save Grafana connection progress", "sessionId", item.SessionID, "error", err)
		}
	}
	if err := s.runner.ConnectStack(ctx, item.StackSlug, item.StackURL, progress); err != nil {
		item.Status = "needs_auth"
		item.Error = "Grafana connection was not completed. Click Connect Grafana to retry browser approval."
		progress(item.Error)
		return
	}
	item.Status, item.Error = "ready", ""
	progress("Grafana Cloud stack is connected and ready")
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

func (s *DeploymentService) provision(item domain.GrafanaStack) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	appendProgress := func(message string) {
		if strings.TrimSpace(message) == "" {
			return
		}
		item.Progress = append(item.Progress, message)
		persistCtx, cancelPersist := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelPersist()
		if err := s.store.UpdateGrafanaStack(persistCtx, item); err != nil {
			s.log.Error("save stack progress", "sessionId", item.SessionID, "error", err)
		}
	}
	stack, err := s.runner.Provision(ctx, item.Region, item.StackName, item.StackSlug, appendProgress)
	if err != nil {
		item.Status = "failed"
		item.Error = err.Error()
		appendProgress("Stack provisioning stopped: " + actionableDeploymentError(err))
	} else {
		item.StackURL, item.OTLPEndpoint, item.InstanceID = stack.URL, stack.OTLPEndpoint, stack.InstanceID
		item.Status = "awaiting_auth"
		appendProgress("Grafana Cloud stack created; connecting Grafana")
	}
	persistCtx, cancelPersist := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPersist()
	if err := s.store.UpdateGrafanaStack(persistCtx, item); err != nil {
		s.log.Error("finish stack provisioning", "sessionId", item.SessionID, "deploymentId", item.ID, "error", err)
		return
	}
	if item.Status == "awaiting_auth" {
		s.connectStack(item)
	}
}

func (s *DeploymentService) startLocal(item domain.Deployment, root, token string) {
	s.localMu.Lock()
	defer s.localMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	appendProgress := s.deploymentProgressAppender(&item)
	startedAt := time.Now().UTC()
	var services []string
	err := s.runner.PreflightLocal(ctx, root)
	if err == nil {
		err = s.stopActiveLocalDeployment(ctx, item.ID, appendProgress)
	}
	if err == nil {
		services, err = s.runner.StartLocal(ctx, root, item.StackSlug, item.OTLPEndpoint, item.InstanceID, token, item.SessionID, item.ID, appendProgress)
	}
	token = ""
	if err != nil {
		item.Status = "failed"
		item.Error = err.Error()
		appendProgress("Local deployment failed: " + err.Error())
	} else {
		item.Status = "verifying"
		appendProgress("Application ready; verifying metrics, logs, and traces in Grafana Cloud")
		_ = s.store.SetSessionState(context.Background(), item.SessionID, "Running")
		if err := s.runner.VerifyTelemetry(ctx, item.StackSlug, item.ID, services, startedAt, appendProgress); err != nil {
			item.Status = "running"
			item.Error = "Telemetry verification failed: " + err.Error()
			appendProgress("Application was ready, but telemetry delivery could not be verified. " + item.Error)
		} else {
			item.Status = "verified"
			item.Error = ""
			appendProgress("Telemetry verified: deployment probes from every application service reached metrics, logs, and traces. Demo-specific queries are not evaluated by this check.")
			_ = s.store.SetSessionState(context.Background(), item.SessionID, "Verified")
		}
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
