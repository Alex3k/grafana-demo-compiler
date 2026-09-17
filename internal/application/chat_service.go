package application

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/Alex3k/grafana-demo-compiler/internal/chat"
	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
	"github.com/Alex3k/grafana-demo-compiler/internal/store"
)

type ChatService struct {
	store *store.Store
	chat  *chat.Service
	brief *BriefService
	log   *slog.Logger
}

func NewChatService(dataStore *store.Store, chatService *chat.Service, briefService *BriefService, logger *slog.Logger) *ChatService {
	return &ChatService{store: dataStore, chat: chatService, brief: briefService, log: logger}
}

type PreparedChatTurn struct {
	Session          domain.Session
	UserMessage      domain.Message
	Activity         domain.Message
	Operation        domain.Operation
	AssistantMessage domain.Message
}

// PrepareTurn validates and persists a chat turn before the transport commits
// to a streaming response.
func (s *ChatService) PrepareTurn(ctx context.Context, sessionID, content string) (PreparedChatTurn, error) {
	content = strings.TrimSpace(content)
	if err := validateMessage(content); err != nil {
		return PreparedChatTurn{}, err
	}
	session, err := s.store.GetSession(ctx, sessionID)
	if errors.Is(err, store.ErrNotFound) {
		return PreparedChatTurn{}, fault(FaultNotFound, "Session not found", err)
	}
	if err != nil {
		return PreparedChatTurn{}, fault(FaultInternal, "Could not load session", err)
	}
	user, err := s.store.CreateMessage(ctx, domain.Message{SessionID: session.ID, Role: "user", Kind: "message", Content: content, Status: "complete"})
	if err != nil {
		return PreparedChatTurn{}, fault(FaultInternal, "Could not save message", err)
	}
	if session.Title == "Untitled demo" {
		title := SuggestedTitle(content)
		if err := s.store.RenameSession(ctx, session.ID, title); err == nil {
			session.Title = title
		}
	}
	activity, _ := s.store.CreateMessage(ctx, domain.Message{SessionID: session.ID, Role: "system", Kind: "activity", Content: "Understanding your demo request", Status: "complete"})
	operation, err := s.store.CreateOperation(ctx, domain.Operation{SessionID: session.ID, Kind: "bedrock_generation", Status: "running", Summary: "Waiting for the assistant"})
	if err != nil {
		return PreparedChatTurn{}, fault(FaultInternal, "Could not start assistant operation", err)
	}
	assistant, err := s.store.CreateMessage(ctx, domain.Message{SessionID: session.ID, Role: "assistant", Kind: "message", Status: "streaming"})
	if err != nil {
		_ = s.store.FinishOperation(ctx, operation.ID, "failed", "Assistant response failed", err.Error())
		return PreparedChatTurn{}, fault(FaultInternal, "Could not start assistant response", err)
	}
	return PreparedChatTurn{Session: session, UserMessage: user, Activity: activity, Operation: operation, AssistantMessage: assistant}, nil
}

func (s *ChatService) RunTurn(ctx context.Context, turn PreparedChatTurn, sink EventSink) error {
	_ = emit(sink, "user_message", turn.UserMessage)
	_ = emit(sink, "activity", turn.Activity)
	_ = emit(sink, "message_started", turn.AssistantMessage)
	session, err := s.store.GetSession(ctx, turn.Session.ID)
	if err != nil {
		return s.failTurn(ctx, turn, "Could not reload conversation", err, sink)
	}
	messages := session.Messages
	var response strings.Builder
	result, err := s.chat.Stream(ctx, session, messages, func(delta string) error {
		response.WriteString(delta)
		if err := s.store.UpdateMessage(ctx, turn.AssistantMessage.ID, response.String(), "streaming"); err != nil {
			return err
		}
		return emit(sink, "delta", map[string]string{"messageId": turn.AssistantMessage.ID, "delta": delta})
	})
	if err != nil {
		return s.failTurn(ctx, turn, FriendlyChatError(err), err, sink)
	}
	assistant := turn.AssistantMessage
	assistant.Content, assistant.Status = result.Text, "complete"
	if err := s.store.UpdateMessage(ctx, assistant.ID, result.Text, "complete"); err != nil {
		turn.AssistantMessage = assistant
		return s.failTurn(ctx, turn, "Could not save the completed response", err, sink)
	}
	_ = s.store.FinishOperation(ctx, turn.Operation.ID, "complete", "Assistant response complete", "")
	_ = emit(sink, "message_completed", assistant)
	if s.brief != nil {
		s.brief.UpdateLivingBrief(ctx, session, sink, result.GenerationID)
	}
	return nil
}

func (s *ChatService) failTurn(ctx context.Context, turn PreparedChatTurn, public string, cause error, sink EventSink) error {
	s.log.Error("assistant response failed", "session", turn.AssistantMessage.SessionID, "operation", turn.Operation.ID, "error", cause)
	content := turn.AssistantMessage.Content
	if content == "" {
		content = public
	}
	_ = s.store.UpdateMessage(ctx, turn.AssistantMessage.ID, content, "failed")
	_ = s.store.FinishOperation(ctx, turn.Operation.ID, "failed", public, cause.Error())
	_ = emit(sink, "error", map[string]string{"messageId": turn.AssistantMessage.ID, "message": public})
	return fault(FaultBadGateway, public, cause)
}

func validateMessage(content string) error {
	if content == "" {
		return fault(FaultInvalid, "A message is required", nil)
	}
	if utf8.RuneCountInString(content) > 32_000 {
		return fault(FaultTooLarge, "Message exceeds 32,000 characters", nil)
	}
	return nil
}

func SuggestedTitle(content string) string {
	words := strings.Fields(content)
	if len(words) > 7 {
		words = words[:7]
	}
	title := strings.Join(words, " ")
	if utf8.RuneCountInString(title) > 56 {
		title = string([]rune(title)[:56])
	}
	if title == "" {
		return "Untitled demo"
	}
	return title
}

func FriendlyChatError(err error) string {
	if errors.Is(err, chat.ErrNotConfigured) {
		return "Amazon Bedrock is not configured. Set AWS_REGION and BEDROCK_MODEL_ID, then restart the compiler."
	}
	if errors.Is(err, context.Canceled) {
		return "The response was interrupted. You can retry your message."
	}
	return "The assistant could not complete this response. Check the server log and retry."
}
