package guidance

import (
	"strings"
	"testing"
)

func TestCatalogDocumentsAreReadable(t *testing.T) {
	for _, name := range strings.Split(Catalog(), ", ") {
		body, err := Read(name)
		if err != nil || strings.TrimSpace(body) == "" {
			t.Fatalf("guidance %q: empty or unreadable: %v", name, err)
		}
	}
	if _, err := Read("grafana-workflow.md"); err != nil {
		t.Fatal(err)
	}
}
