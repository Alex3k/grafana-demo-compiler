package chat

import (
	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
	"testing"
)

func TestSessionStackWithoutPrototypeOrDeployment(t *testing.T) {
	session := domain.Session{ID: "123456789012abcdef", GrafanaStack: &domain.GrafanaStack{StackSlug: "democompiler123456789012", StackURL: "https://democompiler123456789012.grafana.net", Status: "ready"}}
	if got := SessionStack(session); got != session.GrafanaStack.StackSlug {
		t.Fatalf("stack = %q", got)
	}
	session.GrafanaStack.Status = "provisioning"
	if SessionStack(session) != "" {
		t.Fatal("unready stack was exposed")
	}
}
