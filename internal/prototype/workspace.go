package prototype

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
	"github.com/Alex3k/grafana-demo-compiler/internal/telemetryconfig"
)

const (
	maxFiles     = 80
	maxFileBytes = 256 * 1024
	maxTotal     = 2 * 1024 * 1024
)

type Workspace struct {
	root      string
	mu        sync.Mutex
	artifacts map[string]int64
	total     int64
}

func New(root string) (*Workspace, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("prototype root is required")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create prototype workspace: %w", err)
	}
	return &Workspace{root: root, artifacts: make(map[string]int64)}, nil
}

func (w *Workspace) WriteFile(path, content string) (domain.PrototypeArtifact, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	clean, err := safePath(path)
	if err != nil {
		return domain.PrototypeArtifact{}, err
	}
	if !allowedFile(clean) {
		return domain.PrototypeArtifact{}, fmt.Errorf("file type is not allowed: %s", clean)
	}
	size := int64(len(content))
	if size > maxFileBytes {
		return domain.PrototypeArtifact{}, fmt.Errorf("file exceeds %d bytes: %s", maxFileBytes, clean)
	}
	previous, exists := w.artifacts[clean]
	if !exists && len(w.artifacts) >= maxFiles {
		return domain.PrototypeArtifact{}, fmt.Errorf("prototype exceeds %d files", maxFiles)
	}
	if w.total-previous+size > maxTotal {
		return domain.PrototypeArtifact{}, fmt.Errorf("prototype exceeds %d bytes", maxTotal)
	}

	destination := filepath.Join(w.root, filepath.FromSlash(clean))
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return domain.PrototypeArtifact{}, fmt.Errorf("create prototype directory: %w", err)
	}
	if err := os.WriteFile(destination, []byte(content), 0o600); err != nil {
		return domain.PrototypeArtifact{}, fmt.Errorf("write prototype file: %w", err)
	}
	w.artifacts[clean] = size
	w.total = w.total - previous + size
	return domain.PrototypeArtifact{Path: clean, Size: size}, nil
}

func (w *Workspace) Artifacts() []domain.PrototypeArtifact {
	w.mu.Lock()
	defer w.mu.Unlock()
	result := make([]domain.PrototypeArtifact, 0, len(w.artifacts)+1)
	_ = filepath.WalkDir(w.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(w.root, path)
		if err != nil || !allowedFile(filepath.ToSlash(relative)) {
			return nil
		}
		info, err := entry.Info()
		if err == nil {
			result = append(result, domain.PrototypeArtifact{Path: filepath.ToSlash(relative), Size: info.Size()})
		}
		return nil
	})
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result
}

func (w *Workspace) Validate(ctx context.Context) []domain.PrototypeCheck {
	checks := []domain.PrototypeCheck{
		fileCheck(w.root, "go.mod", "Go module"),
		serviceCheck(w.root),
		oneOfFileCheck(w.root, "Compose file", "compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"),
		alloyCheck(w.root),
		composeContentCheck(w.root),
		telemetryContractCheck(w.root),
	}
	checks = append(checks,
		commandCheck(ctx, w.root, "Go build", "go", "build", "-mod=mod", "./..."),
		commandCheck(ctx, w.root, "Compose configuration", "docker", "compose", "config", "--quiet"),
	)
	return checks
}

func telemetryContractCheck(root string) domain.PrototypeCheck {
	if err := telemetryconfig.Validate(root); err != nil {
		return fail("Telemetry contract", err.Error())
	}
	return pass("Telemetry contract", "telemetry configuration satisfies the deployment contract")
}

func safePath(path string) (string, error) {
	path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
	if path == "" || strings.ContainsRune(path, 0) || strings.HasPrefix(path, "/") {
		return "", errors.New("path must be a non-empty relative path")
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("path must stay inside the prototype workspace")
	}
	for _, blocked := range []string{".git/", ".github/", "terraform/", "kubernetes/", "k8s/", "helm/"} {
		if strings.HasPrefix(strings.ToLower(clean), blocked) {
			return "", fmt.Errorf("path is outside MVP scope: %s", clean)
		}
	}
	return clean, nil
}

func allowedFile(path string) bool {
	base := filepath.Base(path)
	if base == "Dockerfile" || strings.HasPrefix(base, "Dockerfile.") || base == "go.mod" || base == "go.sum" || base == ".env.example" || base == ".gitignore" {
		return true
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".go", ".yaml", ".yml", ".alloy", ".md", ".sql", ".json", ".html", ".css", ".js":
		return true
	default:
		return false
	}
}

func fileCheck(root, path, name string) domain.PrototypeCheck {
	if _, err := os.Stat(filepath.Join(root, path)); err == nil {
		return pass(name, path+" exists")
	}
	return fail(name, path+" is missing")
}

func oneOfFileCheck(root, name string, paths ...string) domain.PrototypeCheck {
	for _, path := range paths {
		if _, err := os.Stat(filepath.Join(root, path)); err == nil {
			return pass(name, path+" exists")
		}
	}
	return fail(name, "missing "+strings.Join(paths, " or "))
}

func serviceCheck(root string) domain.PrototypeCheck {
	matches, _ := filepath.Glob(filepath.Join(root, "cmd", "*", "main.go"))
	if len(matches) >= 3 {
		return pass("Go services", fmt.Sprintf("found %d service entrypoints", len(matches)))
	}
	return fail("Go services", fmt.Sprintf("found %d service entrypoints; need at least 3", len(matches)))
}

func alloyCheck(root string) domain.PrototypeCheck {
	found := ""
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && (strings.HasSuffix(strings.ToLower(entry.Name()), ".alloy") || strings.Contains(strings.ToLower(entry.Name()), "alloy")) {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	if found != "" {
		relative, _ := filepath.Rel(root, found)
		return pass("Alloy configuration", filepath.ToSlash(relative)+" exists")
	}
	return fail("Alloy configuration", "an Alloy configuration is required")
}

func composeContentCheck(root string) domain.PrototypeCheck {
	var content []byte
	var selected string
	for _, path := range []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"} {
		if value, err := os.ReadFile(filepath.Join(root, path)); err == nil {
			content, selected = value, path
			break
		}
	}
	if selected == "" {
		return fail("Compose scope", "compose file is missing")
	}
	lower := strings.ToLower(string(content))
	for _, unsupported := range []string{
		"postgres", "mariadb", "mongo", "redis", "sqlite", "cassandra",
		"cockroachdb", "mssql", "sqlserver", "sql server", "oracle", "dynamodb",
		"neo4j", "influxdb", "clickhouse",
	} {
		if strings.Contains(lower, unsupported) {
			return fail("Compose scope", "unsupported database is present; use MySQL when a database is required: "+unsupported)
		}
	}
	for _, image := range []string{"grafana/grafana", "grafana/loki", "grafana/tempo", "prom/prometheus"} {
		if strings.Contains(lower, image) {
			return fail("Compose scope", "local observability backend is not allowed: "+image)
		}
	}
	if !strings.Contains(lower, "alloy") {
		return fail("Compose scope", "Alloy is not present")
	}
	if strings.Contains(lower, "mysql") {
		return pass("Compose scope", "contains optional MySQL and Alloy without local Grafana backends")
	}
	return pass("Compose scope", "contains Alloy without a database or local Grafana backends")
}

func commandCheck(ctx context.Context, root, name, command string, args ...string) domain.PrototypeCheck {
	executable, err := executablePath(command)
	if err != nil {
		return fail(name, err.Error())
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOCACHE="+filepath.Join(os.TempDir(), "grafana-demo-compiler-go-cache"))
	output, err := cmd.CombinedOutput()
	detail := strings.TrimSpace(string(output))
	if len(detail) > 2000 {
		detail = detail[len(detail)-2000:]
	}
	if err != nil {
		if detail == "" {
			detail = err.Error()
		}
		return fail(name, detail)
	}
	if detail == "" {
		detail = "passed"
	}
	return pass(name, detail)
}

func executablePath(command string) (string, error) {
	if path, err := exec.LookPath(command); err == nil {
		return path, nil
	}
	candidates := map[string][]string{
		"go":     {"/opt/homebrew/bin/go", "/usr/local/go/bin/go"},
		"docker": {"/usr/local/bin/docker", "/opt/homebrew/bin/docker"},
	}
	for _, path := range candidates[command] {
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s executable is not installed", command)
}

func pass(name, detail string) domain.PrototypeCheck {
	return domain.PrototypeCheck{Name: name, Status: "passed", Detail: detail}
}

func fail(name, detail string) domain.PrototypeCheck {
	return domain.PrototypeCheck{Name: name, Status: "failed", Detail: detail}
}
