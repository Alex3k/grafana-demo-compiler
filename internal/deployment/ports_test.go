package deployment

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestDynamicHostPorts(t *testing.T) {
	input := `{"name":"demo","services":{"app":{"image":"app","ports":[{"target":8080,"published":"8080","protocol":"tcp","host_ip":"0.0.0.0"},{"target":8125,"protocol":"udp"}],"environment":{"BACKEND":"http://backend:9090"}},"backend":{"image":"backend","expose":["9090"]}},"networks":{"default":{}}}`
	output, err := dynamicHostPorts([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	expected := strings.Replace(input, `"published":"8080"`, `"published":"0"`, 1)
	expected = strings.Replace(expected, `"host_ip":"0.0.0.0"`, `"host_ip":"127.0.0.1"`, 1)
	expected = strings.Replace(expected, `"target":8125,"protocol":"udp"`, `"target":8125,"protocol":"udp","published":"0","host_ip":"127.0.0.1"`, 1)
	var got, want any
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(expected), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected config: %s", output)
	}
}

func TestDynamicHostPortsWithoutPorts(t *testing.T) {
	input := `{"services":{"worker":{"image":"worker","ports":[]},"db":{"image":"db"}}}`
	output, err := dynamicHostPorts([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	json.Unmarshal(output, &got)
	json.Unmarshal([]byte(input), &want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("configuration without ports changed: %s", output)
	}
}

func TestDynamicHostPortsRejectsHostNetworking(t *testing.T) {
	_, err := dynamicHostPorts([]byte(`{"services":{"app":{"network_mode":"host"}}}`))
	if err == nil || !strings.Contains(err.Error(), "host networking") {
		t.Fatalf("expected host networking error, got %v", err)
	}
}

func TestDynamicHostPortsPreservesResolvedDollars(t *testing.T) {
	input := `{"services":{"app":{"command":["sh","-c","echo $$VALUE"],"entrypoint":["sh","-c","echo $$ENTRY"],"healthcheck":{"test":["CMD-SHELL","echo $$CHECK"]},"environment":{"PASSWORD":"pa$$word"}}}}`
	output, err := dynamicHostPorts([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	json.Unmarshal(output, &got)
	json.Unmarshal([]byte(input), &want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved dollar escaping changed: %s", output)
	}
}
