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

func TestAcceptedAssistantPlanUsesProposalBeforeLatestHumanTurn(t *testing.T) {
	messages := []domain.Message{
		{Role: "assistant", Kind: "message", Status: "complete", Content: "Initial questions"},
		{Role: "user", Kind: "message", Status: "complete", Content: "Manufacturing operators"},
		{Role: "assistant", Kind: "message", Status: "complete", Content: "Bounded prototype plan"},
		{Role: "user", Kind: "message", Status: "complete", Content: "I accept this plan"},
		{Role: "assistant", Kind: "message", Status: "complete", Content: "Thanks, I will prepare it"},
	}

	if got := acceptedAssistantPlan(messages); got != "Bounded prototype plan" {
		t.Fatalf("accepted plan = %q, want bounded prototype plan", got)
	}
}

func TestSanitizeAssistantTextRemovesInternalCompletionMarker(t *testing.T) {
	got := sanitizeAssistantText("Ready for generation.\n\n<turn_complete>")
	if got != "Ready for generation." {
		t.Fatalf("sanitized text = %q", got)
	}
}
