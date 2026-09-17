package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/grafana/agento11y/go/agento11y"

	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
)

func TestRoleContextAssociatesInternalAgentsWithSessionConversation(t *testing.T) {
	session := domain.Session{ID: "session-123", Title: "Camera fleet demo"}
	ctx, _ := roleContext(context.Background(), session, roleBuilder)

	if got, ok := agento11y.ConversationIDFromContext(ctx); !ok || got != session.ID {
		t.Fatalf("conversation ID = %q, %v; want %q, true", got, ok, session.ID)
	}
	if got, ok := agento11y.ConversationTitleFromContext(ctx); !ok || got != session.Title {
		t.Fatalf("conversation title = %q, %v; want %q, true", got, ok, session.Title)
	}
	if got := agento11y.TagsFromContext(ctx)["generation.visibility"]; got != "internal" {
		t.Fatalf("generation visibility = %q, want internal", got)
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

func TestFocusContextKeepsTopicInsideMainSession(t *testing.T) {
	got := focusContext(&domain.BriefFocus{Label: "Scenario", Value: "A camera fleet loses connectivity", Status: "proposed"})
	for _, expected := range []string{"<focused_brief_topic>", `"label":"Scenario"`, `"status":"proposed"`, "isolated draft thread", "explicitly confirm"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("focus context missing %q: %s", expected, got)
		}
	}
}

func TestPreserveConfirmedBriefPreventsMainChatDowngrade(t *testing.T) {
	current := domain.BriefContent{
		Telemetry: []domain.BriefItem{{Name: "Logs", Value: "Structured logfmt events", Status: "confirmed"}},
	}
	candidate := domain.BriefContent{
		Telemetry: []domain.BriefItem{{Name: "Logs", Value: "JSON events", Status: "proposed"}},
	}

	got := preserveConfirmedBrief(current, candidate)
	if len(got.Telemetry) != 1 || got.Telemetry[0] != current.Telemetry[0] {
		t.Fatalf("telemetry = %#v, want locked value %#v", got.Telemetry, current.Telemetry[0])
	}
}

func TestPreserveConfirmedBriefRestoresOmittedTopic(t *testing.T) {
	current := domain.BriefContent{
		GrafanaResources: []domain.BriefItem{{Name: "Fleet overview", Value: "Device health dashboard", Status: "confirmed"}},
	}

	got := preserveConfirmedBrief(current, domain.BriefContent{})
	if len(got.GrafanaResources) != 1 || got.GrafanaResources[0] != current.GrafanaResources[0] {
		t.Fatalf("resources = %#v, want locked topic restored", got.GrafanaResources)
	}
}

func TestBriefContextHighlightsConfirmedFactsAfterFullBrief(t *testing.T) {
	session := domain.Session{
		State: "Draft",
		Brief: &domain.LivingBrief{Content: domain.BriefContent{
			Telemetry: []domain.BriefItem{{Name: "Logs", Value: "Structured JSON events", Status: "confirmed"}},
		}},
	}

	got := briefContext(session)
	for _, expected := range []string{"<confirmed_brief_facts>", `"section":"Telemetry"`, `"name":"Logs"`, `"value":"Structured JSON events"`, "supersede older conversation messages"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("brief context missing %q: %s", expected, got)
		}
	}
	if strings.LastIndex(got, "<confirmed_brief_facts>") < strings.LastIndex(got, "</session_context>") {
		t.Fatal("confirmed facts must follow the full session context")
	}
}

func TestBriefContextIncludesAuthoritativeOperationalState(t *testing.T) {
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

	got := briefContext(session)
	for _, expected := range []string{
		`"sessionState":"Running"`, `"iteration":8`, `"prototypeIteration":7`,
		`"stackSlug":"camera-demo"`, `"status":"running"`, `"latestProgress":"Docker Compose is running"`,
		"operational state above comes from the compiler's persisted build and deployment records and is authoritative",
		"it is not a live health check", "Only describe telemetry as verified when the deployment status is verified",
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("brief context missing %q: %s", expected, got)
		}
	}
	for _, sensitive := range []string{"secret-endpoint.example", "12345", "/private/generated/demo", "secret diagnostic output", "New iteration not deployed"} {
		if strings.Contains(got, sensitive) {
			t.Fatalf("brief context exposed deployment credential metadata %q: %s", sensitive, got)
		}
	}
}
