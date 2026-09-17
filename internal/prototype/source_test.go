package prototype

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
)

func TestSourceSnapshotExcludesRuntimeSecretsAndDetectsChanges(t *testing.T) {
	root := t.TempDir()
	w, _ := New(root)
	if _, err := w.WriteFile("main.go", "package main"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("SECRET=private"), 0600); err != nil {
		t.Fatal(err)
	}
	base := domain.PrototypeIteration{RootPath: root, Artifacts: w.Artifacts()}
	files, digest, err := SourceSnapshot(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatal(files)
	}
	if _, err := ReadSource(base, ".env"); err == nil {
		t.Fatal("read runtime credentials")
	}
	if _, err := ReadSource(base, "../main.go"); err == nil {
		t.Fatal("allowed traversal")
	}
	if _, err := w.WriteFile("main.go", "package changed"); err != nil {
		t.Fatal(err)
	}
	_, updated, err := SourceSnapshot(base)
	if err != nil || digest == updated {
		t.Fatal("source changes not detected", err)
	}
	outside := filepath.Join(t.TempDir(), "secret.go")
	_ = os.WriteFile(outside, []byte("secret"), 0600)
	if err := os.Symlink(outside, filepath.Join(root, "escape.go")); err != nil {
		t.Fatal(err)
	}
	base.Artifacts = append(base.Artifacts, domain.PrototypeArtifact{Path: "escape.go"})
	if _, err := ReadSource(base, "escape.go"); err == nil {
		t.Fatal("followed symlink outside prototype")
	}
}
