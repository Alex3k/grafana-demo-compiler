package guard

import "testing"

func TestCheckDeploymentRequest(t *testing.T) {
	tests := []struct {
		name    string
		content string
		blocked bool
	}{
		{name: "blocks AWS deployment", content: "Deploy the application to AWS ECS", blocked: true},
		{name: "blocks remote target despite local comparison", content: "Deploy to AWS and compare it with the local version", blocked: true},
		{name: "blocks Kubernetes deployment", content: "Can you run this on Kubernetes?", blocked: true},
		{name: "allows cloud-shaped local simulation", content: "Simulate an AWS deployment locally with Docker Compose", blocked: false},
		{name: "allows local cloud-shaped runtime", content: "Run an AWS-like architecture locally with Docker Compose", blocked: false},
		{name: "allows Grafana Cloud operations", content: "Use gcx to provision the Grafana Cloud stack", blocked: false},
		{name: "allows architecture discussion", content: "The customer currently runs services in AWS", blocked: false},
		{name: "allows local deployment", content: "Deploy it locally with Docker Compose", blocked: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := CheckDeploymentRequest(test.content).Blocked; got != test.blocked {
				t.Fatalf("blocked = %v, want %v", got, test.blocked)
			}
		})
	}
}
