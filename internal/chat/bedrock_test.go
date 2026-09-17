package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/grafana/agento11y/go/agento11y"

	"github.com/Alex3k/grafana-demo-compiler/internal/contextengine"
	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
	"github.com/Alex3k/grafana-demo-compiler/internal/observability"
)

func TestRoleContextAssociatesInternalAgentsWithSessionConversation(t *testing.T) {
	session := domain.Session{ID: "session-123", Title: "Camera fleet demo"}
	manifest := contextengine.Manifest{
		SchemaVersion: contextengine.SchemaVersion, Role: contextengine.RoleBuilder, BriefVersion: 3,
		IncludedMessageCount: 2, EstimatedTokens: 1200, MaxInputTokens: 32000,
		IncludedSections: []contextengine.SectionDecision{{Name: "implementation_plan"}},
		DroppedSections:  []contextengine.SectionDecision{{Name: "latest_prototype"}}, Truncated: true,
	}
	ctx, _ := roleContext(context.Background(), session, roleBuilder, manifest)

	if got, ok := agento11y.ConversationIDFromContext(ctx); !ok || got != session.ID {
		t.Fatalf("conversation ID = %q, %v; want %q, true", got, ok, session.ID)
	}
	if got, ok := agento11y.ConversationTitleFromContext(ctx); !ok || got != session.Title {
		t.Fatalf("conversation title = %q, %v; want %q, true", got, ok, session.Title)
	}
	if got := agento11y.TagsFromContext(ctx)["generation.visibility"]; got != "internal" {
		t.Fatalf("generation visibility = %q, want internal", got)
	}
	info := contextInfo(ctx)
	if got := info.Metadata["context.message_count"]; got != 2 {
		t.Fatalf("context message count = %#v", got)
	}
	if got := info.Metadata["context.brief_version"]; got != 3 {
		t.Fatalf("context brief version = %#v", got)
	}
	if got := info.Metadata["context.estimated_tokens"]; got != 1200 {
		t.Fatalf("context estimated tokens = %#v", got)
	}
	if got := info.Metadata["context.truncated"]; got != true {
		t.Fatalf("context truncated = %#v", got)
	}
	if got := info.Metadata["context.included_sections"]; len(got.([]string)) != 1 || got.([]string)[0] != "implementation_plan" {
		t.Fatalf("context included sections = %#v", got)
	}
	if _, found := info.Tags["context.message_count"]; found {
		t.Fatal("dynamic context count must not be exported as a metric tag")
	}
	if got, ok := observability.ContextManifestFromContext(ctx); !ok || got.Role != contextengine.RoleBuilder {
		t.Fatalf("context manifest = %#v, %t", got, ok)
	}
}

func TestContextConfigFromEnvUsesOverridesAndSafeDefaults(t *testing.T) {
	t.Setenv("DEMO_COMPILER_CONTEXT_MAX_INPUT_TOKENS", "12000")
	t.Setenv("DEMO_COMPILER_CONTEXT_BYTES_PER_TOKEN", "invalid")

	config := contextConfigFromEnv()
	if config.MaxInputTokens != 12000 || config.BytesPerToken != contextengine.DefaultConfig().BytesPerToken {
		t.Fatalf("context config = %#v", config)
	}
}

func TestCuratorPacketConsumesOnlyLargestChronologicalBatchThatFits(t *testing.T) {
	config := contextengine.DefaultConfig()
	config.MaxInputTokens = 15000
	service := &Service{contextConfig: config}
	messages := []domain.Message{
		{ID: "first", Role: "user", Kind: "message", Status: "complete", Content: strings.Repeat("a", 6000)},
		{ID: "second", Role: "assistant", Kind: "message", Status: "complete", Content: strings.Repeat("b", 6000)},
	}

	packet, consumed, err := service.curatorPacket(domain.Session{}, messages)
	if err != nil {
		t.Fatal(err)
	}
	if consumed != "first" || len(packet.DeltaMessages) != 1 || packet.DeltaMessages[0].ID != "first" {
		t.Fatalf("curator batch consumed %q with messages %#v", consumed, packet.DeltaMessages)
	}
}

func TestCuratorPacketReturnsOverflowWhenOldestMessageCannotFit(t *testing.T) {
	config := contextengine.DefaultConfig()
	config.MaxInputTokens = 13000
	service := &Service{contextConfig: config}
	messages := []domain.Message{{
		ID: "oversized", Role: "user", Kind: "message", Status: "complete", Content: strings.Repeat("x", 6000),
	}}

	packet, consumed, err := service.curatorPacket(domain.Session{}, messages)
	if !contextengine.IsBudgetOverflow(err) {
		t.Fatalf("curatorPacket() error = %v", err)
	}
	if consumed != "" || len(packet.DeltaMessages) != 0 {
		t.Fatalf("overflow consumed %q with packet %#v", consumed, packet)
	}
}

func TestModelMessagesKeepsCompletedConversationOnly(t *testing.T) {
	messages := []domain.Message{
		{Role: "user", Kind: "message", Status: "complete", Content: "Build an IoT demo"},
		{Role: "system", Kind: "activity", Status: "complete", Content: "Planning"},
		{Role: "assistant", Kind: "message", Status: "complete", Content: "Who is the audience?"},
		{Role: "assistant", Kind: "message", Status: "failed", Content: "partial"},
		{Role: "assistant", Kind: "message", Status: "streaming", Content: ""},
		{Role: "user", Kind: "message", Status: "complete", Content: "Plant operations leaders"},
		{Role: "user", Kind: "message", Status: "complete", Content: "Focus on downtime"},
	}

	result := modelMessages(messages)
	if len(result) != 3 {
		t.Fatalf("message count = %d, want 3", len(result))
	}
	if len(result[2].Content) != 1 || result[2].Content[0].Text != "Plant operations leaders\n\nFocus on downtime" {
		t.Fatalf("coalesced user content = %#v", result[2].Content)
	}
}

func TestAcceptedAssistantPlanUsesProposalBeforeLatestHumanTurn(t *testing.T) {
	messages := []domain.Message{
		{Role: "assistant", Kind: "message", Status: "complete", Content: "Initial questions"},
		{Role: "user", Kind: "message", Status: "complete", Content: "Manufacturing operators"},
		{Role: "assistant", Kind: "message", Status: "complete", Content: "Bounded prototype plan"},
		{Role: "user", Kind: "message", Status: "complete", Content: "I accept this plan"},
		{Role: "assistant", Kind: "message", Status: "complete", Content: "Thanks, I will prepare it"},
	}

	if got := acceptedAssistantPlan(messages); got != "Bounded prototype plan" {
		t.Fatalf("accepted plan = %q, want bounded prototype plan", got)
	}
}

func TestSanitizeAssistantTextRemovesInternalCompletionMarker(t *testing.T) {
	got := sanitizeAssistantText("Ready for generation.\n\n<turn_complete>")
	if got != "Ready for generation." {
		t.Fatalf("sanitized text = %q", got)
	}
}

func TestFocusedContextKeepsOnlyCompiledTopicContext(t *testing.T) {
	packet := contextengine.FocusedContext{
		SelectedTopic: domain.BriefFocus{Label: "Scenario", Value: "A camera fleet loses connectivity", Status: "proposed"},
		Dependencies:  []contextengine.Fact{{Topic: "services", Name: "Ingest", Value: "Go service"}},
	}
	got := focusedContext(packet)
	for _, expected := range []string{"<focused_brief_topic>", `"label":"Scenario"`, `"status":"proposed"`, "complete relevant brief context", "human confirms"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("focus context missing %q: %s", expected, got)
		}
	}
}

func TestPreserveConfirmedBriefPreventsMainChatDowngrade(t *testing.T) {
	current := domain.BriefContent{
		Telemetry: []domain.BriefItem{{ID: "tel_logs", Name: "Logs", Value: "Structured logfmt events", Status: "confirmed"}},
	}
	candidate := domain.BriefContent{
		Telemetry: []domain.BriefItem{{ID: "tel_logs", Name: "Journal entries", Value: "JSON events", Status: "proposed"}},
	}

	got := preserveConfirmedBrief(current, candidate)
	if len(got.Telemetry) != 1 || got.Telemetry[0] != current.Telemetry[0] {
		t.Fatalf("telemetry = %#v, want locked value %#v", got.Telemetry, current.Telemetry[0])
	}
}

func TestPreserveConfirmedBriefRestoresOmittedTopic(t *testing.T) {
	current := domain.BriefContent{
		GrafanaResources: []domain.BriefItem{{ID: "graf_fleet", Name: "Fleet overview", Value: "Device health dashboard", Status: "confirmed"}},
	}

	got := preserveConfirmedBrief(current, domain.BriefContent{})
	if len(got.GrafanaResources) != 1 || got.GrafanaResources[0] != current.GrafanaResources[0] {
		t.Fatalf("resources = %#v, want locked topic restored", got.GrafanaResources)
	}
}

func TestCollaboratorContextMakesConfirmedFactsAuthoritative(t *testing.T) {
	packet := contextengine.CollaboratorContext{
		ConfirmedFacts:   []contextengine.Fact{{Topic: "telemetry", Name: "Logs", Value: "Structured JSON events"}},
		OperationalState: contextengine.OperationalState{SessionState: "Draft"},
	}
	got := collaboratorContext(packet)
	for _, expected := range []string{"<collaborator_context>", `"topic":"telemetry"`, `"name":"Logs"`, `"value":"Structured JSON events"`, "supersede contradictory conversation"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("collaborator context missing %q: %s", expected, got)
		}
	}
}

func TestCompiledCollaboratorContextOmitsSensitiveOperationalDetails(t *testing.T) {
	session := domain.Session{
		State: "Running",
		Prototypes: []domain.PrototypeIteration{{
			ID: "prototype-8", Number: 8, BriefVersion: 5, Status: "complete", Summary: "New iteration not deployed",
		}, {
			ID: "prototype-7", Number: 7, BriefVersion: 4, Status: "complete", Summary: "Four Go services",
			RootPath:  "/private/generated/demo",
			Artifacts: []domain.PrototypeArtifact{{Path: "docker-compose.yml"}},
			Checks:    []domain.PrototypeCheck{{Name: "compose", Status: "passed"}, {Name: "security", Status: "failed"}},
		}},
		Deployments: []domain.Deployment{{
			PrototypeIterationID: "prototype-7",
			Target:               "local", Region: "prod-us-east-0", StackName: "Camera demo", StackSlug: "camera-demo",
			StackURL: "https://camera-demo.grafana.net", Status: "running",
			Progress:     []string{"Creating stack", "Docker Compose is running"},
			OTLPEndpoint: "https://secret-endpoint.example/otlp", InstanceID: "12345",
			Error: "secret diagnostic output",
		}},
	}

	packet, err := contextengine.New(6).Collaborator(session)
	if err != nil {
		t.Fatal(err)
	}
	got := collaboratorContext(packet)
	for _, expected := range []string{
		`"sessionState":"Running"`, `"number":8`, `"stackSlug":"camera-demo"`,
		`"status":"running"`, "authoritative evidence of recorded compiler operations", "not a live health check",
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("collaborator context missing %q: %s", expected, got)
		}
	}
	for _, sensitive := range []string{"secret-endpoint.example", "12345", "/private/generated/demo", "secret diagnostic output", "New iteration not deployed"} {
		if strings.Contains(got, sensitive) {
			t.Fatalf("collaborator context exposed operational detail %q: %s", sensitive, got)
		}
	}
}
