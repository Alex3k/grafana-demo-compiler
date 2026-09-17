package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Alex3k/grafana-demo-compiler/internal/gcxtool"
)

func TestGCXApprovalIsSessionBoundAndSingleUse(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	session, err := s.CreateSession(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	input := gcxtool.Request{Args: []string{"resources", "push", "-p", "@manifest"}, Manifest: `{"title":"Approved content"}`}
	a, err := s.CreateGCXAction(ctx, gcxtool.Action{SessionID: session.ID, Stack: "democompiler123456abcdef", Request: input, Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimGCXAction(ctx, "other", a.ID, "running"); err == nil {
		t.Fatal("cross-session approval allowed")
	}
	claimed, err := s.ClaimGCXAction(ctx, session.ID, a.ID, "running")
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Request.Manifest != input.Manifest {
		t.Fatal("payload changed")
	}
	if _, err = s.ClaimGCXAction(ctx, session.ID, a.ID, "running"); err == nil {
		t.Fatal("approval replay allowed")
	}
	if err = s.FinishGCXAction(ctx, a.ID, "complete", "created"); err != nil {
		t.Fatal(err)
	}
	actions, err := s.ListGCXActions(ctx, session.ID)
	if err != nil || len(actions) != 1 || actions[0].Output != "created" {
		t.Fatalf("receipts: %v %v", actions, err)
	}
}
