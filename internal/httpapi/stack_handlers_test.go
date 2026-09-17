package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Alex3k/grafana-demo-compiler/internal/application"
	"github.com/Alex3k/grafana-demo-compiler/internal/deployment"
	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
	"github.com/Alex3k/grafana-demo-compiler/internal/store"
)

type stackOnlyRunner struct{ application.DeploymentRunner }

func (stackOnlyRunner) Provision(context.Context, string, string, string, func(string)) (deployment.Stack, error) {
	return deployment.Stack{URL: "https://demo.grafana.net", InstanceID: "123"}, nil
}

func TestCreateStackEndpointNeedsNoPrototype(t *testing.T) {
	ctx := context.Background()
	data, err := store.Open(ctx, filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	session, err := data.CreateSession(ctx, "Stack first")
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{deployment: application.NewDeploymentService(data, stackOnlyRunner{}, nil, t.TempDir())}
	request := httptest.NewRequest(http.MethodPost, "/api/sessions/"+session.ID+"/stack", strings.NewReader(`{"region":"prod-us-east-0"}`))
	request.SetPathValue("id", session.ID)
	response := httptest.NewRecorder()
	server.createStack(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var item domain.GrafanaStack
	if err := json.Unmarshal(response.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if item.Status != "provisioning" || item.SessionID != session.ID {
		t.Fatalf("response = %#v", item)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		loaded, err := data.GetSession(ctx, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.GrafanaStack != nil && loaded.GrafanaStack.Status == "ready" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("stack did not become ready")
}
