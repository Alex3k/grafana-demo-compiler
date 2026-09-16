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

## Working brief

The compiler maintains a lightweight working brief rather than requiring a
complete specification before anything can be built. It records:

- Audience and technical depth.
- Company, industry, branding, and terminology.
- Desired audience outcome.
- Business stakes and demo-to-win message.
- Required proof points.
- Domain-specific user journey.
- Architecture and service responsibilities.
- Important telemetry for telling the story.
- Agreed and proposed Grafana resources.
- Scenario trigger, recovery, and reset behavior.
- Timed narrative and presenter flow.
- Acceptance criteria.
- Open questions and confirmed decisions.

Items in the brief can be unknown, proposed, or confirmed. The assistant asks
when uncertainty would materially change what gets built, but it does not make
the user finish every detail before showing useful work.

Architecture is stored as Mermaid source and rendered with Mermaid.js in the
UI. Plain-text architecture diagrams are not used.

When the audience, desired outcome, core scenario, primary journey, and a small
architecture are understood well enough, the assistant offers to build an
initial prototype. The user can accept the offer, ask for a smaller prototype,
or continue planning.

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

Generation is iterative. Early prototypes can implement only the main vertical
slice. Feedback updates the working brief, Mermaid architecture, and affected
files. The assistant prefers targeted edits and uses a full regeneration only
when the architecture or direction changes substantially.

No unit or integration tests are generated for application services. Minimum
checks are Go formatting and compilation, Docker Compose validation, presence
of required services and health endpoints, and a scan for embedded credentials
or unsupported remote deployment files.

Manufacturing and IoT will provide the initial development fixture. This is not
a domain imposed on users; the actual demo must follow the approved user intent.

## Observability and Grafana proposal

The assistant proposes a concise set of telemetry and Grafana resources that is
enough to tell the requested story. The proposal briefly explains what signals
matter, which services produce them, which resources are useful, and why they
help the narrative.

- Avoid producing telemetry merely because it is possible.
- Avoid large collections of dashboards, alerts, or SLOs.
- Start with the smallest story-complete set.
- Explicit user requests are authoritative and can expand the set.
- Recommendations remain optional until the user agrees.
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

### Step 2: Collaborative planning and early prototyping

Deliver:

- A lightweight living brief with unknown, proposed, and confirmed items.
- Targeted clarification questions.
- Architecture stored as Mermaid source and rendered with Mermaid.js.
- A concise narrative outline that matures toward ten minutes through
  iteration.
- A small observability and Grafana resource proposal focused on the story.
- An offer to build a prototype as soon as there is enough context for a
  coherent vertical slice.
- The option to continue planning instead of generating immediately.
- Requirement evaluation when the user chooses to approve the completed plan.

Meaningful outcome: a user can shape the idea conversationally and choose when
to turn it into something tangible without waiting for every detail to be
final.

### Step 3: Iterative local prototype generation

Deliver:

- A template-first Go foundation with domain-specific behavior generated from
  the working brief.
- An initial vertical slice with at least three generated Go services.
- MySQL.
- Alloy configuration.
- Dockerfiles and Docker Compose.
- Relevant seed data, health endpoints, and an artifact manifest.
- Repeated prototype iterations inside the active draft.
- Targeted edits to affected services, data, telemetry, and Mermaid
  architecture after user feedback.
- A concise change summary for every iteration.
- Minimum checks only: `gofmt`, `go build ./...`, `docker compose config`,
  required-file checks, and credential and remote-deployment scans.
- No generated unit or integration tests for application services.
- The local-only deployment guard.

Meaningful outcome: a working brief becomes an inspectable prototype that the
user can repeatedly refine before accepting it as the generated demo.

### Step 4: Local execution

Deliver:

- Build, start, stop, and restart actions.
- Health and dependency checks.
- Streamed build and container progress.
- Runtime status and actionable failures.
- A five-minute target from approved revision to usable local demo.

Meaningful outcome: the user can run the generated demo locally.

Once Steps 1 through 4 are available, Steps 2, 3, and 4 operate as one feedback
loop: discuss, prototype, run, review, and revise. Their numbering describes the
order in which the product capabilities are delivered, not a waterfall user
journey.

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
request an early prototype, iteratively refine a Go/MySQL/Alloy project, and run
it locally with Docker Compose. The user can continue planning instead of
prototyping, and requests to deploy the application remotely are blocked.

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
- Use Mermaid.js for architecture diagrams.
- Treat Steps 2 through 4 as an iterative prototype feedback loop rather than a
  waterfall sequence.
- Keep observability and Grafana planning lightweight and story-focused.
- Generate only the smallest useful telemetry and resource set unless the user
  requests more.
- Do not generate unit or integration tests for application services.
