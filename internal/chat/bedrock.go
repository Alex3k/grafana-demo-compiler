package chat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/grafana/agento11y/go/agento11y"
	aisdk "github.com/grafana/ai-sdk"
	"github.com/grafana/ai-sdk/middleware/agentobservability"
	"github.com/grafana/ai-sdk/provider"
	"github.com/grafana/ai-sdk/providers/bedrock"

	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
	"github.com/Alex3k/grafana-demo-compiler/internal/observability"
)

const systemPrompt = `You are Grafana Demo Compiler, a collaborative solutions engineer helping a human design a focused, story-led Grafana demo.

For this first product milestone, hold a natural discovery conversation. Understand the audience, scenario, company context, desired outcome, and important telemetry. Ask a small number of targeted questions when a material requirement is unclear. Do not pretend to generate, deploy, or create Grafana resources yet. Never claim that remote application deployment is supported; the MVP supports local Docker Compose only. Keep responses concise, specific, and collaborative.`

var ErrNotConfigured = errors.New("Amazon Bedrock is not configured")

type Service struct {
	model   provider.LanguageModel
	modelID string
	region  string
}

type Result struct {
	Text string
}

func New(_ context.Context, o11y *observability.Runtime) (*Service, error) {
	region := strings.TrimSpace(os.Getenv("AWS_REGION"))
	modelID := strings.TrimSpace(os.Getenv("BEDROCK_MODEL_ID"))
	if region == "" || modelID == "" {
		return &Service{modelID: modelID, region: region}, nil
	}

	base := bedrock.New(modelID, bedrock.WithRegion(region))
	wrapped := agentobservability.Wrap(base, agentobservability.WrapOptions{
		ClientResolver: func(context.Context) *agento11y.Client {
			if o11y == nil || !o11y.Configured() {
				return nil
			}
			return o11y.Client
		},
		ContextProvider: func(context.Context) agentobservability.ContextInfo {
			return agentobservability.ContextInfo{
				AgentName:    observability.AgentName,
				AgentVersion: observability.AgentVersion,
				Tags:         map[string]string{"runtime": "local"},
			}
		},
		Hooks: agentobservability.HooksOptions{
			Enabled: func(context.Context) bool { return false },
		},
	})

	return &Service{model: wrapped, modelID: modelID, region: region}, nil
}

func (s *Service) Configured() bool {
	return s != nil && s.model != nil && s.region != "" && s.modelID != ""
}

func (s *Service) ModelID() string {
	if s == nil {
		return ""
	}
	return s.modelID
}

func (s *Service) Stream(ctx context.Context, session domain.Session, messages []domain.Message, onDelta func(string) error) (Result, error) {
	if !s.Configured() {
		return Result{}, ErrNotConfigured
	}

	ctx = agento11y.WithConversationID(ctx, session.ID)
	ctx = agento11y.WithConversationTitle(ctx, session.Title)
	ctx = agentobservability.WithGenerationID(ctx, agentobservability.NewGenerationID())

	stream := aisdk.StreamText(ctx, s.model,
		aisdk.WithSystem(systemPrompt),
		aisdk.WithModelMessages(modelMessages(messages)...),
		aisdk.WithMaxOutputTokens(1200),
		aisdk.WithMaxRetries(1),
	)

	var text strings.Builder
	for part := range stream.FullStream() {
		switch value := part.(type) {
		case aisdk.StreamTextDelta:
			text.WriteString(value.Text)
			if err := onDelta(value.Text); err != nil {
				return Result{}, err
			}
		case aisdk.StreamError:
			if value.Error != nil {
				return Result{}, fmt.Errorf("stream Bedrock response: %w", value.Error)
			}
		case aisdk.StreamAbort:
			return Result{}, fmt.Errorf("stream Bedrock response aborted: %s", value.Reason)
		}
	}
	if err := stream.Err(); err != nil {
		return Result{}, fmt.Errorf("stream Bedrock response: %w", err)
	}
	return Result{Text: text.String()}, nil
}

func modelMessages(messages []domain.Message) []provider.Message {
	type turn struct {
		role    string
		content string
	}
	turns := make([]turn, 0, len(messages))
	for _, message := range messages {
		if message.Kind != "message" || message.Content == "" || message.Status == "failed" || message.Status == "interrupted" {
			continue
		}
		if message.Role != "user" && message.Role != "assistant" {
			continue
		}
		if len(turns) > 0 && turns[len(turns)-1].role == message.Role {
			turns[len(turns)-1].content += "\n\n" + message.Content
			continue
		}
		turns = append(turns, turn{role: message.Role, content: message.Content})
	}

	result := make([]provider.Message, 0, len(turns))
	for _, turn := range turns {
		switch turn.role {
		case "user":
			result = append(result, provider.UserText(turn.content))
		case "assistant":
			result = append(result, provider.AssistantText(turn.content))
		}
	}
	return result
}
