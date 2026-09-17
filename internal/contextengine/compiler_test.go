package contextengine

import (
	"encoding/json"
	"reflect"
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

	got := New(3).Collaborator(session)
	if got.CurrentUserMessage == nil || got.CurrentUserMessage.ID != "5" {
		t.Fatalf("current user message = %#v", got.CurrentUserMessage)
	}
	if ids := messageIDs(got.RecentMessages); !reflect.DeepEqual(ids, []string{"2", "3"}) {
		t.Fatalf("recent messages = %v", ids)
	}
	if !reflect.DeepEqual(got.ConfirmedFacts, []Fact{{Topic: "audience", Value: "SREs"}}) {
		t.Fatalf("confirmed facts = %#v", got.ConfirmedFacts)
	}
	if !reflect.DeepEqual(got.ProposedFacts, []Fact{{Topic: "company", Value: "Acme"}}) {
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

	architecture := New(6).Collaborator(domain.Session{Brief: brief, Messages: []domain.Message{
		message("question", "user", "Show me the architecture diagram", "complete"),
	}})
	if architecture.ReferenceState.Mermaid == "" || len(architecture.ReferenceState.Narrative) != 0 {
		t.Fatalf("architecture reference state = %#v", architecture.ReferenceState)
	}

	summary := New(6).Collaborator(domain.Session{Brief: brief, Messages: []domain.Message{
		message("question", "user", "Summarise the current plan", "complete"),
	}})
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

	got := New(0).Curator(session, delta)
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
			Excluded:           []domain.BriefItem{{Name: "Auth", Value: "No auth", Status: "confirmed"}},
			SimulationBoundary: domain.BriefItem{Value: "Cameras are simulated", Status: "confirmed"},
		},
		Services: []domain.BriefItem{{Name: "Ingest", Value: "Go ingest service", Status: "confirmed"}},
		Telemetry: []domain.BriefItem{
			{Name: "Logs", Value: "JSON logs", Status: "confirmed"},
			{Name: "Traces", Value: "OTel traces", Status: "confirmed"},
		},
		GrafanaResources: []domain.BriefItem{{Name: "Dashboard", Value: "Fleet dashboard", Status: "confirmed"}},
	}}}

	got := New(0).Focused(session, domain.BriefFocus{Label: "Telemetry: Logs", Value: "JSON logs", Status: "confirmed"}, []domain.Message{
		message("focused", "user", "include device_id", "complete"),
		message("main", "assistant", "unfinished", "streaming"),
	})

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
	wantTopics := []string{"scenario", "scope.excluded", "scope.simulation_boundary", "services", "telemetry", "telemetry.logs"}
	if !reflect.DeepEqual(got.Manifest.IncludedTopicKeys, wantTopics) {
		t.Fatalf("topics = %v, want %v", got.Manifest.IncludedTopicKeys, wantTopics)
	}
}

func TestEvaluatorPlannerAndBuilderEnvelopes(t *testing.T) {
	brief := domain.LivingBrief{Version: 9, Content: domain.BriefContent{
		Outcome: domain.BriefItem{Value: "Find the slow service", Status: "confirmed"},
	}}
	compiler := New(0)
	evaluator := compiler.Evaluator(brief, "three-service trace story")
	if evaluator.CandidatePlan == "" || len(evaluator.ExplicitRequirements) != 1 {
		t.Fatalf("evaluator = %#v", evaluator)
	}
	iteration := domain.PrototypeIteration{
		ID: "iteration", Number: 3, Status: "failed", Summary: "Built the first camera flow", Error: "compile failed",
		Artifacts: []domain.PrototypeArtifact{{Path: "cmd/api/main.go"}},
		Checks:    []domain.PrototypeCheck{{Name: "go build", Status: "failed", Detail: "missing import"}},
	}
	planner := compiler.Planner(brief, domain.AlignmentEvaluation{Result: "pass"}, &iteration)
	if planner.LatestPrototype == nil || planner.LatestPrototype.ID != "iteration" || planner.LatestPrototype.Summary == "" || planner.LatestPrototype.Failure == "" || len(planner.LatestPrototype.Artifacts) != 1 || len(planner.LatestPrototype.FailedChecks) != 1 {
		t.Fatalf("planner = %#v", planner)
	}
	plan := json.RawMessage(`{"files":["main.go"]}`)
	builder := compiler.Builder(plan, []string{"Go services only", "local deployment only"})
	plan[2] = 'X'
	if !json.Valid(builder.ImplementationPlan) {
		t.Fatal("builder retained caller-owned plan bytes")
	}
	if builder.Manifest.Role != RoleBuilder || builder.Manifest.ApproximateCharacters == 0 {
		t.Fatalf("builder manifest = %#v", builder.Manifest)
	}
}

func message(id, role, content, status string) domain.Message {
	return domain.Message{ID: id, Role: role, Kind: "message", Content: content, Status: status}
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
