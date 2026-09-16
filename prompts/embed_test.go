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
	} {
		if !strings.Contains(prompt, "Non-negotiable MVP invariants") {
			t.Fatalf("%s prompt does not include the shared constitution", name)
		}
	}
}
