package application

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Alex3k/grafana-demo-compiler/internal/chat"
	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
)

type prototypeStoreStub struct {
	session  domain.Session
	finished chan domain.PrototypeIteration
}

func (s *prototypeStoreStub) GetSession(context.Context, string) (domain.Session, error) {
	return s.session, nil
}
func (s *prototypeStoreStub) CreatePrototypeIteration(context.Context, string, int) (domain.PrototypeIteration, error) {
	return domain.PrototypeIteration{ID: "iteration-12345678", SessionID: s.session.ID, Number: 1, Status: "generating", Progress: []string{"Starting a new local prototype iteration"}}, nil
}
func (s *prototypeStoreStub) UpdatePrototypeProgress(context.Context, domain.PrototypeIteration) error {
	return nil
}
func (s *prototypeStoreStub) FinishPrototypeIteration(_ context.Context, iteration domain.PrototypeIteration) error {
	select {
	case s.finished <- iteration:
	default:
	}
	return nil
}
func (s *prototypeStoreStub) CreateOperation(context.Context, domain.Operation) (domain.Operation, error) {
	return domain.Operation{ID: "operation-1"}, nil
}
func (s *prototypeStoreStub) FinishOperation(context.Context, string, string, string, string) error {
	return nil
}
func (s *prototypeStoreStub) CreateMessage(context.Context, domain.Message) (domain.Message, error) {
	return domain.Message{}, nil
}
func (s *prototypeStoreStub) SetSessionState(context.Context, string, string) error { return nil }

type prototypeBuilderStub struct{}

func (prototypeBuilderStub) BuildPrototype(context.Context, domain.Session, string, func(string)) (chat.PrototypeResult, error) {
	return chat.PrototypeResult{
		Summary: "built",
		Checks:  []domain.PrototypeCheck{{Name: "build", Status: "passed"}},
	}, nil
}

func TestPrototypeServiceStartRunsAcceptedIterationInBackground(t *testing.T) {
	brief := &domain.LivingBrief{Version: 3}
	brief.Content.PrototypeOffer.Ready = true
	dataStore := &prototypeStoreStub{
		session:  domain.Session{ID: "session-1", Brief: brief},
		finished: make(chan domain.PrototypeIteration, 2),
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewPrototypeService(dataStore, prototypeBuilderStub{}, logger, t.TempDir())

	accepted, fault := service.Start(context.Background(), "session-1")
	if fault != nil {
		t.Fatalf("Start returned fault: %v", fault)
	}
	if accepted.Status != "generating" || accepted.RootPath == "" {
		t.Fatalf("unexpected accepted iteration: %#v", accepted)
	}

	select {
	case completed := <-dataStore.finished:
		if completed.Status != "complete" || completed.Summary != "built" {
			t.Fatalf("unexpected completed iteration: %#v", completed)
		}
	case <-time.After(time.Second):
		t.Fatal("background prototype did not complete")
	}
}

func TestPrototypeServiceStartRequiresReadyOffer(t *testing.T) {
	dataStore := &prototypeStoreStub{session: domain.Session{ID: "session-1"}, finished: make(chan domain.PrototypeIteration, 1)}
	service := NewPrototypeService(dataStore, prototypeBuilderStub{}, nil, t.TempDir())

	_, fault := service.Start(context.Background(), "session-1")
	if fault == nil || fault.Code != FaultConflict {
		t.Fatalf("expected conflict fault, got %#v", fault)
	}
}
