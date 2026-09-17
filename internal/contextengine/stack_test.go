package contextengine

import (
	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
	"testing"
)

func TestIndependentStackInOperationalContext(t *testing.T) {
	session := domain.Session{GrafanaStack: &domain.GrafanaStack{StackSlug: "demo", Status: "ready", Progress: []string{"long history"}}}
	packet, err := New(0).Collaborator(session)
	if err != nil {
		t.Fatal(err)
	}
	if packet.OperationalState.GrafanaStack == nil || packet.OperationalState.GrafanaStack.StackSlug != "demo" {
		t.Fatal("stack missing without prototype")
	}
	if len(packet.OperationalState.GrafanaStack.Progress) != 0 || len(session.GrafanaStack.Progress) != 1 {
		t.Fatal("stack history copied or source mutated")
	}
}
