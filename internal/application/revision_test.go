package application

import (
	"context"
	"testing"
	"time"

	"github.com/Alex3k/grafana-demo-compiler/internal/chat"
	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
	"github.com/Alex3k/grafana-demo-compiler/internal/prototype"
)

type revisionBuilder struct{ received chan domain.Session }

func (b revisionBuilder) BuildPrototype(_ context.Context, s domain.Session, _ string, _ func(string)) (chat.PrototypeResult, error) {
	b.received <- s
	return chat.PrototypeResult{Checks: []domain.PrototypeCheck{{Status: "passed"}}}, nil
}
func TestRevisionRequiresApprovalAndMatchingSnapshot(t *testing.T) {
	root := t.TempDir()
	w, _ := prototype.New(root)
	_, _ = w.WriteFile("main.go", "package main")
	base := domain.PrototypeIteration{ID: "base", Status: "complete", RootPath: root, Artifacts: w.Artifacts()}
	_, digest, err := prototype.SourceSnapshot(base)
	if err != nil {
		t.Fatal(err)
	}
	brief := &domain.LivingBrief{Version: 2}
	brief.Content.PrototypeOffer.Ready = true
	data := &prototypeStoreStub{session: domain.Session{ID: "s", Brief: brief, Prototypes: []domain.PrototypeIteration{base}}, finished: make(chan domain.PrototypeIteration, 1)}
	b := revisionBuilder{received: make(chan domain.Session, 1)}
	service := NewPrototypeService(data, b, nil, t.TempDir())
	r := domain.RevisionProposal{SessionID: "s", BaseIterationID: "base", BriefVersion: 2, SourceDigest: digest, Files: []string{"main.go"}, Status: "pending"}
	if _, fault := service.StartRevision(context.Background(), "s", r); fault == nil {
		t.Fatal("unapproved build accepted")
	}
	r.Status = "approved"
	r.BriefVersion = 1
	if _, fault := service.StartRevision(context.Background(), "s", r); fault == nil {
		t.Fatal("stale brief accepted")
	}
	r.BriefVersion = 2
	r.SourceDigest = "old"
	if _, fault := service.StartRevision(context.Background(), "s", r); fault == nil {
		t.Fatal("stale source accepted")
	}
	r.SourceDigest = digest
	if _, fault := service.StartRevision(context.Background(), "s", r); fault != nil {
		t.Fatal(fault)
	}
	select {
	case got := <-b.received:
		if got.Revision == nil || got.RevisionFiles["main.go"] != "package main" || got.Brief.Version != 2 {
			t.Fatal("builder did not receive approved context")
		}
	case <-time.After(time.Second):
		t.Fatal("builder not invoked")
	}
}
