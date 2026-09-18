package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Alex3k/grafana-demo-compiler/internal/telemetryconfig"
)

type Stack struct {
	URL                string
	OTLPEndpoint       string
	InstanceID         string
	PrometheusURL      string
	PrometheusUsername string
	LokiURL            string
	LokiUsername       string
}

type Runner struct {
	baseURL    string
	httpClient *http.Client
}

func New() *Runner {
	return &Runner{
		baseURL: "https://grafana.com",
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (r *Runner) Provision(ctx context.Context, region, name, slug string, progress func(string)) (Stack, error) {
	if strings.TrimSpace(region) == "" {
		return Stack{}, errors.New("Grafana Cloud region is required")
	}
	if !validSlug(slug) {
		return Stack{}, errors.New("generated stack slug is invalid")
	}

	progress("Checking gcx Cloud authentication and the selected region")
	regions, err := run(ctx, "gcx", "cloud", "stacks", "list-regions", "-o", "json")
	if err != nil {
		return Stack{}, fmt.Errorf("list Grafana Cloud regions: %w", err)
	}
	if !jsonContainsString(regions, region) {
		return Stack{}, fmt.Errorf("Grafana Cloud region %q is not available", region)
	}

	progress("Checking for this session's existing Grafana Cloud stack")
	_, getErr := run(ctx, "gcx", "cloud", "stacks", "get", slug, "-o", "json")
	exists := getErr == nil
	if getErr != nil && !isNotFound(getErr) {
		return Stack{}, fmt.Errorf("check Grafana Cloud stack: %w", getErr)
	}

	if exists {
		progress("Reusing the existing dedicated Grafana Cloud demo stack")
	} else {
		progress("Validating the stack request with gcx")
		createArgs := []string{"cloud", "stacks", "create", "--name", name, "--slug", slug, "--region", region, "--delete-protection", "-o", "json"}
		if _, err := run(ctx, "gcx", append(createArgs, "--dry-run")...); err != nil {
			return Stack{}, fmt.Errorf("validate Grafana Cloud stack: %w", err)
		}

		progress("Creating the dedicated Grafana Cloud demo stack")
		if _, err := run(ctx, "gcx", createArgs...); err != nil {
			return Stack{}, fmt.Errorf("create Grafana Cloud stack: %w", err)
		}
	}

	progress("Waiting for Grafana Cloud connection details")
	pollCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var lastErr error
	missing := "stack URL and instance ID"
	for {
		if err := pollCtx.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				return Stack{}, fmt.Errorf("Grafana Cloud stack did not provide %s within 5 minutes; check the stack in Grafana Cloud and retry", missing)
			}
			return Stack{}, fmt.Errorf("wait for Grafana Cloud stack: %w", errors.Join(err, lastErr))
		}
		payload, err := run(pollCtx, "gcx", "cloud", "stacks", "get", slug, "-o", "json")
		if err == nil {
			stack := parseStack(payload, slug)
			if stack.URL != "" && stack.InstanceID != "" {
				return stack, nil
			}
			missing = missingStackFields(stack)
			lastErr = fmt.Errorf("stack exists but %s are not ready", missing)
		} else {
			lastErr = err
		}
		select {
		case <-pollCtx.Done():
			continue
		case <-time.After(5 * time.Second):
		}
	}
}

func missingStackFields(stack Stack) string {
	var fields []string
	if stack.URL == "" {
		fields = append(fields, "stack URL")
	}
	if stack.InstanceID == "" {
		fields = append(fields, "instance ID")
	}
	return strings.Join(fields, " and ")
}

func (r *Runner) ResolveOTLP(ctx context.Context, slug, token string) (string, error) {
	if !validSlug(slug) {
		return "", errors.New("Grafana Cloud stack slug is invalid")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", errors.New("Grafana Cloud access token is required")
	}
	if strings.ContainsAny(token, "\r\n") {
		return "", errors.New("Grafana Cloud access token is invalid")
	}

	endpoint := strings.TrimRight(r.baseURL, "/") + "/api/instances/" + url.PathEscape(slug) + "/connections"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("create Grafana Cloud connections request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	response, err := r.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("get Grafana Cloud connections: %w", err)
	}
	defer response.Body.Close()

	const maxResponseBytes = 64 * 1024
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return "", fmt.Errorf("read Grafana Cloud connections: %w", err)
	}
	if len(body) > maxResponseBytes {
		return "", errors.New("Grafana Cloud connections response is too large")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		detail := strings.TrimSpace(string(body))
		detail = strings.ReplaceAll(detail, token, "[redacted]")
		if len(detail) > 1000 {
			detail = detail[:1000]
		}
		if detail == "" {
			return "", fmt.Errorf("get Grafana Cloud connections: HTTP %s", response.Status)
		}
		return "", fmt.Errorf("get Grafana Cloud connections: HTTP %s: %s", response.Status, detail)
	}

	var connections struct {
		OTLPHTTPURL string `json:"otlpHttpUrl"`
	}
	if err := json.Unmarshal(body, &connections); err != nil {
		return "", fmt.Errorf("parse Grafana Cloud connections: %w", err)
	}
	if strings.TrimSpace(connections.OTLPHTTPURL) == "" {
		return "", errors.New("Grafana Cloud connections response did not include otlpHttpUrl")
	}
	return validateOTLPEndpoint(connections.OTLPHTTPURL)
}

func validateOTLPEndpoint(endpoint string) (string, error) {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse Grafana Cloud OTLP endpoint: %w", err)
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
		!strings.HasPrefix(host, "otlp-gateway-") || !strings.HasSuffix(host, ".grafana.net") {
		return "", errors.New("Grafana Cloud returned an invalid OTLP endpoint")
	}
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	if path != "" && path != "/otlp" {
		return "", errors.New("Grafana Cloud returned an invalid OTLP endpoint path")
	}
	parsed.Path = "/otlp"
	parsed.RawPath = ""
	return parsed.String(), nil
}

func isNotFound(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "not found") || strings.Contains(message, "404")
}

func (r *Runner) StartLocal(ctx context.Context, root, stackSlug, endpoint, instanceID, token, projectKey, runID string, progress func(string)) ([]string, error) {
	var services []string
	err := r.startLocal(ctx, root, stackSlug, endpoint, instanceID, token, projectKey, runID, &services, progress)
	return services, err
}

func (r *Runner) PreflightLocal(_ context.Context, root string) error {
	return telemetryconfig.ValidateFoundation(root)
}

func (r *Runner) startLocal(ctx context.Context, root, stackSlug, endpoint, instanceID, token, projectKey, runID string, services *[]string, progress func(string)) error {
	if strings.TrimSpace(root) == "" || !filepath.IsAbs(root) {
		return errors.New("prototype output path must be absolute")
	}
	if strings.TrimSpace(endpoint) == "" || strings.TrimSpace(instanceID) == "" {
		return errors.New("Grafana Cloud OTLP connection details are incomplete")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("Grafana Cloud OTLP access-policy token is required")
	}
	if strings.ContainsAny(token, "\r\n") {
		return errors.New("Grafana Cloud OTLP access-policy token is invalid")
	}
	if err := ensureDockerignore(root); err != nil {
		return err
	}
	if err := telemetryconfig.ValidateFoundation(root); err != nil {
		return err
	}
	if err := telemetryconfig.Validate(root); err != nil {
		return fmt.Errorf("validate telemetry configuration: %w", err)
	}
	stackPayload, err := run(ctx, "gcx", "cloud", "stacks", "get", stackSlug, "-o", "json")
	if err != nil {
		return fmt.Errorf("read Grafana Cloud metrics connection details: %w", err)
	}
	stack := parseStack(stackPayload, stackSlug)
	if stack.PrometheusURL == "" || stack.PrometheusUsername == "" {
		return errors.New("Grafana Cloud metrics connection details are incomplete")
	}

	progress("Writing protected local telemetry configuration")
	project := "democompiler" + shortAlnum(projectKey, 10)
	environment := loadEnvironmentDefaults(root)
	for key, value := range telemetryconfig.Environment(telemetryconfig.Connection{
		OTLPEndpoint: endpoint, InstanceID: instanceID, Token: token,
		PrometheusURL: stack.PrometheusURL, PrometheusUsername: stack.PrometheusUsername,
		LokiURL: stack.LokiURL, LokiUsername: stack.LokiUsername,
	}) {
		environment[key] = value
	}
	if err := telemetryconfig.ValidateEnvironment(root, environment); err != nil {
		return fmt.Errorf("resolve telemetry configuration: %w", err)
	}
	environment["COMPOSE_PROJECT_NAME"] = project
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, key+"="+environment[key])
	}
	content := strings.Join(lines, "\n") + "\n"
	envPath := filepath.Join(root, ".env")
	temporary, err := os.CreateTemp(root, ".env.tmp-")
	if err != nil {
		return fmt.Errorf("write local deployment configuration: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.WriteString(content); err != nil {
		temporary.Close()
		return fmt.Errorf("write local deployment configuration: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close local deployment configuration: %w", err)
	}
	if err := os.Rename(temporaryPath, envPath); err != nil {
		return fmt.Errorf("activate local deployment configuration: %w", err)
	}

	configuration, err := runIn(ctx, root, "docker", "compose", "config", "--format", "json")
	if err != nil {
		return fmt.Errorf("validate local Docker Compose application: %w", redactError(err, token))
	}
	if composeBuildUsesSecret(configuration, token) {
		return errors.New("Docker Compose build arguments must not contain the Grafana Cloud token")
	}
	var resolved map[string]any
	if err := json.Unmarshal(configuration, &resolved); err != nil {
		return err
	}
	resolvedServices, _ := resolved["services"].(map[string]any)
	alloy, _ := resolvedServices["alloy"].(map[string]any)
	if alloy == nil {
		return errors.New("compiler telemetry requires the Compose service alloy")
	}
	cloudEnv := map[string]any{}
	for key := range telemetryconfig.Environment(telemetryconfig.Connection{}) {
		cloudEnv[key] = environment[key]
	}
	alloy["environment"] = cloudEnv
	configuration, err = json.Marshal(resolved)
	if err != nil {
		return err
	}
	configuration, *services, err = telemetryconfig.WireFoundation(configuration, root, project, runID)
	if err != nil {
		return fmt.Errorf("wire compiler telemetry: %w", err)
	}
	if err := telemetryconfig.ValidateResolved(root, configuration); err != nil {
		return fmt.Errorf("validate container telemetry environment: %w", err)
	}
	configuration, err = dynamicHostPorts(configuration)
	if err != nil {
		return fmt.Errorf("configure local Docker Compose ports: %w", redactError(err, token))
	}
	composeFile, err := os.CreateTemp(root, ".env.tmp-compose-*.json")
	if err != nil {
		return fmt.Errorf("create local Docker Compose configuration: %w", err)
	}
	defer os.Remove(composeFile.Name())
	if _, err := composeFile.Write(configuration); err != nil {
		composeFile.Close()
		return fmt.Errorf("write local Docker Compose configuration: %w", err)
	}
	if err := composeFile.Close(); err != nil {
		return fmt.Errorf("close local Docker Compose configuration: %w", err)
	}

	progress("Building and starting the local Docker Compose services")
	if _, err := runIn(ctx, root, "docker", "compose", "--project-directory", root, "-f", composeFile.Name(), "up", "-d", "--build"); err != nil {
		return fmt.Errorf("start local Docker Compose application: %w", redactError(err, token))
	}

	progress("Containers started; waiting for application readiness")
	return r.WaitReady(ctx, root, project, *services, progress)
}

func (r *Runner) StopLocal(ctx context.Context, root, projectKey string, progress func(string)) error {
	if strings.TrimSpace(root) == "" || !filepath.IsAbs(root) {
		return errors.New("prototype output path must be absolute")
	}

	projectID := shortAlnum(projectKey, 10)
	if projectID == "" {
		return errors.New("local Docker Compose project key is invalid")
	}
	project := "democompiler" + projectID
	progress("Stopping the previous local Docker Compose application")
	if _, err := runIn(ctx, root, "docker", "compose", "--project-name", project, "down", "--remove-orphans"); err != nil {
		return fmt.Errorf("stop local Docker Compose application: %w", err)
	}
	return nil
}

func loadEnvironmentDefaults(root string) map[string]string {
	result := make(map[string]string)
	content, err := os.ReadFile(filepath.Join(root, ".env.example"))
	if err != nil {
		return result
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		key = strings.TrimSpace(key)
		if !found || strings.HasPrefix(key, "GRAFANA_") || !validEnvKey(key) || strings.Contains(value, "<") {
			continue
		}
		result[key] = value
	}
	return result
}

func validEnvKey(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		if index == 0 && character >= '0' && character <= '9' {
			return false
		}
		if (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func ensureDockerignore(root string) error {
	path := filepath.Join(root, ".dockerignore")
	content, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read Docker build exclusions: %w", err)
	}
	normalized := strings.TrimRight(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
	lines := []string{}
	if normalized != "" {
		lines = strings.Split(normalized, "\n")
	}
	present := make(map[string]bool, len(lines))
	for _, line := range lines {
		present[strings.TrimSpace(line)] = true
	}
	for _, required := range []string{".env", ".env.tmp*"} {
		if !present[required] {
			lines = append(lines, required)
		}
	}
	updated := strings.Join(lines, "\n") + "\n"
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, []byte(updated), 0o600); err != nil {
		return fmt.Errorf("write Docker build exclusions: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("activate Docker build exclusions: %w", err)
	}
	return nil
}

func composeBuildUsesSecret(configuration []byte, secret string) bool {
	if secret == "" {
		return false
	}
	var compose struct {
		Services map[string]struct {
			Build struct {
				Args map[string]any `json:"args"`
			} `json:"build"`
		} `json:"services"`
	}
	if json.Unmarshal(configuration, &compose) != nil {
		return true
	}
	for _, service := range compose.Services {
		for _, value := range service.Build.Args {
			if strings.Contains(fmt.Sprint(value), secret) {
				return true
			}
		}
	}
	return false
}

func redactError(err error, secret string) error {
	if err == nil || secret == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), secret, "[redacted]"))
}

func run(ctx context.Context, command string, args ...string) ([]byte, error) {
	path, err := executable(command)
	if err != nil {
		return nil, fmt.Errorf("%s is not installed", command)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "GCX_AGENT_MODE=1")
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			if len(output) > 0 {
				output = append(output, '\n')
			}
			output = append(output, exitErr.Stderr...)
		}
		return nil, commandError(err, output)
	}
	return output, nil
}

func runIn(ctx context.Context, root, command string, args ...string) ([]byte, error) {
	path, err := executable(command)
	if err != nil {
		return nil, fmt.Errorf("%s is not installed", command)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = root
	if command == "docker" {
		cmd.Env = dockerCommandEnv()
	}
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			if len(output) > 0 {
				output = append(output, '\n')
			}
			output = append(output, exitErr.Stderr...)
		}
		return nil, commandError(err, output)
	}
	return output, nil
}

func dockerCommandEnv() []string {
	environment := os.Environ()
	path := os.Getenv("PATH")
	prefix := strings.Join([]string{
		"/usr/local/bin",
		"/opt/homebrew/bin",
		"/Applications/Docker.app/Contents/Resources/bin",
	}, string(os.PathListSeparator))
	if path != "" {
		path = prefix + string(os.PathListSeparator) + path
	} else {
		path = prefix
	}
	for index, item := range environment {
		if strings.HasPrefix(item, "PATH=") {
			environment[index] = "PATH=" + path
			return environment
		}
	}
	return append(environment, "PATH="+path)
}

func executable(command string) (string, error) {
	if path, err := exec.LookPath(command); err == nil {
		return path, nil
	}
	for _, path := range map[string][]string{
		"gcx":    {"/opt/homebrew/bin/gcx", "/usr/local/bin/gcx"},
		"docker": {"/usr/local/bin/docker", "/opt/homebrew/bin/docker"},
	}[command] {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s is not installed", command)
}

func commandError(err error, output []byte) error {
	detail := strings.TrimSpace(string(output))
	if len(detail) > 2000 {
		detail = detail[len(detail)-2000:]
	}
	if detail == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, detail)
}

func jsonContainsString(payload []byte, target string) bool {
	var value any
	if json.Unmarshal(payload, &value) != nil {
		return false
	}
	return containsString(value, target)
}

func containsString(value any, target string) bool {
	switch typed := value.(type) {
	case string:
		return typed == target
	case []any:
		for _, item := range typed {
			if containsString(item, target) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if containsString(item, target) {
				return true
			}
		}
	}
	return false
}

func parseStack(payload []byte, slug string) Stack {
	var value map[string]any
	if json.Unmarshal(payload, &value) != nil {
		return Stack{}
	}
	stack := Stack{
		URL:                firstString(value, "url", "stack_url", "stackUrl"),
		OTLPEndpoint:       firstString(value, "otlp_url", "otlpUrl", "otlpHttpUrl", "otlp_endpoint", "otlpEndpoint"),
		InstanceID:         firstScalar(value, "id", "instance_id", "instanceId"),
		PrometheusURL:      firstString(value, "hmInstancePromUrl"),
		PrometheusUsername: firstScalar(value, "hmInstancePromId"),
		LokiURL:            firstString(value, "hlInstanceUrl"),
		LokiUsername:       firstScalar(value, "hlInstanceId"),
	}
	if stack.OTLPEndpoint != "" {
		stack.OTLPEndpoint, _ = validateOTLPEndpoint(stack.OTLPEndpoint)
	}
	if stack.URL == "" && slug != "" {
		stack.URL = "https://" + slug + ".grafana.net"
	}
	if stack.LokiURL != "" {
		parsed, err := url.Parse(strings.TrimRight(stack.LokiURL, "/"))
		if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
			!strings.HasPrefix(strings.ToLower(parsed.Hostname()), "logs-") || !strings.HasSuffix(strings.ToLower(parsed.Hostname()), ".grafana.net") {
			stack.LokiURL = ""
		} else {
			parsed.Path = strings.TrimSuffix(strings.TrimRight(parsed.Path, "/"), "/loki/api/v1/push") + "/loki/api/v1/push"
			stack.LokiURL = parsed.String()
		}
	}
	if stack.PrometheusURL != "" {
		parsed, err := url.Parse(strings.TrimRight(stack.PrometheusURL, "/"))
		if err != nil {
			stack.PrometheusURL = ""
			return stack
		}
		host := strings.ToLower(parsed.Hostname())
		if parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
			!strings.HasPrefix(host, "prometheus-") || !strings.HasSuffix(host, ".grafana.net") {
			stack.PrometheusURL = ""
		} else {
			parsed.Path = strings.TrimRight(parsed.Path, "/") + "/api/prom/push"
			stack.PrometheusURL = parsed.String()
		}
	}
	return stack
}

func firstString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if candidate, ok := value[key].(string); ok && candidate != "" {
			return candidate
		}
	}
	return ""
}

func firstScalar(value map[string]any, keys ...string) string {
	for _, key := range keys {
		switch candidate := value[key].(type) {
		case string:
			if candidate != "" {
				return candidate
			}
		case float64:
			return strconv.FormatInt(int64(candidate), 10)
		}
	}
	return ""
}

func validSlug(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < 'a' || character > 'z' {
			if character < '0' || character > '9' {
				return false
			}
		}
	}
	return true
}

func shortAlnum(value string, limit int) string {
	var result strings.Builder
	for _, character := range strings.ToLower(value) {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') {
			result.WriteRune(character)
			if result.Len() == limit {
				break
			}
		}
	}
	return result.String()
}
