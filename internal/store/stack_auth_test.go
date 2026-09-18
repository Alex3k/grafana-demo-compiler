package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
)

func TestAuthenticationClaimBlocksDeletionAndRecoversAfterRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(ctx, "Demo")
	if err != nil {
		t.Fatal(err)
	}
	stack, _, err := s.ClaimGrafanaStack(ctx, domain.GrafanaStack{SessionID: session.ID, Region: "region", StackName: "Demo", StackSlug: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	stack.Status, stack.StackURL = "needs_auth", "https://demo.grafana.net"
	if err := s.UpdateGrafanaStack(ctx, stack); err != nil {
		t.Fatal(err)
	}
	if claimed, err := s.ClaimStackConnect(ctx, session.ID); err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	if claimed, _ := s.ClaimStackConnect(ctx, session.ID); claimed {
		t.Fatal("duplicate claim")
	}
	if err := s.BeginSessionDeletion(ctx, session.ID); err == nil {
		t.Fatal("deleted during OAuth")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	recovered, err := s.GetGrafanaStack(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != "needs_auth" {
		t.Fatalf("status %s", recovered.Status)
	}
	if err := s.BeginSessionDeletion(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	if claimed, _ := s.ClaimStackConnect(ctx, session.ID); claimed {
		t.Fatal("connected during deletion")
	}
}
