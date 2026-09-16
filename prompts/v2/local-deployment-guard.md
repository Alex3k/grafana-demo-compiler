# Local deployment guard contract

This is a deterministic application guard, not an LLM prompt.

## Purpose

Prevent Demo Compiler from deploying an application anywhere except the local
Docker Compose runtime while allowing required Grafana Cloud operations through
`gcx`.

## Decision rules

Allow:

- building local application artifacts;
- Docker and Docker Compose operations on the user's machine;
- local ports and loopback access;
- `gcx` creation and management of the per-session Grafana Cloud stack;
- `gcx` creation, update, inspection, and verification of Grafana resources;
- Alloy exporting local application telemetry to the per-session stack;
- compiler Agent Observability export to the central operations stack.

Block:

- application deployment to AWS, Azure, GCP, or another CSP;
- application deployment to Kubernetes, ECS, Lambda, Cloud Run, virtual
  machines, remote Docker hosts, or any other non-local runtime;
- generated deployment commands or infrastructure manifests that would perform
  those remote application deployments.

Clarify before execution when the requested target is absent or ambiguous.
Grafana Cloud, `gcx`, remote telemetry export, or a cloud-shaped scenario do not
by themselves mean the user requested remote application deployment.

## Blocked-request response

When blocked, record the guard outcome and tell the human:

1. which application deployment target was blocked;
2. that the MVP supports local Docker Compose only;
3. that the requested architecture or failure mode can be modelled locally when
   feasible;
4. that required Grafana Cloud operations through `gcx` remain supported.

The guard must run when interpreting a deployment request and again immediately
before a runtime or deployment tool executes. Prompt compliance is not a
substitute for this check.
