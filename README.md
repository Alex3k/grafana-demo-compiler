# Grafana Demo Compiler

Grafana Demo Compiler is a collaborative, local-first assistant for designing,
building, running, and validating short, story-led Grafana demos.

The MVP generates Go application services backed by MySQL and runs them locally
with Docker Compose. Remote deployment targets are unsupported and blocked.
Grafana Alloy sends demo telemetry to a dedicated Grafana Cloud stack that
`gcx` creates and manages for each demo session.

The compiler sends its own agent telemetry to a separately provided central
Grafana operations stack for Agent Observability.

Development changes are made through pull requests. The initial repository
bootstrap is the only direct commit to `main`.
