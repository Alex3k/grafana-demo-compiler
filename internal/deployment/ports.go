package deployment

import (
	"encoding/json"
	"errors"
	"fmt"
)

// dynamicHostPorts rewrites normalized Compose configuration for local deployment.
// Container ports and service connections stay unchanged; Docker assigns host ports.
func dynamicHostPorts(configuration []byte) ([]byte, error) {
	var compose map[string]any
	if err := json.Unmarshal(configuration, &compose); err != nil {
		return nil, errors.New("invalid resolved Docker Compose configuration")
	}
	services, ok := compose["services"].(map[string]any)
	if !ok {
		return nil, errors.New("resolved Docker Compose configuration has no services")
	}
	for name, value := range services {
		service, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid configuration for service %q", name)
		}
		if service["network_mode"] == "host" {
			return nil, fmt.Errorf("service %q uses host networking, which cannot assign dynamic host ports", name)
		}
		if service["ports"] == nil {
			continue
		}
		ports, ok := service["ports"].([]any)
		if !ok {
			return nil, fmt.Errorf("invalid resolved ports for service %q", name)
		}
		for _, value := range ports {
			port, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid resolved port for service %q", name)
			}
			port["published"] = "0"
			port["host_ip"] = "127.0.0.1"
		}
	}
	// Compose config output already escapes literal dollars for replay.
	return json.Marshal(compose)
}
