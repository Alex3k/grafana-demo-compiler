package store

import (
	"context"
	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
	"path/filepath"
	"testing"
)

func TestRevisionApprovalIsSingleUseAndSessionBound(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	session, err := s.CreateSession(ctx, "revision")
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.CreateRevision(ctx, domain.RevisionProposal{SessionID: session.ID, Goal: "Fix tracing", Files: []string{"main.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimRevision(ctx, "other", r.ID, "approved"); err == nil {
		t.Fatal("cross-session approval")
	}
	if _, err := s.ClaimRevision(ctx, session.ID, r.ID, "approved"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimRevision(ctx, session.ID, r.ID, "approved"); err == nil {
		t.Fatal("approval replay")
	}
	items, err := s.ListRevisions(ctx, session.ID)
	if err != nil || len(items) != 1 || items[0].Status != "approved" {
		t.Fatal(items, err)
	}
}
