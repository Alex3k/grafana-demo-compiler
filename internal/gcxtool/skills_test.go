package gcxtool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mockSkillCLI(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gcx"), []byte("#!/bin/sh\n"+body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestReadSkillArguments(t *testing.T) {
	mockSkillCLI(t, "printf '%s\\n' \"$@\"")
	for _, tc := range []struct{ name, reference, want string }{
		{"", "", "agent\nskills\nlist\n-o\njson\n"},
		{"create-dashboard", "", "agent\nskills\nget\ncreate-dashboard\n-o\ntext\n"},
		{"create-dashboard", "references/dashboard-patterns.md", "agent\nskills\nget\ncreate-dashboard\nreferences/dashboard-patterns.md\n-o\ntext\n"},
	} {
		got, err := ReadSkill(context.Background(), tc.name, tc.reference)
		if err != nil || got != tc.want {
			t.Fatalf("ReadSkill(%q, %q) = %q, %v", tc.name, tc.reference, got, err)
		}
	}
}

func TestReadSkillRejectsInvalidInputsBeforeExecution(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "executed")
	mockSkillCLI(t, ": > '"+marker+"'")
	for _, tc := range []struct{ name, reference string }{
		{"", "references/a.md"}, {"--help", ""}, {"../gcx", ""}, {"gcx foo", ""},
		{"gcx", "../../secret"}, {"gcx", "/references/a.md"}, {"gcx", "references/../secret"},
		{"gcx", "references/./a.md"}, {"gcx", "references//a.md"}, {"gcx", "references/"},
		{"gcx", "references/a\\b.md"}, {"gcx", "references/%2e%2e/a.md"}, {"gcx", "--help"},
	} {
		if _, err := ReadSkill(context.Background(), tc.name, tc.reference); err == nil {
			t.Errorf("accepted %q %q", tc.name, tc.reference)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("invalid skill input executed CLI")
	}
}

func TestReadSkillDoesNotTruncateInstructions(t *testing.T) {
	mockSkillCLI(t, "printf '%s' '"+strings.Repeat("x", maxSkillBytes+1)+"'")
	got, err := ReadSkill(context.Background(), "gcx", "")
	if err == nil || got != "" || !strings.Contains(err.Error(), "no partial instructions") {
		t.Fatalf("expected explicit size error and no partial skill, got %d bytes, %v", len(got), err)
	}
}

func TestReadSkillReturnsCLIError(t *testing.T) {
	mockSkillCLI(t, "printf 'unknown skill' >&2; exit 1")
	if got, err := ReadSkill(context.Background(), "missing", ""); err == nil || got != "" || !strings.Contains(err.Error(), "unknown skill") {
		t.Fatalf("got %q, %v", got, err)
	}
}
