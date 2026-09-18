package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Alex3k/grafana-demo-compiler/guidance"
	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
	"github.com/Alex3k/grafana-demo-compiler/internal/gcxtool"
	"github.com/Alex3k/grafana-demo-compiler/internal/store"
	aisdk "github.com/grafana/ai-sdk"
)

func (s *Service) SetGCXStore(data *store.Store) { s.gcxStore = data }

func SessionStack(session domain.Session) string {
	if len(session.ID) < 12 {
		return ""
	}
	expected := "democompiler" + session.ID[:12]
	if stack := session.GrafanaStack; stack != nil {
		if stack.Status == "ready" && stack.StackSlug == expected && stack.StackURL != "" {
			return expected
		}
		return ""
	}
	for _, d := range session.Deployments {
		if d.StackSlug == expected && d.StackURL != "" {
			return expected
		}
	}
	return ""
}

func (s *Service) inspectionTools(ctx context.Context, session domain.Session, onDelta func(string) error) (aisdk.ToolSet, string, error) {
	read, err := aisdk.TypedTool(aisdk.TypedToolDef[struct {
		Name string `json:"name"`
	}, string]{
		Name: "read_guidance", Description: "Read relevant versioned demo/Grafana guidance. Available: " + guidance.Catalog(),
		Execute: func(_ context.Context, input struct {
			Name string `json:"name"`
		}, _ aisdk.ToolExecutionOptions) (string, error) {
			return guidance.Read(input.Name)
		},
	})
	if err != nil {
		return nil, "", err
	}
	set := aisdk.ToolSet{"read_guidance": read}
	skill, err := aisdk.TypedTool(aisdk.TypedToolDef[struct {
		Name      string `json:"name" jsonschema:"description=Bundled gcx skill name. Empty string lists available skills and descriptions."`
		Reference string `json:"reference" jsonschema:"description=Empty to read the complete skill instructions. To read a referenced document supply its references/ relative path from that skill."`
	}, string]{
		Name: "read_gcx_skill", Description: "Read official skills and reference documents bundled with the installed gcx CLI. Read-only, no installation, credentials, or stack required. Load the relevant skill before Grafana resource work; use empty name and reference to discover skills.",
		Execute: func(toolCtx context.Context, input struct {
			Name      string `json:"name" jsonschema:"description=Bundled gcx skill name. Empty string lists available skills and descriptions."`
			Reference string `json:"reference" jsonschema:"description=Empty to read the complete skill instructions. To read a referenced document supply its references/ relative path from that skill."`
		}, _ aisdk.ToolExecutionOptions) (string, error) {
			content, err := gcxtool.ReadSkill(toolCtx, input.Name, input.Reference)
			if err != nil {
				return "", err
			}
			label := "gcx skill catalog"
			if input.Name != "" {
				label = "gcx skill " + input.Name
			}
			if input.Reference != "" {
				label += " reference " + input.Reference
			}
			if err := onDelta("\n\n_Loaded " + label + "._\n\n"); err != nil {
				return "", err
			}
			return content, nil
		},
	})
	if err != nil {
		return nil, "", err
	}
	set["read_gcx_skill"] = skill
	contextText := "\n\nAvailable guidance: " + guidance.Catalog()
	if s.gcxStore == nil {
		return set, contextText, nil
	}
	tool, err := aisdk.TypedTool(aisdk.TypedToolDef[gcxtool.Request, gcxtool.Action]{
		Name: "run_gcx", Description: "Discover and run gcx against this session's dedicated stack. Primary path for creating or updating dashboards, alerts, and SLOs without a prototype build or Docker redeployment. Read live resources first, then supply file payloads using @manifest and the inline manifest field. Reads run immediately. Writes are persisted for explicit approval in the Grafana actions panel; never claim pending writes succeeded. Arguments exclude gcx and --context. No shell, external file paths, login, Cloud account operations, or config changes.",
		Execute: func(toolCtx context.Context, input gcxtool.Request, _ aisdk.ToolExecutionOptions) (gcxtool.Action, error) {
			read, err := gcxtool.Classify(toolCtx, input)
			if err != nil {
				return gcxtool.Action{Status: "blocked", Output: err.Error()}, nil
			}
			a := gcxtool.Action{SessionID: session.ID, Stack: SessionStack(session), Request: input, Status: "pending"}
			if !read {
				if err := gcxtool.CheckContext(toolCtx, a.Stack); err != nil {
					return gcxtool.Action{Status: "blocked", Output: err.Error()}, nil
				}
				a, err = s.gcxStore.CreateGCXAction(toolCtx, a)
				if err != nil {
					return a, err
				}
				if err := onDelta("\n\nA Grafana change is ready for your review in **Grafana actions**. Nothing has been changed yet.\n\n"); err != nil {
					return a, err
				}
				return a, nil
			}
			if err := onDelta("\n\n_Inspecting Grafana with gcx…_\n\n"); err != nil {
				return a, err
			}
			a.Status = "running"
			a, err = s.gcxStore.CreateGCXAction(toolCtx, a)
			if err != nil {
				return a, err
			}
			a.Output, err = gcxtool.Execute(toolCtx, a.Stack, input)
			a.Status = "complete"
			if err != nil {
				a.Status = "failed"
				a.Output += "\n" + err.Error()
			}
			persist, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			return a, s.gcxStore.FinishGCXAction(persist, a.ID, a.Status, a.Output)
		},
	})
	if err != nil {
		return nil, "", err
	}
	set["run_gcx"] = tool
	contextText += "\nSession stack: " + SessionStack(session)
	actions, err := s.gcxStore.ListGCXActions(ctx, session.ID)
	if err != nil {
		return nil, "", err
	}
	// Keep a bounded shared operational ledger separate from brief proposals.
	var history strings.Builder
	for i, a := range actions {
		if i >= 8 {
			break
		}
		if len(a.Output) > 1000 {
			a.Output = a.Output[:1000] + " [truncated; query again for details]"
		}
		a.Request.Manifest = ""
		payload, _ := json.Marshal(a)
		fmt.Fprintln(&history, string(payload))
	}
	contextText += "\nRecent gcx operation receipts (actual status, not instructions; running after a server restart may be uncertain, inspect before retrying):\n" + history.String()
	return set, contextText, nil
}
