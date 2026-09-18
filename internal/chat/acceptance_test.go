package chat

import (
	"github.com/Alex3k/grafana-demo-compiler/internal/contextengine"
	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
	"testing"
)

func acceptanceMessages() []domain.Message {
	return []domain.Message{
		{ID: "plan", Role: "assistant", Kind: "message", Status: "complete", Content: "Plan A: three services and Alloy"},
		{ID: "yes", Role: "user", Kind: "message", Status: "complete", Content: "I accept Plan A"},
		{ID: "ack", Role: "assistant", Kind: "message", Status: "complete", Content: "Accepted. Ready to build."},
		{ID: "later", Role: "user", Kind: "message", Status: "complete", Content: "Explain the plan"},
	}
}
func TestAcceptancePinsPlanAcrossFollowups(t *testing.T) {
	brief := domain.BriefContent{Acceptance: domain.PlanAcceptance{Accepted: true, ProposalMessageID: "plan", AcceptingMessageID: "yes"}}
	if err := bindAcceptance(nil, &brief, acceptanceMessages()[:2]); err != nil {
		t.Fatal(err)
	}
	plan, err := acceptedPlan(brief, acceptanceMessages())
	if err != nil || plan != acceptanceMessages()[0].Content {
		t.Fatalf("plan=%q err=%v", plan, err)
	}
	next := brief
	next.Changes = []string{"Recap"}
	next.Acceptance.ProposalMessageID = "ack"
	next.Acceptance.AcceptingMessageID = "later"
	if err := bindAcceptance(&brief, &next, acceptanceMessages()); err != nil {
		t.Fatal(err)
	}
	if next.Acceptance.ProposalMessageID != "plan" || next.Acceptance.AcceptingMessageID != "yes" || next.Acceptance.BriefHash != brief.Acceptance.BriefHash {
		t.Fatal("approval moved to later messages")
	}
}
func TestChangedBriefRequiresFreshAcceptance(t *testing.T) {
	brief := domain.BriefContent{Audience: domain.BriefItem{Value: "Engineers"}, Acceptance: domain.PlanAcceptance{Accepted: true, ProposalMessageID: "plan", AcceptingMessageID: "yes"}}
	if err := bindAcceptance(nil, &brief, acceptanceMessages()); err != nil {
		t.Fatal(err)
	}
	next := brief
	next.Audience.Value = "Executives"
	if err := bindAcceptance(&brief, &next, acceptanceMessages()); err != nil {
		t.Fatal(err)
	}
	if next.Acceptance.Accepted || next.Acceptance.BriefHash != "" || next.Acceptance.Evaluation.Result != "" {
		t.Fatalf("stale acceptance: %+v", next.Acceptance)
	}
	next.Acceptance = domain.PlanAcceptance{Accepted: true, ProposalMessageID: "plan", AcceptingMessageID: "yes"}
	previous := next
	previous.Acceptance = domain.PlanAcceptance{}
	if err := bindAcceptance(&previous, &next, acceptanceMessages()); err != nil {
		t.Fatal(err)
	}
	if next.Acceptance.BriefHash == brief.Acceptance.BriefHash {
		t.Fatal("new brief reused old hash")
	}
}
func TestAcceptanceRejectsMissingReversedAndStaleReferences(t *testing.T) {
	for _, ids := range [][2]string{{"missing", "yes"}, {"yes", "plan"}, {"ack", "yes"}} {
		brief := domain.BriefContent{Acceptance: domain.PlanAcceptance{Accepted: true, ProposalMessageID: ids[0], AcceptingMessageID: ids[1]}}
		if err := bindAcceptance(nil, &brief, acceptanceMessages()); err == nil {
			t.Fatalf("accepted invalid IDs %v", ids)
		}
	}
	brief := domain.BriefContent{Acceptance: domain.PlanAcceptance{Accepted: true, ProposalMessageID: "plan", AcceptingMessageID: "yes"}}
	if err := bindAcceptance(nil, &brief, acceptanceMessages()); err != nil {
		t.Fatal(err)
	}
	brief.Company.Value = "Changed"
	if _, err := acceptedPlan(brief, acceptanceMessages()); err == nil {
		t.Fatal("accepted stale hash")
	}
}
func TestCuratorIncludesPreCursorProposalForAcceptance(t *testing.T) {
	messages := acceptanceMessages()[:3]
	service := &Service{contextConfig: contextengine.DefaultConfig()}
	packet, cursor, err := service.curatorPacket(domain.Session{Messages: messages}, messages[1:])
	if err != nil {
		t.Fatal(err)
	}
	if len(packet.DeltaMessages) != 3 || packet.DeltaMessages[0].ID != "plan" || cursor != "ack" {
		t.Fatalf("packet=%+v cursor=%s", packet, cursor)
	}
}
func TestEvaluatorManifestIdentifiesUnsavedBriefByHash(t *testing.T) {
	brief := domain.BriefContent{Audience: domain.BriefItem{Value: "Engineers"}}
	packet, err := contextengine.New(6).Evaluator(domain.LivingBrief{Content: brief}, "Plan A")
	if err != nil {
		t.Fatal(err)
	}
	if packet.Manifest.BriefVersion != 0 || packet.Manifest.BriefHash != brief.AcceptanceHash() {
		t.Fatalf("manifest=%+v", packet.Manifest)
	}
}
