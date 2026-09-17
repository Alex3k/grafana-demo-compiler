package chat

import (
	"strings"
	"testing"

	"github.com/Alex3k/grafana-demo-compiler/internal/contextengine"
	aisdk "github.com/grafana/ai-sdk"
	"github.com/grafana/ai-sdk/provider"
)

func TestContextUsageIsolatesSessionsAndReplacesRoleSnapshot(t *testing.T) {
	s := &Service{}
	s.recordContextUsage("a", contextengine.Manifest{Role: contextengine.RoleCollaborator, EstimatedTokens: 100, MaxInputTokens: 1000})
	s.recordContextUsage("b", contextengine.Manifest{Role: contextengine.RoleCollaborator, EstimatedTokens: 200, MaxInputTokens: 1000})
	s.recordContextUsage("a", contextengine.Manifest{Role: contextengine.RoleCollaborator, EstimatedTokens: 900, MaxInputTokens: 1000, Truncated: true})
	got := s.ContextUsage("a")
	if len(got) != 1 || got[0].EstimatedTokens != 900 || !got[0].Truncated || got[0].UpdatedAt.IsZero() {
		t.Fatalf("usage = %#v", got)
	}
	got[0].EstimatedTokens = 0
	if s.ContextUsage("a")[0].EstimatedTokens != 900 || s.ContextUsage("b")[0].EstimatedTokens != 200 || len(s.ContextUsage("missing")) != 0 {
		t.Fatal("snapshots leaked across sessions or to caller")
	}
}

func TestBuilderMeterReportsGrowingTranscriptAndOverflow(t *testing.T) {
	s := &Service{contextConfig: contextengine.DefaultConfig()}
	guard := s.builderBudgetGuard("session")
	if _, err := guard(aisdk.PrepareStepState{Messages: []provider.Message{provider.UserText("build")}}); err != nil {
		t.Fatal(err)
	}
	initial := s.ContextUsage("session")[0].EstimatedTokens
	_, err := guard(aisdk.PrepareStepState{Messages: []provider.Message{provider.UserText(strings.Repeat("x", 120000))}})
	if err != nil || s.ContextUsage("session")[0].MaxInputTokens != 128000 {
		t.Fatalf("builder should accept context above the old 32k limit: %v", err)
	}
	_, err = guard(aisdk.PrepareStepState{Messages: []provider.Message{provider.UserText(strings.Repeat("x", 400000))}})
	got := s.ContextUsage("session")[0]
	if !contextengine.IsBudgetOverflow(err) || got.EstimatedTokens <= initial || got.EstimatedTokens <= got.MaxInputTokens {
		t.Fatalf("usage=%#v error=%v", got, err)
	}
}
