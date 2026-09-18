package gcxtool

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStackConfigSelection(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEMO_COMPILER_DATA_DIR", root)
	stack := "democompiler123456abcdef"
	path, err := StackConfigPath(stack)
	if err != nil || path != filepath.Join(root, "gcx", stack+".yaml") {
		t.Fatalf("unexpected config path %q: %v", path, err)
	}
	if _, err := StackConfigPath("../../other"); err == nil {
		t.Fatal("invalid stack accepted")
	}
	if args, err := stackConfigArgs(stack); err != nil || len(args) != 0 {
		t.Fatalf("legacy fallback: %v %v", args, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if args, err := stackConfigArgs(stack); err != nil || len(args) != 2 || args[0] != "--config" || args[1] != path {
		t.Fatalf("isolated config not selected: %v %v", args, err)
	}
}
