package gcxtool

import (
	"context"
	"os/exec"
	"testing"
)

func TestCommandPolicy(t *testing.T) {
	if _, err := exec.LookPath("gcx"); err != nil {
		t.Skip("gcx not installed")
	}
	for _, tc := range []struct {
		name          string
		req           Request
		read, blocked bool
	}{
		{"discover", Request{Args: []string{"help-tree", "--depth", "1"}}, true, false},
		{"read", Request{Args: []string{"metrics", "query", "up", "--since", "5m"}}, true, false},
		{"write", Request{Args: []string{"resources", "delete", "dashboards/example"}}, false, false},
		{"dashboard short filename", Request{Args: []string{"dashboards", "create", "-f", "@manifest"}, Manifest: `{"kind":"Dashboard"}`}, false, false},
		{"dashboard long filename", Request{Args: []string{"dashboards", "create", "--filename=@manifest"}, Manifest: `{"kind":"Dashboard"}`}, false, false},
		{"dashboard filename escape", Request{Args: []string{"dashboards", "create", "-f", "/etc/passwd"}}, false, true},
		{"dashboard filename missing manifest", Request{Args: []string{"dashboards", "create", "-f", "@manifest"}}, false, true},
		{"dashboard filename stdin", Request{Args: []string{"dashboards", "create", "--filename=-"}, Manifest: `{"kind":"Dashboard"}`}, false, true},
		{"payload", Request{Args: []string{"resources", "push", "-p", "@manifest"}, Manifest: `{"kind":"Dashboard"}`}, false, false},
		{"no help bypass", Request{Args: []string{"resources", "delete", "dashboards/example", "--help", "--help=false"}}, false, false},
		{"context override", Request{Args: []string{"resources", "get", "dashboards", "--context=democompiler"}}, false, true},
		{"file escape", Request{Args: []string{"resources", "push", "-p", "/tmp/other"}}, false, true},
		{"config", Request{Args: []string{"config", "view", "--raw"}}, false, true},
		{"cloud", Request{Args: []string{"cloud", "stacks", "delete", "democompiler"}}, false, true},
		{"manifest secret source", Request{Args: []string{"resources", "push", "-p", "@manifest"}, Manifest: `{"fromFile":"/etc/passwd"}`}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			read, err := Classify(context.Background(), tc.req)
			if (err != nil) != tc.blocked || err == nil && read != tc.read {
				t.Fatalf("read=%v error=%v", read, err)
			}
		})
	}
}

func TestRedaction(t *testing.T) {
	for _, value := range []string{`{"token":"a-secret"}`, `Bearer abcdef`, `glsa_abcdef`} {
		if Redact(value) == value {
			t.Fatalf("not redacted: %s", value)
		}
	}
}
