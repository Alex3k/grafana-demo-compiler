package deployment

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveOTLP(t *testing.T) {
	t.Parallel()

	const token = "secret-access-token"
	runner := New()
	runner.baseURL = "https://cloud.example.com"
	runner.httpClient = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", req.Method)
		}
		if req.URL.Path != "/api/instances/demostack/connections" {
			t.Errorf("path = %q", req.URL.Path)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("Authorization = %q", got)
		}
		if got := req.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q", got)
		}
		return jsonResponse(http.StatusOK, `{"otlpHttpUrl":"https://otlp-gateway-test.grafana.net/otlp/"}`), nil
	})}
	endpoint, err := runner.ResolveOTLP(context.Background(), "demostack", token)
	if err != nil {
		t.Fatalf("ResolveOTLP() error = %v", err)
	}
	if endpoint != "https://otlp-gateway-test.grafana.net/otlp" {
		t.Fatalf("ResolveOTLP() = %q", endpoint)
	}
}

func TestResolveOTLPAppendsOTLP(t *testing.T) {
	t.Parallel()

	runner := New()
	runner.baseURL = "https://cloud.example.com/"
	runner.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"otlpHttpUrl":"https://otlp-gateway-test.grafana.net"}`), nil
	})}
	endpoint, err := runner.ResolveOTLP(context.Background(), "demostack", "token")
	if err != nil {
		t.Fatalf("ResolveOTLP() error = %v", err)
	}
	if endpoint != "https://otlp-gateway-test.grafana.net/otlp" {
		t.Fatalf("ResolveOTLP() = %q", endpoint)
	}
}

func TestResolveOTLPMissingEndpoint(t *testing.T) {
	t.Parallel()

	runner := New()
	runner.baseURL = "https://cloud.example.com"
	runner.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"other":"value"}`), nil
	})}
	_, err := runner.ResolveOTLP(context.Background(), "demostack", "token")
	if err == nil || !strings.Contains(err.Error(), "did not include otlpHttpUrl") {
		t.Fatalf("ResolveOTLP() error = %v", err)
	}
}

func TestResolveOTLPHTTPErrorRedactsToken(t *testing.T) {
	t.Parallel()

	const token = "must-not-leak"
	runner := New()
	runner.baseURL = "https://cloud.example.com"
	runner.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusUnauthorized, "rejected token "+token), nil
	})}
	_, err := runner.ResolveOTLP(context.Background(), "demostack", token)
	if err == nil {
		t.Fatal("ResolveOTLP() error = nil")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("ResolveOTLP() leaked token in error: %v", err)
	}
	if !strings.Contains(err.Error(), "401 Unauthorized") {
		t.Fatalf("ResolveOTLP() error = %v", err)
	}
}

func TestResolveOTLPValidatesInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		slug  string
		token string
	}{
		{name: "empty slug", token: "token"},
		{name: "invalid slug", slug: "Demo Stack", token: "token"},
		{name: "empty token", slug: "demostack"},
		{name: "newline in token", slug: "demostack", token: "token\nvalue"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := New()
			runner.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("request must not be sent")
			})}
			if _, err := runner.ResolveOTLP(context.Background(), test.slug, test.token); err == nil {
				t.Fatal("ResolveOTLP() error = nil")
			}
		})
	}
}

func TestResolveOTLPRejectsUntrustedEndpoint(t *testing.T) {
	t.Parallel()

	tests := []string{
		"http://otlp-gateway-test.grafana.net",
		"https://attacker.example.com",
		"https://otlp-gateway-test.grafana.net:8443",
		"https://user@otlp-gateway-test.grafana.net",
		"https://otlp-gateway-test.grafana.net/other",
		"https://otlp-gateway-test.grafana.net?target=other",
	}
	for _, endpoint := range tests {
		t.Run(endpoint, func(t *testing.T) {
			runner := New()
			runner.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, fmt.Sprintf(`{"otlpHttpUrl":%q}`, endpoint)), nil
			})}
			if _, err := runner.ResolveOTLP(context.Background(), "demostack", "token"); err == nil {
				t.Fatal("ResolveOTLP() error = nil")
			}
		})
	}
}

func TestParseStackReadyWithoutOTLP(t *testing.T) {
	t.Parallel()

	stack := parseStack([]byte(`{"url":"https://demostack.grafana.net","id":12345}`), "demostack")
	if stack.URL == "" || stack.InstanceID == "" {
		t.Fatalf("parseStack() = %#v, want URL and instance ID", stack)
	}
	if stack.OTLPEndpoint != "" {
		t.Fatalf("parseStack() OTLP endpoint = %q, want empty", stack.OTLPEndpoint)
	}
	if missing := missingStackFields(stack); missing != "" {
		t.Fatalf("missingStackFields() = %q", missing)
	}
}

func TestParseStackPreservesAndNormalizesOTLP(t *testing.T) {
	t.Parallel()

	stack := parseStack([]byte(`{"url":"https://demostack.grafana.net","id":"12345","otlpHttpUrl":"https://otlp-gateway-test.grafana.net/otlp/"}`), "demostack")
	if stack.OTLPEndpoint != "https://otlp-gateway-test.grafana.net/otlp" {
		t.Fatalf("parseStack() OTLP endpoint = %q", stack.OTLPEndpoint)
	}
}

func TestEnsureDockerignoreExcludesTelemetrySecrets(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".dockerignore")
	if err := os.WriteFile(path, []byte("vendor\n.env\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureDockerignore(root); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "vendor\n.env\n.env.tmp*\n" {
		t.Fatalf(".dockerignore = %q", content)
	}
}

func TestComposeBuildUsesSecret(t *testing.T) {
	const token = "secret-token"
	unsafe := []byte(`{"services":{"api":{"build":{"context":".","args":{"TOKEN":"secret-token"}}}}}`)
	if !composeBuildUsesSecret(unsafe, token) {
		t.Fatal("secret build argument was not detected")
	}
	safe := []byte(`{"services":{"api":{"build":{"context":".","args":{"VERSION":"1"}},"environment":{"GRAFANA_CLOUD_API_KEY":"secret-token"}}}}`)
	if composeBuildUsesSecret(safe, token) {
		t.Fatal("runtime environment secret was treated as a build argument")
	}
	if !composeBuildUsesSecret([]byte("not-json"), token) {
		t.Fatal("invalid Compose configuration must fail closed")
	}
}

func TestRedactError(t *testing.T) {
	const token = "secret-token"
	err := redactError(fmt.Errorf("docker echoed %s", token), token)
	if strings.Contains(err.Error(), token) || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("redactError() = %q", err)
	}
}

func TestRunInKeepsSuccessfulStderrOutOfStructuredOutput(t *testing.T) {
	output, err := runIn(context.Background(), t.TempDir(), "sh", "-c", `printf '{"services":{}}'; printf 'warning' >&2`)
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != `{"services":{}}` {
		t.Fatalf("runIn() = %q", output)
	}
}

func TestDockerCommandEnvIncludesCredentialHelperDirectory(t *testing.T) {
	for _, item := range dockerCommandEnv() {
		if strings.HasPrefix(item, "PATH=") && strings.Contains(item, "/Applications/Docker.app/Contents/Resources/bin") {
			return
		}
	}
	t.Fatal("Docker Desktop credential helper directory is missing from PATH")
}

func TestParseStackIncludesPrometheusConnection(t *testing.T) {
	stack := parseStack([]byte(`{"url":"https://demostack.grafana.net","id":12345,"hmInstancePromUrl":"https://prometheus-prod-56-prod-us-east-2.grafana.net","hmInstancePromId":67890}`), "demostack")
	if stack.PrometheusURL != "https://prometheus-prod-56-prod-us-east-2.grafana.net/api/prom/push" {
		t.Fatalf("PrometheusURL = %q", stack.PrometheusURL)
	}
	if stack.PrometheusUsername != "67890" {
		t.Fatalf("PrometheusUsername = %q", stack.PrometheusUsername)
	}
}

func TestLoadEnvironmentDefaultsSkipsGrafanaAndPlaceholders(t *testing.T) {
	root := t.TempDir()
	content := "MYSQL_PASSWORD=demo\nGRAFANA_API_KEY=<token>\nINVALID-KEY=value\n"
	if err := os.WriteFile(filepath.Join(root, ".env.example"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	defaults := loadEnvironmentDefaults(root)
	if defaults["MYSQL_PASSWORD"] != "demo" || len(defaults) != 1 {
		t.Fatalf("loadEnvironmentDefaults() = %#v", defaults)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
