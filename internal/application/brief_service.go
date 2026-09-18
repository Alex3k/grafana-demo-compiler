package application

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Alex3k/grafana-demo-compiler/internal/brieftopics"
	"github.com/Alex3k/grafana-demo-compiler/internal/chat"
	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
	"github.com/Alex3k/grafana-demo-compiler/internal/store"
)

// BriefService owns living-brief extraction and focused brief conversations.
// It deliberately contains no HTTP concerns; handlers only translate Faults and
// Events to their wire representation.
type BriefService struct {
	store *store.Store
	chat  *chat.Service
	log   *slog.Logger
}

func NewBriefService(dataStore *store.Store, chatService *chat.Service, logger *slog.Logger) *BriefService {
	return &BriefService{store: dataStore, chat: chatService, log: logger}
}

type PreparedFocusedTurn struct {
	Session          domain.Session
	Thread           domain.BriefThread
	UserMessage      domain.Message
	Activity         domain.Message
	AssistantMessage domain.Message
}

type ConfirmedBriefTopic struct {
	Thread   domain.BriefThread `json:"thread"`
	Brief    domain.LivingBrief `json:"brief"`
	Activity domain.Message     `json:"activity"`
}

func (s *BriefService) OpenThread(ctx context.Context, sessionID, topicID string) (domain.BriefThread, error) {
	topicID = strings.TrimSpace(topicID)
	if topicID == "" || utf8.RuneCountInString(topicID) > 120 {
		return domain.BriefThread{}, fault(FaultInvalid, "Invalid brief topic", nil)
	}
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return domain.BriefThread{}, fault(FaultNotFound, "Session not found", err)
		}
		return domain.BriefThread{}, fault(FaultInternal, "Session not found", err)
	}
	if session.Brief == nil {
		return domain.BriefThread{}, fault(FaultConflict, "The living brief is not ready for a focused conversation", nil)
	}
	focus, ok := brieftopics.Resolve(&session.Brief.Content, topicID)
	if !ok || (focus.Status != "proposed" && focus.Status != "confirmed") {
		return domain.BriefThread{}, fault(FaultInvalid, "Brief topic is unavailable", nil)
	}
	thread, err := s.store.OpenBriefThread(ctx, sessionID, focus)
	if err != nil {
		return domain.BriefThread{}, fault(FaultInternal, "Could not open focused brief chat", err)
	}
	return thread, nil
}

// PrepareFocusedTurn performs all validation and persistence that must happen
// before a streaming response begins.
func (s *BriefService) PrepareFocusedTurn(ctx context.Context, sessionID, threadID, content string) (PreparedFocusedTurn, error) {
	content = strings.TrimSpace(content)
	if err := validateMessage(content); err != nil {
		return PreparedFocusedTurn{}, err
	}
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return PreparedFocusedTurn{}, fault(FaultNotFound, "Session not found", err)
	}
	thread, err := s.store.GetBriefThread(ctx, session.ID, threadID)
	if err != nil {
		return PreparedFocusedTurn{}, fault(FaultNotFound, "Focused brief chat not found", err)
	}
	if thread.State != "draft" {
		return PreparedFocusedTurn{}, fault(FaultConflict, "This focused brief chat is already confirmed", nil)
	}
	userMessage, err := s.store.CreateBriefThreadMessage(ctx, thread.ID, domain.Message{SessionID: session.ID, Role: "user", Kind: "message", Content: content, Status: "complete"})
	if err != nil {
		return PreparedFocusedTurn{}, fault(FaultInternal, "Could not save focused message", err)
	}
	if err := s.store.UpdateBriefThreadCandidate(ctx, session.ID, thread.ID, ""); err != nil {
		return PreparedFocusedTurn{}, fault(FaultInternal, "Could not reset the proposed brief value", err)
	}
	activity, _ := s.store.CreateBriefThreadMessage(ctx, thread.ID, domain.Message{SessionID: session.ID, Role: "system", Kind: "activity", Content: "Refining " + thread.Focus.Label, Status: "complete"})
	assistant, err := s.store.CreateBriefThreadMessage(ctx, thread.ID, domain.Message{SessionID: session.ID, Role: "assistant", Kind: "message", Status: "streaming"})
	if err != nil {
		return PreparedFocusedTurn{}, fault(FaultInternal, "Could not start focused response", err)
	}
	return PreparedFocusedTurn{Session: session, Thread: thread, UserMessage: userMessage, Activity: activity, AssistantMessage: assistant}, nil
}

func (s *BriefService) RunFocusedTurn(ctx context.Context, turn PreparedFocusedTurn, sink EventSink) error {
	_ = emit(sink, "user_message", turn.UserMessage)
	_ = emit(sink, "activity", turn.Activity)
	_ = emit(sink, "message_started", turn.AssistantMessage)
	_ = emit(sink, "candidate_updated", map[string]string{"value": ""})

	messages, err := s.store.ListBriefThreadMessages(ctx, turn.Thread.ID)
	if err != nil {
		return s.failFocusedTurn(ctx, turn.AssistantMessage, "Could not reload focused conversation", err, sink)
	}
	var response strings.Builder
	result, err := s.chat.StreamFocused(ctx, turn.Session, messages, turn.Thread.Focus, func(delta string) error {
		response.WriteString(delta)
		if err := s.store.UpdateBriefThreadMessage(ctx, turn.AssistantMessage.ID, response.String(), "streaming"); err != nil {
			return err
		}
		return emit(sink, "delta", map[string]string{"messageId": turn.AssistantMessage.ID, "delta": delta})
	})
	if err != nil {
		return s.failFocusedTurn(ctx, turn.AssistantMessage, FriendlyChatError(err), err, sink)
	}
	assistant := turn.AssistantMessage
	assistant.Content, assistant.Status = result.Text, "complete"
	candidate, ok := FocusedCandidate(result.Text)
	if !ok {
		err = errors.New("focused response missing proposed brief value")
		return s.failFocusedTurn(ctx, assistant, "The focused response did not include a proposed brief value. Please retry your message.", err, sink)
	}
	if err := s.store.UpdateBriefThreadMessage(ctx, assistant.ID, result.Text, "complete"); err != nil {
		return s.failFocusedTurn(ctx, assistant, "Could not save the focused response", err, sink)
	}
	if err := s.store.UpdateBriefThreadCandidate(ctx, turn.Session.ID, turn.Thread.ID, candidate); err != nil {
		return s.failFocusedTurn(ctx, assistant, "Could not save the proposed brief value", err, sink)
	}
	_ = emit(sink, "message_completed", assistant)
	_ = emit(sink, "candidate_updated", map[string]string{"value": candidate})
	_ = emit(sink, "turn_completed", map[string]string{"threadId": turn.Thread.ID})
	return nil
}

func (s *BriefService) ConfirmThread(ctx context.Context, sessionID, threadID string) (ConfirmedBriefTopic, error) {
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return ConfirmedBriefTopic{}, fault(FaultNotFound, "Session not found", err)
	}
	thread, err := s.store.GetBriefThread(ctx, session.ID, threadID)
	if err != nil {
		return ConfirmedBriefTopic{}, fault(FaultNotFound, "Focused brief chat not found", err)
	}
	if thread.State != "draft" {
		return ConfirmedBriefTopic{}, fault(FaultConflict, "This focused brief chat is already confirmed", nil)
	}
	if strings.TrimSpace(thread.CandidateValue) == "" {
		return ConfirmedBriefTopic{}, fault(FaultInvalid, "Discuss this topic until there is a proposed brief value before confirming it", nil)
	}
	if session.Brief == nil {
		return ConfirmedBriefTopic{}, fault(FaultConflict, "The living brief is not ready for a focused confirmation", nil)
	}
	content := session.Brief.Content
	previousHash := content.AcceptanceHash()
	if !brieftopics.Apply(&content, thread.Focus.TopicID, thread.CandidateValue) {
		return ConfirmedBriefTopic{}, fault(FaultUnprocessable, "The confirmed topic could not be matched in the living brief", nil)
	}
	if content.AcceptanceHash() != previousHash {
		content.Acceptance = domain.PlanAcceptance{}
	}
	brief, err := s.store.ApplyBriefThread(ctx, session.ID, thread.ID, content)
	if err != nil {
		return ConfirmedBriefTopic{}, fault(FaultInternal, "Could not save the confirmed topic", err)
	}
	s.updateSessionState(ctx, session, content, nil)
	thread.State = "confirmed"
	activity, _ := s.store.CreateMessage(ctx, domain.Message{SessionID: session.ID, Role: "system", Kind: "activity", Content: "Confirmed brief topic: " + thread.Focus.Label, Status: "complete"})
	return ConfirmedBriefTopic{Thread: thread, Brief: brief, Activity: activity}, nil
}

func (s *BriefService) UpdateLivingBrief(ctx context.Context, session domain.Session, sink EventSink, parentGenerationIDs ...string) {
	defer func() { _ = emit(sink, "turn_completed", map[string]string{"sessionId": session.ID}) }()
	activity, _ := s.store.CreateMessage(ctx, domain.Message{SessionID: session.ID, Role: "system", Kind: "activity", Content: "Checking for brief changes", Status: "streaming"})
	_ = emit(sink, "activity", activity)
	activity.Content, activity.Status = "Brief update failed", "failed"
	defer func() {
		persistCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if activity.ID != "" {
			_ = s.store.UpdateMessage(persistCtx, activity.ID, activity.Content, activity.Status)
			_ = emit(sink, "activity", activity)
		}
	}()
	operation, err := s.store.CreateOperation(ctx, domain.Operation{SessionID: session.ID, Kind: "living_brief_update", Status: "running", Summary: "Extracting decisions, narrative, and architecture"})
	if err != nil {
		s.log.Error("could not start living brief update", "session", session.ID, "error", err)
		return
	}
	sourceMessageID := ""
	if session.Brief != nil {
		sourceMessageID = session.Brief.SourceMessageID
	}
	messages, err := s.store.ListMessagesSince(ctx, session.ID, sourceMessageID)
	if err == nil {
		var result chat.BriefResult
		result, err = s.chat.BuildBrief(ctx, session, messages, parentGenerationIDs...)
		if err == nil && result.Unchanged && session.Brief != nil {
			err = s.store.AdvanceBriefCursor(ctx, session.ID, session.Brief.Version, result.ConsumedMessageID)
			if err == nil {
				activity.Content, activity.Status = "Brief unchanged", "complete"
				_ = s.store.FinishOperation(ctx, operation.ID, "complete", "Brief unchanged", "")
				return
			}
		}
		if err == nil {
			content := result.Content
			if content.Acceptance.Accepted {
				evaluationActivity, _ := s.store.CreateMessage(ctx, domain.Message{SessionID: session.ID, Role: "system", Kind: "activity", Content: "Evaluating the accepted plan against the demo requirement", Status: "complete"})
				_ = emit(sink, "activity", evaluationActivity)
				var allMessages []domain.Message
				allMessages, err = s.store.ListMessages(ctx, session.ID)
				var evaluation chat.EvaluationResult
				if err == nil {
					evaluation, err = s.chat.EvaluatePlan(ctx, session, content, allMessages, result.GenerationID)
				}
				if err == nil {
					content.Acceptance.Evaluation = evaluation.Evaluation
				}
			}
			if err == nil {
				var brief domain.LivingBrief
				brief, err = s.store.SaveBriefWithCursor(ctx, session.ID, result.ConsumedMessageID, content)
				if err == nil {
					activity.Content, activity.Status = "Living brief updated", "complete"
					s.updateSessionState(ctx, session, content, sink)
					_ = s.store.FinishOperation(ctx, operation.ID, "complete", "Living brief updated", "")
					_ = emit(sink, "brief_updated", brief)
					return
				}
			}
		}
	}
	s.log.Error("living brief update failed", "session", session.ID, "error", err)
	errorText := ""
	if err != nil {
		errorText = err.Error()
	}
	_ = s.store.FinishOperation(ctx, operation.ID, "failed", "Living brief update failed", errorText)
	failure, _ := s.store.CreateMessage(ctx, domain.Message{SessionID: session.ID, Role: "system", Kind: "activity", Content: "The conversation is saved, but the living brief needs another pass", Status: "complete"})
	_ = emit(sink, "activity", failure)
}

func latestConversationalMessageID(messages []domain.Message) string {
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Kind == "message" && message.Status == "complete" && (message.Role == "user" || message.Role == "assistant") {
			return message.ID
		}
	}
	return ""
}

func (s *BriefService) updateSessionState(ctx context.Context, session domain.Session, content domain.BriefContent, sink EventSink) {
	if content.Acceptance.Accepted && content.Acceptance.Evaluation.Result != "does_not_meet" {
		if err := s.store.UpdateSessionState(ctx, session.ID, "Ready"); err != nil {
			s.log.Error("could not mark accepted plan ready", "session", session.ID, "error", err)
		} else {
			_ = emit(sink, "session_state", map[string]string{"state": "Ready"})
		}
	} else if session.State == "Ready" {
		if err := s.store.UpdateSessionState(ctx, session.ID, "Draft"); err != nil {
			s.log.Error("could not return revised plan to draft", "session", session.ID, "error", err)
		} else {
			_ = emit(sink, "session_state", map[string]string{"state": "Draft"})
		}
	}
}

func (s *BriefService) failFocusedTurn(ctx context.Context, message domain.Message, public string, cause error, sink EventSink) error {
	s.log.Error("focused assistant response failed", "session", message.SessionID, "message", message.ID, "error", cause)
	content := message.Content
	if content == "" {
		content = public
	}
	_ = s.store.UpdateBriefThreadMessage(ctx, message.ID, content, "failed")
	_ = emit(sink, "error", map[string]string{"messageId": message.ID, "message": public})
	return fault(FaultBadGateway, public, cause)
}

func FocusedCandidate(response string) (string, bool) {
	const heading = "### Proposed brief value"
	index := strings.LastIndex(response, heading)
	if index < 0 {
		return "", false
	}
	value := strings.TrimSpace(response[index+len(heading):])
	return value, value != "" && utf8.RuneCountInString(value) <= 8_000
}

func emit(sink EventSink, name string, data any) error {
	if sink == nil {
		return nil
	}
	return sink(Event{Name: name, Data: data})
}

func fault(code FaultCode, public string, cause error) *Fault {
	return &Fault{Code: code, Public: public, Cause: cause}
}
