package httpapi

import (
	"testing"

	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
)

func TestMarkBriefTopicConfirmed(t *testing.T) {
	content := domain.BriefContent{
		Scenario:  domain.BriefItem{Name: "Scenario", Value: "Camera outage", Status: "proposed"},
		Telemetry: []domain.BriefItem{{Name: "Structured logs", Value: "Firmware events", Status: "proposed"}},
	}
	if !markBriefTopicConfirmed(&content, "Telemetry: Structured logs") {
		t.Fatal("telemetry topic was not matched")
	}
	if content.Telemetry[0].Status != "confirmed" {
		t.Fatalf("telemetry status = %q", content.Telemetry[0].Status)
	}
	if !markBriefTopicConfirmed(&content, "Scenario") || content.Scenario.Status != "confirmed" {
		t.Fatal("core scenario topic was not confirmed")
	}
}

func TestFocusedTranscriptExcludesActivityAndIncompleteMessages(t *testing.T) {
	got := focusedTranscript([]domain.Message{
		{Role: "system", Kind: "activity", Status: "complete", Content: "Refining telemetry"},
		{Role: "user", Kind: "message", Status: "complete", Content: "Use JSON logs"},
		{Role: "assistant", Kind: "message", Status: "complete", Content: "Use structured JSON with stable fields"},
		{Role: "assistant", Kind: "message", Status: "failed", Content: "partial"},
	})
	want := "User: Use JSON logs\n\nAssistant: Use structured JSON with stable fields"
	if got != want {
		t.Fatalf("transcript = %q, want %q", got, want)
	}
}
