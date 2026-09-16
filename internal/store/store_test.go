package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
)

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
	if err := dataStore.ConfirmBriefThread(ctx, session.ID, thread.ID); err != nil {
		t.Fatal(err)
	}
	newDraft, err := dataStore.OpenBriefThread(ctx, session.ID, thread.Focus)
	if err != nil {
		t.Fatal(err)
	}
	if newDraft.ID == thread.ID {
		t.Fatal("confirmed thread was reused instead of creating a new draft")
	}
}
