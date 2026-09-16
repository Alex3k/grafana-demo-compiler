package chat

import (
	"context"
	"encoding/json"
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

const systemPrompt = `You are Grafana Demo Compiler, a collaborative solutions engineer helping a human design a focused, story-led Grafana demo that can be presented in ten minutes or less.

Develop the narrative alongside the system: audience and stakes, normal state, inciting change, investigation in Grafana, diagnosis and action, recovery, and proved outcome. Ask only a small number of targeted questions when an answer would materially change that story. Pair questions with a concrete current proposal so the human has something useful to react to.

Propose only the telemetry and Grafana resources needed to tell the story. Treat resources explicitly requested by the human as requirements. When enough is known for a coherent vertical slice, offer an early prototype with at least three Go services and MySQL, state its bounded scope and unresolved assumptions, and let the human prototype now, reduce the slice, or keep planning. Do not treat silence as approval. Do not pretend to generate or deploy yet. Never claim remote deployment is supported; the MVP supports local Docker Compose only. Keep responses concise, specific, and collaborative.`

const briefSystemPrompt = `Maintain the structured living brief for a collaborative Grafana demo design conversation.

Return only the requested JSON object, with concise values and fewer than 4,000 tokens total. Use these rules:
- Every brief item status is exactly unknown, proposed, or confirmed.
- A fact explicitly stated by the human is confirmed. An assistant suggestion is proposed until the human explicitly accepts it. Silence is never approval.
- Preserve confirmed decisions unless the human explicitly corrects them. Record corrections as a new confirmed decision and keep concise evidence.
- changes lists only the concise material differences from currentBrief; on the first version, list the important facts established so far.
- Preserve the human's wording for outcomes, proof points, and requested Grafana resources where practical.
- Requested Grafana resources are confirmed. Assistant recommendations are proposed.
- Keep telemetry and Grafana resources deliberately small and story-relevant, each with a one-sentence reason in value.
- Mermaid contains raw Mermaid source only, begins with flowchart LR, has audience-friendly labels, and describes a small architecture with at least three Go application services and MySQL once enough context exists. Never return a code fence or an ASCII diagram.
- Narrative uses the stages audience/stakes, normal, change, investigate, act, recover/outcome. Keep the total at ten minutes or less; when it cannot fit, add an open question asking what to cut.
- A prototype offer is ready only when audience, outcome, scenario, a beginning-to-end journey, three or more Go services, and MySQL are sufficiently understood with no slice-changing ambiguity. Its scope stays bounded and lists unresolved assumptions.
- acceptance.accepted is true only when the human explicitly accepts the current plan. When true, evaluate whether the plan answers the confirmed audience, outcome, proof points, scenario, and requested resources. evaluation.result is exactly meets, partially_meets, or does_not_meet. Otherwise leave the evaluation strings and lists empty.
- If a material requirement changes after a prior acceptance, acceptance.accepted becomes false until the human accepts the revised direction.
- Never introduce ecommerce unless the human requests it. Never assume missing material requirements; keep them unknown and ask through openQuestions.

Use exactly this JSON shape and value types. Every array shown with strings must contain strings, not objects:
{"changes":["string"],"audience":{"name":"Audience","value":"string","status":"unknown|proposed|confirmed"},"company":{"name":"Company","value":"string","status":"unknown|proposed|confirmed"},"outcome":{"name":"Outcome","value":"string","status":"unknown|proposed|confirmed"},"stakes":{"name":"Stakes","value":"string","status":"unknown|proposed|confirmed"},"scenario":{"name":"Scenario","value":"string","status":"unknown|proposed|confirmed"},"journey":{"name":"Journey","value":"string","status":"unknown|proposed|confirmed"},"proofPoints":[{"name":"string","value":"string","status":"unknown|proposed|confirmed"}],"services":[{"name":"string","value":"string","status":"unknown|proposed|confirmed"}],"telemetry":[{"name":"string","value":"string","status":"unknown|proposed|confirmed"}],"grafanaResources":[{"name":"string","value":"string","status":"unknown|proposed|confirmed"}],"narrative":[{"stage":"string","detail":"string","minutes":1}],"mermaid":"flowchart LR...","openQuestions":["string"],"decisions":[{"summary":"string","status":"unknown|proposed|confirmed","evidence":"string"}],"prototypeOffer":{"ready":false,"summary":"string","services":["string"],"scenario":"string","telemetry":["string"],"grafanaResources":["string"],"assumptions":["string"]},"acceptance":{"accepted":false,"evidence":"string","evaluation":{"result":"","explanation":"","missing":[],"evidence":[]}}}`

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
		aisdk.WithSystem(systemPrompt+briefContext(session.Brief)),
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

func (s *Service) BuildBrief(ctx context.Context, session domain.Session, messages []domain.Message) (domain.BriefContent, error) {
	if !s.Configured() {
		return domain.BriefContent{}, ErrNotConfigured
	}

	payload, err := json.Marshal(struct {
		Current  *domain.LivingBrief `json:"currentBrief,omitempty"`
		Messages []domain.Message    `json:"conversation"`
	}{Current: session.Brief, Messages: conversationalMessages(messages)})
	if err != nil {
		return domain.BriefContent{}, fmt.Errorf("encode living brief context: %w", err)
	}

	ctx = agento11y.WithConversationID(ctx, session.ID)
	ctx = agento11y.WithConversationTitle(ctx, session.Title)
	ctx = agentobservability.WithGenerationID(ctx, agentobservability.NewGenerationID())
	stream := aisdk.StreamText(ctx, s.model,
		aisdk.WithSystem(briefSystemPrompt),
		aisdk.WithModelMessages(provider.UserText(string(payload))),
		aisdk.WithMaxOutputTokens(6000),
		aisdk.WithMaxRetries(1),
	)
	for part := range stream.FullStream() {
		if streamError, ok := part.(aisdk.StreamError); ok && streamError.Error != nil {
			return domain.BriefContent{}, fmt.Errorf("generate living brief: %w", streamError.Error)
		}
	}
	if err := stream.Err(); err != nil {
		return domain.BriefContent{}, fmt.Errorf("generate living brief: %w", err)
	}
	var brief domain.BriefContent
	if err := json.Unmarshal([]byte(stripJSONFence(stream.Text())), &brief); err != nil {
		return domain.BriefContent{}, fmt.Errorf("decode living brief: %w", err)
	}
	return normalizeBrief(brief), nil
}

func stripJSONFence(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "```json") {
		value = strings.TrimPrefix(value, "```json")
		value = strings.TrimSuffix(strings.TrimSpace(value), "```")
	}
	return strings.TrimSpace(value)
}

func briefContext(brief *domain.LivingBrief) string {
	if brief == nil {
		return ""
	}
	payload, err := json.Marshal(brief.Content)
	if err != nil {
		return ""
	}
	return "\n\nCurrent living brief (use it to avoid repeated questions; proposed is not confirmed):\n" + string(payload)
}

func conversationalMessages(messages []domain.Message) []domain.Message {
	result := make([]domain.Message, 0, len(messages))
	for _, message := range messages {
		if message.Kind == "message" && message.Content != "" && message.Status == "complete" {
			result = append(result, message)
		}
	}
	return result
}

func normalizeBrief(brief domain.BriefContent) domain.BriefContent {
	normalizeItem := func(item domain.BriefItem) domain.BriefItem {
		item.Name = strings.TrimSpace(item.Name)
		item.Value = strings.TrimSpace(item.Value)
		if item.Value == "" {
			item.Status = "unknown"
		} else if item.Status != "confirmed" && item.Status != "proposed" {
			item.Status = "unknown"
		}
		return item
	}
	brief.Audience = normalizeItem(brief.Audience)
	brief.Company = normalizeItem(brief.Company)
	brief.Outcome = normalizeItem(brief.Outcome)
	brief.Stakes = normalizeItem(brief.Stakes)
	brief.Scenario = normalizeItem(brief.Scenario)
	brief.Journey = normalizeItem(brief.Journey)
	for _, items := range [][]domain.BriefItem{brief.ProofPoints, brief.Services, brief.Telemetry, brief.GrafanaResources} {
		for index := range items {
			items[index] = normalizeItem(items[index])
		}
	}
	brief.Mermaid = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(brief.Mermaid), "```mermaid"), "```"))
	if brief.Mermaid != "" && !strings.HasPrefix(brief.Mermaid, "flowchart") && !strings.HasPrefix(brief.Mermaid, "graph") {
		brief.Mermaid = ""
	}
	if brief.Acceptance.Accepted {
		switch brief.Acceptance.Evaluation.Result {
		case "meets", "partially_meets", "does_not_meet":
		default:
			brief.Acceptance.Evaluation.Result = "does_not_meet"
		}
	} else {
		brief.Acceptance.Evaluation = domain.AlignmentEvaluation{}
	}
	return brief
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
