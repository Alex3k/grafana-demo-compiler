// Package contextengine builds small, role-specific inputs for the agent.
//
// It deliberately uses deterministic selection over the structured session
// state. It does not summarize with an LLM or perform semantic retrieval.
package contextengine

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
)

const SchemaVersion = "context.v1"

const defaultRecentMessageLimit = 6

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
	SchemaVersion            string   `json:"schemaVersion"`
	Role                     Role     `json:"role"`
	BriefVersion             int      `json:"briefVersion,omitempty"`
	IncludedMessageCount     int      `json:"includedMessageCount"`
	IncludedTopicKeys        []string `json:"includedTopicKeys,omitempty"`
	IncludesOperationalState bool     `json:"includesOperationalState"`
	ApproximateCharacters    int      `json:"approximateCharacters"`
}

type Fact struct {
	Topic string `json:"topic"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value"`
}

type OperationalState struct {
	SessionState     string             `json:"sessionState"`
	LatestPrototype  *PrototypeSummary  `json:"latestPrototype,omitempty"`
	LatestDeployment *DeploymentSummary `json:"latestDeployment,omitempty"`
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
	Manifest           Manifest                   `json:"manifest"`
	CurrentUserMessage *domain.Message            `json:"currentUserMessage,omitempty"`
	RecentMessages     []domain.Message           `json:"recentMessages,omitempty"`
	ConfirmedFacts     []Fact                     `json:"confirmedFacts,omitempty"`
	ProposedFacts      []Fact                     `json:"proposedFacts,omitempty"`
	OpenQuestions      []string                   `json:"openQuestions,omitempty"`
	ReferenceState     CollaboratorReferenceState `json:"referenceState,omitempty"`
	OperationalState   OperationalState           `json:"operationalState"`
}

type CuratorContext struct {
	Manifest      Manifest             `json:"manifest"`
	CurrentBrief  *domain.BriefContent `json:"currentBrief,omitempty"`
	DeltaMessages []domain.Message     `json:"deltaMessages"`
}

type FocusedContext struct {
	Manifest          Manifest          `json:"manifest"`
	SelectedTopic     domain.BriefFocus `json:"selectedTopic"`
	GlobalConstraints []Fact            `json:"globalConstraints,omitempty"`
	Dependencies      []Fact            `json:"dependencies,omitempty"`
	FocusedMessages   []domain.Message  `json:"focusedMessages,omitempty"`
}

type EvaluatorContext struct {
	Manifest             Manifest            `json:"manifest"`
	ApprovedBrief        domain.BriefContent `json:"approvedBrief"`
	ExplicitRequirements []Fact              `json:"explicitRequirements"`
	CandidatePlan        string              `json:"candidatePlan"`
}

type PlannerContext struct {
	Manifest        Manifest                   `json:"manifest"`
	ApprovedBrief   domain.BriefContent        `json:"approvedBrief"`
	Evaluation      domain.AlignmentEvaluation `json:"evaluation"`
	LatestPrototype *PrototypeSummary          `json:"latestPrototype,omitempty"`
}

type BuilderContext struct {
	Manifest           Manifest        `json:"manifest"`
	ImplementationPlan json.RawMessage `json:"implementationPlan"`
	Constraints        []string        `json:"constraints"`
}

// Compiler controls deterministic context bounds.
type Compiler struct {
	RecentMessageLimit int
}

func New(recentMessageLimit int) Compiler {
	if recentMessageLimit <= 0 {
		recentMessageLimit = defaultRecentMessageLimit
	}
	return Compiler{RecentMessageLimit: recentMessageLimit}
}

func (c Compiler) Collaborator(session domain.Session) CollaboratorContext {
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
		ReferenceState:     collaboratorReferenceState(session.Brief, current),
		OperationalState:   operationalState(session),
	}
	topics := append(factTopics(facts), factTopics(proposals)...)
	result.Manifest = manifest(RoleCollaborator, briefVersion, len(recent)+boolCount(current != nil), topics, true, result)
	return result
}

// Curator includes only the caller-supplied delta, never the session history.
func (c Compiler) Curator(session domain.Session, delta []domain.Message) CuratorContext {
	messages := conversationalMessages(delta)
	result := CuratorContext{CurrentBrief: cloneBriefContent(session.Brief), DeltaMessages: messages}
	version := 0
	if session.Brief != nil {
		version = session.Brief.Version
	}
	result.Manifest = manifest(RoleCurator, version, len(messages), nil, false, result)
	return result
}

// Focused selects the chosen topic, global confirmed scope constraints, and
// confirmed facts which the chosen topic directly depends on.
func (c Compiler) Focused(session domain.Session, focus domain.BriefFocus, messages []domain.Message) FocusedContext {
	facts := confirmedFacts(session.Brief)
	topic := topicKey(focus.Label)
	constraints := filterFacts(facts, func(f Fact) bool { return strings.HasPrefix(f.Topic, "scope.") })
	dependencies := filterFacts(facts, func(f Fact) bool { return factMatchesFocus(topic, f) || focusedDependency(topic, f.Topic) })
	dependencies = removeFacts(dependencies, constraints)
	focusedMessages := conversationalMessages(messages)
	version := 0
	if session.Brief != nil {
		version = session.Brief.Version
	}
	topics := append([]string{topic}, factTopics(constraints)...)
	topics = append(topics, factTopics(dependencies)...)
	result := FocusedContext{
		SelectedTopic:     focus,
		GlobalConstraints: constraints,
		Dependencies:      dependencies,
		FocusedMessages:   focusedMessages,
	}
	result.Manifest = manifest(RoleFocused, version, len(focusedMessages), topics, false, result)
	return result
}

func (c Compiler) Evaluator(approved domain.LivingBrief, candidatePlan string) EvaluatorContext {
	requirements := confirmedFacts(&approved)
	result := EvaluatorContext{
		ApprovedBrief:        approved.Content,
		ExplicitRequirements: requirements,
		CandidatePlan:        candidatePlan,
	}
	result.Manifest = manifest(RoleEvaluator, approved.Version, 0, factTopics(requirements), false, result)
	return result
}

func (c Compiler) Planner(approved domain.LivingBrief, evaluation domain.AlignmentEvaluation, latest *domain.PrototypeIteration) PlannerContext {
	result := PlannerContext{ApprovedBrief: approved.Content, Evaluation: evaluation}
	if latest != nil {
		result.LatestPrototype = prototypeSummary(*latest, true)
	}
	result.Manifest = manifest(RolePlanner, approved.Version, 0, factTopics(confirmedFacts(&approved)), false, result)
	return result
}

// Builder accepts serialized plan data to avoid coupling this package to the
// chat provider's private plan types.
func (c Compiler) Builder(plan json.RawMessage, constraints []string) BuilderContext {
	result := BuilderContext{
		ImplementationPlan: append(json.RawMessage(nil), plan...),
		Constraints:        append([]string(nil), constraints...),
	}
	result.Manifest = manifest(RoleBuilder, 0, 0, nil, false, result)
	return result
}

func (c Compiler) limit() int {
	if c.RecentMessageLimit <= 0 {
		return defaultRecentMessageLimit
	}
	return c.RecentMessageLimit
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
	addItem := func(topic string, item domain.BriefItem) {
		if item.Status == status && strings.TrimSpace(item.Value) != "" {
			facts = append(facts, Fact{Topic: topic, Name: item.Name, Value: item.Value})
		}
	}
	addItem("audience", content.Audience)
	addItem("company", content.Company)
	addItem("outcome", content.Outcome)
	addItem("stakes", content.Stakes)
	addItem("scenario", content.Scenario)
	addItem("journey", content.Journey)
	addItems := func(topic string, items []domain.BriefItem) {
		for _, item := range items {
			addItem(topic, item)
		}
	}
	addItems("proof_points", content.ProofPoints)
	addItems("scope.included", content.Scope.Included)
	addItems("scope.excluded", content.Scope.Excluded)
	addItem("scope.simulation_boundary", content.Scope.SimulationBoundary)
	addItems("services", content.Services)
	addItems("telemetry", content.Telemetry)
	addItems("grafana_resources", content.GrafanaResources)
	for _, decision := range content.Decisions {
		if decision.Status == status && strings.TrimSpace(decision.Summary) != "" {
			facts = append(facts, Fact{Topic: "decisions", Name: decision.Evidence, Value: decision.Summary})
		}
	}
	return facts
}

func operationalState(session domain.Session) OperationalState {
	state := OperationalState{SessionState: session.State}
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

func collaboratorReferenceState(brief *domain.LivingBrief, current *domain.Message) CollaboratorReferenceState {
	if brief == nil || current == nil {
		return CollaboratorReferenceState{}
	}
	query := strings.ToLower(current.Content)
	wants := func(words ...string) bool {
		for _, word := range words {
			if strings.Contains(query, word) {
				return true
			}
		}
		return false
	}
	all := wants("agreed", "brief", "plan", "everything", "summary")
	result := CollaboratorReferenceState{}
	if all || wants("architecture", "diagram", "mermaid") {
		result.Mermaid = brief.Content.Mermaid
	}
	if all || wants("narrative", "story", "timeline", "run of show") {
		result.Narrative = append([]domain.NarrativeBeat(nil), brief.Content.Narrative...)
	}
	if all || wants("prototype", "build", "generate", "scope") {
		offer := brief.Content.PrototypeOffer
		result.PrototypeOffer = &offer
	}
	if all || wants("accept", "approve", "ready", "confirm") {
		acceptance := brief.Content.Acceptance
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

func focusedDependency(focus, candidate string) bool {
	if candidate == focus || strings.HasPrefix(candidate, focus+".") {
		return true
	}
	category := strings.Split(focus, ".")[0]
	allowed := map[string][]string{
		"telemetry":         {"scenario", "journey", "services"},
		"services":          {"scenario", "journey"},
		"grafana_resources": {"audience", "outcome", "scenario", "proof_points", "telemetry"},
		"narrative":         {"audience", "outcome", "stakes", "scenario", "journey", "proof_points"},
		"architecture":      {"scenario", "services", "telemetry"},
	}
	for _, prefix := range allowed[category] {
		if candidate == prefix || strings.HasPrefix(candidate, prefix+".") {
			return true
		}
	}
	return false
}

func factMatchesFocus(focus string, fact Fact) bool {
	if fact.Topic == focus {
		return true
	}
	if fact.Name == "" {
		return false
	}
	return fact.Topic+"."+topicKey(fact.Name) == focus
}

func topicKey(label string) string {
	key := strings.ToLower(strings.TrimSpace(label))
	replacer := strings.NewReplacer(":", ".", " / ", ".", "/", ".")
	key = replacer.Replace(key)
	segments := strings.Split(key, ".")
	normalized := make([]string, 0, len(segments))
	for _, segment := range segments {
		segment = strings.ReplaceAll(segment, "-", " ")
		segment = strings.Join(strings.Fields(segment), "_")
		if segment != "" {
			normalized = append(normalized, segment)
		}
	}
	return strings.Join(normalized, ".")
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
		topics = append(topics, fact.Topic)
	}
	return uniqueSorted(topics)
}

func manifest(role Role, briefVersion, messageCount int, topics []string, operational bool, payload any) Manifest {
	encoded, _ := json.Marshal(payload)
	return Manifest{
		SchemaVersion: SchemaVersion, Role: role, BriefVersion: briefVersion,
		IncludedMessageCount: messageCount, IncludedTopicKeys: uniqueSorted(topics),
		IncludesOperationalState: operational, ApproximateCharacters: len(encoded),
	}
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
