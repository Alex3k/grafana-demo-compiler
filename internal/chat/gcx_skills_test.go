package chat

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
	aisdk "github.com/grafana/ai-sdk"
)

func TestReadGCXSkillAvailableWithoutStack(t *testing.T) {
	if _, err := exec.LookPath("gcx"); err != nil {
		t.Skip("gcx not installed")
	}
	var progress strings.Builder
	service := &Service{}
	tools, instructions, err := service.inspectionTools(context.Background(), domain.Session{}, func(delta string) error { progress.WriteString(delta); return nil })
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := tools["read_gcx_skill"]
	if !ok || !strings.Contains(instructions, "grafana-workflow.md") {
		t.Fatal("skill tool or routing missing")
	}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"create-dashboard","reference":""}`), aisdk.ToolExecutionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var body string
	if err := json.Unmarshal(result, &body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(body), "dashboard") || !strings.Contains(progress.String(), "Loaded gcx skill create-dashboard") {
		t.Fatal("skill was not delivered with visible progress")
	}
}
