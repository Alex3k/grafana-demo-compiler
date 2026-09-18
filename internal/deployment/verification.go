package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Alex3k/grafana-demo-compiler/internal/gcxtool"
)

var errVerificationAuth = errors.New("Grafana read authentication failed; use Connect Grafana and retry verification")

type verificationBuffer struct {
	data     []byte
	limit    int
	exceeded bool
}

func (b *verificationBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - len(b.data)
	if len(p) > remaining {
		p = p[:remaining]
		b.exceeded = true
	}
	b.data = append(b.data, p...)
	return n, nil
}

// No command output enters errors: even failed CLI requests can contain secrets.
func verificationCommand(ctx context.Context, root string, isolated bool, command string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	path, err := executable(command)
	if err != nil {
		return nil, fmt.Errorf("%s is not installed", command)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = root
	cmd.WaitDelay = time.Second
	if isolated {
		for _, key := range []string{"HOME", "PATH", "USER", "TMPDIR", "XDG_CONFIG_HOME", "SSL_CERT_FILE"} {
			if value, ok := os.LookupEnv(key); ok {
				cmd.Env = append(cmd.Env, key+"="+value)
			}
		}
		cmd.Env = append(cmd.Env, "GCX_AGENT_MODE=1")
	} else if command == "docker" {
		cmd.Env = dockerCommandEnv()
	}
	stdout, stderr := &verificationBuffer{limit: 1024 * 1024}, &verificationBuffer{limit: 8192}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		message := strings.ToLower(string(stdout.data) + string(stderr.data))
		for _, needle := range []string{"401", "403", "unauthorized", "forbidden", "credential", "oauth", "authentication", "not logged in", "token expired"} {
			if strings.Contains(message, needle) {
				return nil, errVerificationAuth
			}
		}
		return nil, fmt.Errorf("%s verification command failed (check connectivity and datasource permissions)", command)
	}
	if stdout.exceeded {
		return nil, errors.New("verification response exceeds 1 MiB; narrow the query")
	}
	return stdout.data, nil
}

// Demo stacks use Grafana Cloud's standard datasource UIDs. Do not rely on
// datasource list type metadata, which gcx can return empty on Cloud stacks.
func verificationDatasource(signal string) string {
	switch signal {
	case "metrics":
		return "grafanacloud-prom"
	case "logs":
		return "grafanacloud-logs"
	case "traces":
		return "grafanacloud-traces"
	default:
		return ""
	}
}

// hasTelemetryResult accepts signal result records, never metadata or status alone.
func hasTelemetryResult(payload []byte, signal string) (bool, error) {
	var response struct {
		Status     string          `json:"status"`
		Error      json.RawMessage `json:"error"`
		Data       json.RawMessage `json:"data"`
		ResultType string          `json:"resultType"`
		Result     json.RawMessage `json:"result"`
		Traces     []struct {
			TraceID string `json:"traceID"`
		} `json:"traces"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		return false, errors.New("invalid telemetry query JSON")
	}
	if len(response.Error) > 0 && string(response.Error) != "null" {
		return false, errors.New("telemetry query returned an error")
	}
	if response.Status != "" && response.Status != "success" {
		return false, errors.New("telemetry query did not succeed")
	}
	if signal == "traces" {
		for _, trace := range response.Traces {
			if trace.TraceID != "" {
				return true, nil
			}
		}
		return false, nil
	}
	if len(response.Data) > 0 {
		var data struct {
			ResultType string          `json:"resultType"`
			Result     json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(response.Data, &data); err != nil {
			return false, errors.New("invalid telemetry data")
		}
		response.ResultType, response.Result = data.ResultType, data.Result
	}
	if len(response.Result) == 0 {
		return false, errors.New("telemetry response has no result records")
	}
	if signal == "logs" {
		if response.ResultType != "streams" {
			return false, errors.New("unexpected log result type")
		}
		var streams []struct {
			Values []json.RawMessage `json:"values"`
		}
		if err := json.Unmarshal(response.Result, &streams); err != nil {
			return false, errors.New("invalid log result records")
		}
		for _, stream := range streams {
			for _, entry := range stream.Values {
				var record struct {
					Line string `json:"line"`
				}
				if err := json.Unmarshal(entry, &record); err != nil {
					// Native Loki pairs remain supported alongside gcx's objects.
					var pair []json.RawMessage
					if json.Unmarshal(entry, &pair) != nil || len(pair) != 2 || json.Unmarshal(pair[1], &record.Line) != nil {
						return false, errors.New("invalid log entry")
					}
				}
				if strings.Contains(record.Line, "demo.compiler.probe") {
					return true, nil
				}
			}
		}
		return false, nil
	}
	var results []struct {
		Value  []json.RawMessage   `json:"value"`
		Values [][]json.RawMessage `json:"values"`
	}
	if err := json.Unmarshal(response.Result, &results); err != nil {
		return false, errors.New("invalid telemetry result records")
	}
	for _, result := range results {
		values := result.Values
		if len(result.Value) > 0 {
			values = append(values, result.Value)
		}
		for _, pair := range values {
			if len(pair) != 2 {
				continue
			}
			var value string
			if json.Unmarshal(pair[1], &value) != nil {
				continue
			}
			if signal == "metrics" && (response.ResultType == "vector" || response.ResultType == "matrix") {
				number, err := strconv.ParseFloat(value, 64)
				if err == nil && number > 0 && !math.IsInf(number, 0) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func telemetryQuery(signal, uid, service, runID string, since, until time.Time) []string {
	quotedService, quotedRun := strconv.Quote(service), strconv.Quote(runID)
	var expression string
	switch signal {
	case "metrics":
		expression = "demo_compiler_probe_total{demo_run_id=" + quotedRun + ",service_name=" + quotedService + "} > 0"
	case "logs":
		expression = "{service_name=" + quotedService + "} |= " + quotedRun + " |= \"demo.compiler.probe\""
	case "traces":
		expression = "{resource.service.name=" + quotedService + " && resource.demo.run.id=" + quotedRun + " && name=\"demo.compiler.probe\"}"
	}
	args := []string{signal, "query", "--datasource", uid, "--expr", expression, "--from", since.UTC().Format(time.RFC3339Nano), "--to", until.UTC().Format(time.RFC3339Nano), "-o", "json"}
	if signal == "metrics" {
		args = append(args, "--step", "1s")
	} else {
		args = append(args, "--limit", "1")
	}
	return args
}

// VerifyTelemetry uses only read queries in this session's isolated gcx context.
func (r *Runner) VerifyTelemetry(ctx context.Context, stack, runID string, services []string, since time.Time, progress func(string)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if runID == "" || len(services) == 0 || since.IsZero() || since.After(time.Now()) {
		return errors.New("current deployment identity, start time, and application services are required")
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	config, err := gcxtool.StackConfigPath(stack)
	if err != nil {
		return err
	}
	if _, err = os.Stat(config); err != nil {
		return errVerificationAuth
	}
	query := func(args ...string) ([]byte, error) {
		return verificationCommand(ctx, "", true, "gcx", append(args, "--config", config, "--context", stack)...)
	}
	// Validate the URL through the redacted view; never inspect credential files.
	view, err := query("config", "view", "--minify", "-o", "json")
	if err != nil {
		return err
	}
	var configView struct {
		Contexts map[string]struct {
			Stack string `json:"stack"`
		} `json:"contexts"`
		Stacks map[string]struct {
			Grafana struct {
				Server string `json:"server"`
			} `json:"grafana"`
		} `json:"stacks"`
	}
	if json.Unmarshal(view, &configView) != nil || strings.TrimRight(configView.Stacks[configView.Contexts[stack].Stack].Grafana.Server, "/") != "https://"+stack+".grafana.net" {
		return errors.New("verification context does not target this session's Grafana stack")
	}
	missing := map[string]map[string]bool{}
	for _, service := range services {
		missing[service] = map[string]bool{"metrics": true, "logs": true, "traces": true}
	}
	lastError := ""
	for {
		for _, service := range services {
			for _, signal := range []string{"metrics", "logs", "traces"} {
				if !missing[service][signal] {
					continue
				}
				if ctx.Err() != nil {
					break
				}
				payload, err := query(telemetryQuery(signal, verificationDatasource(signal), service, runID, since, time.Now())...)
				if errors.Is(err, errVerificationAuth) {
					return err
				}
				if err != nil {
					lastError = err.Error()
					continue
				}
				found, err := hasTelemetryResult(payload, signal)
				if err != nil {
					lastError = err.Error()
					continue
				}
				if found {
					delete(missing[service], signal)
				}
			}
		}
		var pending []string
		for _, service := range services {
			var signals []string
			for signal := range missing[service] {
				signals = append(signals, signal)
			}
			sort.Strings(signals)
			if len(signals) > 0 {
				pending = append(pending, service+": "+strings.Join(signals, ", "))
			}
		}
		if len(pending) == 0 {
			return nil
		}
		if progress != nil {
			progress("Waiting for current deployment telemetry: " + strings.Join(pending, "; "))
		}
		if err := verificationPause(ctx, 3*time.Second); err != nil {
			detail := ""
			if lastError != "" {
				detail = "; last query failure: " + lastError
			}
			return fmt.Errorf("telemetry verification incomplete (%s)%s: %w", strings.Join(pending, "; "), detail, err)
		}
	}
}
