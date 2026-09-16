package chat

import (
	"testing"

	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
)

func TestModelMessagesKeepsCompletedConversationOnly(t *testing.T) {
	messages := []domain.Message{
		{Role: "user", Kind: "message", Status: "complete", Content: "Build an IoT demo"},
		{Role: "system", Kind: "activity", Status: "complete", Content: "Planning"},
		{Role: "assistant", Kind: "message", Status: "complete", Content: "Who is the audience?"},
		{Role: "assistant", Kind: "message", Status: "failed", Content: "partial"},
		{Role: "assistant", Kind: "message", Status: "streaming", Content: ""},
		{Role: "user", Kind: "message", Status: "complete", Content: "Plant operations leaders"},
		{Role: "user", Kind: "message", Status: "complete", Content: "Focus on downtime"},
	}

	result := modelMessages(messages)
	if len(result) != 3 {
		t.Fatalf("message count = %d, want 3", len(result))
	}
	if len(result[2].Content) != 1 || result[2].Content[0].Text != "Plant operations leaders\n\nFocus on downtime" {
		t.Fatalf("coalesced user content = %#v", result[2].Content)
	}
}
