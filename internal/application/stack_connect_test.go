package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
)

func TestStackConnectFailureCanRetryWithoutProvisioning(t *testing.T) {
	data := &deploymentStoreStub{session: domain.Session{ID: "session", GrafanaStack: &domain.GrafanaStack{SessionID: "session", Status: "needs_auth", StackSlug: "demo", StackURL: "https://demo.grafana.net"}}, stackUpdates: make(chan domain.GrafanaStack, 20)}
	runner := &deploymentRunnerStub{connectErr: errors.New("cancelled"), connectWait: make(chan struct{})}
	service := NewDeploymentService(data, runner, nil, t.TempDir())
	item, fault := service.ConnectStack(context.Background(), "session")
	if fault != nil || item.Status != "awaiting_auth" {
		t.Fatalf("connect: %#v %v", item, fault)
	}
	if _, fault := service.ConnectStack(context.Background(), "session"); fault == nil {
		t.Fatal("duplicate login accepted")
	}
	close(runner.connectWait)
	waitStatus := func(status string) {
		t.Helper()
		for {
			select {
			case item := <-data.stackUpdates:
				if item.Status == status {
					return
				}
			case <-time.After(time.Second):
				t.Fatalf("did not reach %s", status)
			}
		}
	}
	waitStatus("needs_auth")
	runner.connectErr = nil
	if _, fault := service.ConnectStack(context.Background(), "session"); fault != nil {
		t.Fatal(fault)
	}
	waitStatus("ready")
	if runner.provisions.Load() != 0 {
		t.Fatal("connecting reprovisioned stack")
	}
}
