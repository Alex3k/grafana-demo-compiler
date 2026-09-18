// Package telemetryconfig defines the credentials shared by generation and deployment.
package telemetryconfig

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v4"
)

type Connection struct {
	OTLPEndpoint, InstanceID, Token                          string
	PrometheusURL, PrometheusUsername, LokiURL, LokiUsername string
}

func Environment(c Connection) map[string]string {
	return map[string]string{
		"GRAFANA_CLOUD_OTLP_ENDPOINT": c.OTLPEndpoint,
		"GRAFANA_CLOUD_INSTANCE_ID":   c.InstanceID,
		"GRAFANA_CLOUD_API_KEY":       c.Token,
		"GRAFANA_CLOUD_OTLP_USERNAME": c.InstanceID,
		"GRAFANA_CLOUD_OTLP_PASSWORD": c.Token,
		"GRAFANA_OTLP_ENDPOINT":       c.OTLPEndpoint,
		"GRAFANA_OTLP_USER":           c.InstanceID,
		"GRAFANA_API_KEY":             c.Token,
		"GRAFANA_OTLP_TOKEN":          c.Token,
		"GRAFANA_PROM_URL":            c.PrometheusURL,
		"GRAFANA_PROM_USER":           c.PrometheusUsername,
		"GRAFANA_LOKI_URL":            c.LokiURL,
		"GRAFANA_LOKI_USER":           c.LokiUsername,
		"GRAFANA_LOKI_TOKEN":          c.Token,
	}
}

func Prompt() string {
	names := make([]string, 0)
	for name := range Environment(Connection{}) {
		names = append(names, name)
	}
	sort.Strings(names)
	return "Compiler-owned telemetry foundation: protected internal/telemetry/telemetry.go and alloy/config.alloy are already installed; do not author or edit them. Each Go main must import <module>/internal/telemetry, call shutdown, err := telemetry.Init(context.Background(), <compose-service-name>) before application initialization, handle the error and shutdown, then call telemetry.MarkReady() only after dependencies and business listener are ready. Give every built service a Compose healthcheck using its actual binary path with --telemetry-healthcheck. Use Go 1.25 or newer and OpenTelemetry v1.45.0 for otel, sdk, sdk/metric, exporters/otlp/otlpmetric/otlpmetrichttp, and exporters/otlp/otlptrace/otlptracehttp. Include a Compose service named alloy with image grafana/alloy:latest as a placeholder; deployment installs its fixed configuration and final wiring. Emit business telemetry using global OTel providers without custom SDK/exporter bootstrap; business logs need not use any mandated format. Deployment supplies the local Alloy endpoint, service identity, and run ID. Cloud configuration is only passed to Alloy: " + strings.Join(names, ", ") + ". Never put Cloud credentials, direct GRAFANA_* references, or authentication headers in application services."
}

var reference = regexp.MustCompile(`(?:sys\.)?env\s*\(\s*"(GRAFANA_[A-Z0-9_]+)"\s*\)`)
var basicAuth = regexp.MustCompile(`(?s)basic_auth\s*\{([^{}]*)\}`)
var emptyUsername = regexp.MustCompile(`\busername\s*=\s*"\s*"`)
var username = regexp.MustCompile(`\busername\s*=`)

// ValidateEnvironment ensures deployment can supply every referenced credential.
func ValidateEnvironment(root string, values map[string]string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".alloy" && filepath.Ext(path) != ".river" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range reference.FindAllSubmatch(data, -1) {
			name := string(match[1])
			if strings.TrimSpace(values[name]) == "" {
				return fmt.Errorf("deployment requires nonempty %s", name)
			}
		}
		return nil
	})
}

type service struct {
	Image       string `yaml:"image"`
	Environment any    `yaml:"environment"`
	EnvFile     any    `yaml:"env_file"`
	Volumes     []any  `yaml:"volumes"`
}

// Validate checks generated Alloy references without reading or reporting secrets.
// The deployment-created root .env is accepted even before deployment writes it.
func Validate(root string) error {
	if _, err := os.Stat(filepath.Join(root, "internal", "telemetry", "telemetry.go")); err == nil {
		return ValidateFoundation(root)
	}
	return validate(root, nil, false)
}

// ValidateResolved checks actual container environment values from docker compose
// config, after interpolation and env_file processing, without reporting values.
func ValidateResolved(root string, configuration []byte) error {
	return validate(root, configuration, true)
}

func validate(root string, configuration []byte, resolved bool) error {
	configs := map[string][]string{}
	supported := Environment(Connection{})
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".alloy" && filepath.Ext(path) != ".river" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range reference.FindAllSubmatch(data, -1) {
			name := string(match[1])
			if _, ok := supported[name]; !ok {
				return fmt.Errorf("unsupported Grafana environment variable %s", name)
			}
			configs[path] = append(configs[path], name)
		}
		if len(configs[path]) > 0 {
			for _, block := range basicAuth.FindAllSubmatch(data, -1) {
				if !username.Match(block[1]) || emptyUsername.Match(block[1]) {
					return fmt.Errorf("Grafana basic_auth requires a nonempty username")
				}
			}
		}
		return nil
	})
	if err != nil || len(configs) == 0 {
		return err
	}
	if !resolved {
		for _, name := range []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"} {
			data, err := os.ReadFile(filepath.Join(root, name))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			configuration = data
			break
		}
	}
	var compose struct {
		Services map[string]service `yaml:"services"`
	}
	if yaml.Unmarshal(configuration, &compose) != nil {
		return fmt.Errorf("cannot parse Compose telemetry configuration")
	}
	services := compose.Services
	for path, names := range configs {
		hasMount := false
		for _, svc := range services {
			hasMount = hasMount || mounts(root, path, svc.Volumes)
		}
		matched := false
		for name, svc := range services {
			if !mounts(root, path, svc.Volumes) && (hasMount || !strings.Contains(strings.ToLower(name+" "+svc.Image), "alloy")) {
				continue
			}
			matched = true
			passed := map[string]bool{}
			files := envFiles(svc.EnvFile)
			if resolved {
				files = nil
			}
			for _, file := range files {
				if filepath.Clean(file) == ".env" {
					for key := range supported {
						passed[key] = true
					}
					continue
				}
				data, err := os.ReadFile(filepath.Join(root, file))
				if err != nil {
					continue
				}
				for _, line := range strings.Split(string(data), "\n") {
					key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
					if ok {
						passed[key] = strings.Trim(strings.TrimSpace(value), "\"'") != ""
					}
				}
			}
			for key, present := range passedEnvironment(svc.Environment) {
				passed[key] = present
			}
			if resolved {
				if env, ok := svc.Environment.(map[string]any); ok {
					for key, value := range env {
						if value == nil {
							passed[key] = false
						}
					}
				}
			}
			for _, variable := range names {
				if !passed[variable] {
					return fmt.Errorf("Compose service %s must pass nonempty %s to Alloy", name, variable)
				}
			}
		}
		if !matched {
			return fmt.Errorf("Grafana Alloy configuration has no identifiable Compose service")
		}
	}
	return nil
}

func passedEnvironment(value any) map[string]bool {
	result := map[string]bool{}
	switch env := value.(type) {
	case map[string]any:
		for key, value := range env {
			result[key] = value == nil || strings.TrimSpace(fmt.Sprint(value)) != ""
		}
	case []any:
		for _, entry := range env {
			key, value, assigned := strings.Cut(fmt.Sprint(entry), "=")
			result[key] = !assigned || strings.TrimSpace(value) != ""
		}
	}
	return result
}

func envFiles(value any) []string {
	switch files := value.(type) {
	case string:
		return []string{files}
	case map[string]any:
		if path, ok := files["path"].(string); ok {
			return []string{path}
		}
	case []any:
		var result []string
		for _, file := range files {
			result = append(result, envFiles(file)...)
		}
		return result
	}
	return nil
}

func mounts(root, path string, volumes []any) bool {
	for _, volume := range volumes {
		var source string
		switch v := volume.(type) {
		case string:
			source, _, _ = strings.Cut(v, ":")
		case map[string]any:
			source, _ = v["source"].(string)
		}
		if source == "" {
			continue
		}
		if !filepath.IsAbs(source) {
			source = filepath.Join(root, source)
		}
		rel, err := filepath.Rel(source, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
