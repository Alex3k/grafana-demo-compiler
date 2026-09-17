package store

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
)

func TestStackClaimSerializesCreationAndPreservesRetryIdentity(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "stacks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	session, err := s.CreateSession(ctx, "Independent stack")
	if err != nil {
		t.Fatal(err)
	}
	var claimed atomic.Int32
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, ok, err := s.ClaimGrafanaStack(ctx, domain.GrafanaStack{SessionID: session.ID, Region: "bad-region", StackName: "demo", StackSlug: "demo-slug"})
			if err != nil {
				t.Error(err)
			}
			if ok {
				claimed.Add(1)
			}
		}()
	}
	group.Wait()
	if claimed.Load() != 1 {
		t.Fatalf("claims = %d", claimed.Load())
	}
	original, err := s.GetGrafanaStack(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	original.Status = "failed"
	if err := s.UpdateGrafanaStack(ctx, *original); err != nil {
		t.Fatal(err)
	}
	retried, ok, err := s.ClaimGrafanaStack(ctx, domain.GrafanaStack{SessionID: session.ID, Region: "fixed-region", StackName: "changed", StackSlug: "changed"})
	if err != nil || !ok {
		t.Fatalf("retry: %v, %v", ok, err)
	}
	if retried.ID != original.ID || retried.StackSlug != original.StackSlug || retried.Region != "fixed-region" {
		t.Fatalf("retry = %#v", retried)
	}
	loaded, err := s.GetSession(ctx, session.ID)
	if err != nil || loaded.State != "Draft" || loaded.GrafanaStack == nil {
		t.Fatalf("session = %#v, %v", loaded, err)
	}
}

func TestStackCreationAndSessionDeletionClaimsAreMutuallyExclusive(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "claims.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for attempt := 0; attempt < 30; attempt++ {
		session, err := s.CreateSession(ctx, "Concurrent claims")
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		stackClaim := make(chan bool, 1)
		deletionClaim := make(chan bool, 1)
		go func() {
			<-start
			_, claimed, err := s.ClaimGrafanaStack(ctx, domain.GrafanaStack{SessionID: session.ID, Region: "region", StackName: "demo", StackSlug: "slug"})
			if err != nil {
				t.Error(err)
			}
			stackClaim <- claimed
		}()
		go func() { <-start; deletionClaim <- s.BeginSessionDeletion(ctx, session.ID) == nil }()
		close(start)
		stackWon, deletionWon := <-stackClaim, <-deletionClaim
		if stackWon == deletionWon {
			t.Fatalf("stack claimed = %v, deletion claimed = %v", stackWon, deletionWon)
		}
		if deletionWon {
			if err := s.BeginSessionDeletion(ctx, session.ID); err != nil {
				t.Fatalf("deletion retry failed: %v", err)
			}
		}
	}
}

func TestMigrationBackfillsExistingDeploymentStackWithoutChangingDeployment(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "migration.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(ctx, "Legacy")
	if err != nil {
		t.Fatal(err)
	}
	prototype, err := s.CreatePrototypeIteration(ctx, session.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateDeployment(ctx, domain.Deployment{SessionID: session.ID, PrototypeIterationID: prototype.ID, Target: "local", Region: "region", StackName: "Legacy", StackSlug: "legacy", StackURL: "https://legacy.grafana.net", OTLPEndpoint: "https://otlp.example/otlp", InstanceID: "123", Status: "needs_token"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	loaded, err := s.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.GrafanaStack == nil || loaded.GrafanaStack.Status != "ready" || loaded.GrafanaStack.StackURL != item.StackURL || loaded.GrafanaStack.OTLPEndpoint != item.OTLPEndpoint || loaded.GrafanaStack.InstanceID != item.InstanceID {
		t.Fatalf("missing migrated stack: %#v", loaded.GrafanaStack)
	}
	if len(loaded.Deployments) != 1 || loaded.Deployments[0].Status != "needs_token" || loaded.Deployments[0].ID != item.ID {
		t.Fatalf("deployment changed: %#v", loaded.Deployments)
	}
}
