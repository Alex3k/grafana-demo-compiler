package telemetryconfig

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

//go:embed telemetry.go.txt
var helperSource []byte

//go:embed config.alloy.txt
var alloySource []byte

var foundationFiles = map[string][]byte{
	"internal/telemetry/telemetry.go": helperSource,
	"alloy/config.alloy":              alloySource,
}

func IsFoundationPath(path string) bool {
	_, ok := foundationFiles[filepath.ToSlash(filepath.Clean(path))]
	return ok
}

// InstallFoundation creates fixed files, never replacing an existing revision.
func InstallFoundation(root string) error {
	for path, want := range foundationFiles {
		target := filepath.Join(root, path)
		if got, err := os.ReadFile(target); err == nil {
			if !bytes.Equal(got, want) {
				return fmt.Errorf("compiler-owned telemetry file %s differs; regenerate this prototype", path)
			}
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(target, want, 0600); err != nil {
			return err
		}
	}
	return nil
}

func validateFoundationFiles(root string) error {
	for path, want := range foundationFiles {
		info, err := os.Lstat(filepath.Join(root, path))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("compiler-owned telemetry foundation missing at %s; regenerate this prototype before deployment", path)
		}
		got, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return err
		}
		if !bytes.Equal(got, want) {
			return fmt.Errorf("compiler-owned telemetry file %s has changed; regenerate this prototype", path)
		}
	}
	return nil
}

// ValidateFoundation rejects legacy/custom transport and requires each service
// entrypoint to initialize the shared helper and explicitly mark readiness.
func ValidateFoundation(root string) error {
	if err := validateFoundationFiles(root); err != nil {
		return err
	}
	mains := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "vendor" || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if filepath.Ext(path) != ".go" || IsFoundationPath(rel) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte("GRAFANA_")) {
			return fmt.Errorf("%s references Cloud credentials; application telemetry must use the compiler helper", rel)
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, data, 0)
		if err != nil {
			return fmt.Errorf("parse application telemetry in %s: %w", rel, err)
		}
		helper := ""
		for _, spec := range file.Imports {
			name, _ := strconv.Unquote(spec.Path.Value)
			if strings.Contains(name, "go.opentelemetry.io/otel/exporters/") || strings.HasPrefix(name, "go.opentelemetry.io/otel/sdk") {
				return fmt.Errorf("%s configures custom telemetry providers; use the compiler helper", rel)
			}
			if strings.HasSuffix(name, "/internal/telemetry") {
				helper = "telemetry"
				if spec.Name != nil {
					helper = spec.Name.Name
				}
			}
		}
		if file.Name.Name != "main" || entry.Name() != "main.go" {
			return nil
		}
		mains++
		initialized, marked := false, false
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := selector.X.(*ast.Ident)
			if !ok || helper == "" || ident.Name != helper {
				return true
			}
			initialized = initialized || selector.Sel.Name == "Init"
			marked = marked || selector.Sel.Name == "MarkReady"
			return true
		})
		if !initialized || !marked {
			return fmt.Errorf("%s must call the compiler telemetry.Init and telemetry.MarkReady after application initialization", rel)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if mains == 0 {
		return fmt.Errorf("no Go application entrypoints found for telemetry foundation")
	}
	return nil
}

// WireFoundation takes resolved Compose JSON and installs compiler-owned runtime
// wiring. Cloud environment values already resolved on Alloy are retained.
func WireFoundation(configuration []byte, root, project, runID string) ([]byte, []string, error) {
	if err := ValidateFoundation(root); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(project) == "" || strings.TrimSpace(runID) == "" {
		return nil, nil, fmt.Errorf("Compose project and deployment run ID are required")
	}
	var compose map[string]any
	if err := json.Unmarshal(configuration, &compose); err != nil {
		return nil, nil, fmt.Errorf("parse resolved Compose: %w", err)
	}
	services, ok := compose["services"].(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("resolved Compose services are missing")
	}
	alloy, ok := services["alloy"].(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("Compose requires the compiler Alloy service named alloy")
	}
	applicationNames := []string{}
	for name, raw := range services {
		svc, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("invalid Compose service %s", name)
		}
		if name == "alloy" {
			continue
		}
		if ports, ok := svc["ports"].([]any); ok {
			kept := make([]any, 0, len(ports))
			for _, port := range ports {
				entry, ok := port.(map[string]any)
				if !ok {
					return nil, nil, fmt.Errorf("service %s has unresolved port configuration", name)
				}
				target := fmt.Sprint(entry["target"])
				if target != "4317" && target != "4318" && target != "9464" {
					kept = append(kept, port)
				}
			}
			svc["ports"] = kept
		}
		env, _ := svc["environment"].(map[string]any)
		if env == nil {
			env = map[string]any{}
		}
		for key := range env {
			if strings.HasPrefix(key, "GRAFANA_") || strings.HasPrefix(key, "OTEL_EXPORTER_") {
				delete(env, key)
			}
		}
		svc["environment"] = env
		delete(svc, "env_file") // config was resolved; don't allow a later credential reinjection.
		if svc["build"] == nil {
			continue
		}
		applicationNames = append(applicationNames, name)
		env["OTEL_EXPORTER_OTLP_ENDPOINT"] = "http://alloy:4318"
		env["OTEL_EXPORTER_OTLP_PROTOCOL"] = "http/protobuf"
		env["OTEL_SERVICE_NAME"] = name
		env["DEMO_RUN_ID"] = runID
		env["OTEL_TRACES_SAMPLER"] = "always_on"
		env["OTEL_SDK_DISABLED"] = "false"
		delete(env, "OTEL_RESOURCE_ATTRIBUTES")
		if svc["network_mode"] != nil {
			return nil, nil, fmt.Errorf("application service %s cannot use network_mode with compiler telemetry", name)
		}
		addFoundationNetwork(svc)
	}
	sort.Strings(applicationNames)
	if len(applicationNames) == 0 {
		return nil, nil, fmt.Errorf("Compose has no built Go application services")
	}
	env, _ := alloy["environment"].(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	env["DEMO_COMPOSE_PROJECT"] = project
	env["DEMO_RUN_ID"] = runID
	escaped := make([]string, len(applicationNames))
	for i, name := range applicationNames {
		escaped[i] = regexp.QuoteMeta(name)
	}
	env["DEMO_APPLICATION_SERVICES"] = "(" + strings.Join(escaped, "|") + ")"
	alloy["environment"] = env
	alloy["image"] = "grafana/alloy@sha256:b8ec653c44235fbe910879145dac3597d66b0aaecf60bcbbe82580767771a839"
	delete(alloy, "build")
	delete(alloy, "entrypoint")
	alloy["command"] = []any{"run", "--server.http.listen-addr=0.0.0.0:12345", "/etc/alloy/config.alloy"}
	alloy["volumes"] = []any{
		map[string]any{"type": "bind", "source": filepath.Join(root, "alloy", "config.alloy"), "target": "/etc/alloy/config.alloy", "read_only": true},
		map[string]any{"type": "bind", "source": "/var/run/docker.sock", "target": "/var/run/docker.sock", "read_only": true},
	}
	delete(alloy, "ports")
	delete(alloy, "network_mode")
	delete(alloy, "env_file")
	addFoundationNetwork(alloy)
	networks, _ := compose["networks"].(map[string]any)
	if networks == nil {
		networks = map[string]any{}
	}
	networks["compiler_telemetry"] = map[string]any{}
	compose["networks"] = networks
	data, err := json.Marshal(compose)
	return data, applicationNames, err
}

func addFoundationNetwork(service map[string]any) {
	networks, _ := service["networks"].(map[string]any)
	if networks == nil {
		networks = map[string]any{}
		if entries, ok := service["networks"].([]any); ok {
			for _, entry := range entries {
				networks[fmt.Sprint(entry)] = nil
			}
		} else {
			networks["default"] = nil
		}
	}
	networks["compiler_telemetry"] = nil
	service["networks"] = networks
}
