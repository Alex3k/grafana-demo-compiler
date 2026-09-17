package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
	"github.com/Alex3k/grafana-demo-compiler/internal/store"
)

type cleanupStub struct {
	DeploymentRunner
	fail  bool
	calls []string
}

func (r *cleanupStub) DeleteLocal(_ context.Context, root, id string) error {
	r.calls = append(r.calls, "local")
	return nil
}
func (r *cleanupStub) DeleteStack(_ context.Context, slug string) error {
	r.calls = append(r.calls, slug)
	if r.fail {
		return errors.New("cloud unavailable")
	}
	return nil
}

func TestSessionDeletionRetriesCleanupAndPreservesOtherSessions(t *testing.T) {
	ctx := context.Background()
	data, err := store.Open(ctx, filepath.Join(t.TempDir(), "compiler.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	session, err := data.CreateSession(ctx, "Delete me")
	if err != nil {
		t.Fatal(err)
	}
	other, err := data.CreateSession(ctx, "Keep me")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	p, err := data.CreatePrototypeIteration(ctx, session.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	p.RootPath = filepath.Join(root, session.ID, "iterations", "001")
	if err := os.MkdirAll(p.RootPath, 0700); err != nil {
		t.Fatal(err)
	}
	p.Status = "complete"
	if err := data.FinishPrototypeIteration(ctx, p); err != nil {
		t.Fatal(err)
	}
	_, err = data.CreateDeployment(ctx, domain.Deployment{SessionID: session.ID, PrototypeIterationID: p.ID, Target: "local", Status: "running", StackSlug: "democompiler" + session.ID[:12]})
	if err != nil {
		t.Fatal(err)
	}
	runner := &cleanupStub{fail: true}
	service := NewDeploymentService(data, runner, nil, root)
	if err := service.DeleteSession(ctx, data, session.ID); err == nil {
		t.Fatal("expected cleanup failure")
	}
	if _, err := data.GetSession(ctx, session.ID); err != nil {
		t.Fatal("lost retryable session", err)
	}
	if deleting, err := data.SessionDeleting(ctx, session.ID); err != nil || !deleting {
		t.Fatal("missing deletion marker", err)
	}
	if _, err := os.Stat(p.RootPath); err != nil {
		t.Fatal("files removed before cloud cleanup", err)
	}
	runner.fail = false
	if err := service.DeleteSession(ctx, data, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := data.GetSession(ctx, session.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("session remains", err)
	}
	if _, err := os.Stat(p.RootPath); !os.IsNotExist(err) {
		t.Fatal("files remain", err)
	}
	if _, err := data.GetSession(ctx, other.ID); err != nil {
		t.Fatal("other session affected", err)
	}
	if len(runner.calls) != 4 || runner.calls[0] != "local" || runner.calls[1] != "democompiler"+session.ID[:12] {
		t.Fatal(runner.calls)
	}
}

func TestSessionDeletionRejectsActiveWork(t *testing.T) {
	ctx := context.Background()
	data, err := store.Open(ctx, filepath.Join(t.TempDir(), "compiler.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	session, err := data.CreateSession(ctx, "Busy")
	if err != nil {
		t.Fatal(err)
	}
	_, err = data.CreatePrototypeIteration(ctx, session.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	runner := &cleanupStub{}
	service := NewDeploymentService(data, runner, nil, t.TempDir())
	if err := service.DeleteSession(ctx, data, session.ID); err == nil {
		t.Fatal("deleted active build")
	}
	if len(runner.calls) != 0 {
		t.Fatal("cleanup ran for active build")
	}
}
