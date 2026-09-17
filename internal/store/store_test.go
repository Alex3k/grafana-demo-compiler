package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
)

func TestLivingBriefCursorSelectsOnlyNewMessages(t *testing.T) {
	ctx := context.Background()
	dataStore, err := Open(ctx, filepath.Join(t.TempDir(), "compiler.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()

	session, err := dataStore.CreateSession(ctx, "Camera fleet demo")
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, time.September, 17, 12, 0, 0, 0, time.UTC)
	messages := []domain.Message{
		{ID: "message-a", SessionID: session.ID, Role: "user", Kind: "message", Content: "first", Status: "complete", CreatedAt: createdAt},
		{ID: "message-b", SessionID: session.ID, Role: "assistant", Kind: "message", Content: "second", Status: "complete", CreatedAt: createdAt.Add(time.Second)},
		{ID: "message-c", SessionID: session.ID, Role: "user", Kind: "message", Content: "third", Status: "complete", CreatedAt: createdAt.Add(2 * time.Second)},
	}
	for _, message := range messages {
		if _, err := dataStore.CreateMessage(ctx, message); err != nil {
			t.Fatal(err)
		}
	}

	brief, err := dataStore.SaveBriefWithCursor(ctx, session.ID, messages[1].ID, domain.BriefContent{Scenario: domain.BriefItem{Value: "Camera outage"}})
	if err != nil {
		t.Fatal(err)
	}
	if brief.SourceMessageID != messages[1].ID {
		t.Fatalf("source message ID = %q, want %q", brief.SourceMessageID, messages[1].ID)
	}
	restored, err := dataStore.GetBrief(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.SourceMessageID != messages[1].ID {
		t.Fatalf("restored source message ID = %q, want %q", restored.SourceMessageID, messages[1].ID)
	}
	newMessages, err := dataStore.ListMessagesSince(ctx, session.ID, restored.SourceMessageID)
	if err != nil {
		t.Fatal(err)
	}
	if len(newMessages) != 1 || newMessages[0].ID != messages[2].ID {
		t.Fatalf("new messages = %#v, want only %q", newMessages, messages[2].ID)
	}

	thread, err := dataStore.OpenBriefThread(ctx, session.ID, domain.BriefFocus{Label: "Telemetry: Logs", Value: "JSON", Status: "proposed"})
	if err != nil {
		t.Fatal(err)
	}
	if err := dataStore.UpdateBriefThreadCandidate(ctx, session.ID, thread.ID, "logfmt"); err != nil {
		t.Fatal(err)
	}
	confirmed, err := dataStore.ApplyBriefThread(ctx, session.ID, thread.ID, domain.BriefContent{Telemetry: []domain.BriefItem{{Name: "Logs", Value: "logfmt", Status: "confirmed"}}})
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.SourceMessageID != messages[1].ID {
		t.Fatalf("confirmed brief source message ID = %q, want carried cursor %q", confirmed.SourceMessageID, messages[1].ID)
	}
}

func TestListLocalCleanupCandidatesReturnsLatestActionableDeploymentPerSession(t *testing.T) {
	ctx := context.Background()
	dataStore, err := Open(ctx, filepath.Join(t.TempDir(), "compiler.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()

	firstSession, err := dataStore.CreateSession(ctx, "First demo")
	if err != nil {
		t.Fatal(err)
	}
	firstPrototype, err := dataStore.CreatePrototypeIteration(ctx, firstSession.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreateDeployment(ctx, domain.Deployment{SessionID: firstSession.ID, PrototypeIterationID: firstPrototype.ID, Target: "local", Region: "region", StackName: "first", StackSlug: "first", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	latestFailed, err := dataStore.CreateDeployment(ctx, domain.Deployment{SessionID: firstSession.ID, PrototypeIterationID: firstPrototype.ID, Target: "local", Region: "region", StackName: "first", StackSlug: "first", Status: "failed"})
	if err != nil {
		t.Fatal(err)
	}

	secondSession, err := dataStore.CreateSession(ctx, "Second demo")
	if err != nil {
		t.Fatal(err)
	}
	secondPrototype, err := dataStore.CreatePrototypeIteration(ctx, secondSession.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreateDeployment(ctx, domain.Deployment{SessionID: secondSession.ID, PrototypeIterationID: secondPrototype.ID, Target: "local", Region: "region", StackName: "second", StackSlug: "second", Status: "needs_token"}); err != nil {
		t.Fatal(err)
	}

	candidates, err := dataStore.ListLocalCleanupCandidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].ID != latestFailed.ID {
		t.Fatalf("cleanup candidates = %#v, want latest %q first and both actionable records", candidates, latestFailed.ID)
	}

	for _, candidate := range candidates {
		candidate.Status = "interrupted"
		candidate.Error = ""
		if err := dataStore.UpdateDeployment(ctx, candidate); err != nil {
			t.Fatal(err)
		}
	}
	candidates, err = dataStore.ListLocalCleanupCandidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("cleaned deployment remained a candidate: %#v", candidates)
	}
}

func TestLivingBriefCursorMigrationBaselinesExistingRows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "compiler.db")
	dataStore, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	session, err := dataStore.CreateSession(ctx, "Existing demo")
	if err != nil {
		t.Fatal(err)
	}
	curatedMessage, err := dataStore.CreateMessage(ctx, domain.Message{SessionID: session.ID, Role: "user", Kind: "message", Content: "already curated", Status: "complete"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SaveBrief(ctx, session.ID, domain.BriefContent{Scenario: domain.BriefItem{Value: "Existing brief"}}); err != nil {
		t.Fatal(err)
	}
	uncuratedMessage, err := dataStore.CreateMessage(ctx, domain.Message{SessionID: session.ID, Role: "user", Kind: "message", Content: "not curated yet", Status: "complete"})
	if err != nil {
		t.Fatal(err)
	}
	if err := dataStore.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE living_briefs DROP COLUMN source_message_id`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	brief, err := migrated.GetBrief(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if brief.SourceMessageID != curatedMessage.ID {
		t.Fatalf("migrated source message ID = %q, want %q", brief.SourceMessageID, curatedMessage.ID)
	}
	delta, err := migrated.ListMessagesSince(ctx, session.ID, brief.SourceMessageID)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta) != 1 || delta[0].ID != uncuratedMessage.ID {
		t.Fatalf("post-migration delta = %#v, want only %q", delta, uncuratedMessage.ID)
	}
}

func TestSessionPersistsAndStreamingMessageIsRecovered(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "compiler.db")

	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	session, err := first.CreateSession(ctx, "Factory line RCA")
	if err != nil {
		t.Fatal(err)
	}
	message, err := first.CreateMessage(ctx, domain.Message{
		SessionID: session.ID,
		Role:      "assistant",
		Kind:      "message",
		Content:   "partial response",
		Status:    "streaming",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	restored, err := second.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Title != session.Title {
		t.Fatalf("restored title = %q, want %q", restored.Title, session.Title)
	}
	if len(restored.Messages) != 1 || restored.Messages[0].ID != message.ID {
		t.Fatalf("restored messages = %#v", restored.Messages)
	}
	if restored.Messages[0].Status != "interrupted" {
		t.Fatalf("restored status = %q, want interrupted", restored.Messages[0].Status)
	}
}

func TestBriefThreadPersistsOutsideMainConversation(t *testing.T) {
	ctx := context.Background()
	dataStore, err := Open(ctx, filepath.Join(t.TempDir(), "compiler.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	session, err := dataStore.CreateSession(ctx, "Camera fleet demo")
	if err != nil {
		t.Fatal(err)
	}
	thread, err := dataStore.OpenBriefThread(ctx, session.ID, domain.BriefFocus{Label: "Telemetry: Structured logs", Value: "Plain-text logs", Status: "proposed"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = dataStore.CreateBriefThreadMessage(ctx, thread.ID, domain.Message{SessionID: session.ID, Role: "user", Kind: "message", Content: "Use JSON logs", Status: "complete"})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := dataStore.OpenBriefThread(ctx, session.ID, thread.Focus)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.ID != thread.ID || len(reopened.Messages) != 1 {
		t.Fatalf("reopened thread = %#v", reopened)
	}
	mainMessages, err := dataStore.ListMessages(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(mainMessages) != 0 {
		t.Fatalf("main messages = %#v, want none", mainMessages)
	}
	if err := dataStore.UpdateBriefThreadCandidate(ctx, session.ID, thread.ID, "Structured JSON logs"); err != nil {
		t.Fatal(err)
	}
	brief, err := dataStore.ApplyBriefThread(ctx, session.ID, thread.ID, domain.BriefContent{Telemetry: []domain.BriefItem{{Name: "Logs", Value: "Structured JSON logs", Status: "confirmed"}}})
	if err != nil {
		t.Fatal(err)
	}
	if brief.Version != 1 || brief.Content.Telemetry[0].Value != "Structured JSON logs" {
		t.Fatalf("applied brief = %#v", brief)
	}
	newDraft, err := dataStore.OpenBriefThread(ctx, session.ID, thread.Focus)
	if err != nil {
		t.Fatal(err)
	}
	if newDraft.ID == thread.ID {
		t.Fatal("confirmed thread was reused instead of creating a new draft")
	}
}

func TestClaimDeploymentStartOnlySucceedsOnce(t *testing.T) {
	ctx := context.Background()
	dataStore, err := Open(ctx, filepath.Join(t.TempDir(), "compiler.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()

	session, err := dataStore.CreateSession(ctx, "Camera fleet demo")
	if err != nil {
		t.Fatal(err)
	}
	iteration, err := dataStore.CreatePrototypeIteration(ctx, session.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := dataStore.CreateDeployment(ctx, domain.Deployment{
		SessionID:            session.ID,
		PrototypeIterationID: iteration.ID,
		Target:               "local",
		Region:               "prod-us-east-0",
		StackName:            "Demo Compiler",
		StackSlug:            "democompilertest",
		Status:               "needs_token",
	})
	if err != nil {
		t.Fatal(err)
	}
	deployment.Status = "starting"
	deployment.Progress = []string{"Telemetry token received securely"}

	claimed, err := dataStore.ClaimDeploymentStart(ctx, deployment)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("first deployment start was not claimed")
	}
	claimed, err = dataStore.ClaimDeploymentStart(ctx, deployment)
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("second deployment start unexpectedly claimed the same deployment")
	}

	stored, err := dataStore.GetDeployment(ctx, session.ID, deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "starting" {
		t.Fatalf("deployment status = %q, want starting", stored.Status)
	}
}
