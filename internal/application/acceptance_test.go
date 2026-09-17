package application

import (
	"context"
	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
	"github.com/Alex3k/grafana-demo-compiler/internal/store"
	"path/filepath"
	"testing"
)

func TestFocusedEditClearsPersistedAcceptance(t *testing.T) {
	ctx := context.Background()
	data, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	session, err := data.CreateSession(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	content := domain.BriefContent{Audience: domain.BriefItem{Value: "Engineers", Status: "confirmed"}, Acceptance: domain.PlanAcceptance{Accepted: true, ProposalMessageID: "plan", AcceptingMessageID: "yes", Evaluation: domain.AlignmentEvaluation{Result: "meets"}}}
	content.Acceptance.BriefHash = content.AcceptanceHash()
	saved, err := data.SaveBrief(ctx, session.ID, content)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := data.GetBrief(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Content.Acceptance.ProposalMessageID != "plan" || loaded.Content.Acceptance.AcceptingMessageID != "yes" || loaded.Content.Acceptance.BriefHash != content.Acceptance.BriefHash {
		t.Fatal("acceptance references did not survive persistence")
	}
	service := NewBriefService(data, nil, nil)
	thread, err := service.OpenThread(ctx, session.ID, "audience")
	if err != nil {
		t.Fatal(err)
	}
	if err := data.UpdateBriefThreadCandidate(ctx, session.ID, thread.ID, "Executives"); err != nil {
		t.Fatal(err)
	}
	result, err := service.ConfirmThread(ctx, session.ID, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Brief.Version <= saved.Version || result.Brief.Content.Acceptance.Accepted || result.Brief.Content.Acceptance.Evaluation.Result != "" || result.Brief.Content.Acceptance.BriefHash != "" {
		t.Fatalf("stale acceptance: %+v", result.Brief)
	}
}
