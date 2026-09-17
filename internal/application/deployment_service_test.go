package application

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Alex3k/grafana-demo-compiler/internal/deployment"
	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
)

type deploymentStoreStub struct {
	mu         sync.Mutex
	session    domain.Session
	deployment domain.Deployment
	active     []domain.Deployment
	sessions   map[string]domain.Session
	states     map[string]string
	updates    chan domain.Deployment
}

func (s *deploymentStoreStub) GetSession(_ context.Context, id string) (domain.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session, ok := s.sessions[id]; ok {
		return session, nil
	}
	return s.session, nil
}
func (s *deploymentStoreStub) CreateDeployment(_ context.Context, item domain.Deployment) (domain.Deployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item.ID = "deployment-1"
	s.deployment = item
	return item, nil
}
func (s *deploymentStoreStub) GetDeployment(context.Context, string, string) (domain.Deployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deployment, nil
}
func (s *deploymentStoreStub) UpdateDeployment(_ context.Context, item domain.Deployment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deployment = item
	for index := range s.active {
		if s.active[index].ID == item.ID {
			s.active[index] = item
		}
	}
	select {
	case s.updates <- item:
	default:
	}
	return nil
}
func (s *deploymentStoreStub) ListLocalCleanupCandidates(context.Context) ([]domain.Deployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.Deployment(nil), s.active...), nil
}
func (s *deploymentStoreStub) ClaimDeploymentStart(_ context.Context, item domain.Deployment) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deployment = item
	return true, nil
}
func (s *deploymentStoreStub) SetSessionState(_ context.Context, id, state string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.states == nil {
		s.states = map[string]string{}
	}
	s.states[id] = state
	return nil
}

func (s *deploymentStoreStub) activeStatusesAndSessionState(sessionID string) ([]string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	statuses := make([]string, len(s.active))
	for index := range s.active {
		statuses[index] = s.active[index].Status
	}
	return statuses, s.states[sessionID]
}

type deploymentRunnerStub struct {
	started chan string
	actions chan string
	stopErr error
}

func (r *deploymentRunnerStub) Provision(_ context.Context, _, _, _ string, progress func(string)) (deployment.Stack, error) {
	progress("creating stack")
	return deployment.Stack{URL: "https://demo.grafana.net", OTLPEndpoint: "https://otlp-gateway-prod-us-east-0.grafana.net/otlp", InstanceID: "123"}, nil
}
func (r *deploymentRunnerStub) ResolveOTLP(context.Context, string, string) (string, error) {
	return "https://otlp-gateway-prod-us-east-0.grafana.net/otlp", nil
}
func (r *deploymentRunnerStub) StopLocal(_ context.Context, root, _ string, progress func(string)) error {
	progress("stopping")
	if r.actions != nil {
		r.actions <- "stop:" + root
	}
	return r.stopErr
}
func (r *deploymentRunnerStub) StartLocal(_ context.Context, _, _, _, _, token, _ string, progress func(string)) error {
	progress("starting")
	if r.actions != nil {
		r.actions <- "start"
	}
	r.started <- token
	return nil
}

func TestDeploymentServiceStartProvisionsInBackground(t *testing.T) {
	prototype := domain.PrototypeIteration{ID: "prototype-1", Status: "complete"}
	dataStore := &deploymentStoreStub{
		session: domain.Session{ID: "ABCDEF123456789", Title: "Camera demo", Prototypes: []domain.PrototypeIteration{prototype}},
		updates: make(chan domain.Deployment, 10),
	}
	runner := &deploymentRunnerStub{started: make(chan string, 1)}
	service := NewDeploymentService(dataStore, runner, slog.New(slog.NewTextHandler(io.Discard, nil)), t.TempDir())

	accepted, fault := service.Start(context.Background(), dataStore.session.ID, "local", "prod-us-east-0")
	if fault != nil {
		t.Fatalf("Start returned fault: %v", fault)
	}
	if accepted.Status != "provisioning" || accepted.StackSlug != "democompilerabcdef123456" {
		t.Fatalf("unexpected accepted deployment: %#v", accepted)
	}

	deadline := time.After(time.Second)
	for {
		select {
		case updated := <-dataStore.updates:
			if updated.Status == "needs_token" {
				return
			}
		case <-deadline:
			t.Fatal("background provisioning did not reach needs_token")
		}
	}
}

func TestDeploymentServiceConfigureTokenStartsLocalWithoutPersistingToken(t *testing.T) {
	root := t.TempDir()
	prototype := domain.PrototypeIteration{ID: "prototype-1", Status: "complete", RootPath: root + "/iteration"}
	dataStore := &deploymentStoreStub{
		session: domain.Session{ID: "session-1", Prototypes: []domain.PrototypeIteration{prototype}},
		deployment: domain.Deployment{
			ID: "deployment-1", SessionID: "session-1", PrototypeIterationID: "prototype-1",
			Status: "needs_token", StackSlug: "democompilersession1", InstanceID: "123",
		},
		updates: make(chan domain.Deployment, 10),
	}
	runner := &deploymentRunnerStub{started: make(chan string, 1)}
	service := NewDeploymentService(dataStore, runner, nil, root)

	accepted, fault := service.ConfigureToken(context.Background(), "session-1", "deployment-1", "secret-token")
	if fault != nil {
		t.Fatalf("ConfigureToken returned fault: %v", fault)
	}
	if accepted.Status != "starting" || accepted.OTLPEndpoint == "" {
		t.Fatalf("unexpected accepted deployment: %#v", accepted)
	}
	if accepted.Error == "secret-token" {
		t.Fatal("token was exposed through persisted deployment")
	}

	select {
	case token := <-runner.started:
		if token != "secret-token" {
			t.Fatalf("runner received wrong token %q", token)
		}
	case <-time.After(time.Second):
		t.Fatal("local deployment was not started")
	}
}

func TestDeploymentServiceRejectsRemoteTarget(t *testing.T) {
	service := NewDeploymentService(&deploymentStoreStub{}, &deploymentRunnerStub{}, nil, t.TempDir())
	_, fault := service.Start(context.Background(), "session-1", "aws", "prod-us-east-0")
	if fault == nil || fault.Code != FaultUnprocessable {
		t.Fatalf("expected unprocessable fault, got %#v", fault)
	}
}

func TestDeploymentServiceAllowsAValidatedIterationToReplaceRunningSession(t *testing.T) {
	prototype := domain.PrototypeIteration{ID: "prototype-2", Status: "complete"}
	dataStore := &deploymentStoreStub{
		session: domain.Session{
			ID: "session-1", Title: "Camera demo", Prototypes: []domain.PrototypeIteration{prototype},
			Deployments: []domain.Deployment{{ID: "deployment-1", Status: "running"}},
		},
		updates: make(chan domain.Deployment, 10),
	}
	service := NewDeploymentService(dataStore, &deploymentRunnerStub{started: make(chan string, 1)}, nil, t.TempDir())

	accepted, fault := service.Start(context.Background(), dataStore.session.ID, "local", "prod-us-east-0")
	if fault != nil {
		t.Fatalf("Start returned fault: %v", fault)
	}
	if accepted.Status != "provisioning" {
		t.Fatalf("replacement status = %q, want provisioning", accepted.Status)
	}
}

func TestDeploymentServiceStopsPreviousDemoBeforeStartingReplacement(t *testing.T) {
	root := t.TempDir()
	oldRoot := filepath.Join(root, "old")
	failedRoot := filepath.Join(root, "failed")
	newRoot := filepath.Join(root, "new")
	oldDeployment := domain.Deployment{ID: "old-deployment", SessionID: "old-session", PrototypeIterationID: "old-prototype", Target: "local", Status: "running"}
	failedDeployment := domain.Deployment{ID: "failed-deployment", SessionID: "old-session", PrototypeIterationID: "failed-prototype", Target: "local", Status: "failed"}
	newDeployment := domain.Deployment{
		ID: "new-deployment", SessionID: "new-session", PrototypeIterationID: "new-prototype",
		Status: "needs_token", StackSlug: "democompilernew", InstanceID: "456", OTLPEndpoint: "https://otlp-gateway-prod-us-east-0.grafana.net/otlp",
	}
	dataStore := &deploymentStoreStub{
		deployment: newDeployment,
		active:     []domain.Deployment{failedDeployment, oldDeployment},
		sessions: map[string]domain.Session{
			"new-session": {ID: "new-session", Prototypes: []domain.PrototypeIteration{{ID: "new-prototype", Status: "complete", RootPath: newRoot}}},
			"old-session": {ID: "old-session", Prototypes: []domain.PrototypeIteration{
				{ID: "failed-prototype", Status: "complete", RootPath: failedRoot},
				{ID: "old-prototype", Status: "complete", RootPath: oldRoot},
			}},
		},
		updates: make(chan domain.Deployment, 20),
	}
	runner := &deploymentRunnerStub{started: make(chan string, 1), actions: make(chan string, 2)}
	service := NewDeploymentService(dataStore, runner, nil, root)

	if _, fault := service.ConfigureToken(context.Background(), "new-session", "new-deployment", "secret-token"); fault != nil {
		t.Fatalf("ConfigureToken returned fault: %v", fault)
	}
	wantActions := []string{"stop:" + oldRoot, "start"}
	for _, want := range wantActions {
		select {
		case got := <-runner.actions:
			if got != want {
				t.Fatalf("action = %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for action %q", want)
		}
	}
	deadline := time.After(time.Second)
	for {
		select {
		case updated := <-dataStore.updates:
			if updated.ID == "new-deployment" && updated.Status == "running" {
				goto completed
			}
		case <-deadline:
			t.Fatal("replacement was not marked running")
		}
	}

completed:
	statuses, state := dataStore.activeStatusesAndSessionState("old-session")
	if len(statuses) != 2 || statuses[0] != "interrupted" || statuses[1] != "interrupted" || state != "Generated" {
		t.Fatalf("old deployments were not retired: statuses=%v state=%q", statuses, state)
	}
}

func TestDeploymentServiceDoesNotStartReplacementWhenStopFails(t *testing.T) {
	root := t.TempDir()
	oldRoot := filepath.Join(root, "old")
	newRoot := filepath.Join(root, "new")
	dataStore := &deploymentStoreStub{
		deployment: domain.Deployment{
			ID: "new-deployment", SessionID: "new-session", PrototypeIterationID: "new-prototype",
			Status: "needs_token", StackSlug: "democompilernew", InstanceID: "456", OTLPEndpoint: "https://otlp-gateway-prod-us-east-0.grafana.net/otlp",
		},
		active: []domain.Deployment{{ID: "old-deployment", SessionID: "old-session", PrototypeIterationID: "old-prototype", Target: "local", Status: "running"}},
		sessions: map[string]domain.Session{
			"new-session": {ID: "new-session", Prototypes: []domain.PrototypeIteration{{ID: "new-prototype", Status: "complete", RootPath: newRoot}}},
			"old-session": {ID: "old-session", Prototypes: []domain.PrototypeIteration{{ID: "old-prototype", Status: "complete", RootPath: oldRoot}}},
		},
		updates: make(chan domain.Deployment, 20),
	}
	runner := &deploymentRunnerStub{started: make(chan string, 1), actions: make(chan string, 2), stopErr: errors.New("compose down failed")}
	service := NewDeploymentService(dataStore, runner, nil, root)

	if _, fault := service.ConfigureToken(context.Background(), "new-session", "new-deployment", "secret-token"); fault != nil {
		t.Fatalf("ConfigureToken returned fault: %v", fault)
	}
	select {
	case action := <-runner.actions:
		if action != "stop:"+oldRoot {
			t.Fatalf("action = %q", action)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement did not attempt to stop old demo")
	}
	select {
	case token := <-runner.started:
		t.Fatalf("replacement started with token %q after stop failure", token)
	case <-time.After(100 * time.Millisecond):
	}
	deadline := time.After(time.Second)
	for {
		select {
		case updated := <-dataStore.updates:
			if updated.ID == "new-deployment" && updated.Status == "failed" {
				return
			}
		case <-deadline:
			t.Fatal("replacement was not marked failed")
		}
	}
}
