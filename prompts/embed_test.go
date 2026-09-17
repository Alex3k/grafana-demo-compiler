package prompts

import (
	"strings"
	"testing"
)

func TestRolePromptsIncludeSharedConstitution(t *testing.T) {
	for name, prompt := range map[string]string{
		"collaborator": Collaborator(),
		"curator":      Curator(),
		"evaluator":    Evaluator(),
		"builder":      Builder(),
		"planner":      PrototypePlanner(),
	} {
		if !strings.Contains(prompt, "Non-negotiable MVP invariants") {
			t.Fatalf("%s prompt does not include the shared constitution", name)
		}
	}
}

func TestCollaboratorTreatsCustomerProductionConstraintsAsContext(t *testing.T) {
	prompt := strings.Join(strings.Fields(Collaborator()), " ")
	for _, instruction := range []string{
		"Customer production constraints normally explain why the demo matters",
		"Do not turn it into a data residency discussion",
		"do not ask where the demo will run",
	} {
		if !strings.Contains(prompt, instruction) {
			t.Fatalf("collaborator prompt is missing %q", instruction)
		}
	}
}

func TestPromptsTreatDatabaseAsOptionalAndStandardizeOnMySQL(t *testing.T) {
	for name, prompt := range map[string]string{
		"collaborator": Collaborator(),
		"curator":      Curator(),
		"builder":      Builder(),
	} {
		normalized := strings.Join(strings.Fields(prompt), " ")
		if !strings.Contains(normalized, "A database is optional") {
			t.Fatalf("%s prompt does not make the database optional", name)
		}
		if !strings.Contains(normalized, "use MySQL") && !strings.Contains(normalized, "must be MySQL") {
			t.Fatalf("%s prompt does not standardize selected databases on MySQL", name)
		}
	}
}
