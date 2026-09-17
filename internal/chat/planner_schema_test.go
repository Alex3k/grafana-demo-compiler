package chat

import (
	"encoding/json"
	"testing"
)

func TestPlannerDraftOmitsBackendOwnedContext(t *testing.T) {
	draft := prototypePlanDraft{Summary: "Small implementation", Files: []prototypeFilePlan{{Path: "main.go", Purpose: "Service"}}}
	payload, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"title", "demoContract"} {
		if _, exists := fields[key]; exists {
			t.Fatalf("planner must not generate %s", key)
		}
	}
	plan := prototypeBuildPlan{Title: "Saved title", prototypePlanDraft: draft}
	payload, err = json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"title", "demoContract", "summary", "files"} {
		if _, exists := fields[key]; !exists {
			t.Fatalf("builder missing %s", key)
		}
	}
}
