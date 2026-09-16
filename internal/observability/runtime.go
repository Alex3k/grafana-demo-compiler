package observability

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/grafana/agento11y/go/agento11y"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
)

const (
	AgentName    = "grafana-demo-compiler"
	AgentVersion = "0.1.0"
)

var requiredEnvironment = []string{
	"AGENTO11Y_ENDPOINT",
	"AGENTO11Y_PROTOCOL",
	"AGENTO11Y_AUTH_MODE",
	"AGENTO11Y_AUTH_TENANT_ID",
	"AGENTO11Y_AUTH_TOKEN",
	"OTEL_EXPORTER_OTLP_ENDPOINT",
	"OTEL_EXPORTER_OTLP_HEADERS",
}

type Runtime struct {
	Client         *agento11y.Client
	tracerProvider *trace.TracerProvider
	meterProvider  *metric.MeterProvider
	missing        []string
	initError      error
}

func New(ctx context.Context) *Runtime {
	runtime := &Runtime{missing: missingEnvironment()}
	if len(runtime.missing) > 0 {
		return runtime
	}

	res, err := resource.New(ctx, resource.WithAttributes(
		attribute.String("service.name", AgentName),
		attribute.String("service.version", AgentVersion),
		attribute.String("deployment.environment.name", "local"),
	))
	if err != nil {
		runtime.initError = fmt.Errorf("create OTel resource: %w", err)
		return runtime
	}

	traceExporter, err := otlptracehttp.New(ctx)
	if err != nil {
		runtime.initError = fmt.Errorf("create trace exporter: %w", err)
		return runtime
	}
	runtime.tracerProvider = trace.NewTracerProvider(
		trace.WithBatcher(traceExporter),
		trace.WithResource(res),
	)
	otel.SetTracerProvider(runtime.tracerProvider)

	metricExporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		runtime.initError = fmt.Errorf("create metric exporter: %w", err)
		_ = runtime.tracerProvider.Shutdown(ctx)
		runtime.tracerProvider = nil
		return runtime
	}
	runtime.meterProvider = metric.NewMeterProvider(
		metric.WithReader(metric.NewPeriodicReader(metricExporter)),
		metric.WithResource(res),
	)
	otel.SetMeterProvider(runtime.meterProvider)

	redactInputs := true
	runtime.Client = agento11y.NewClient(agento11y.Config{
		AgentName:           AgentName,
		AgentVersion:        AgentVersion,
		TracerProvider:      runtime.tracerProvider,
		MeterProvider:       runtime.meterProvider,
		GenerationSanitizer: agento11y.NewSecretRedactionSanitizer(agento11y.SecretRedactionOptions{RedactInputMessages: &redactInputs}),
	})
	return runtime
}

func (r *Runtime) Configured() bool {
	return r != nil && r.Client != nil && r.initError == nil && len(r.missing) == 0
}

func (r *Runtime) Status() (string, string) {
	if r == nil {
		return "error", "observability runtime is unavailable"
	}
	if r.initError != nil {
		return "error", r.initError.Error()
	}
	if len(r.missing) > 0 {
		return "not_configured", "missing " + strings.Join(r.missing, ", ")
	}
	return "configured", "export credentials and endpoints are configured"
}

func (r *Runtime) RecordDeploymentGuard(ctx context.Context, sessionID, target string) {
	if r == nil || r.tracerProvider == nil {
		return
	}
	_, span := r.tracerProvider.Tracer(AgentName).Start(ctx, "deployment.guard")
	span.SetAttributes(
		attribute.String("demo.session.id", sessionID),
		attribute.String("guard.name", "local-application-deployment"),
		attribute.String("guard.outcome", "blocked"),
		attribute.String("deployment.target", target),
	)
	span.End()
}

func (r *Runtime) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	var failures []string
	if r.Client != nil {
		if err := r.Client.Shutdown(ctx); err != nil {
			failures = append(failures, "agento11y: "+err.Error())
		}
	}
	if r.tracerProvider != nil {
		if err := r.tracerProvider.Shutdown(ctx); err != nil {
			failures = append(failures, "traces: "+err.Error())
		}
	}
	if r.meterProvider != nil {
		if err := r.meterProvider.Shutdown(ctx); err != nil {
			failures = append(failures, "metrics: "+err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("shutdown observability: %s", strings.Join(failures, "; "))
	}
	return nil
}

func missingEnvironment() []string {
	missing := make([]string, 0)
	for _, key := range requiredEnvironment {
		if strings.TrimSpace(os.Getenv(key)) == "" {
			missing = append(missing, key)
		}
	}
	return missing
}
