package brieftopics

import (
	"strings"
	"testing"

	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
)

func TestReconcileAssignsAndPreservesStableIDs(t *testing.T) {
	first := domain.BriefContent{Telemetry: []domain.BriefItem{{Name: "Logs", Value: "logfmt", Status: "proposed"}}}
	Reconcile(nil, &first)
	if first.Audience.ID != Audience || !strings.HasPrefix(first.Telemetry[0].ID, "tel_") {
		t.Fatalf("IDs were not assigned: %#v", first)
	}

	second := domain.BriefContent{Telemetry: []domain.BriefItem{{Name: "logs", Value: "JSON", Status: "proposed"}}}
	Reconcile(&first, &second)
	if second.Telemetry[0].ID != first.Telemetry[0].ID {
		t.Fatalf("collection ID changed: %q -> %q", first.Telemetry[0].ID, second.Telemetry[0].ID)
	}
}

func TestResolveAndApplyUseIDNotDisplayLabel(t *testing.T) {
	content := domain.BriefContent{Telemetry: []domain.BriefItem{{ID: "tel_123", Name: "Logs", Value: "logfmt", Status: "proposed"}}}
	focus, ok := Resolve(&content, "tel_123")
	if !ok || focus.TopicID != "tel_123" || focus.Label != "Telemetry: Logs" {
		t.Fatalf("Resolve() = %#v, %t", focus, ok)
	}
	content.Telemetry[0].Name = "Journal des événements"
	if !Apply(&content, "tel_123", "JSON") {
		t.Fatal("Apply() did not use the stable ID")
	}
	if content.Telemetry[0].Value != "JSON" || content.Telemetry[0].Status != "confirmed" {
		t.Fatalf("topic = %#v", content.Telemetry[0])
	}
}

func TestReconcileUsesKnownIDWhenDisplayNameChanges(t *testing.T) {
	previous := domain.BriefContent{Telemetry: []domain.BriefItem{{ID: "tel_known", Name: "Logs"}}}
	next := domain.BriefContent{Telemetry: []domain.BriefItem{{ID: "tel_known", Name: "Journaux"}}}

	Reconcile(&previous, &next)

	if next.Telemetry[0].ID != "tel_known" {
		t.Fatalf("renamed topic ID = %q", next.Telemetry[0].ID)
	}
}

func TestReconcileRejectsModelInventedID(t *testing.T) {
	previous := domain.BriefContent{Telemetry: []domain.BriefItem{{ID: "tel_known", Name: "Logs"}}}
	next := domain.BriefContent{Telemetry: []domain.BriefItem{{ID: "tel_invented", Name: "Metrics"}}}

	Reconcile(&previous, &next)

	if next.Telemetry[0].ID == "tel_invented" || !strings.HasPrefix(next.Telemetry[0].ID, "tel_") {
		t.Fatalf("invented ID was accepted: %q", next.Telemetry[0].ID)
	}
}

func TestEnsurePreservesTrustedIDs(t *testing.T) {
	content := domain.BriefContent{Telemetry: []domain.BriefItem{{ID: "tel_known", Name: "Logs"}}}

	Ensure(&content)

	if content.Telemetry[0].ID != "tel_known" {
		t.Fatalf("trusted ID changed: %q", content.Telemetry[0].ID)
	}
}
