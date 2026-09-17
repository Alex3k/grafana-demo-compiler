package prompts

import (
	_ "embed"
	"strings"
)

const Version = "v2.6"

//go:embed v2/shared-constitution.md
var sharedConstitution string

//go:embed v2/demo-collaborator.md
var demoCollaborator string

//go:embed v2/living-brief-curator.md
var livingBriefCurator string

//go:embed v2/requirement-evaluator.md
var requirementEvaluator string

//go:embed v2/prototype-builder.md
var prototypeBuilder string

//go:embed v2/prototype-planner.md
var prototypePlanner string

func Collaborator() string {
	return combine(sharedConstitution, demoCollaborator)
}

func Curator() string {
	return combine(sharedConstitution, livingBriefCurator)
}

func Evaluator() string {
	return combine(sharedConstitution, requirementEvaluator)
}

func Builder() string {
	return combine(sharedConstitution, prototypeBuilder)
}

func PrototypePlanner() string {
	return combine(sharedConstitution, prototypePlanner)
}

func combine(parts ...string) string {
	trimmed := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			trimmed = append(trimmed, value)
		}
	}
	return strings.Join(trimmed, "\n\n")
}
