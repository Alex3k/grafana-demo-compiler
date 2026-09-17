package telemetryconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvironment(t *testing.T) {
	c := Connection{OTLPEndpoint: "https://otlp", InstanceID: "123", Token: "raw-secret", LokiURL: "https://loki", LokiUsername: "456"}
	env := Environment(c)
	for _, name := range []string{"GRAFANA_CLOUD_API_KEY", "GRAFANA_CLOUD_OTLP_PASSWORD", "GRAFANA_API_KEY", "GRAFANA_OTLP_TOKEN", "GRAFANA_LOKI_TOKEN"} {
		if env[name] != c.Token {
			t.Errorf("%s must contain raw token", name)
		}
	}
	for _, name := range []string{"GRAFANA_CLOUD_INSTANCE_ID", "GRAFANA_CLOUD_OTLP_USERNAME", "GRAFANA_OTLP_USER"} {
		if env[name] != c.InstanceID {
			t.Errorf("%s must contain instance ID", name)
		}
	}
	for name := range env {
		if !strings.Contains(Prompt(), name) {
			t.Errorf("prompt missing %s", name)
		}
	}
}

func TestValidate(t *testing.T) {
	config := `otelcol.auth.basic "cloud" { basic_auth { username = sys.env("GRAFANA_OTLP_USER") password = env("GRAFANA_OTLP_TOKEN") } }`
	for _, test := range []struct{ name, config, service, want string }{
		{"mapping", config, "environment:\n      GRAFANA_OTLP_USER: ${GRAFANA_OTLP_USER}\n      GRAFANA_OTLP_TOKEN: ${GRAFANA_OTLP_TOKEN}", ""},
		{"list", config, "environment: [GRAFANA_OTLP_USER, GRAFANA_OTLP_TOKEN]", ""},
		{"env file", config, "env_file: [.env]", ""},
		{"env file object", config, "env_file: [{path: .env}]", ""},
		{"missing pass through", config, "image: grafana/alloy", "GRAFANA_OTLP_USER"},
		{"unknown", `x = sys.env("GRAFANA_UNKNOWN_SECRET")`, "env_file: [.env]", "GRAFANA_UNKNOWN_SECRET"},
		{"empty username", `basic_auth { username = "" password = sys.env("GRAFANA_OTLP_TOKEN") }`, "env_file: [.env]", "nonempty username"},
		{"absent username", `basic_auth { password = sys.env("GRAFANA_OTLP_TOKEN") }`, "env_file: [.env]", "nonempty username"},
		{"empty override", config, "env_file: [.env]\n    environment:\n      GRAFANA_OTLP_USER: ''", "GRAFANA_OTLP_USER"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, "config.alloy", test.config)
			write(t, root, "compose.yaml", "services:\n  alloy:\n    "+test.service+"\n")
			err := Validate(root)
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want %s", err, test.want)
			}
		})
	}
}

func TestValidateMountAndEnvironment(t *testing.T) {
	root := t.TempDir()
	write(t, root, "config.alloy", `endpoint = sys.env("GRAFANA_LOKI_URL")`)
	write(t, root, "compose.yml", "services:\n  collector:\n    volumes: ['./config.alloy:/etc/alloy/config.alloy']\n    env_file: .env\n")
	if err := Validate(root); err != nil {
		t.Fatal(err)
	}
	if err := ValidateEnvironment(root, Environment(Connection{})); err == nil || !strings.Contains(err.Error(), "GRAFANA_LOKI_URL") {
		t.Fatalf("expected missing Loki endpoint, got %v", err)
	}
	if err := ValidateEnvironment(root, Environment(Connection{LokiURL: "https://loki"})); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestEnvFilePrecedence(t *testing.T) {
	for _, test := range []struct {
		name, files, environment string
		wantError                bool
	}{
		{"last file empties", "[.env, override.env]", "", true},
		{"last file supplies", "[override.env, .env]", "", false},
		{"environment overrides file", "[.env, override.env]", "\n    environment: [GRAFANA_OTLP_USER]", false},
		{"empty environment overrides file", "[override.env, .env]", "\n    environment: {GRAFANA_OTLP_USER: ''}", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, "config.alloy", `username = sys.env("GRAFANA_OTLP_USER")`)
			write(t, root, "override.env", "GRAFANA_OTLP_USER=\n")
			write(t, root, "compose.yaml", "services:\n  alloy:\n    env_file: "+test.files+test.environment+"\n")
			if err := Validate(root); (err != nil) != test.wantError {
				t.Fatalf("unexpected result: %v", err)
			}
		})
	}
}

func TestValidateResolved(t *testing.T) {
	root := t.TempDir()
	write(t, root, "config.alloy", `username = sys.env("GRAFANA_OTLP_USER")`)
	write(t, root, "compose.yaml", "services:\n  alloy:\n    environment:\n      GRAFANA_OTLP_USER: ${TYPO}\n")
	if err := Validate(root); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, value string
		wantError   bool
	}{
		{"empty interpolation", `""`, true},
		{"unresolved null", `null`, true},
		{"present", `"12345"`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			configuration := []byte(`{"services":{"alloy":{"environment":{"GRAFANA_OTLP_USER":` + test.value + `}}}}`)
			err := ValidateResolved(root, configuration)
			if (err != nil) != test.wantError {
				t.Fatalf("unexpected result: %v", err)
			}
			if err != nil && !strings.Contains(err.Error(), "GRAFANA_OTLP_USER") {
				t.Fatalf("expected variable name, got %v", err)
			}
		})
	}
	if err := ValidateResolved(root, []byte(`{"services":{"alloy":{"env_file":[".env"]}}}`)); err == nil {
		t.Fatal("resolved validation must use actual environment, not env_file")
	}
}
