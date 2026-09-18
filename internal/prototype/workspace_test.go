package prototype

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestWorkspaceInstallsAndProtectsFoundation(t *testing.T) {
	root := t.TempDir()
	w, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	path := "internal/telemetry/telemetry.go"
	content, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteFile(path, "package telemetry"); err == nil {
		t.Fatal("accepted foundation edit")
	}
	if _, err := w.WriteFile(path, string(content)); err != nil {
		t.Fatalf("identical revision copy: %v", err)
	}
	if len(w.Artifacts()) != 2 {
		t.Fatalf("foundation files absent from artifacts: %v", w.Artifacts())
	}
}

func TestWorkspaceDoesNotRetrofitLegacyPrototype(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module legacy"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "internal", "telemetry", "telemetry.go")); !os.IsNotExist(err) {
		t.Fatalf("legacy foundation was silently installed: %v", err)
	}
}

func TestValidateTidiesPlatformDependenciesBeforeReadonlyLinuxBuild(t *testing.T) {
	for _, brokenLinux := range []bool{false, true} {
		name := "complete Linux module"
		if brokenLinux {
			name = "Linux-only compile failure"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dependency := t.TempDir()
			write := func(path, content string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(dependency, "go.mod"), "module example.com/linuxdependency\n\ngo 1.25.0\n")
			write(filepath.Join(dependency, "dependency.go"), "package linuxdependency\nfunc Value() int { return 1 }\n")
			// No require entry: readonly build can only pass if validation tidies first.
			write(filepath.Join(root, "go.mod"), "module example.com/demo\n\ngo 1.25.0\n\nreplace example.com/linuxdependency => "+filepath.ToSlash(dependency)+"\n")
			write(filepath.Join(root, "demo.go"), "package demo\n")
			linuxSource := "package demo\nimport dep \"example.com/linuxdependency\"\nvar value = dep.Value()\n"
			if brokenLinux {
				linuxSource += "var invalid = linuxOnlyUndefinedSymbol\n"
			}
			write(filepath.Join(root, "demo_linux.go"), linuxSource)
			// These inherited settings must not redirect validation to another
			// workspace or cause it to ignore the module's dependency state.
			t.Setenv("GOWORK", filepath.Join(root, "missing.go.work"))
			t.Setenv("GOFLAGS", "-mod=vendor")
			t.Setenv("GOOS", "darwin")
			t.Setenv("CGO_ENABLED", "1")
			bin := t.TempDir()
			if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			workspace := &Workspace{root: root}
			foundTidy, foundBuild := false, false
			for _, check := range workspace.Validate(context.Background()) {
				switch check.Name {
				case "Go dependencies":
					foundTidy = true
					if check.Status != "passed" {
						t.Fatalf("tidy failed: %#v", check)
					}
				case "Go build":
					foundBuild = true
					if !foundTidy {
						t.Fatal("build ran before dependency check")
					}
					if brokenLinux {
						if check.Status != "failed" || !strings.Contains(check.Detail, "linuxOnlyUndefinedSymbol") {
							t.Fatalf("Linux-only error was missed: %#v", check)
						}
					} else if check.Status != "passed" {
						t.Fatalf("readonly Linux build failed after tidy: %#v", check)
					}
				}
			}
			if !foundTidy || !foundBuild {
				t.Fatal("validation omitted Go dependency or build checks")
			}
			module, err := os.ReadFile(filepath.Join(root, "go.mod"))
			if err != nil || !strings.Contains(string(module), "require example.com/linuxdependency") {
				t.Fatalf("tidy did not record Linux dependency: %s, %v", module, err)
			}
		})
	}
}
