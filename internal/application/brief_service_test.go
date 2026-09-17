package application

import (
	"strings"
	"testing"

	"github.com/Alex3k/grafana-demo-compiler/internal/brieftopics"
	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
)

func TestFocusedCandidateUsesLastHeading(t *testing.T) {
	t.Parallel()

	response := "### Proposed brief value\nold\n\nDiscussion\n\n### Proposed brief value\nUse JSON logs"
	value, ok := FocusedCandidate(response)
	if !ok || value != "Use JSON logs" {
		t.Fatalf("FocusedCandidate() = %q, %t", value, ok)
	}
	if _, ok := FocusedCandidate("No proposal"); ok {
		t.Fatal("FocusedCandidate() accepted a response without the required heading")
	}
	if _, ok := FocusedCandidate("### Proposed brief value\n" + strings.Repeat("x", 8_001)); ok {
		t.Fatal("FocusedCandidate() accepted an oversized candidate")
	}
}

func TestApplyBriefTopic(t *testing.T) {
	t.Parallel()

	content := domain.BriefContent{
		Audience:  domain.BriefItem{ID: "audience", Value: "SREs", Status: "proposed"},
		Telemetry: []domain.BriefItem{{ID: "tel_logs", Name: "Logs", Value: "logfmt", Status: "proposed"}},
	}
	if !brieftopics.Apply(&content, "audience", "Platform engineers") {
		t.Fatal("ApplyBriefTopic() did not recognize core topic")
	}
	if content.Audience.Value != "Platform engineers" || content.Audience.Status != "confirmed" {
		t.Fatalf("Audience = %#v", content.Audience)
	}
	if !brieftopics.Apply(&content, "tel_logs", "JSON") {
		t.Fatal("Apply() did not recognize collection topic ID")
	}
	if content.Telemetry[0].Value != "JSON" || content.Telemetry[0].Status != "confirmed" {
		t.Fatalf("Telemetry[0] = %#v", content.Telemetry[0])
	}
	if brieftopics.Apply(&content, "unknown", "value") {
		t.Fatal("Apply() accepted an unknown topic")
	}
}

func TestLatestConversationalMessageIDSkipsIncompleteAndActivityMessages(t *testing.T) {
	messages := []domain.Message{
		{ID: "user", Role: "user", Kind: "message", Status: "complete"},
		{ID: "activity", Role: "system", Kind: "activity", Status: "complete"},
		{ID: "partial", Role: "assistant", Kind: "message", Status: "streaming"},
		{ID: "assistant", Role: "assistant", Kind: "message", Status: "complete"},
	}
	if got := latestConversationalMessageID(messages); got != "assistant" {
		t.Fatalf("latestConversationalMessageID() = %q", got)
	}
}
