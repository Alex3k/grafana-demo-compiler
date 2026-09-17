package chat

import "testing"

func TestGrafanaResourceOnly(t *testing.T) {
	for _, tt := range []struct {
		name  string
		files []string
		want  bool
	}{
		{"dashboard", []string{"dashboards/overview.json"}, true},
		{"legacy single dashboard", []string{"grafana/dashboard.json"}, true},
		{"datasource", []string{"datasources/cloud.yaml"}, true},
		{"grafana resources", []string{"grafana/dashboards/overview.json", "grafana/alerts/errors.yaml", "grafana/slos/availability.yml"}, true},
		{"alerts and slos", []string{"alerts/errors.yaml", "slos/availability.json"}, true},
		{"normalized manifest", []string{"./dashboards/overview.json"}, true},
		{"mixed application and resource", []string{"app/main.go", "dashboards/overview.json"}, false},
		{"application code", []string{"app/dashboard.go"}, false},
		{"resource generator code", []string{"grafana/dashboards/main.go"}, false},
		{"instrumentation config", []string{"grafana/alloy.yaml"}, false},
		{"ambiguous config", []string{"config.json"}, false},
		{"similar directory", []string{"dashboards-app/config.json"}, false},
		{"outside resource directory", []string{"dashboards/../app/config.json"}, false},
		{"empty", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := grafanaResourceOnly(tt.files); got != tt.want {
				t.Fatalf("grafanaResourceOnly(%q) = %v, want %v", tt.files, got, tt.want)
			}
		})
	}
}
