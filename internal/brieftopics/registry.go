package brieftopics

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
)

const (
	Audience           = "audience"
	Company            = "company"
	Outcome            = "outcome"
	Stakes             = "stakes"
	Scenario           = "scenario"
	Journey            = "journey"
	SimulationBoundary = "scope.simulation_boundary"
)

type Section string

const (
	SectionAudience         Section = "audience"
	SectionCompany          Section = "company"
	SectionOutcome          Section = "outcome"
	SectionStakes           Section = "stakes"
	SectionScenario         Section = "scenario"
	SectionJourney          Section = "journey"
	SectionProofPoints      Section = "proof_points"
	SectionScopeIncluded    Section = "scope.included"
	SectionScopeExcluded    Section = "scope.excluded"
	SectionSimulation       Section = "scope.simulation_boundary"
	SectionServices         Section = "services"
	SectionTelemetry        Section = "telemetry"
	SectionGrafanaResources Section = "grafana_resources"
)

type fixedTopic struct {
	id      string
	label   string
	section Section
	item    func(*domain.BriefContent) *domain.BriefItem
}

var fixed = []fixedTopic{
	{Audience, "Audience", SectionAudience, func(c *domain.BriefContent) *domain.BriefItem { return &c.Audience }},
	{Company, "Company", SectionCompany, func(c *domain.BriefContent) *domain.BriefItem { return &c.Company }},
	{Outcome, "Outcome", SectionOutcome, func(c *domain.BriefContent) *domain.BriefItem { return &c.Outcome }},
	{Stakes, "Stakes", SectionStakes, func(c *domain.BriefContent) *domain.BriefItem { return &c.Stakes }},
	{Scenario, "Scenario", SectionScenario, func(c *domain.BriefContent) *domain.BriefItem { return &c.Scenario }},
	{Journey, "Journey", SectionJourney, func(c *domain.BriefContent) *domain.BriefItem { return &c.Journey }},
	{SimulationBoundary, "Simulation boundary", SectionSimulation, func(c *domain.BriefContent) *domain.BriefItem { return &c.Scope.SimulationBoundary }},
}

type collectionTopic struct {
	prefix  string
	label   string
	section Section
	items   func(*domain.BriefContent) *[]domain.BriefItem
}

var collections = []collectionTopic{
	{"proof_", "Proof points", SectionProofPoints, func(c *domain.BriefContent) *[]domain.BriefItem { return &c.ProofPoints }},
	{"scopein_", "Included scope", SectionScopeIncluded, func(c *domain.BriefContent) *[]domain.BriefItem { return &c.Scope.Included }},
	{"scopeout_", "Deliberately excluded", SectionScopeExcluded, func(c *domain.BriefContent) *[]domain.BriefItem { return &c.Scope.Excluded }},
	{"svc_", "Services", SectionServices, func(c *domain.BriefContent) *[]domain.BriefItem { return &c.Services }},
	{"tel_", "Telemetry", SectionTelemetry, func(c *domain.BriefContent) *[]domain.BriefItem { return &c.Telemetry }},
	{"graf_", "Grafana resources", SectionGrafanaResources, func(c *domain.BriefContent) *[]domain.BriefItem { return &c.GrafanaResources }},
}

// Reconcile assigns application-owned IDs to a model-produced brief. Existing
// collection IDs survive whole-brief replacements when the same named item is
// present; new items receive opaque IDs.
func Reconcile(previous *domain.BriefContent, next *domain.BriefContent) {
	for _, topic := range fixed {
		topic.item(next).ID = topic.id
	}
	for _, collection := range collections {
		var old []domain.BriefItem
		if previous != nil {
			old = *collection.items(previous)
		}
		reconcileItems(old, *collection.items(next), collection.prefix)
	}
}

// Ensure repairs IDs on already-trusted persisted content without replacing
// valid application-owned IDs. Model output must go through Reconcile instead.
func Ensure(content *domain.BriefContent) {
	for _, topic := range fixed {
		topic.item(content).ID = topic.id
	}
	for _, collection := range collections {
		used := make(map[string]bool)
		items := collection.items(content)
		for index := range *items {
			id := (*items)[index].ID
			if !strings.HasPrefix(id, collection.prefix) || used[id] {
				id = newTopicID(collection.prefix)
				(*items)[index].ID = id
			}
			used[id] = true
		}
	}
}

func reconcileItems(previous, next []domain.BriefItem, prefix string) {
	byName := make(map[string]string, len(previous))
	ambiguousNames := make(map[string]bool)
	known := make(map[string]bool, len(previous))
	for _, item := range previous {
		if item.ID != "" {
			known[item.ID] = true
			name := normalized(item.Name)
			if existing := byName[name]; existing != "" && existing != item.ID {
				ambiguousNames[name] = true
				delete(byName, name)
			} else if !ambiguousNames[name] {
				byName[name] = item.ID
			}
		}
	}
	used := make(map[string]bool, len(next))
	for index := range next {
		if next[index].ID != "" && !used[next[index].ID] && known[next[index].ID] {
			used[next[index].ID] = true
			continue
		}
		if id := byName[normalized(next[index].Name)]; id != "" && !used[id] {
			next[index].ID = id
			used[id] = true
			continue
		}
		next[index].ID = newTopicID(prefix)
		used[next[index].ID] = true
	}
}

// Resolve returns the server-owned display snapshot for a stable topic ID.
func Resolve(content *domain.BriefContent, topicID string) (domain.BriefFocus, bool) {
	for _, topic := range fixed {
		if topic.id == topicID {
			item := topic.item(content)
			return domain.BriefFocus{TopicID: topic.id, Label: topic.label, Value: item.Value, Status: item.Status}, true
		}
	}
	for _, collection := range collections {
		for _, item := range *collection.items(content) {
			if item.ID == topicID {
				return domain.BriefFocus{TopicID: topicID, Label: collection.label + ": " + item.Name, Value: item.Value, Status: item.Status}, true
			}
		}
	}
	return domain.BriefFocus{}, false
}

// ResolveLegacyLabel is used only while migrating records written before
// stable topic IDs existed. Runtime selection never routes on labels.
func ResolveLegacyLabel(content *domain.BriefContent, label string) (domain.BriefFocus, bool) {
	for _, topic := range fixed {
		if topic.label == label {
			return Resolve(content, topic.id)
		}
	}
	for _, collection := range collections {
		prefix := collection.label + ": "
		if !strings.HasPrefix(label, prefix) {
			continue
		}
		name := strings.TrimPrefix(label, prefix)
		for _, item := range *collection.items(content) {
			if strings.EqualFold(item.Name, name) {
				return Resolve(content, item.ID)
			}
		}
	}
	return domain.BriefFocus{}, false
}

func Apply(content *domain.BriefContent, topicID, value string) bool {
	for _, topic := range fixed {
		if topic.id == topicID {
			item := topic.item(content)
			item.Value, item.Status = value, "confirmed"
			return true
		}
	}
	for _, collection := range collections {
		items := collection.items(content)
		for index := range *items {
			if (*items)[index].ID == topicID {
				(*items)[index].Value, (*items)[index].Status = value, "confirmed"
				return true
			}
		}
	}
	return false
}

func SectionFor(topicID string) (Section, bool) {
	for _, topic := range fixed {
		if topic.id == topicID {
			return topic.section, true
		}
	}
	for _, collection := range collections {
		if strings.HasPrefix(topicID, collection.prefix) {
			return collection.section, true
		}
	}
	return "", false
}

func IsScope(topicID string) bool {
	section, ok := SectionFor(topicID)
	return ok && (section == SectionScopeIncluded || section == SectionScopeExcluded || section == SectionSimulation)
}

// IsDependency centralizes the section relationships used by focused context.
func IsDependency(focusedTopicID, candidateTopicID string) bool {
	if focusedTopicID != "" && focusedTopicID == candidateTopicID {
		return true
	}
	focus, ok := SectionFor(focusedTopicID)
	if !ok {
		return false
	}
	candidate, ok := SectionFor(candidateTopicID)
	if !ok {
		return false
	}
	allowed := map[Section][]Section{
		SectionTelemetry:        {SectionScenario, SectionJourney, SectionServices},
		SectionServices:         {SectionScenario, SectionJourney},
		SectionGrafanaResources: {SectionAudience, SectionOutcome, SectionScenario, SectionProofPoints, SectionTelemetry},
	}
	for _, section := range allowed[focus] {
		if candidate == section {
			return true
		}
	}
	return false
}

func newTopicID(prefix string) string {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return prefix + hex.EncodeToString(bytes)
}

func normalized(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
