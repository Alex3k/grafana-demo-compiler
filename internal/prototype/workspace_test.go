package prototype

import (
	"os"
	"path/filepath"
	"testing"
)

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
