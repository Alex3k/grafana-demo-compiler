package chat

import (
	"github.com/Alex3k/grafana-demo-compiler/internal/contextengine"
	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
	"testing"
)

func TestCollaboratorBudgetDoesNotChangeFocusedBudget(t *testing.T) {
	t.Setenv("DEMO_COMPILER_COLLABORATOR_MAX_INPUT_TOKENS", "")
	s := &Service{contextConfig: contextengine.DefaultConfig()}
	packet, err := s.conversationCompiler("prompt", false).Collaborator(domain.Session{})
	if err != nil || packet.Manifest.MaxInputTokens != 128000 {
		t.Fatalf("collaborator budget = %d, err = %v", packet.Manifest.MaxInputTokens, err)
	}
	focused, err := s.conversationCompiler("prompt", true).Focused(domain.Session{}, domain.BriefFocus{}, nil)
	if err != nil || focused.Manifest.MaxInputTokens != 32000 {
		t.Fatalf("focused budget = %d, err = %v", focused.Manifest.MaxInputTokens, err)
	}
	t.Setenv("DEMO_COMPILER_COLLABORATOR_MAX_INPUT_TOKENS", "96000")
	packet, err = s.conversationCompiler("prompt", false).Collaborator(domain.Session{})
	if err != nil || packet.Manifest.MaxInputTokens != 96000 {
		t.Fatalf("configured budget = %d, err = %v", packet.Manifest.MaxInputTokens, err)
	}
}
