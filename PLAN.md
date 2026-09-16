# Grafana Demo Compiler MVP Plan

## Product outcome

Grafana Demo Compiler is a persistent, collaborative assistant that helps a
human design, generate, run, and validate a story-led Grafana demo. The demo
must be runnable locally within five minutes and presentable in no more than ten
minutes.

The narrative is the source of truth. Generated services, telemetry, Grafana
resources, scenarios, and presenter material exist to prove the outcome agreed
with the user.

## MVP boundaries

### Included

- A Go backend, React UI, and SQLite persistence for the compiler.
- Persistent single-user demo sessions and revision history.
- Collaborative discovery with targeted clarification questions.
- At least three generated Go application services.
- MySQL for generated applications.
- Local execution with Docker Compose.
- Grafana Alloy for application telemetry.
- A dedicated Grafana Cloud stack for each demo session, created and managed
  through `gcx`.
- User-requested Grafana resources, with optional recommendations from the
  assistant.
- A deterministic demo scenario, recovery path, and reset mechanism.
- A presenter runbook for a demo of no more than ten minutes.
- Verification of telemetry and Grafana resources through `gcx`.
- Agent telemetry sent to the central Grafana operations stack at
  `https://democompiler.grafana.net/` through the `democompiler` gcx context.
- One evaluation: whether the result meets the approved demo requirements.
- One guard: application deployment is restricted to the local machine.

### Excluded

- Application deployment to AWS, Azure, GCP, Kubernetes, or any other remote
  environment.
- Generated application services in languages other than Go.
- Multi-user collaboration and role-based access control.
- Continuous integration requirements.
- A large blueprint marketplace.
- Unapproved or autonomous Grafana mutations.

## Product workflow

1. Discuss intent.
2. Identify material gaps and ask targeted questions.
3. Propose a structured specification, narrative, and architecture.
4. Let the human accept, reject, or modify the proposal.
5. Approve the revision for generation.
6. Generate the local application and Grafana resource manifests.
7. Create the session's Grafana Cloud stack through `gcx`.
8. Run the application locally with Docker Compose.
9. Verify telemetry and create the approved Grafana resources through `gcx`.
10. Rehearse and validate the completed demo.
11. Create another revision when the user wants to iterate.

The assistant must ask whenever a material requirement has multiple reasonable
interpretations. It must not silently choose the domain, desired outcome,
failure scenario, telemetry, or Grafana resource scope.

## Session and revision model

Each session stores:

- Conversation history.
- Current structured specification.
- Active revision.
- Grafana Cloud stack and `gcx` context details.
- Generated artifacts.
- Runtime history.
- Approval and validation evidence.

Revisions become immutable once approved. Restoring an earlier revision creates
a new draft and does not modify Grafana until the human approves a new
reconciliation plan.

The user-visible states are:

```text
Draft -> Ready -> Generated -> Running -> Verified
```

## Demo specification

Every revision records:

- Audience and technical depth.
- Company, industry, branding, and terminology.
- Desired audience outcome.
- Business stakes and demo-to-win message.
- Required proof points.
- Domain-specific user journey.
- Architecture and service responsibilities.
- Telemetry requirements.
- Requested, recommended, and excluded Grafana resources.
- Scenario trigger, recovery, and reset behavior.
- Timed narrative and presenter flow.
- Acceptance criteria.
- Open questions and confirmed decisions.

A revision cannot become `Ready` while material questions remain unresolved.

## Generated demo contract

Every generated demo contains:

- At least three interacting Go services.
- MySQL schema and seed data.
- Health endpoints.
- Structured logs, metrics, and distributed traces.
- Grafana Alloy.
- Dockerfiles and Docker Compose.
- Realistic traffic or sensor simulation.
- Repeatable baseline, trigger, recovery, and reset controls.

Manufacturing and IoT will provide the initial development fixture. This is not
a domain imposed on users; the actual demo must follow the approved user intent.

## Grafana resource contract

The approved specification separates Grafana artifacts into:

```yaml
grafanaArtifacts:
  requested: []
  recommended: []
  excluded: []
```

- Explicit user requests are authoritative.
- Recommendations are optional and require approval.
- There is no fixed dashboard, SLO, or alert count.
- When no scope is supplied, the assistant proposes the smallest
  story-complete set and asks for approval.
- Every resource must support a narrative beat or proof point.
- `gcx` is responsible for stack creation, resource discovery, validation,
  dry-run, mutation, reconciliation, and verification.
- Grafana changes are previewed before approval.

## Local-only deployment guard

The only supported application deployment target is:

```yaml
deployment:
  target: local
  runtime: docker-compose
```

If a user requests deployment to a cloud provider or another remote target, the
compiler blocks the deployment, explains the MVP boundary, and offers to adapt
the requested architecture into a local simulation. The guard is enforced both
when interpreting the request and immediately before running a deployment tool.

## Agent Observability

The compiler sends its conversations, generations, tool activity, latency,
errors, workflow stage, and guard outcomes to the central Grafana operations
stack at `https://democompiler.grafana.net/` through the `democompiler` gcx
context.

One milestone evaluation answers:

> Does the proposed or completed demo satisfy the approved demo requirements?

It returns whether the requirements are met, a short explanation, and any
missing or contradicted requirements. It runs against the completed plan and
the completed validated demo, not every clarification turn.

## Incremental delivery sequence

### Step 1: Persistent conversational shell

Deliver:

- Go backend and React UI.
- SQLite session persistence.
- Create, list, and resume sessions.
- Streamed responses and visible activity updates.
- Agent Observability telemetry to the central operations stack.

Meaningful outcome: a user can hold and resume a collaborative conversation.

### Step 2: Specification and narrative

Deliver:

- A living structured specification.
- Targeted clarification questions.
- A narrative of no more than ten minutes.
- An architecture proposal.
- Requested, recommended, and excluded Grafana resources.
- Plan approval and transition from `Draft` to `Ready`.
- Requirement evaluation on the completed plan.

Meaningful outcome: a user can collaboratively produce and approve a complete
demo design.

### Step 3: Local project generation

Deliver:

- At least three generated Go services.
- MySQL.
- Alloy configuration.
- Dockerfiles and Docker Compose.
- Seed data, health endpoints, and an artifact manifest.
- Static validation of the generated project.
- The local-only deployment guard.

Meaningful outcome: an approved plan becomes a complete, inspectable local
project.

### Step 4: Local execution

Deliver:

- Build, start, stop, and restart actions.
- Health and dependency checks.
- Streamed build and container progress.
- Runtime status and actionable failures.
- A five-minute target from approved revision to usable local demo.

Meaningful outcome: the user can run the generated demo locally.

### Step 5: Grafana Cloud stack and telemetry

Deliver:

- Creation of a dedicated Grafana Cloud stack through `gcx`.
- Storage of the session's stack and context identifiers.
- Alloy delivery of metrics, logs, and traces.
- `gcx` verification that expected telemetry has arrived.

Meaningful outcome: the local application is observable in its own stack.

### Step 6: User-driven Grafana artifacts

Deliver:

- Generation of the approved resource set.
- `gcx` validation and dry-run.
- A human-readable change preview and approval.
- Resource creation and read-back through `gcx`.

Meaningful outcome: the user receives a story-specific Grafana experience.

### Step 7: Scenario and presenter experience

Deliver:

- Realistic traffic or sensor generation.
- Deterministic trigger, recovery, and reset controls.
- A timed presenter runbook.
- Talking points, screen actions, transitions, and expected evidence.

Meaningful outcome: the generated environment becomes a presentable demo.

### Step 8: End-to-end verification

Deliver:

- Application journey and scenario validation.
- Metrics, logs, and traces verification through `gcx`.
- Grafana resource verification.
- Ten-minute narrative validation.
- Final requirement evaluation.
- An evidence-backed validation report.

Meaningful outcome: the user has evidence that the demo satisfies the approved
intent.

### Step 9: Revision and reconciliation

Deliver:

- Immutable revision history and comparison.
- Restore as a new draft.
- Grafana reconciliation preview.
- Fresh approval before changing remote resources.

Meaningful outcome: the user can safely iterate over multiple sessions and
days.

## Approved Milestone 1

Milestone 1 consists of **Steps 1 through 4**:

1. Persistent conversational shell.
2. Specification and narrative.
3. Local project generation.
4. Local execution.

At the end of Milestone 1, a user can discuss a demo, resolve ambiguities,
approve a persistent plan, generate a Go/MySQL/Alloy project, and run it locally
with Docker Compose. Requests to deploy the application remotely are blocked.

Grafana Cloud stack creation, live telemetry verification, generated Grafana
resources, presenter tooling, and full reconciliation remain subsequent
milestones.

## Decision log

### 2026-09-16

- Use `Alex3k/grafana-demo-compiler` as the collaborative repository.
- Use pull requests after the one-time repository bootstrap.
- Do not require CI for the MVP.
- Implement the compiler with a Go backend, React UI, and SQLite.
- Generate application services in Go only.
- Use MySQL for generated applications.
- Use Grafana Alloy for telemetry.
- Support local Docker Compose deployment only.
- Have `gcx` create and manage a dedicated Grafana Cloud stack for each demo
  session.
- Send compiler telemetry to `https://democompiler.grafana.net/` through the
  `democompiler` gcx context for Agent Observability.
- Use one requirement-alignment evaluation and one local-only deployment guard.
- Approve Milestone 1 as Steps 1 through 4.
