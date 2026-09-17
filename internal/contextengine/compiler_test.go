package contextengine

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
)

func TestCollaboratorBoundsConversationAndUsesConfirmedFacts(t *testing.T) {
	session := domain.Session{
		State: "generated",
		Messages: []domain.Message{
			message("1", "user", "old request", "complete"),
			message("2", "assistant", "old response", "complete"),
			{ID: "activity", Role: "system", Kind: "activity", Content: "working", Status: "complete"},
			message("3", "user", "recent request", "complete"),
			message("4", "assistant", "partial response", "streaming"),
			message("5", "user", "current request", "complete"),
		},
		Brief: &domain.LivingBrief{Version: 4, Content: domain.BriefContent{
			Audience:      domain.BriefItem{Value: "SREs", Status: "confirmed"},
			Company:       domain.BriefItem{Value: "Acme", Status: "proposed"},
			OpenQuestions: []string{"Which region?"},
		}},
		Prototypes: []domain.PrototypeIteration{
			{ID: "new", Number: 2, BriefVersion: 4, Status: "complete"},
			{ID: "old", Number: 1, Status: "failed"},
		},
		Deployments: []domain.Deployment{{ID: "deploy", Target: "local", Status: "running"}},
	}

	got, err := New(3).Collaborator(session)
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentUserMessage == nil || got.CurrentUserMessage.ID != "5" {
		t.Fatalf("current user message = %#v", got.CurrentUserMessage)
	}
	if ids := messageIDs(got.RecentMessages); !reflect.DeepEqual(ids, []string{"2", "3"}) {
		t.Fatalf("recent messages = %v", ids)
	}
	if !reflect.DeepEqual(got.ConfirmedFacts, []Fact{{TopicID: "audience", Topic: "audience", Value: "SREs"}}) {
		t.Fatalf("confirmed facts = %#v", got.ConfirmedFacts)
	}
	if !reflect.DeepEqual(got.ProposedFacts, []Fact{{TopicID: "company", Topic: "company", Value: "Acme"}}) {
		t.Fatalf("proposed facts = %#v", got.ProposedFacts)
	}
	if got.OperationalState.LatestPrototype == nil || got.OperationalState.LatestPrototype.ID != "new" {
		t.Fatalf("prototype state = %#v", got.OperationalState.LatestPrototype)
	}
	if got.Manifest.BriefVersion != 4 || got.Manifest.IncludedMessageCount != 3 || !got.Manifest.IncludesOperationalState {
		t.Fatalf("manifest = %#v", got.Manifest)
	}
	if !reflect.DeepEqual(got.Manifest.IncludedTopicKeys, []string{"audience", "company"}) {
		t.Fatalf("manifest topics = %v", got.Manifest.IncludedTopicKeys)
	}
	assertManifest(t, got.Manifest, RoleCollaborator)
}

func TestCollaboratorSelectsPersistedReferenceStateForLatestQuestion(t *testing.T) {
	brief := &domain.LivingBrief{Content: domain.BriefContent{
		Mermaid:        "flowchart LR; A-->B",
		Narrative:      []domain.NarrativeBeat{{Stage: "Incident", Detail: "Find the fault", Minutes: 2}},
		PrototypeOffer: domain.PrototypeOffer{Ready: true, Summary: "Small camera demo"},
		Acceptance:     domain.PlanAcceptance{Accepted: true},
	}}

	architecture, err := New(6).Collaborator(domain.Session{Brief: brief, Messages: []domain.Message{
		message("question", "user", "Show me the architecture diagram", "complete"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if architecture.ReferenceState.Mermaid == "" || len(architecture.ReferenceState.Narrative) != 1 || architecture.ReferenceState.PrototypeOffer == nil || architecture.ReferenceState.Acceptance == nil {
		t.Fatalf("architecture reference state = %#v", architecture.ReferenceState)
	}

	summary, err := New(6).Collaborator(domain.Session{Brief: brief, Messages: []domain.Message{
		message("question", "user", "Summarise the current plan", "complete"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if summary.ReferenceState.Mermaid == "" || len(summary.ReferenceState.Narrative) != 1 || summary.ReferenceState.PrototypeOffer == nil || summary.ReferenceState.Acceptance == nil {
		t.Fatalf("summary reference state = %#v", summary.ReferenceState)
	}
}

func TestCuratorUsesOnlySuppliedDelta(t *testing.T) {
	session := domain.Session{
		Messages: []domain.Message{message("history", "user", "must not leak", "complete")},
		Brief:    &domain.LivingBrief{Version: 2},
	}
	delta := []domain.Message{
		message("new", "user", "new decision", "complete"),
		message("partial", "assistant", "unfinished", "streaming"),
		{ID: "activity", Role: "system", Kind: "activity", Status: "complete"},
	}

	got, err := New(0).Curator(session, delta)
	if err != nil {
		t.Fatal(err)
	}
	if ids := messageIDs(got.DeltaMessages); !reflect.DeepEqual(ids, []string{"new"}) {
		t.Fatalf("delta messages = %v", ids)
	}
	if got.Manifest.IncludedMessageCount != 1 || got.Manifest.BriefVersion != 2 {
		t.Fatalf("manifest = %#v", got.Manifest)
	}
}

func TestFocusedIncludesOnlyTopicDependenciesAndGlobalConstraints(t *testing.T) {
	session := domain.Session{Brief: &domain.LivingBrief{Version: 7, Content: domain.BriefContent{
		Audience: domain.BriefItem{Value: "Executives", Status: "confirmed"},
		Scenario: domain.BriefItem{Value: "Camera outage", Status: "confirmed"},
		Scope: domain.BriefScope{
			Excluded:           []domain.BriefItem{{ID: "scopeout_auth", Name: "Auth", Value: "No auth", Status: "confirmed"}},
			SimulationBoundary: domain.BriefItem{Value: "Cameras are simulated", Status: "confirmed"},
		},
		Services: []domain.BriefItem{{ID: "svc_ingest", Name: "Ingest", Value: "Go ingest service", Status: "confirmed"}},
		Telemetry: []domain.BriefItem{
			{ID: "tel_logs", Name: "Logs", Value: "JSON logs", Status: "confirmed"},
			{ID: "tel_traces", Name: "Traces", Value: "OTel traces", Status: "confirmed"},
		},
		GrafanaResources: []domain.BriefItem{{ID: "graf_dashboard", Name: "Dashboard", Value: "Fleet dashboard", Status: "confirmed"}},
	}}}

	got, err := New(0).Focused(session, domain.BriefFocus{TopicID: "tel_logs", Label: "Telemetry: Logs", Value: "JSON logs", Status: "confirmed"}, []domain.Message{
		message("focused", "user", "include device_id", "complete"),
		message("main", "assistant", "unfinished", "streaming"),
	})
	if err != nil {
		t.Fatal(err)
	}

	if !hasFact(got.Dependencies, "telemetry", "Logs") || hasFact(got.Dependencies, "telemetry", "Traces") {
		t.Fatalf("telemetry dependencies = %#v", got.Dependencies)
	}
	if !hasTopic(got.Dependencies, "scenario") || !hasTopic(got.Dependencies, "services") {
		t.Fatalf("missing direct dependencies: %#v", got.Dependencies)
	}
	if hasTopic(got.Dependencies, "audience") || hasTopic(got.Dependencies, "grafana_resources") {
		t.Fatalf("unrelated facts leaked: %#v", got.Dependencies)
	}
	if len(got.GlobalConstraints) != 2 || got.Manifest.IncludedMessageCount != 1 {
		t.Fatalf("focused context = %#v", got)
	}
	wantTopics := []string{"scenario", "scope.simulation_boundary", "scopeout_auth", "svc_ingest", "tel_logs"}
	if !reflect.DeepEqual(got.Manifest.IncludedTopicKeys, wantTopics) {
		t.Fatalf("topics = %v, want %v", got.Manifest.IncludedTopicKeys, wantTopics)
	}
}

func TestEvaluatorPlannerAndBuilderEnvelopes(t *testing.T) {
	brief := domain.LivingBrief{Version: 9, Content: domain.BriefContent{
		Outcome: domain.BriefItem{Value: "Find the slow service", Status: "confirmed"},
	}}
	compiler := New(0)
	evaluator, err := compiler.Evaluator(brief, "three-service trace story")
	if err != nil {
		t.Fatal(err)
	}
	if evaluator.CandidatePlan == "" || len(evaluator.ExplicitRequirements) != 1 {
		t.Fatalf("evaluator = %#v", evaluator)
	}
	iteration := domain.PrototypeIteration{
		ID: "iteration", Number: 3, Status: "failed", Summary: "Built the first camera flow", Error: "compile failed",
		Artifacts: []domain.PrototypeArtifact{{Path: "cmd/api/main.go"}},
		Checks:    []domain.PrototypeCheck{{Name: "go build", Status: "failed", Detail: "missing import"}},
	}
	planner, err := compiler.Planner(brief, domain.AlignmentEvaluation{Result: "pass"}, &iteration)
	if err != nil {
		t.Fatal(err)
	}
	if planner.LatestPrototype == nil || planner.LatestPrototype.ID != "iteration" || planner.LatestPrototype.Summary == "" || planner.LatestPrototype.Failure == "" || len(planner.LatestPrototype.Artifacts) != 1 || len(planner.LatestPrototype.FailedChecks) != 1 {
		t.Fatalf("planner = %#v", planner)
	}
	plan := json.RawMessage(`{"files":["main.go"]}`)
	builder, err := compiler.Builder(plan, []string{"Go services only", "local deployment only"})
	if err != nil {
		t.Fatal(err)
	}
	plan[2] = 'X'
	if !json.Valid(builder.ImplementationPlan) {
		t.Fatal("builder retained caller-owned plan bytes")
	}
	if builder.Manifest.Role != RoleBuilder || builder.Manifest.ApproximateCharacters == 0 {
		t.Fatalf("builder manifest = %#v", builder.Manifest)
	}
}

func TestManifestV2ReportsBudgetAndNamedSections(t *testing.T) {
	got, err := New(0).Builder(json.RawMessage(`{"files":["main.go"]}`), []string{"local only"})
	if err != nil {
		t.Fatal(err)
	}
	manifest := got.Manifest
	if manifest.SchemaVersion != "context.v2" || manifest.Estimator != "utf8-bytes/3-ceil" {
		t.Fatalf("manifest identity = %#v", manifest)
	}
	if manifest.EstimatedTokens <= manifest.FixedTokens || manifest.MaxInputTokens != defaultMaxInputTokens {
		t.Fatalf("manifest budget = %#v", manifest)
	}
	if manifest.SafetyMarginTokens != defaultSafetyMarginTokens || manifest.ProviderOverheadTokens != defaultProviderOverheadTokens {
		t.Fatalf("manifest overhead = %#v", manifest)
	}
	want := []string{"implementation_plan", "constraints"}
	if names := decisionNames(manifest.IncludedSections); !reflect.DeepEqual(names, want) {
		t.Fatalf("included sections = %v, want %v", names, want)
	}
	if manifest.Truncated || len(manifest.DroppedSections) != 0 {
		t.Fatalf("unexpected truncation = %#v", manifest)
	}
}

func TestCollaboratorDropsOptionalSectionsByPriority(t *testing.T) {
	session := domain.Session{
		State: "running",
		Messages: []domain.Message{
			message("old", "assistant", stringsOfLength(90), "complete"),
			message("current", "user", "current request", "complete"),
		},
		Brief: &domain.LivingBrief{Version: 2, Content: domain.BriefContent{
			Audience:      domain.BriefItem{Value: "SREs", Status: "confirmed"},
			Company:       domain.BriefItem{Value: stringsOfLength(60), Status: "proposed"},
			OpenQuestions: []string{stringsOfLength(60)},
		}},
		Deployments: []domain.Deployment{{ID: "deployment", Target: "local", Status: "running"}},
	}
	wideConfig := DefaultConfig()
	wideConfig.FixedTokens = 1
	wideConfig.SafetyMarginTokens = 1
	wideConfig.ProviderOverheadTokens = 1
	wide, err := NewWithConfig(wideConfig).Collaborator(session)
	if err != nil {
		t.Fatal(err)
	}
	budget := 3 + decisionTokens(wide.Manifest.IncludedSections, "current_user", "confirmed_facts", "operational_state")
	tightConfig := wideConfig
	tightConfig.MaxInputTokens = budget

	got, err := NewWithConfig(tightConfig).Collaborator(session)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Manifest.Truncated || !got.Manifest.IncludesOperationalState {
		t.Fatalf("manifest = %#v", got.Manifest)
	}
	if names := decisionNames(got.Manifest.IncludedSections); !reflect.DeepEqual(names, []string{"current_user", "confirmed_facts", "operational_state"}) {
		t.Fatalf("included sections = %v", names)
	}
	if names := decisionNames(got.Manifest.DroppedSections); !reflect.DeepEqual(names, []string{"recent_messages", "proposed_facts", "open_questions"}) {
		t.Fatalf("dropped sections = %v", names)
	}
	if !reflect.DeepEqual(got.Manifest.IncludedTopicKeys, []string{"audience"}) {
		t.Fatalf("included topics = %v", got.Manifest.IncludedTopicKeys)
	}
	if len(got.RecentMessages) != 0 || len(got.ProposedFacts) != 0 || len(got.OpenQuestions) != 0 {
		t.Fatalf("dropped payload remains = %#v", got)
	}
	if got.Manifest.IncludedMessageCount != 1 {
		t.Fatalf("included message count = %d", got.Manifest.IncludedMessageCount)
	}
}

func TestRequiredContextOverflowReturnsTypedError(t *testing.T) {
	config := DefaultConfig()
	config.MaxInputTokens = 10
	config.FixedTokens = 8
	config.SafetyMarginTokens = 1
	config.ProviderOverheadTokens = 1
	_, err := NewWithConfig(config).Builder(json.RawMessage(`{"large":"required"}`), []string{"also required"})
	if err == nil || !IsBudgetOverflow(err) {
		t.Fatalf("error = %v", err)
	}
	var overflow *BudgetOverflowError
	if !errors.As(err, &overflow) || overflow.Role != RoleBuilder || overflow.RequiredTokens <= overflow.MaxInputTokens {
		t.Fatalf("overflow = %#v", overflow)
	}
}

func TestConfiguredEstimatorUsesUTF8Bytes(t *testing.T) {
	config := DefaultConfig()
	config.BytesPerToken = 2
	config.FixedTokens = 1
	config.SafetyMarginTokens = 1
	config.ProviderOverheadTokens = 1
	got, err := NewWithConfig(config).Builder(json.RawMessage(`{"word":"café"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Manifest.Estimator != "utf8-bytes/2-ceil" {
		t.Fatalf("estimator = %q", got.Manifest.Estimator)
	}
	planTokens := decisionTokens(got.Manifest.IncludedSections, "implementation_plan")
	encoded, _ := json.Marshal(got.ImplementationPlan)
	want := (len(encoded) + 1) / 2
	if planTokens != want {
		t.Fatalf("plan tokens = %d, want %d for %d bytes", planTokens, want, len(encoded))
	}
}

func TestManifestIsNotIncludedInModelPayload(t *testing.T) {
	got, err := New(0).Builder(json.RawMessage(`{"files":["main.go"]}`), []string{"local only"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "schemaVersion") || strings.Contains(string(payload), "includedSections") {
		t.Fatalf("context manifest leaked into model payload: %s", payload)
	}
}

func TestFocusedBoundsHistoryToMostRecentMessages(t *testing.T) {
	config := DefaultConfig()
	config.FocusedMessageLimit = 2
	messages := []domain.Message{
		message("one", "user", "one", "complete"),
		message("two", "assistant", "two", "complete"),
		message("three", "user", "three", "complete"),
	}
	got, err := NewWithConfig(config).Focused(domain.Session{}, domain.BriefFocus{Label: "Scenario"}, messages)
	if err != nil {
		t.Fatal(err)
	}
	if ids := messageIDs(got.FocusedMessages); !reflect.DeepEqual(ids, []string{"two", "three"}) {
		t.Fatalf("focused messages = %v", ids)
	}
	if got.Manifest.IncludedMessageCount != 2 {
		t.Fatalf("included message count = %d", got.Manifest.IncludedMessageCount)
	}
}

func TestFocusedRetainsCurrentUserAndNewestHistoryWithinBudget(t *testing.T) {
	messages := []domain.Message{
		message("old", "user", stringsOfLength(6_000), "complete"),
		message("older", "assistant", stringsOfLength(600), "complete"),
		message("current", "user", "Use JSON logs for this scenario", "complete"),
		message("newest", "assistant", stringsOfLength(600), "complete"),
		message("streaming", "user", stringsOfLength(6_000), "streaming"),
	}
	for _, tt := range []struct {
		name   string
		budget int
		ids    []string
	}{
		{name: "current only", budget: 150, ids: []string{"current"}},
		{name: "partial history", budget: 400, ids: []string{"current", "newest"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := DefaultConfig()
			config.MaxInputTokens = tt.budget
			config.FixedTokens = 1
			config.SafetyMarginTokens = 1
			config.ProviderOverheadTokens = 1
			got, err := NewWithConfig(config).Focused(domain.Session{}, domain.BriefFocus{Label: "Scenario"}, messages)
			if err != nil {
				t.Fatal(err)
			}
			if ids := messageIDs(got.FocusedMessages); !reflect.DeepEqual(ids, tt.ids) {
				t.Fatalf("focused messages = %v, want %v", ids, tt.ids)
			}
			if got.Manifest.IncludedMessageCount != len(tt.ids) {
				t.Fatalf("included message count = %d, want %d", got.Manifest.IncludedMessageCount, len(tt.ids))
			}
			if !got.Manifest.Truncated || len(got.Manifest.DroppedSections) == 0 || got.Manifest.EstimatedTokens > tt.budget {
				t.Fatalf("manifest = %#v", got.Manifest)
			}
		})
	}
}

func TestFocusedMessageLimitRetainsCurrentUserBeforeAssistant(t *testing.T) {
	config := DefaultConfig()
	config.FocusedMessageLimit = 1
	got, err := NewWithConfig(config).Focused(domain.Session{}, domain.BriefFocus{Label: "Scenario"}, []domain.Message{
		message("current", "user", "Use JSON logs", "complete"),
		message("assistant", "assistant", "JSON logs selected", "complete"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if ids := messageIDs(got.FocusedMessages); !reflect.DeepEqual(ids, []string{"current"}) {
		t.Fatalf("focused messages = %v, want [current]", ids)
	}
	if got.Manifest.IncludedMessageCount != 1 {
		t.Fatalf("included message count = %d, want 1", got.Manifest.IncludedMessageCount)
	}
}

func TestFocusedOversizedCurrentUserReturnsBudgetOverflow(t *testing.T) {
	config := DefaultConfig()
	config.MaxInputTokens = 150
	config.FixedTokens = 1
	config.SafetyMarginTokens = 1
	config.ProviderOverheadTokens = 1
	_, err := NewWithConfig(config).Focused(domain.Session{}, domain.BriefFocus{Label: "Scenario"}, []domain.Message{
		message("old", "user", "Earlier request", "complete"),
		message("current", "user", stringsOfLength(6_000), "complete"),
		message("assistant", "assistant", "Short response", "complete"),
	})
	if err == nil || !IsBudgetOverflow(err) {
		t.Fatalf("error = %v, want budget overflow", err)
	}
	var overflow *BudgetOverflowError
	if !errors.As(err, &overflow) || overflow.Role != RoleFocused || overflow.RequiredTokens <= overflow.MaxInputTokens {
		t.Fatalf("overflow = %#v", overflow)
	}
}

func TestBudgetDecisionsAreDeterministic(t *testing.T) {
	config := DefaultConfig()
	config.MaxInputTokens = 5_500
	session := domain.Session{State: "draft", Messages: []domain.Message{
		message("assistant", "assistant", stringsOfLength(300), "complete"),
		message("user", "user", "show the current plan", "complete"),
	}}
	first, err := NewWithConfig(config).Collaborator(session)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewWithConfig(config).Collaborator(session)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Manifest, second.Manifest) {
		t.Fatalf("manifests differ:\n%#v\n%#v", first.Manifest, second.Manifest)
	}
}

func message(id, role, content, status string) domain.Message {
	return domain.Message{ID: id, Role: role, Kind: "message", Content: content, Status: status}
}

func decisionNames(decisions []SectionDecision) []string {
	names := make([]string, len(decisions))
	for index, decision := range decisions {
		names[index] = decision.Name
	}
	return names
}

func decisionTokens(decisions []SectionDecision, names ...string) int {
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		wanted[name] = struct{}{}
	}
	total := 0
	for _, decision := range decisions {
		if _, ok := wanted[decision.Name]; ok {
			total += decision.EstimatedTokens
		}
	}
	return total
}

func stringsOfLength(length int) string {
	return strings.Repeat("x", length)
}

func messageIDs(messages []domain.Message) []string {
	ids := make([]string, len(messages))
	for index, message := range messages {
		ids[index] = message.ID
	}
	return ids
}

func hasFact(facts []Fact, topic, name string) bool {
	for _, fact := range facts {
		if fact.Topic == topic && fact.Name == name {
			return true
		}
	}
	return false
}

func hasTopic(facts []Fact, topic string) bool {
	for _, fact := range facts {
		if fact.Topic == topic {
			return true
		}
	}
	return false
}

func assertManifest(t *testing.T, manifest Manifest, role Role) {
	t.Helper()
	if manifest.SchemaVersion != SchemaVersion || manifest.Role != role || manifest.ApproximateCharacters <= 0 {
		t.Fatalf("manifest = %#v", manifest)
	}
}
