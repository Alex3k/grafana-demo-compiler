// Package contextengine builds small, role-specific inputs for the agent.
//
// It deliberately uses deterministic selection over the structured session
// state. It does not summarize with an LLM or perform semantic retrieval.
package contextengine

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Alex3k/grafana-demo-compiler/internal/brieftopics"
	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
)

const SchemaVersion = "context.v2"

const defaultRecentMessageLimit = 6
const defaultFocusedMessageLimit = 12

const (
	defaultMaxInputTokens         = 32_000
	defaultFixedTokens            = 2_500
	defaultSafetyMarginTokens     = 1_500
	defaultProviderOverheadTokens = 1_000
	defaultBytesPerToken          = 3
)

type Role string

const (
	RoleCollaborator Role = "collaborator"
	RoleCurator      Role = "brief-curator"
	RoleFocused      Role = "focused-topic"
	RoleEvaluator    Role = "requirement-evaluator"
	RolePlanner      Role = "prototype-planner"
	RoleBuilder      Role = "prototype-builder"
)

// Manifest describes what was selected without exposing prompt contents.
// It is suitable for attaching to Agent Observability generations.
type Manifest struct {
	BriefHash                string            `json:"briefHash,omitempty"`
	SchemaVersion            string            `json:"schemaVersion"`
	Role                     Role              `json:"role"`
	BriefVersion             int               `json:"briefVersion,omitempty"`
	IncludedMessageCount     int               `json:"includedMessageCount"`
	IncludedTopicKeys        []string          `json:"includedTopicKeys,omitempty"`
	IncludesOperationalState bool              `json:"includesOperationalState"`
	ApproximateCharacters    int               `json:"approximateCharacters"`
	EstimatedTokens          int               `json:"estimatedTokens"`
	MaxInputTokens           int               `json:"maxInputTokens"`
	FixedTokens              int               `json:"fixedTokens"`
	SafetyMarginTokens       int               `json:"safetyMarginTokens"`
	ProviderOverheadTokens   int               `json:"providerOverheadTokens"`
	Estimator                string            `json:"estimator"`
	IncludedSections         []SectionDecision `json:"includedSections"`
	DroppedSections          []SectionDecision `json:"droppedSections,omitempty"`
	Truncated                bool              `json:"truncated"`
}

// SectionDecision explains a deterministic context-budget decision without
// exposing the section's contents.
type SectionDecision struct {
	Name            string `json:"name"`
	Required        bool   `json:"required"`
	Priority        int    `json:"priority"`
	EstimatedTokens int    `json:"estimatedTokens"`
	Reason          string `json:"reason,omitempty"`
}

// Config controls deterministic context bounds and the conservative token
// estimate. Start with DefaultConfig and override the fields needed by the
// caller. Non-positive values are restored to their defaults.
type Config struct {
	RecentMessageLimit     int
	FocusedMessageLimit    int
	MaxInputTokens         int
	FixedTokens            int
	SafetyMarginTokens     int
	ProviderOverheadTokens int
	BytesPerToken          int
}

func DefaultConfig() Config {
	return Config{
		RecentMessageLimit: defaultRecentMessageLimit, FocusedMessageLimit: defaultFocusedMessageLimit,
		MaxInputTokens: defaultMaxInputTokens, FixedTokens: defaultFixedTokens,
		SafetyMarginTokens: defaultSafetyMarginTokens, ProviderOverheadTokens: defaultProviderOverheadTokens,
		BytesPerToken: defaultBytesPerToken,
	}
}

// BudgetOverflowError reports when authoritative context cannot fit. Callers
// should fail the generation rather than silently omit required information.
type BudgetOverflowError struct {
	Role           Role
	RequiredTokens int
	MaxInputTokens int
}

func (e *BudgetOverflowError) Error() string {
	return fmt.Sprintf("%s required context needs %d tokens, exceeding %d token input budget", e.Role, e.RequiredTokens, e.MaxInputTokens)
}

func IsBudgetOverflow(err error) bool {
	var target *BudgetOverflowError
	return errors.As(err, &target)
}

type Fact struct {
	TopicID string `json:"topicId"`
	Topic   string `json:"topic"`
	Name    string `json:"name,omitempty"`
	Value   string `json:"value"`
}

type OperationalState struct {
	SessionState     string               `json:"sessionState"`
	GrafanaStack     *domain.GrafanaStack `json:"grafanaStack,omitempty"`
	LatestPrototype  *PrototypeSummary    `json:"latestPrototype,omitempty"`
	LatestDeployment *DeploymentSummary   `json:"latestDeployment,omitempty"`
}

type PrototypeSummary struct {
	ID            string         `json:"id"`
	Number        int            `json:"number"`
	BriefVersion  int            `json:"briefVersion"`
	Status        string         `json:"status"`
	ArtifactCount int            `json:"artifactCount"`
	ChecksPassed  int            `json:"checksPassed"`
	ChecksTotal   int            `json:"checksTotal"`
	Summary       string         `json:"summary,omitempty"`
	Failure       string         `json:"failure,omitempty"`
	Artifacts     []string       `json:"artifacts,omitempty"`
	FailedChecks  []CheckSummary `json:"failedChecks,omitempty"`
}

type CheckSummary struct {
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
}

type CollaboratorReferenceState struct {
	Mermaid        string                 `json:"mermaid,omitempty"`
	Narrative      []domain.NarrativeBeat `json:"narrative,omitempty"`
	PrototypeOffer *domain.PrototypeOffer `json:"prototypeOffer,omitempty"`
	Acceptance     *domain.PlanAcceptance `json:"acceptance,omitempty"`
}

type DeploymentSummary struct {
	ID                 string `json:"id"`
	PrototypeIteration int    `json:"prototypeIteration,omitempty"`
	Target             string `json:"target"`
	Region             string `json:"region,omitempty"`
	StackName          string `json:"stackName,omitempty"`
	StackSlug          string `json:"stackSlug,omitempty"`
	StackURL           string `json:"stackUrl,omitempty"`
	Status             string `json:"status"`
	LatestProgress     string `json:"latestProgress,omitempty"`
}

type CollaboratorContext struct {
	Manifest           Manifest                   `json:"-"`
	CurrentUserMessage *domain.Message            `json:"currentUserMessage,omitempty"`
	RecentMessages     []domain.Message           `json:"recentMessages,omitempty"`
	ConfirmedFacts     []Fact                     `json:"confirmedFacts,omitempty"`
	ProposedFacts      []Fact                     `json:"proposedFacts,omitempty"`
	OpenQuestions      []string                   `json:"openQuestions,omitempty"`
	ReferenceState     CollaboratorReferenceState `json:"referenceState,omitempty"`
	OperationalState   OperationalState           `json:"operationalState"`
}

type CuratorContext struct {
	Manifest      Manifest             `json:"-"`
	CurrentBrief  *domain.BriefContent `json:"currentBrief,omitempty"`
	DeltaMessages []domain.Message     `json:"deltaMessages"`
}

type FocusedContext struct {
	Manifest          Manifest          `json:"-"`
	SelectedTopic     domain.BriefFocus `json:"selectedTopic"`
	GlobalConstraints []Fact            `json:"globalConstraints,omitempty"`
	Dependencies      []Fact            `json:"dependencies,omitempty"`
	FocusedMessages   []domain.Message  `json:"focusedMessages,omitempty"`
}

type EvaluatorContext struct {
	Manifest             Manifest            `json:"-"`
	ApprovedBrief        domain.BriefContent `json:"approvedBrief"`
	ExplicitRequirements []Fact              `json:"explicitRequirements"`
	CandidatePlan        string              `json:"candidatePlan"`
}

type PlannerContext struct {
	Manifest        Manifest                   `json:"-"`
	ApprovedBrief   domain.BriefContent        `json:"approvedBrief"`
	Evaluation      domain.AlignmentEvaluation `json:"evaluation"`
	LatestPrototype *PrototypeSummary          `json:"latestPrototype,omitempty"`
}

type BuilderContext struct {
	Manifest           Manifest        `json:"-"`
	ImplementationPlan json.RawMessage `json:"implementationPlan"`
	Constraints        []string        `json:"constraints"`
}

// Compiler controls deterministic context bounds.
type Compiler struct {
	config Config
}

func New(recentMessageLimit int) Compiler {
	config := DefaultConfig()
	if recentMessageLimit > 0 {
		config.RecentMessageLimit = recentMessageLimit
	}
	return NewWithConfig(config)
}

func NewWithConfig(config Config) Compiler {
	defaults := DefaultConfig()
	if config.RecentMessageLimit <= 0 {
		config.RecentMessageLimit = defaults.RecentMessageLimit
	}
	if config.FocusedMessageLimit <= 0 {
		config.FocusedMessageLimit = defaults.FocusedMessageLimit
	}
	if config.MaxInputTokens <= 0 {
		config.MaxInputTokens = defaults.MaxInputTokens
	}
	if config.FixedTokens <= 0 {
		config.FixedTokens = defaults.FixedTokens
	}
	if config.SafetyMarginTokens <= 0 {
		config.SafetyMarginTokens = defaults.SafetyMarginTokens
	}
	if config.ProviderOverheadTokens <= 0 {
		config.ProviderOverheadTokens = defaults.ProviderOverheadTokens
	}
	if config.BytesPerToken <= 0 {
		config.BytesPerToken = defaults.BytesPerToken
	}
	return Compiler{config: config}
}

func (c Compiler) Collaborator(session domain.Session) (CollaboratorContext, error) {
	complete := conversationalMessages(session.Messages)
	currentIndex := -1
	for index := len(complete) - 1; index >= 0; index-- {
		if complete[index].Role == "user" {
			currentIndex = index
			break
		}
	}

	var current *domain.Message
	var recent []domain.Message
	if currentIndex >= 0 {
		message := complete[currentIndex]
		current = &message
		start := currentIndex - (c.limit() - 1)
		if start < 0 {
			start = 0
		}
		recent = cloneMessages(complete[start:currentIndex])
	}

	facts := confirmedFacts(session.Brief)
	proposals := proposedFacts(session.Brief)
	questions := []string(nil)
	briefVersion := 0
	if session.Brief != nil {
		briefVersion = session.Brief.Version
		questions = append(questions, session.Brief.Content.OpenQuestions...)
	}
	result := CollaboratorContext{
		CurrentUserMessage: current,
		RecentMessages:     recent,
		ConfirmedFacts:     facts,
		ProposedFacts:      proposals,
		OpenQuestions:      questions,
		ReferenceState:     collaboratorReferenceState(session.Brief),
		OperationalState:   operationalState(session),
	}
	sections := []budgetSection{
		section("current_user", true, 100, current, nil, nil),
		section("confirmed_facts", true, 100, facts, factTopics(facts), nil),
		section("recent_messages", false, 70, recent, nil, func() { result.RecentMessages = nil }),
		section("proposed_facts", false, 60, proposals, factTopics(proposals), func() { result.ProposedFacts = nil }),
		section("open_questions", false, 65, questions, nil, func() { result.OpenQuestions = nil }),
		section("reference_state", false, 90, result.ReferenceState, nil, func() { result.ReferenceState = CollaboratorReferenceState{} }),
		section("operational_state", false, 80, result.OperationalState, nil, func() { result.OperationalState = OperationalState{} }),
	}
	manifest, err := c.manifest(RoleCollaborator, briefVersion, len(recent)+boolCount(current != nil), true, sections)
	if err != nil {
		return CollaboratorContext{}, err
	}
	result.Manifest = manifest
	return result, nil
}

// Curator includes only the caller-supplied delta, never the session history.
func (c Compiler) Curator(session domain.Session, delta []domain.Message) (CuratorContext, error) {
	messages := conversationalMessages(delta)
	result := CuratorContext{CurrentBrief: cloneBriefContent(session.Brief), DeltaMessages: messages}
	version := 0
	if session.Brief != nil {
		version = session.Brief.Version
	}
	manifest, err := c.manifest(RoleCurator, version, len(messages), false, []budgetSection{
		section("current_brief", true, 100, result.CurrentBrief, nil, nil),
		section("delta_messages", true, 100, messages, nil, nil),
	})
	if err != nil {
		return CuratorContext{}, err
	}
	result.Manifest = manifest
	return result, nil
}

// Focused selects the chosen topic, global confirmed scope constraints, and
// confirmed facts which the chosen topic directly depends on.
func (c Compiler) Focused(session domain.Session, focus domain.BriefFocus, messages []domain.Message) (FocusedContext, error) {
	facts := confirmedFacts(session.Brief)
	topic := focus.TopicID
	constraints := filterFacts(facts, func(f Fact) bool { return brieftopics.IsScope(f.TopicID) })
	dependencies := filterFacts(facts, func(f Fact) bool { return brieftopics.IsDependency(topic, f.TopicID) })
	dependencies = removeFacts(dependencies, constraints)
	complete := conversationalMessages(messages)
	focusedMessages := tailMessages(complete, c.focusedLimit())
	// The history window must not discard the request before budgeting it.
	for index := len(complete) - 1; index >= 0; index-- {
		if complete[index].Role == "user" {
			if index < len(complete)-len(focusedMessages) {
				focusedMessages[0] = complete[index]
			}
			break
		}
	}
	version := 0
	if session.Brief != nil {
		version = session.Brief.Version
	}
	result := FocusedContext{
		SelectedTopic:     focus,
		GlobalConstraints: constraints,
		Dependencies:      dependencies,
		FocusedMessages:   focusedMessages,
	}
	sections := []budgetSection{
		section("selected_topic", true, 100, focus, []string{topic}, nil),
		section("global_constraints", true, 100, constraints, factTopics(constraints), nil),
		section("dependencies", false, 80, dependencies, factTopics(dependencies), func() { result.Dependencies = nil }),
	}
	latestUser := -1
	for index := len(focusedMessages) - 1; index >= 0; index-- {
		if focusedMessages[index].Role == "user" {
			latestUser = index
			break
		}
	}
	dropped := make([]bool, len(focusedMessages))
	// Equal-priority messages are considered newest first by their section name.
	// Keep the original chronological order when assembling the final packet.
	for index := len(focusedMessages) - 1; index >= 0; index-- {
		name := fmt.Sprintf("focused_message_%08d", len(focusedMessages)-1-index)
		sections = append(sections, section(name, index == latestUser, 70, focusedMessages[index], nil, func() { dropped[index] = true }))
	}
	manifest, err := c.manifest(RoleFocused, version, len(focusedMessages), false, sections)
	if err != nil {
		return FocusedContext{}, err
	}
	result.FocusedMessages = nil
	for index, message := range focusedMessages {
		if !dropped[index] {
			result.FocusedMessages = append(result.FocusedMessages, message)
		}
	}
	manifest.IncludedMessageCount = len(result.FocusedMessages)
	result.Manifest = manifest
	return result, nil
}

func (c Compiler) Evaluator(approved domain.LivingBrief, candidatePlan string) (EvaluatorContext, error) {
	requirements := confirmedFacts(&approved)
	result := EvaluatorContext{
		ApprovedBrief:        approved.Content,
		ExplicitRequirements: requirements,
		CandidatePlan:        candidatePlan,
	}
	manifest, err := c.manifest(RoleEvaluator, approved.Version, 0, false, []budgetSection{
		section("approved_brief", true, 100, result.ApprovedBrief, nil, nil),
		section("explicit_requirements", true, 100, requirements, factTopics(requirements), nil),
		section("candidate_plan", true, 100, candidatePlan, nil, nil),
	})
	if err != nil {
		return EvaluatorContext{}, err
	}
	manifest.BriefHash = approved.Content.AcceptanceHash()
	result.Manifest = manifest
	return result, nil
}

func (c Compiler) Planner(approved domain.LivingBrief, evaluation domain.AlignmentEvaluation, latest *domain.PrototypeIteration) (PlannerContext, error) {
	result := PlannerContext{ApprovedBrief: approved.Content, Evaluation: evaluation}
	if latest != nil {
		result.LatestPrototype = prototypeSummary(*latest, true)
	}
	manifest, err := c.manifest(RolePlanner, approved.Version, 0, false, []budgetSection{
		section("approved_brief", true, 100, result.ApprovedBrief, factTopics(confirmedFacts(&approved)), nil),
		section("evaluation", true, 100, evaluation, nil, nil),
		section("latest_prototype", false, 60, result.LatestPrototype, nil, func() { result.LatestPrototype = nil }),
	})
	if err != nil {
		return PlannerContext{}, err
	}
	result.Manifest = manifest
	return result, nil
}

// Builder accepts serialized plan data to avoid coupling this package to the
// chat provider's private plan types.
func (c Compiler) Builder(plan json.RawMessage, constraints []string) (BuilderContext, error) {
	result := BuilderContext{
		ImplementationPlan: append(json.RawMessage(nil), plan...),
		Constraints:        append([]string(nil), constraints...),
	}
	manifest, err := c.manifest(RoleBuilder, 0, 0, false, []budgetSection{
		section("implementation_plan", true, 100, result.ImplementationPlan, nil, nil),
		section("constraints", true, 100, result.Constraints, nil, nil),
	})
	if err != nil {
		return BuilderContext{}, err
	}
	result.Manifest = manifest
	return result, nil
}

func (c Compiler) limit() int {
	if c.config.RecentMessageLimit <= 0 {
		return defaultRecentMessageLimit
	}
	return c.config.RecentMessageLimit
}

func (c Compiler) focusedLimit() int {
	if c.config.FocusedMessageLimit <= 0 {
		return defaultFocusedMessageLimit
	}
	return c.config.FocusedMessageLimit
}

func conversationalMessages(messages []domain.Message) []domain.Message {
	result := make([]domain.Message, 0, len(messages))
	for _, message := range messages {
		if message.Kind == "message" && message.Status == "complete" && (message.Role == "user" || message.Role == "assistant") {
			result = append(result, message)
		}
	}
	return result
}

func confirmedFacts(brief *domain.LivingBrief) []Fact {
	return factsWithStatus(brief, "confirmed")
}

func proposedFacts(brief *domain.LivingBrief) []Fact {
	return factsWithStatus(brief, "proposed")
}

func factsWithStatus(brief *domain.LivingBrief, status string) []Fact {
	if brief == nil {
		return nil
	}
	content := brief.Content
	var facts []Fact
	addItem := func(topic, fallbackID string, item domain.BriefItem) {
		if item.Status == status && strings.TrimSpace(item.Value) != "" {
			topicID := item.ID
			if topicID == "" {
				topicID = fallbackID
			}
			facts = append(facts, Fact{TopicID: topicID, Topic: topic, Name: item.Name, Value: item.Value})
		}
	}
	addItem("audience", brieftopics.Audience, content.Audience)
	addItem("company", brieftopics.Company, content.Company)
	addItem("outcome", brieftopics.Outcome, content.Outcome)
	addItem("stakes", brieftopics.Stakes, content.Stakes)
	addItem("scenario", brieftopics.Scenario, content.Scenario)
	addItem("journey", brieftopics.Journey, content.Journey)
	addItems := func(topic string, items []domain.BriefItem) {
		for _, item := range items {
			addItem(topic, "", item)
		}
	}
	addItems("proof_points", content.ProofPoints)
	addItems("scope.included", content.Scope.Included)
	addItems("scope.excluded", content.Scope.Excluded)
	addItem("scope.simulation_boundary", brieftopics.SimulationBoundary, content.Scope.SimulationBoundary)
	addItems("services", content.Services)
	addItems("telemetry", content.Telemetry)
	addItems("grafana_resources", content.GrafanaResources)
	for _, decision := range content.Decisions {
		if decision.Status == status && strings.TrimSpace(decision.Summary) != "" {
			facts = append(facts, Fact{TopicID: "decisions", Topic: "decisions", Name: decision.Evidence, Value: decision.Summary})
		}
	}
	return facts
}

func operationalState(session domain.Session) OperationalState {
	state := OperationalState{SessionState: session.State}
	if session.GrafanaStack != nil {
		stack := *session.GrafanaStack
		stack.Progress = nil
		state.GrafanaStack = &stack
	}
	if len(session.Prototypes) > 0 {
		latest := session.Prototypes[0]
		for _, candidate := range session.Prototypes[1:] {
			if candidate.Number > latest.Number || (candidate.Number == latest.Number && candidate.UpdatedAt.After(latest.UpdatedAt)) {
				latest = candidate
			}
		}
		state.LatestPrototype = prototypeSummary(latest, false)
	}
	if len(session.Deployments) > 0 {
		latest := session.Deployments[0]
		for _, candidate := range session.Deployments[1:] {
			if candidate.UpdatedAt.After(latest.UpdatedAt) {
				latest = candidate
			}
		}
		prototypeIteration := 0
		for _, prototype := range session.Prototypes {
			if prototype.ID == latest.PrototypeIterationID {
				prototypeIteration = prototype.Number
				break
			}
		}
		progress := ""
		if latest.Status != "failed" && latest.Status != "interrupted" && len(latest.Progress) > 0 {
			progress = latest.Progress[len(latest.Progress)-1]
			characters := []rune(progress)
			if len(characters) > 240 {
				progress = string(characters[:240])
			}
		}
		state.LatestDeployment = &DeploymentSummary{
			ID: latest.ID, PrototypeIteration: prototypeIteration,
			Target: latest.Target, Region: latest.Region, StackName: latest.StackName,
			StackSlug: latest.StackSlug, StackURL: latest.StackURL, Status: latest.Status,
			LatestProgress: progress,
		}
	}
	return state
}

func prototypeSummary(item domain.PrototypeIteration, includeIterationDetails bool) *PrototypeSummary {
	passed := 0
	for _, check := range item.Checks {
		if check.Status == "passed" {
			passed++
		}
	}
	result := &PrototypeSummary{
		ID: item.ID, Number: item.Number, BriefVersion: item.BriefVersion,
		Status: item.Status, ArtifactCount: len(item.Artifacts), ChecksPassed: passed, ChecksTotal: len(item.Checks),
	}
	if includeIterationDetails {
		result.Summary = bounded(item.Summary, 800)
		result.Failure = bounded(item.Error, 500)
		for _, artifact := range item.Artifacts {
			if len(result.Artifacts) == 20 {
				break
			}
			result.Artifacts = append(result.Artifacts, artifact.Path)
		}
		for _, check := range item.Checks {
			if check.Status != "passed" {
				result.FailedChecks = append(result.FailedChecks, CheckSummary{Name: check.Name, Detail: bounded(check.Detail, 300)})
			}
		}
	}
	return result
}

func collaboratorReferenceState(brief *domain.LivingBrief) CollaboratorReferenceState {
	if brief == nil {
		return CollaboratorReferenceState{}
	}
	offer := brief.Content.PrototypeOffer
	acceptance := brief.Content.Acceptance
	result := CollaboratorReferenceState{
		Mermaid:   brief.Content.Mermaid,
		Narrative: append([]domain.NarrativeBeat(nil), brief.Content.Narrative...),
	}
	if offer.Ready || offer.Summary != "" || len(offer.Included)+len(offer.Excluded)+len(offer.Services)+len(offer.Telemetry)+len(offer.GrafanaResources) > 0 {
		result.PrototypeOffer = &offer
	}
	if acceptance.Accepted || acceptance.Evidence != "" || acceptance.Evaluation.Result != "" {
		result.Acceptance = &acceptance
	}
	return result
}

func bounded(value string, limit int) string {
	characters := []rune(strings.TrimSpace(value))
	if len(characters) <= limit {
		return string(characters)
	}
	return string(characters[:limit])
}

func filterFacts(facts []Fact, keep func(Fact) bool) []Fact {
	var result []Fact
	for _, fact := range facts {
		if keep(fact) {
			result = append(result, fact)
		}
	}
	return result
}

func removeFacts(facts, excluded []Fact) []Fact {
	blocked := make(map[Fact]struct{}, len(excluded))
	for _, fact := range excluded {
		blocked[fact] = struct{}{}
	}
	result := make([]Fact, 0, len(facts))
	for _, fact := range facts {
		if _, found := blocked[fact]; !found {
			result = append(result, fact)
		}
	}
	return result
}

func factTopics(facts []Fact) []string {
	topics := make([]string, 0, len(facts))
	for _, fact := range facts {
		topics = append(topics, fact.TopicID)
	}
	return uniqueSorted(topics)
}

type budgetSection struct {
	name     string
	required bool
	priority int
	bytes    int
	present  bool
	topics   []string
	drop     func()
}

func section(name string, required bool, priority int, value any, topics []string, drop func()) budgetSection {
	encoded, _ := json.Marshal(value)
	present := required || !emptyJSON(encoded)
	return budgetSection{
		name: name, required: required, priority: priority, bytes: len(encoded), present: present,
		topics: uniqueSorted(topics), drop: drop,
	}
}

func emptyJSON(encoded []byte) bool {
	value := string(encoded)
	return value == "null" || value == "[]" || value == "{}" || value == `""`
}

func (c Compiler) manifest(role Role, briefVersion, messageCount int, operational bool, sections []budgetSection) (Manifest, error) {
	fixed := c.config.FixedTokens + c.config.SafetyMarginTokens + c.config.ProviderOverheadTokens
	requiredTokens := fixed
	for _, candidate := range sections {
		if candidate.required {
			requiredTokens += c.estimate(candidate.bytes)
		}
	}
	if requiredTokens > c.config.MaxInputTokens {
		return Manifest{}, &BudgetOverflowError{Role: role, RequiredTokens: requiredTokens, MaxInputTokens: c.config.MaxInputTokens}
	}

	optional := make([]budgetSection, 0, len(sections))
	for _, candidate := range sections {
		if !candidate.required && candidate.present {
			optional = append(optional, candidate)
		}
	}
	sort.SliceStable(optional, func(i, j int) bool {
		if optional[i].priority == optional[j].priority {
			return optional[i].name < optional[j].name
		}
		return optional[i].priority > optional[j].priority
	})

	includedOptional := make(map[string]bool, len(optional))
	total := requiredTokens
	for _, candidate := range optional {
		tokens := c.estimate(candidate.bytes)
		if total+tokens <= c.config.MaxInputTokens {
			includedOptional[candidate.name] = true
			total += tokens
		}
	}

	manifest := Manifest{
		SchemaVersion: SchemaVersion, Role: role, BriefVersion: briefVersion,
		IncludesOperationalState: operational,
		EstimatedTokens:          total, MaxInputTokens: c.config.MaxInputTokens,
		FixedTokens: c.config.FixedTokens, SafetyMarginTokens: c.config.SafetyMarginTokens,
		ProviderOverheadTokens: c.config.ProviderOverheadTokens,
		Estimator:              fmt.Sprintf("utf8-bytes/%d-ceil", c.config.BytesPerToken),
	}
	for _, candidate := range sections {
		if !candidate.present {
			continue
		}
		decision := SectionDecision{
			Name: candidate.name, Required: candidate.required, Priority: candidate.priority,
			EstimatedTokens: c.estimate(candidate.bytes),
		}
		if candidate.required || includedOptional[candidate.name] {
			manifest.IncludedSections = append(manifest.IncludedSections, decision)
			manifest.ApproximateCharacters += candidate.bytes
			manifest.IncludedTopicKeys = append(manifest.IncludedTopicKeys, candidate.topics...)
			continue
		}
		decision.Reason = "context budget exhausted"
		manifest.DroppedSections = append(manifest.DroppedSections, decision)
		manifest.Truncated = true
		if candidate.drop != nil {
			candidate.drop()
		}
	}
	manifest.IncludedTopicKeys = uniqueSorted(manifest.IncludedTopicKeys)
	manifest.IncludedMessageCount = includedMessageCount(role, messageCount, manifest.DroppedSections)
	if operational && sectionWasDropped(manifest.DroppedSections, "operational_state") {
		manifest.IncludesOperationalState = false
	}
	return manifest, nil
}

func (c Compiler) estimate(bytes int) int {
	if bytes <= 0 {
		return 0
	}
	return (bytes + c.config.BytesPerToken - 1) / c.config.BytesPerToken
}

func includedMessageCount(role Role, original int, dropped []SectionDecision) int {
	for _, decision := range dropped {
		if (role == RoleCollaborator && decision.Name == "recent_messages") ||
			(role == RoleFocused && decision.Name == "focused_messages") {
			return boolCount(role == RoleCollaborator && original > 0)
		}
	}
	return original
}

func sectionWasDropped(sections []SectionDecision, name string) bool {
	for _, candidate := range sections {
		if candidate.Name == name {
			return true
		}
	}
	return false
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, found := seen[value]; !found {
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func cloneMessages(messages []domain.Message) []domain.Message {
	return append([]domain.Message(nil), messages...)
}

func tailMessages(messages []domain.Message, limit int) []domain.Message {
	if len(messages) <= limit {
		return cloneMessages(messages)
	}
	return cloneMessages(messages[len(messages)-limit:])
}

func cloneBriefContent(brief *domain.LivingBrief) *domain.BriefContent {
	if brief == nil {
		return nil
	}
	copy := brief.Content
	return &copy
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}
