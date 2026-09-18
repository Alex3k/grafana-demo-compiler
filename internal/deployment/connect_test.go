package deployment

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConnectStackRejectsOtherDestinations(t *testing.T) {
	for _, server := range []string{"https://other.grafana.net", "http://democompilerdemo.grafana.net", "https://democompilerdemo.grafana.net/path", "https://democompilerdemo.grafana.net?token=x"} {
		if err := New().ConnectStack(context.Background(), "democompilerdemo", server, func(string) { t.Fatal("started invalid connection") }); err == nil {
			t.Fatalf("accepted %s", server)
		}
	}
}

func TestStackAuthDropsAmbientCredentialOverrides(t *testing.T) {
	dir := t.TempDir()
	// Only a local fake command is executed, never gcx or OAuth.
	script := "#!/bin/sh\nif [ -n \"$GRAFANA_TOKEN$GCX_CONFIG$GCX_SERVER$GCX_CONTEXT$CLAUDECODE$CURSOR_AGENT\" ]; then exit 19; fi\nif [ \"$1\" = login ]; then [ -z \"$GCX_AGENT_MODE\" ] || exit 20; else [ \"$GCX_AGENT_MODE\" = 1 ] || exit 21; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "gcx"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	for _, key := range strings.Fields("GRAFANA_TOKEN GCX_CONFIG GCX_SERVER GCX_CONTEXT GCX_AGENT_MODE CLAUDECODE CURSOR_AGENT") {
		t.Setenv(key, "must-not-inherit")
	}
	if err := runStackAuth(context.Background(), "login", "fake"); err != nil {
		t.Fatal(err)
	}
	if err := runStackAuth(context.Background(), "datasources", "list"); err != nil {
		t.Fatal(err)
	}
}
