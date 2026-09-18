package telemetryconfig

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func foundationFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := InstallFoundation(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "cmd", "api")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "main.go"), []byte(`package main
import "example/internal/telemetry"
func main() { telemetry.Init(nil, "api"); telemetry.MarkReady() }
`), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestFoundationRejectsMissingModifiedAndCustomTransport(t *testing.T) {
	if err := ValidateFoundation(t.TempDir()); err == nil || !strings.Contains(err.Error(), "regenerate") {
		t.Fatalf("missing: %v", err)
	}
	for _, kind := range []string{"modified", "credentials", "exporter", "readiness"} {
		t.Run(kind, func(t *testing.T) {
			root := foundationFixture(t)
			path := filepath.Join(root, "cmd", "api", "main.go")
			content := ""
			switch kind {
			case "modified":
				path = filepath.Join(root, "alloy", "config.alloy")
				content = "custom"
			case "credentials":
				content = "package main\nvar credential = \"GRAFANA_CLOUD_API_KEY\""
			case "exporter":
				content = "package main\nimport _ \"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp\""
			case "readiness":
				content = "package main\nfunc main() {}"
			}
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if err := ValidateFoundation(root); err == nil {
				t.Fatal("accepted invalid foundation")
			}
			if kind == "modified" {
				if err := InstallFoundation(root); err == nil {
					t.Fatal("silently replaced existing foundation")
				}
			}
		})
	}
}

func TestWireFoundationOwnsTransportAndIsolation(t *testing.T) {
	root := foundationFixture(t)
	raw := []byte(`{"services":{"api":{"build":{"context":"."},"environment":{"GRAFANA_CLOUD_API_KEY":"secret","OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":"https://outside","OTEL_EXPORTER_OTLP_HEADERS":"Authorization=secret","BUSINESS_SETTING":"keep"},"env_file":[".env"],"networks":{"business":null}},"worker":{"build":{"context":"."}},"mysql":{"image":"mysql:8","environment":{"GRAFANA_API_KEY":"secret"}},"alloy":{"image":"grafana/alloy:latest","environment":{"GRAFANA_CLOUD_API_KEY":"secret"},"ports":[{"target":4318,"published":"4318"}]}},"networks":{"business":{}}}`)
	wired, expected, err := WireFoundation(raw, root, "project-one", "run-two")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(expected, ",") != "api,worker" {
		t.Fatalf("expected apps %v", expected)
	}
	var compose map[string]any
	if err := json.Unmarshal(wired, &compose); err != nil {
		t.Fatal(err)
	}
	services := compose["services"].(map[string]any)
	for _, name := range expected {
		service := services[name].(map[string]any)
		env := service["environment"].(map[string]any)
		if env["OTEL_EXPORTER_OTLP_ENDPOINT"] != "http://alloy:4318" || env["DEMO_RUN_ID"] != "run-two" || env["OTEL_SERVICE_NAME"] != name {
			t.Fatalf("wrong environment %v", env)
		}
		for key := range env {
			if strings.HasPrefix(key, "GRAFANA_") || strings.HasSuffix(key, "HEADERS") || strings.HasSuffix(key, "TRACES_ENDPOINT") {
				t.Fatalf("credential or endpoint override survived: %s", key)
			}
		}
		if _, ok := service["env_file"]; ok {
			t.Fatal("env_file can reintroduce credentials")
		}
		if _, ok := service["networks"].(map[string]any)["compiler_telemetry"]; !ok {
			t.Fatal("app missing Alloy network")
		}
	}
	alloy := services["alloy"].(map[string]any)
	if alloy["ports"] != nil {
		t.Fatal("Alloy exposes host ports")
	}
	env := alloy["environment"].(map[string]any)
	if env["DEMO_COMPOSE_PROJECT"] != "project-one" || env["DEMO_APPLICATION_SERVICES"] != "(api|worker)" || env["GRAFANA_CLOUD_API_KEY"] != "secret" {
		t.Fatalf("wrong Alloy environment %v", env)
	}
	if bytes.Contains(wired, []byte("https://outside")) {
		t.Fatal("retained alternate exporter")
	}
}

func TestGeneratedHelperCompilesAndReadinessRequiresMark(t *testing.T) {
	root := t.TempDir()
	if err := InstallFoundation(root); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"go.mod", "go.sum"} {
		data, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	const helperTest = `package telemetry
import (
 "bytes"
 "context"
 "io"
 "net/http"
 "net/http/httptest"
 "testing"
 "time"
)
func TestReadinessLifecycle(t *testing.T) {
 metrics, traces := make(chan []byte, 10), make(chan []byte, 10)
 collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  payload, _ := io.ReadAll(r.Body)
  if r.URL.Path == "/v1/metrics" && bytes.Contains(payload, []byte("demo_compiler_probe_total")) { metrics <- payload }
  if r.URL.Path == "/v1/traces" && bytes.Contains(payload, []byte("demo.compiler.probe")) { traces <- payload }
  w.WriteHeader(200)
 }))
 defer collector.Close()
 t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)
 t.Setenv("DEMO_RUN_ID", "test-run")
 shutdown, err := Init(context.Background(), "test-service")
 if err != nil { t.Fatal(err) }
 defer shutdown(context.Background())
 if err := CheckReady(context.Background()); err == nil { t.Fatal("ready before application initialization") }
 select {
 case <-metrics: t.Fatal("metric probe exported before readiness")
 case <-traces: t.Fatal("trace probe exported before readiness")
 case <-time.After(5200*time.Millisecond):
 }
 MarkReady()
 if err := CheckReady(context.Background()); err != nil { t.Fatal(err) }
 timeout := time.After(12*time.Second)
 metricEvents, traceEvents := metrics, traces
 for metricEvents != nil || traceEvents != nil {
  var payload []byte
  select {
  case payload = <-metricEvents: metricEvents = nil
  case payload = <-traceEvents: traceEvents = nil
  case <-timeout: t.Fatal("missing metric or trace probe after readiness")
  }
  for _, marker := range []string{"service.name", "test-service", "demo.run.id", "test-run"} {
   if !bytes.Contains(payload, []byte(marker)) { t.Fatalf("OTLP payload missing resource marker %s", marker) }
  }
 }
}
`
	if err := os.WriteFile(filepath.Join(root, "internal", "telemetry", "telemetry_test.go"), []byte(helperTest), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "test", "-mod=mod", "./internal/telemetry")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated helper: %v\n%s", err, output)
	}
}
