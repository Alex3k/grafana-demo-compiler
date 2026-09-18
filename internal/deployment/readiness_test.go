package deployment

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestComposeReadiness(t *testing.T) {
	for _, tt := range []struct{ name, payload, pending, failure string }{
		{"array", `[{"Service":"api","State":"running","Health":"healthy"},{"Service":"alloy","State":"running"}]`, "", ""},
		{"ndjson", "{\"Service\":\"api\",\"State\":\"running\",\"Health\":\"healthy\"}\n{\"Service\":\"alloy\",\"State\":\"running\"}", "", ""},
		{"missing", `[{"Service":"alloy","State":"running"}]`, "api (missing)", ""},
		{"no healthcheck", `[{"Service":"api","State":"running"}]`, "", "no healthcheck"},
		{"starting", `[{"Service":"api","State":"running","Health":"starting"},{"Service":"alloy","State":"running"}]`, "api", ""},
		{"unhealthy dependency", `[{"Service":"db","State":"running","Health":"unhealthy"}]`, "", "db"},
		{"starting dependency", `[{"Service":"api","State":"running","Health":"healthy"},{"Service":"alloy","State":"running"},{"Service":"db","State":"running","Health":"starting"}]`, "db", ""},
		{"exited", `[{"Service":"api","State":"exited"}]`, "", "api"},
		{"alloy health", `[{"Service":"api","State":"running","Health":"healthy"},{"Service":"alloy","State":"running","Health":"starting"}]`, "alloy", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			services, err := parseComposeServices([]byte(tt.payload))
			if err != nil {
				t.Fatal(err)
			}
			pending, err := checkComposeReady(services, []string{"api"})
			if tt.failure != "" {
				if err == nil || !strings.Contains(err.Error(), tt.failure) {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err != nil || pending != tt.pending {
				t.Fatalf("pending %q error %v", pending, err)
			}
		})
	}
}

func TestWaitReadyCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := New().WaitReady(ctx, t.TempDir(), "demo", []string{"api"}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestMalformedComposeStatus(t *testing.T) {
	if _, err := parseComposeServices([]byte(`{"Service":`)); err == nil {
		t.Fatal("accepted malformed response")
	}
}
