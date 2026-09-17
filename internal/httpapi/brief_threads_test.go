package httpapi

import (
	"testing"

	"github.com/Alex3k/grafana-demo-compiler/internal/application"
	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
)

func TestApplyBriefTopic(t *testing.T) {
	content := domain.BriefContent{
		Scenario:  domain.BriefItem{Name: "Scenario", Value: "Camera outage", Status: "proposed"},
		Telemetry: []domain.BriefItem{{Name: "Structured logs", Value: "Firmware events", Status: "proposed"}},
	}
	if !application.ApplyBriefTopic(&content, "Telemetry: Structured logs", "Structured JSON firmware events") {
		t.Fatal("telemetry topic was not matched")
	}
	if content.Telemetry[0].Status != "confirmed" {
		t.Fatalf("telemetry status = %q", content.Telemetry[0].Status)
	}
	if content.Telemetry[0].Value != "Structured JSON firmware events" {
		t.Fatalf("telemetry value = %q", content.Telemetry[0].Value)
	}
	if !application.ApplyBriefTopic(&content, "Scenario", "Camera config outage") || content.Scenario.Status != "confirmed" || content.Scenario.Value != "Camera config outage" {
		t.Fatal("core scenario topic was not confirmed")
	}
}

func TestFocusedCandidateUsesFinalProposedValue(t *testing.T) {
	response := "Let's use JSON logs.\n\n### Proposed brief value\nStructured JSON logs keyed by firmware and region."
	value, ok := application.FocusedCandidate(response)
	if !ok || value != "Structured JSON logs keyed by firmware and region." {
		t.Fatalf("candidate = %q, %v", value, ok)
	}
}
