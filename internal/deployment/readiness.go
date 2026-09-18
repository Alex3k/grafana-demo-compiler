package deployment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

type composeService struct {
	Service string
	State   string
	Health  string
}

func parseComposeServices(payload []byte) ([]composeService, error) {
	payload = bytes.TrimSpace(payload)
	var services []composeService
	if len(payload) == 0 {
		return services, nil
	}
	if payload[0] == '[' {
		err := json.Unmarshal(payload, &services)
		return services, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	for {
		var service composeService
		if err := decoder.Decode(&service); err != nil {
			if errors.Is(err, io.EOF) {
				return services, nil
			}
			return nil, errors.New("invalid Docker Compose service status")
		}
		services = append(services, service)
	}
}

func checkComposeReady(services []composeService, expected []string) (string, error) {
	required := map[string]bool{"alloy": false}
	for _, name := range expected {
		required[name] = true
	}
	seen := map[string]bool{}
	var pending []string
	for _, service := range services {
		state, health := strings.ToLower(service.State), strings.ToLower(service.Health)
		if state == "exited" || state == "dead" || health == "unhealthy" {
			return "", fmt.Errorf("service %s is %s (%s); inspect its Docker Compose status and repair it before retrying", service.Service, state, health)
		}
		needsHealth, needed := required[service.Service]
		if !needed {
			if state != "running" || (health != "" && health != "healthy") {
				pending = append(pending, service.Service)
			}
			continue
		}
		seen[service.Service] = true
		if needsHealth && state == "running" && health == "" {
			return "", fmt.Errorf("service %s has no healthcheck; add a working Docker Compose healthcheck before deploying", service.Service)
		}
		if state != "running" || (needsHealth && health != "healthy") || (health != "" && health != "healthy") {
			pending = append(pending, service.Service)
		}
	}
	for name := range required {
		if !seen[name] {
			pending = append(pending, name+" (missing)")
		}
	}
	return strings.Join(pending, ", "), nil
}

// WaitReady checks this deployment's containers without reading container logs.
func (r *Runner) WaitReady(ctx context.Context, root, project string, expected []string, progress func(string)) error {
	if len(expected) == 0 {
		return errors.New("no application services were selected for readiness verification")
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	last := "waiting for service status"
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("container readiness incomplete: %s: %w", last, err)
		}
		payload, err := verificationCommand(ctx, root, false, "docker", "compose", "--project-name", project, "ps", "--all", "--format", "json")
		if err != nil {
			return fmt.Errorf("check container readiness: %w", err)
		}
		services, err := parseComposeServices(payload)
		if err != nil {
			return err
		}
		last, err = checkComposeReady(services, expected)
		if err != nil {
			return err
		}
		if last == "" {
			return nil
		}
		if progress != nil {
			progress("Waiting for healthy containers: " + last)
		}
		if err := verificationPause(ctx, 2*time.Second); err != nil {
			return fmt.Errorf("container readiness incomplete: %s: %w", last, err)
		}
	}
}

func verificationPause(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
