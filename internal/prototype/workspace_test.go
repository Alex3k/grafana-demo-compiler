package prototype

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Alex3k/grafana-demo-compiler/internal/telemetryconfig"
)

func TestValidateReportsTelemetryContractFailure(t *testing.T) {
	root := t.TempDir()
	workspace, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.WriteFile("config.alloy", `endpoint = sys.env("GRAFANA_UNKNOWN_SECRET")`); err != nil {
		t.Fatal(err)
	}
	writeCompose(t, root, "services:\n  alloy:\n    image: grafana/alloy:latest\n    env_file: [.env]\n")
	contractErr := telemetryconfig.Validate(root)
	if contractErr == nil {
		t.Fatal("unsupported telemetry variable unexpectedly satisfies telemetry contract")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, check := range workspace.Validate(ctx) {
		if check.Name == "Telemetry contract" {
			if check.Status != "failed" || check.Detail != contractErr.Error() {
				t.Fatalf("telemetry contract failure did not retain repair details: %#v", check)
			}
			return
		}
	}
	t.Fatal("workspace validation did not report a telemetry contract check")
}

func TestComposeContentCheckAllowsNoDatabase(t *testing.T) {
	root := t.TempDir()
	writeCompose(t, root, "services:\n  api:\n    image: demo-api\n  alloy:\n    image: grafana/alloy:latest\n")

	check := composeContentCheck(root)
	if check.Status != "passed" {
		t.Fatalf("compose check = %#v", check)
	}
}

func TestComposeContentCheckAllowsMySQLWhenDatabaseIsIncluded(t *testing.T) {
	root := t.TempDir()
	writeCompose(t, root, "services:\n  mysql:\n    image: mysql:8.0\n  alloy:\n    image: grafana/alloy:latest\n")

	check := composeContentCheck(root)
	if check.Status != "passed" {
		t.Fatalf("compose check = %#v", check)
	}
}

func TestComposeContentCheckRejectsAnotherDatabase(t *testing.T) {
	for _, image := range []string{"postgres:17", "redis:7", "alpine/sqlite:latest", "neo4j:5", "clickhouse/clickhouse-server:latest"} {
		t.Run(image, func(t *testing.T) {
			root := t.TempDir()
			writeCompose(t, root, "services:\n  database:\n    image: "+image+"\n  alloy:\n    image: grafana/alloy:latest\n")

			check := composeContentCheck(root)
			if check.Status != "failed" {
				t.Fatalf("compose check = %#v", check)
			}
		})
	}
}

func writeCompose(t *testing.T, root, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "compose.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
