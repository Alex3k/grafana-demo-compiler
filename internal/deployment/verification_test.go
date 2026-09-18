package deployment

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVerificationCommandIsolationAndAuthErrors(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ -n \"$GRAFANA_TOKEN$GRAFANA_SERVER$GCX_CONFIG$GCX_CONTEXT\" ]; then exit 19; fi\nif [ \"$1\" = auth ]; then echo '401 unauthorized secret-do-not-display' >&2; exit 1; fi\nprintf '{\"ok\":true}'\n"
	if err := os.WriteFile(filepath.Join(dir, "gcx"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	for _, key := range []string{"GRAFANA_TOKEN", "GRAFANA_SERVER", "GCX_CONFIG", "GCX_CONTEXT"} {
		t.Setenv(key, "must-not-inherit")
	}
	if _, err := verificationCommand(context.Background(), "", true, "gcx", "check"); err != nil {
		t.Fatal(err)
	}
	_, err := verificationCommand(context.Background(), "", true, "gcx", "auth")
	if !errors.Is(err, errVerificationAuth) || strings.Contains(err.Error(), "secret-do-not-display") {
		t.Fatalf("error=%v", err)
	}
}

func TestVerifyTelemetryCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := New().VerifyTelemetry(ctx, "democompiler123456abcdef", "run", []string{"api"}, time.Now(), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestTelemetryResults(t *testing.T) {
	for _, tt := range []struct {
		name, signal, payload string
		found                 bool
	}{
		{"positive vector", "metrics", `{"status":"success","data":{"resultType":"vector","result":[{"value":[1,"2"]}]}}`, true},
		{"zero vector", "metrics", `{"status":"success","data":{"resultType":"vector","result":[{"value":[1,"0"]}]}}`, false},
		{"positive range", "metrics", `{"status":"success","data":{"resultType":"matrix","result":[{"values":[[1,"1"]]}]}}`, true},
		{"empty vector", "metrics", `{"status":"success","data":{"resultType":"vector","result":[]}}`, false},
		{"metadata", "metrics", `{"status":"success","data":{"stats":{"total":1}}}`, false},
		{"nan", "metrics", `{"resultType":"vector","result":[{"value":[1,"NaN"]}]}`, false},
		{"probe log", "logs", `{"status":"success","data":{"resultType":"streams","result":[{"values":[["1","demo.compiler.probe run"]]}]}}`, true},
		{"gcx probe log", "logs", `{"status":"success","data":{"resultType":"streams","result":[{"values":[{"timestamp":"1789729341165826763","line":"{\"event\":\"demo.compiler.probe\",\"demo_run_id\":\"run\"}","structuredMetadata":{"detected_level":"unknown"}}]}]}}`, true},
		{"gcx unrelated log", "logs", `{"resultType":"streams","result":[{"values":[{"timestamp":"1","line":"ordinary log"}]}]}`, false},
		{"gcx metadata only", "logs", `{"resultType":"streams","result":[{"values":[{"timestamp":"1","structuredMetadata":{"event":"demo.compiler.probe"}}]}]}`, false},
		{"malformed log", "logs", `{"resultType":"streams","result":[{"values":[42]}]}`, false},
		{"no log lines", "logs", `{"status":"success","data":{"resultType":"streams","result":[{"values":[]}]}}`, false},
		{"unrelated log", "logs", `{"status":"success","data":{"resultType":"streams","result":[{"values":[["1","something else"]]}]}}`, false},
		{"trace", "traces", `{"traces":[{"traceID":"123"}],"metrics":{"inspectedBytes":100}}`, true},
		{"empty traces", "traces", `{"traces":[],"metrics":{"inspectedBytes":100}}`, false},
		{"trace metadata", "traces", `{"traces":[{"duration":100}]}`, false},
		{"error", "metrics", `{"error":"bad query","data":{"resultType":"vector","result":[{"value":[1,"2"]}]}}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			found, _ := hasTelemetryResult([]byte(tt.payload), tt.signal)
			if found != tt.found {
				t.Fatalf("found=%v", found)
			}
		})
	}
}

func TestCloudTelemetryDatasources(t *testing.T) {
	for signal, want := range map[string]string{"metrics": "grafanacloud-prom", "logs": "grafanacloud-logs", "traces": "grafanacloud-traces"} {
		if got := verificationDatasource(signal); got != want {
			t.Fatalf("%s datasource = %q, want %q", signal, got, want)
		}
	}
}

func TestTelemetryQueriesScopeToDeployment(t *testing.T) {
	start := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)
	for _, signal := range []string{"metrics", "logs", "traces"} {
		args := telemetryQuery(signal, verificationDatasource(signal), "checkout", "run-123", start, start.Add(time.Minute))
		joined := strings.Join(args, " ")
		for _, expected := range []string{verificationDatasource(signal), "checkout", "run-123", "--from 2026-09-18T09:00:00Z", "--to 2026-09-18T09:01:00Z"} {
			if !strings.Contains(joined, expected) {
				t.Fatalf("%s missing %s", joined, expected)
			}
		}
	}
}

func TestVerificationBufferBounded(t *testing.T) {
	buffer := verificationBuffer{limit: 4}
	if n, err := buffer.Write([]byte("123456")); n != 6 || err != nil {
		t.Fatalf("write %d %v", n, err)
	}
	buffer.Write([]byte("789"))
	if string(buffer.data) != "1234" || !buffer.exceeded {
		t.Fatalf("buffer=%+v", buffer)
	}
}
