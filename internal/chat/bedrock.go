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
	appPrompts "github.com/Alex3k/grafana-demo-compiler/prompts"
)

const (
	roleCollaborator = "demo-collaborator"
	roleCurator      = "living-brief-curator"
	roleEvaluator    = "requirement-evaluator"
)

var ErrNotConfigured = errors.New("Amazon Bedrock is not configured")

type Service struct {
	model   provider.LanguageModel
	modelID string
	region  string
}

type Result struct {
	Text         string
	GenerationID string
}

type BriefResult struct {
	Content      domain.BriefContent
	GenerationID string
}

type EvaluationResult struct {
	Evaluation   domain.AlignmentEvaluation
	GenerationID string
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
		ContextProvider: func(ctx context.Context) agentobservability.ContextInfo {
			name := observability.AgentName
			if value, ok := agento11y.AgentNameFromContext(ctx); ok {
				name = value
			}
			version := observability.AgentVersion
			if value, ok := agento11y.AgentVersionFromContext(ctx); ok {
				version = value
			}
			tags := agento11y.TagsFromContext(ctx)
			if tags == nil {
				tags = make(map[string]string)
			}
			tags["runtime"] = "local"
			return agentobservability.ContextInfo{
				AgentName:    name,
				AgentVersion: version,
				Tags:         tags,
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
	return s.stream(ctx, session, messages, nil, onDelta)
}

func (s *Service) StreamFocused(ctx context.Context, session domain.Session, messages []domain.Message, focus domain.BriefFocus, onDelta func(string) error) (Result, error) {
	return s.stream(ctx, session, messages, &focus, onDelta)
}

func (s *Service) stream(ctx context.Context, session domain.Session, messages []domain.Message, focus *domain.BriefFocus, onDelta func(string) error) (Result, error) {
	if !s.Configured() {
		return Result{}, ErrNotConfigured
	}

	ctx, generationID := roleContext(ctx, session, roleCollaborator)

	stream := aisdk.StreamText(ctx, s.model,
		aisdk.WithSystem(appPrompts.Collaborator()+briefContext(session)+focusContext(focus)),
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
	return Result{Text: sanitizeAssistantText(text.String()), GenerationID: generationID}, nil
}

func focusContext(focus *domain.BriefFocus) string {
	if focus == nil {
		return ""
	}
	payload, err := json.Marshal(focus)
	if err != nil {
		return ""
	}
	return "\n\n<focused_brief_topic>\n" + string(payload) + "\nThe human is iterating this topic in an isolated draft thread. Use the current living brief only as background, address the selected topic directly, and avoid broadening unless a dependency must be surfaced. Do not treat draft statements as confirmed, use the word confirmed for a draft change, claim that the living brief changed, or resume the main conversation. Describe the emerging result as a draft update and help the human converge on concise wording that they can explicitly confirm and apply later. End every response with a `### Proposed brief value` heading followed by one self-contained, concise paragraph containing exactly the value that should replace this topic if the human confirms it. This final section is required, must reflect the latest focused discussion, and must not contain questions.\n</focused_brief_topic>"
}

func (s *Service) BuildBrief(ctx context.Context, session domain.Session, messages []domain.Message, parentGenerationIDs ...string) (BriefResult, error) {
	if !s.Configured() {
		return BriefResult{}, ErrNotConfigured
	}

	payload, err := json.Marshal(struct {
		Current  *domain.LivingBrief `json:"currentBrief,omitempty"`
		Messages []domain.Message    `json:"conversation"`
	}{Current: session.Brief, Messages: conversationalMessages(messages)})
	if err != nil {
		return BriefResult{}, fmt.Errorf("encode living brief context: %w", err)
	}

	ctx, generationID := roleContext(ctx, session, roleCurator, parentGenerationIDs...)
	stream := aisdk.StreamText(ctx, s.model,
		aisdk.WithSystem(appPrompts.Curator()),
		aisdk.WithModelMessages(provider.UserText(string(payload))),
		aisdk.WithMaxOutputTokens(6000),
		aisdk.WithMaxRetries(1),
	)
	for part := range stream.FullStream() {
		if streamError, ok := part.(aisdk.StreamError); ok && streamError.Error != nil {
			return BriefResult{}, fmt.Errorf("generate living brief: %w", streamError.Error)
		}
	}
	if err := stream.Err(); err != nil {
		return BriefResult{}, fmt.Errorf("generate living brief: %w", err)
	}
	var brief domain.BriefContent
	if err := json.Unmarshal([]byte(stripJSONFence(stream.Text())), &brief); err != nil {
		return BriefResult{}, fmt.Errorf("decode living brief: %w", err)
	}
	content := normalizeBrief(brief)
	if session.Brief != nil {
		content = preserveConfirmedBrief(session.Brief.Content, content)
	}
	return BriefResult{Content: content, GenerationID: generationID}, nil
}

func (s *Service) EvaluatePlan(ctx context.Context, session domain.Session, brief domain.BriefContent, messages []domain.Message, parentGenerationIDs ...string) (EvaluationResult, error) {
	if !s.Configured() {
		return EvaluationResult{}, ErrNotConfigured
	}
	payload, err := json.Marshal(struct {
		Brief         domain.BriefContent `json:"brief"`
		CandidatePlan string              `json:"candidatePlan"`
	}{Brief: brief, CandidatePlan: acceptedAssistantPlan(messages)})
	if err != nil {
		return EvaluationResult{}, fmt.Errorf("encode requirement evaluation context: %w", err)
	}

	ctx, generationID := roleContext(ctx, session, roleEvaluator, parentGenerationIDs...)
	stream := aisdk.StreamText(ctx, s.model,
		aisdk.WithSystem(appPrompts.Evaluator()),
		aisdk.WithModelMessages(provider.UserText(string(payload))),
		aisdk.WithMaxOutputTokens(3000),
		aisdk.WithMaxRetries(1),
	)
	for part := range stream.FullStream() {
		if streamError, ok := part.(aisdk.StreamError); ok && streamError.Error != nil {
			return EvaluationResult{}, fmt.Errorf("evaluate demo requirement: %w", streamError.Error)
		}
	}
	if err := stream.Err(); err != nil {
		return EvaluationResult{}, fmt.Errorf("evaluate demo requirement: %w", err)
	}
	var evaluation domain.AlignmentEvaluation
	if err := json.Unmarshal([]byte(stripJSONFence(stream.Text())), &evaluation); err != nil {
		return EvaluationResult{}, fmt.Errorf("decode requirement evaluation: %w", err)
	}
	return EvaluationResult{Evaluation: normalizeEvaluation(evaluation), GenerationID: generationID}, nil
}

func stripJSONFence(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "```json") {
		value = strings.TrimPrefix(value, "```json")
		value = strings.TrimSuffix(strings.TrimSpace(value), "```")
	}
	return strings.TrimSpace(value)
}

func sanitizeAssistantText(value string) string {
	for _, marker := range []string{"<turn_complete>", "</turn_complete>"} {
		value = strings.ReplaceAll(value, marker, "")
	}
	return strings.TrimSpace(value)
}

func briefContext(session domain.Session) string {
	payload, err := json.Marshal(struct {
		State string              `json:"sessionState"`
		Brief *domain.LivingBrief `json:"currentBrief,omitempty"`
	}{State: session.State, Brief: session.Brief})
	if err != nil {
		return ""
	}
	context := "\n\n<session_context>\n" + string(payload) + "\n</session_context>"
	if facts := confirmedBriefFacts(session.Brief); facts != "" {
		context += "\n\n<confirmed_brief_facts>\nThese are the current locked facts. They supersede older conversation messages that proposed alternatives or called them unresolved. Use them as stated and do not reopen them.\n" + facts + "\n</confirmed_brief_facts>"
	}
	return context
}

func confirmedBriefFacts(brief *domain.LivingBrief) string {
	if brief == nil {
		return ""
	}
	type fact struct {
		Section string `json:"section"`
		Name    string `json:"name"`
		Value   string `json:"value"`
	}
	facts := make([]fact, 0)
	add := func(section string, item domain.BriefItem) {
		if item.Status == "confirmed" {
			facts = append(facts, fact{Section: section, Name: item.Name, Value: item.Value})
		}
	}
	add("Audience", brief.Content.Audience)
	add("Company", brief.Content.Company)
	add("Outcome", brief.Content.Outcome)
	add("Stakes", brief.Content.Stakes)
	add("Scenario", brief.Content.Scenario)
	add("Journey", brief.Content.Journey)
	add("Simulation boundary", brief.Content.Scope.SimulationBoundary)
	for _, group := range []struct {
		section string
		items   []domain.BriefItem
	}{
		{section: "Proof points", items: brief.Content.ProofPoints},
		{section: "Included scope", items: brief.Content.Scope.Included},
		{section: "Excluded scope", items: brief.Content.Scope.Excluded},
		{section: "Services", items: brief.Content.Services},
		{section: "Telemetry", items: brief.Content.Telemetry},
		{section: "Grafana resources", items: brief.Content.GrafanaResources},
	} {
		for _, item := range group.items {
			add(group.section, item)
		}
	}
	if len(facts) == 0 {
		return ""
	}
	payload, err := json.Marshal(facts)
	if err != nil {
		return ""
	}
	return string(payload)
}

func roleContext(ctx context.Context, session domain.Session, role string, parentGenerationIDs ...string) (context.Context, string) {
	generationID := agentobservability.NewGenerationID()
	ctx = agento11y.WithConversationID(ctx, session.ID)
	ctx = agento11y.WithConversationTitle(ctx, session.Title)
	ctx = agento11y.WithAgentName(ctx, observability.AgentName+"/"+role)
	ctx = agento11y.WithAgentVersion(ctx, observability.AgentVersion)
	ctx = agento11y.WithTags(ctx, map[string]string{"agent.role": role, "prompt.version": appPrompts.Version})
	ctx = agentobservability.WithGenerationID(ctx, generationID)
	ctx = agentobservability.WithParentGenerationIDs(ctx, parentGenerationIDs...)
	return ctx, generationID
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
	brief.Scope.SimulationBoundary = normalizeItem(brief.Scope.SimulationBoundary)
	for _, items := range [][]domain.BriefItem{brief.ProofPoints, brief.Scope.Included, brief.Scope.Excluded, brief.Services, brief.Telemetry, brief.GrafanaResources} {
		for index := range items {
			items[index] = normalizeItem(items[index])
		}
	}
	brief.Mermaid = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(brief.Mermaid), "```mermaid"), "```"))
	if brief.Mermaid != "" && !strings.HasPrefix(brief.Mermaid, "flowchart") && !strings.HasPrefix(brief.Mermaid, "graph") {
		brief.Mermaid = ""
	}
	if !brief.Acceptance.Accepted {
		brief.Acceptance.Evaluation = domain.AlignmentEvaluation{}
	}
	return brief
}

// preserveConfirmedBrief prevents main-chat curation from changing decisions
// that the human locked through a focused brief thread. Focused threads apply
// their confirmed value directly and do not use BuildBrief.
func preserveConfirmedBrief(current, candidate domain.BriefContent) domain.BriefContent {
	candidate.Audience = preserveConfirmedItem(current.Audience, candidate.Audience)
	candidate.Company = preserveConfirmedItem(current.Company, candidate.Company)
	candidate.Outcome = preserveConfirmedItem(current.Outcome, candidate.Outcome)
	candidate.Stakes = preserveConfirmedItem(current.Stakes, candidate.Stakes)
	candidate.Scenario = preserveConfirmedItem(current.Scenario, candidate.Scenario)
	candidate.Journey = preserveConfirmedItem(current.Journey, candidate.Journey)
	candidate.Scope.SimulationBoundary = preserveConfirmedItem(current.Scope.SimulationBoundary, candidate.Scope.SimulationBoundary)
	candidate.ProofPoints = preserveConfirmedItems(current.ProofPoints, candidate.ProofPoints)
	candidate.Scope.Included = preserveConfirmedItems(current.Scope.Included, candidate.Scope.Included)
	candidate.Scope.Excluded = preserveConfirmedItems(current.Scope.Excluded, candidate.Scope.Excluded)
	candidate.Services = preserveConfirmedItems(current.Services, candidate.Services)
	candidate.Telemetry = preserveConfirmedItems(current.Telemetry, candidate.Telemetry)
	candidate.GrafanaResources = preserveConfirmedItems(current.GrafanaResources, candidate.GrafanaResources)
	return candidate
}

func preserveConfirmedItem(current, candidate domain.BriefItem) domain.BriefItem {
	if current.Status == "confirmed" {
		return current
	}
	return candidate
}

func preserveConfirmedItems(current, candidate []domain.BriefItem) []domain.BriefItem {
	result := append([]domain.BriefItem(nil), candidate...)
	for _, locked := range current {
		if locked.Status != "confirmed" {
			continue
		}
		found := false
		for index := range result {
			if strings.EqualFold(strings.TrimSpace(locked.Name), strings.TrimSpace(result[index].Name)) {
				result[index] = locked
				found = true
				break
			}
		}
		if !found {
			result = append(result, locked)
		}
	}
	return result
}

func normalizeEvaluation(evaluation domain.AlignmentEvaluation) domain.AlignmentEvaluation {
	switch evaluation.Result {
	case "meets", "partially_meets", "does_not_meet":
	default:
		evaluation.Result = "does_not_meet"
	}
	evaluation.Explanation = strings.TrimSpace(evaluation.Explanation)
	return evaluation
}

func acceptedAssistantPlan(messages []domain.Message) string {
	latestUserIndex := -1
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Kind == "message" && message.Role == "user" && message.Status == "complete" {
			latestUserIndex = index
			break
		}
	}
	for index := latestUserIndex - 1; index >= 0; index-- {
		message := messages[index]
		if message.Kind == "message" && message.Role == "assistant" && message.Status == "complete" {
			return message.Content
		}
	}
	return ""
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
