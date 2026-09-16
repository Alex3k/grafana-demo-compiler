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
