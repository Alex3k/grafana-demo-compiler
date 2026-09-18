package application

import (
	"errors"
	"testing"

	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
)

func TestDeliveryVerificationControlsFinalStatus(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		err        error
	}{
		{"verified", "verified", nil},
		{"missing traces", "running", errors.New("missing gateway traces")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &deploymentStoreStub{}
			runner := &deploymentRunnerStub{started: make(chan string, 1), verifyErr: tc.err}
			service := NewDeploymentService(store, runner, nil, t.TempDir())
			service.startLocal(domain.Deployment{ID: "deployment", SessionID: "session"}, t.TempDir(), "secret")
			if store.deployment.Status != tc.want {
				t.Fatalf("status = %s", store.deployment.Status)
			}
			if tc.err != nil && store.deployment.Error == "" {
				t.Fatal("verification failure was hidden")
			}
			if tc.err == nil && store.states["session"] != "Verified" {
				t.Fatal("successful signals not recorded")
			}
		})
	}
}

func TestLegacyFoundationFailureDoesNotStopExistingDemo(t *testing.T) {
	store := &deploymentStoreStub{}
	runner := &deploymentRunnerStub{started: make(chan string, 1), actions: make(chan string, 2), preflightErr: errors.New("regenerate prototype")}
	service := NewDeploymentService(store, runner, nil, t.TempDir())
	service.startLocal(domain.Deployment{ID: "deployment", SessionID: "session"}, t.TempDir(), "secret")
	if store.deployment.Status != "failed" {
		t.Fatal("legacy prototype accepted")
	}
	if len(runner.actions) != 0 {
		t.Fatal("preflight failure mutated existing deployment")
	}
}
