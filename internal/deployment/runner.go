package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Stack struct {
	URL          string
	OTLPEndpoint string
	InstanceID   string
}

type Runner struct{}

func New() *Runner { return &Runner{} }

func (r *Runner) Provision(ctx context.Context, organization, region, name, slug string, progress func(string)) (Stack, error) {
	if strings.TrimSpace(organization) == "" || strings.TrimSpace(region) == "" {
		return Stack{}, errors.New("Grafana Cloud organization and region are required")
	}
	if !validSlug(slug) {
		return Stack{}, errors.New("generated stack slug is invalid")
	}

	progress("Checking gcx Cloud authentication and organization access")
	stacks, err := run(ctx, "gcx", "cloud", "stacks", "list", "--org", organization, "-o", "json")
	if err != nil {
		return Stack{}, fmt.Errorf("gcx Cloud preflight failed: %w", err)
	}
	exists := jsonContainsString(stacks, slug)

	progress("Checking that the selected Grafana Cloud region is available")
	regions, err := run(ctx, "gcx", "cloud", "stacks", "list-regions", "-o", "json")
	if err != nil {
		return Stack{}, fmt.Errorf("list Grafana Cloud regions: %w", err)
	}
	if !jsonContainsString(regions, region) {
		return Stack{}, fmt.Errorf("Grafana Cloud region %q is not available", region)
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
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return Stack{}, fmt.Errorf("wait for Grafana Cloud stack: %w", errors.Join(err, lastErr))
		}
		payload, err := run(ctx, "gcx", "cloud", "stacks", "get", slug, "-o", "json")
		if err == nil {
			stack := parseStack(payload, slug)
			if stack.URL != "" && stack.OTLPEndpoint != "" && stack.InstanceID != "" {
				return stack, nil
			}
			lastErr = errors.New("stack exists but OTLP connection details are not ready")
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			continue
		case <-time.After(5 * time.Second):
		}
	}
}

func (r *Runner) StartLocal(ctx context.Context, root, endpoint, instanceID, token, deploymentID string, progress func(string)) error {
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

	progress("Writing protected local telemetry configuration")
	project := "democompiler" + shortAlnum(deploymentID, 10)
	content := strings.Join([]string{
		"GRAFANA_CLOUD_OTLP_ENDPOINT=" + endpoint,
		"GRAFANA_CLOUD_INSTANCE_ID=" + instanceID,
		"GRAFANA_CLOUD_API_KEY=" + token,
		"COMPOSE_PROJECT_NAME=" + project,
		"",
	}, "\n")
	envPath := filepath.Join(root, ".env")
	temporary := envPath + ".tmp"
	if err := os.WriteFile(temporary, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write local deployment configuration: %w", err)
	}
	if err := os.Rename(temporary, envPath); err != nil {
		return fmt.Errorf("activate local deployment configuration: %w", err)
	}

	progress("Building and starting the local Docker Compose services")
	if _, err := runIn(ctx, root, "docker", "compose", "up", "-d", "--build"); err != nil {
		return fmt.Errorf("start local Docker Compose application: %w", err)
	}

	progress("Checking local container health")
	output, err := runIn(ctx, root, "docker", "compose", "ps", "--format", "json")
	if err != nil {
		return fmt.Errorf("check local Docker Compose application: %w", err)
	}
	if strings.TrimSpace(string(output)) == "" {
		return errors.New("Docker Compose started no services")
	}
	return nil
}

func run(ctx context.Context, command string, args ...string) ([]byte, error) {
	path, err := executable(command)
	if err != nil {
		return nil, fmt.Errorf("%s is not installed", command)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "GCX_AGENT_MODE=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
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
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, commandError(err, output)
	}
	return output, nil
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
		URL:          firstString(value, "url", "stack_url", "stackUrl"),
		OTLPEndpoint: firstString(value, "otlp_url", "otlpUrl", "otlp_endpoint", "otlpEndpoint"),
		InstanceID:   firstScalar(value, "id", "instance_id", "instanceId"),
	}
	if stack.OTLPEndpoint != "" && !strings.HasSuffix(strings.TrimRight(stack.OTLPEndpoint, "/"), "/otlp") {
		stack.OTLPEndpoint = strings.TrimRight(stack.OTLPEndpoint, "/") + "/otlp"
	}
	if stack.URL == "" && slug != "" {
		stack.URL = "https://" + slug + ".grafana.net"
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
