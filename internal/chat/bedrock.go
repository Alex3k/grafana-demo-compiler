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
	"github.com/Alex3k/grafana-demo-compiler/internal/prototype"
	appPrompts "github.com/Alex3k/grafana-demo-compiler/prompts"
)

const (
	roleCollaborator        = "demo-collaborator"
	roleCurator             = "living-brief-curator"
	roleEvaluator           = "requirement-evaluator"
	rolePrototypePlanner    = "prototype-planner"
	roleBuilder             = "prototype-builder"
	briefUpdateTool         = "propose_brief_update"
	recordPrototypePlanTool = "record_prototype_plan"
	writeDemoFileTool       = "write_demo_file"
	validatePrototypeTool   = "validate_prototype"
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

type PrototypeResult struct {
	Summary      string
	Artifacts    []domain.PrototypeArtifact
	Checks       []domain.PrototypeCheck
	GenerationID string
}

type prototypeBuildPlan struct {
	Summary              string                   `json:"summary"`
	Decisions            []prototypeDecision      `json:"decisions"`
	AlternativesRejected []prototypeAlternative   `json:"alternativesRejected"`
	Files                []prototypeFilePlan      `json:"files"`
	TelemetryMapping     []prototypeTelemetryPlan `json:"telemetryMapping"`
	Assumptions          []string                 `json:"assumptions"`
	Risks                []string                 `json:"risks"`
	Validation           []string                 `json:"validation"`
}

type prototypeDecision struct {
	Decision  string `json:"decision"`
	Rationale string `json:"rationale"`
	Evidence  string `json:"evidence"`
}

type prototypeAlternative struct {
	Alternative string `json:"alternative"`
	Reason      string `json:"reason"`
}

type prototypeFilePlan struct {
	Path    string `json:"path"`
	Purpose string `json:"purpose"`
}

type prototypeTelemetryPlan struct {
	Signal    string `json:"signal"`
	Source    string `json:"source"`
	StoryBeat string `json:"storyBeat"`
}

type prototypePlanReceipt struct {
	Status    string `json:"status"`
	FileCount int    `json:"fileCount"`
}

type writeDemoFileInput struct {
	Path    string `json:"path" jsonschema:"description=Relative path for the prototype file"`
	Content string `json:"content" jsonschema:"description=Complete file content"`
}

type prototypeValidationInput struct{}

type prototypeValidationOutput struct {
	Checks []domain.PrototypeCheck `json:"checks"`
}

type briefUpdateReceipt struct {
	Status      string `json:"status"`
	ChangeCount int    `json:"changeCount"`
}

// briefUpdateProposal mirrors the curator-owned brief contract. The persisted
// PlanAcceptance also contains an evaluator result, but the curator must not
// produce that field.
type briefUpdateProposal struct {
	Changes          []string               `json:"changes"`
	Audience         domain.BriefItem       `json:"audience"`
	Company          domain.BriefItem       `json:"company"`
	Outcome          domain.BriefItem       `json:"outcome"`
	Stakes           domain.BriefItem       `json:"stakes"`
	Scenario         domain.BriefItem       `json:"scenario"`
	Journey          domain.BriefItem       `json:"journey"`
	ProofPoints      []domain.BriefItem     `json:"proofPoints"`
	Scope            domain.BriefScope      `json:"scope"`
	Services         []domain.BriefItem     `json:"services"`
	Telemetry        []domain.BriefItem     `json:"telemetry"`
	GrafanaResources []domain.BriefItem     `json:"grafanaResources"`
	Narrative        []domain.NarrativeBeat `json:"narrative"`
	Mermaid          string                 `json:"mermaid"`
	OpenQuestions    []string               `json:"openQuestions"`
	Decisions        []domain.BriefDecision `json:"decisions"`
	PrototypeOffer   domain.PrototypeOffer  `json:"prototypeOffer"`
	Acceptance       briefAcceptance        `json:"acceptance"`
}

type briefAcceptance struct {
	Accepted bool   `json:"accepted"`
	Evidence string `json:"evidence"`
}

func (proposal briefUpdateProposal) content() domain.BriefContent {
	return domain.BriefContent{
		Changes:          proposal.Changes,
		Audience:         proposal.Audience,
		Company:          proposal.Company,
		Outcome:          proposal.Outcome,
		Stakes:           proposal.Stakes,
		Scenario:         proposal.Scenario,
		Journey:          proposal.Journey,
		ProofPoints:      proposal.ProofPoints,
		Scope:            proposal.Scope,
		Services:         proposal.Services,
		Telemetry:        proposal.Telemetry,
		GrafanaResources: proposal.GrafanaResources,
		Narrative:        proposal.Narrative,
		Mermaid:          proposal.Mermaid,
		OpenQuestions:    proposal.OpenQuestions,
		Decisions:        proposal.Decisions,
		PrototypeOffer:   proposal.PrototypeOffer,
		Acceptance: domain.PlanAcceptance{
			Accepted: proposal.Acceptance.Accepted,
			Evidence: proposal.Acceptance.Evidence,
		},
	}
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

	var candidate *domain.BriefContent
	tool, err := aisdk.TypedTool(aisdk.TypedToolDef[briefUpdateProposal, briefUpdateReceipt]{
		Name:        briefUpdateTool,
		Title:       "Propose living brief update",
		Description: "Submit the complete proposed living brief. This records a draft only; it cannot confirm human decisions or overwrite locked confirmed topics.",
		Execute: func(_ context.Context, proposal briefUpdateProposal, _ aisdk.ToolExecutionOptions) (briefUpdateReceipt, error) {
			content := normalizeBrief(proposal.content())
			if session.Brief != nil {
				content = preserveConfirmedBrief(session.Brief.Content, content)
			}
			candidate = &content
			return briefUpdateReceipt{Status: "draft_received", ChangeCount: len(content.Changes)}, nil
		},
	})
	if err != nil {
		return BriefResult{}, fmt.Errorf("create living brief tool: %w", err)
	}

	ctx, generationID := roleContext(ctx, session, roleCurator, parentGenerationIDs...)
	stream := aisdk.StreamText(ctx, s.model,
		aisdk.WithSystem(appPrompts.Curator()),
		aisdk.WithModelMessages(provider.UserText(string(payload))),
		aisdk.WithTools(aisdk.ToolSet{briefUpdateTool: tool}),
		aisdk.WithToolChoice(provider.ToolChoice{Type: provider.ToolChoiceTool, ToolName: briefUpdateTool}),
		aisdk.WithStopWhen(aisdk.StepCountIs(1)),
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
	if candidate == nil {
		return BriefResult{}, fmt.Errorf("generate living brief: model did not call %s", briefUpdateTool)
	}
	return BriefResult{Content: *candidate, GenerationID: generationID}, nil
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

func (s *Service) BuildPrototype(ctx context.Context, session domain.Session, root string, onProgress func(string)) (PrototypeResult, error) {
	if !s.Configured() {
		return PrototypeResult{}, ErrNotConfigured
	}
	if session.Brief == nil || !session.Brief.Content.PrototypeOffer.Ready {
		return PrototypeResult{}, errors.New("the living brief is not ready to prototype")
	}
	if onProgress != nil {
		onProgress("Creating an auditable implementation decision record")
	}
	plan, planGenerationID, err := s.planPrototype(ctx, session)
	if err != nil {
		return PrototypeResult{}, err
	}
	if onProgress != nil {
		onProgress("Build plan ready: " + plan.Summary)
	}
	workspace, err := prototype.New(root)
	if err != nil {
		return PrototypeResult{}, err
	}
	if onProgress != nil {
		onProgress("Prepared an isolated workspace for this revision")
	}

	writeTool, err := aisdk.TypedTool(aisdk.TypedToolDef[writeDemoFileInput, domain.PrototypeArtifact]{
		Name:        writeDemoFileTool,
		Title:       "Write demo file",
		Description: "Write one complete file inside the current local prototype revision.",
		Execute: func(_ context.Context, input writeDemoFileInput, _ aisdk.ToolExecutionOptions) (domain.PrototypeArtifact, error) {
			if onProgress != nil {
				onProgress("Writing " + input.Path)
			}
			artifact, err := workspace.WriteFile(input.Path, input.Content)
			return artifact, err
		},
	})
	if err != nil {
		return PrototypeResult{}, fmt.Errorf("create prototype writer tool: %w", err)
	}
	var checks []domain.PrototypeCheck
	validateTool, err := aisdk.TypedTool(aisdk.TypedToolDef[prototypeValidationInput, prototypeValidationOutput]{
		Name:        validatePrototypeTool,
		Title:       "Validate prototype",
		Description: "Run the fixed local prototype checks. This does not start containers or deploy resources.",
		Execute: func(toolCtx context.Context, _ prototypeValidationInput, _ aisdk.ToolExecutionOptions) (prototypeValidationOutput, error) {
			if onProgress != nil {
				onProgress("Running local validation checks")
			}
			checks = workspace.Validate(toolCtx)
			return prototypeValidationOutput{Checks: checks}, nil
		},
	})
	if err != nil {
		return PrototypeResult{}, fmt.Errorf("create prototype validation tool: %w", err)
	}

	payload, err := json.Marshal(struct {
		Title string             `json:"title"`
		Brief domain.LivingBrief `json:"livingBrief"`
		Plan  prototypeBuildPlan `json:"implementationPlan"`
	}{Title: session.Title, Brief: *session.Brief, Plan: plan})
	if err != nil {
		return PrototypeResult{}, fmt.Errorf("encode prototype context: %w", err)
	}
	ctx, generationID := roleContext(ctx, session, roleBuilder, planGenerationID)
	if onProgress != nil {
		onProgress("Reviewing the approved brief and planning the smallest viable demo")
	}
	stream := aisdk.StreamText(ctx, s.model,
		aisdk.WithSystem(appPrompts.Builder()),
		aisdk.WithModelMessages(provider.UserText(string(payload))),
		aisdk.WithTools(aisdk.ToolSet{writeDemoFileTool: writeTool, validatePrototypeTool: validateTool}),
		aisdk.WithStopWhen(aisdk.StepCountIs(40)),
		aisdk.WithMaxOutputTokens(6000),
		aisdk.WithMaxRetries(1),
	)
	for part := range stream.FullStream() {
		if streamError, ok := part.(aisdk.StreamError); ok && streamError.Error != nil {
			return PrototypeResult{}, fmt.Errorf("build prototype: %w", streamError.Error)
		}
	}
	if err := stream.Err(); err != nil {
		return PrototypeResult{}, fmt.Errorf("build prototype: %w", err)
	}
	if len(checks) == 0 {
		return PrototypeResult{}, fmt.Errorf("build prototype: model did not call %s", validatePrototypeTool)
	}
	return PrototypeResult{
		Summary:      sanitizeAssistantText(stream.Text()),
		Artifacts:    workspace.Artifacts(),
		Checks:       checks,
		GenerationID: generationID,
	}, nil
}

func (s *Service) planPrototype(ctx context.Context, session domain.Session) (prototypeBuildPlan, string, error) {
	payload, err := json.Marshal(struct {
		Title string             `json:"title"`
		Brief domain.LivingBrief `json:"livingBrief"`
	}{Title: session.Title, Brief: *session.Brief})
	if err != nil {
		return prototypeBuildPlan{}, "", fmt.Errorf("encode prototype planning context: %w", err)
	}
	var plan prototypeBuildPlan
	tool, err := aisdk.TypedTool(aisdk.TypedToolDef[prototypeBuildPlan, prototypePlanReceipt]{
		Name:        recordPrototypePlanTool,
		Title:       "Record prototype implementation plan",
		Description: "Record the complete, auditable implementation decision record before prototype files are written.",
		Execute: func(_ context.Context, input prototypeBuildPlan, _ aisdk.ToolExecutionOptions) (prototypePlanReceipt, error) {
			plan = input
			return prototypePlanReceipt{Status: "recorded", FileCount: len(input.Files)}, nil
		},
	})
	if err != nil {
		return prototypeBuildPlan{}, "", fmt.Errorf("create prototype planning tool: %w", err)
	}
	ctx, generationID := roleContext(ctx, session, rolePrototypePlanner)
	stream := aisdk.StreamText(ctx, s.model,
		aisdk.WithSystem(appPrompts.PrototypePlanner()),
		aisdk.WithModelMessages(provider.UserText(string(payload))),
		aisdk.WithTools(aisdk.ToolSet{recordPrototypePlanTool: tool}),
		aisdk.WithStopWhen(aisdk.StepCountIs(1)),
		aisdk.WithMaxOutputTokens(6000),
		aisdk.WithMaxRetries(1),
	)
	for part := range stream.FullStream() {
		if streamError, ok := part.(aisdk.StreamError); ok && streamError.Error != nil {
			return prototypeBuildPlan{}, generationID, fmt.Errorf("plan prototype: %w", streamError.Error)
		}
	}
	if err := stream.Err(); err != nil {
		return prototypeBuildPlan{}, generationID, fmt.Errorf("plan prototype: %w", err)
	}
	if strings.TrimSpace(plan.Summary) == "" || len(plan.Files) == 0 {
		return prototypeBuildPlan{}, generationID, fmt.Errorf("prototype planner did not call %s with a complete plan", recordPrototypePlanTool)
	}
	return plan, generationID, nil
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
	visibility := "internal"
	ctx = agento11y.WithConversationID(ctx, session.ID)
	ctx = agento11y.WithConversationTitle(ctx, session.Title)
	if role == roleCollaborator {
		visibility = "user"
	}
	ctx = agento11y.WithAgentName(ctx, observability.AgentName+"/"+role)
	ctx = agento11y.WithAgentVersion(ctx, observability.AgentVersion)
	ctx = agento11y.WithTags(ctx, map[string]string{"agent.role": role, "generation.visibility": visibility, "prompt.version": appPrompts.Version})
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
