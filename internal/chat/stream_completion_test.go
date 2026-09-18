package chat

import (
	"errors"
	"testing"

	aisdk "github.com/grafana/ai-sdk"
	"github.com/grafana/ai-sdk/provider"
)

func TestConversationCompletionRejectsAbnormalFinish(t *testing.T) {
	for _, reason := range []provider.UnifiedFinishReason{provider.FinishReasonLength, provider.FinishReasonContentFilter, provider.FinishReasonError, provider.FinishReasonOther} {
		t.Run(string(reason), func(t *testing.T) {
			for _, part := range []aisdk.TextStreamPart{
				aisdk.StreamFinishStep{FinishReason: provider.FinishReason{Unified: reason}},
				aisdk.StreamFinish{FinishReason: provider.FinishReason{Unified: reason}},
			} {
				completion := conversationCompletion{}
				if err := completion.observe(part); !errors.Is(err, ErrIncompleteResponse) {
					t.Fatalf("expected incomplete response, got %v", err)
				}
			}
		})
	}
}

func TestConversationCompletionAcceptsToolsThenNormalStop(t *testing.T) {
	completion := conversationCompletion{}
	for _, part := range []aisdk.TextStreamPart{
		aisdk.StreamToolInputStart{ID: "proposal"},
		aisdk.StreamToolInputEnd{ID: "proposal"},
		aisdk.StreamToolCall{ToolCallID: "proposal", ToolName: "run_gcx"},
		aisdk.StreamFinishStep{FinishReason: provider.FinishReason{Unified: provider.FinishReasonToolCalls}},
		aisdk.StreamFinishStep{FinishReason: provider.FinishReason{Unified: provider.FinishReasonStop}},
		aisdk.StreamFinish{FinishReason: provider.FinishReason{Unified: provider.FinishReasonStop}},
	} {
		if err := completion.observe(part); err != nil {
			t.Fatal(err)
		}
	}
	if !completion.finished {
		t.Fatal("normal response not marked finished")
	}
}

func TestConversationCompletionRejectsIncompleteTool(t *testing.T) {
	completion := conversationCompletion{}
	_ = completion.observe(aisdk.StreamToolInputStart{ID: "proposal"})
	_ = completion.observe(aisdk.StreamToolInputEnd{ID: "proposal"})
	err := completion.observe(aisdk.StreamFinishStep{FinishReason: provider.FinishReason{Unified: provider.FinishReasonStop}})
	if !errors.Is(err, ErrIncompleteResponse) {
		t.Fatalf("expected incomplete tool error, got %v", err)
	}
}

func TestConversationCompletionRejectsInvalidToolAndStepCap(t *testing.T) {
	for _, part := range []aisdk.TextStreamPart{
		aisdk.StreamToolCall{Invalid: true},
		aisdk.StreamToolCall{Error: errors.New("private payload must not leak")},
		aisdk.StreamFinish{FinishReason: provider.FinishReason{Unified: provider.FinishReasonToolCalls}},
	} {
		completion := conversationCompletion{}
		if err := completion.observe(part); !errors.Is(err, ErrIncompleteResponse) {
			t.Fatalf("expected incomplete response, got %v", err)
		}
	}
}
