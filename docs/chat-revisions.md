# Human-approved prototype revisions

The main chat can use `read_prototype_file` to inspect a completed iteration's
generated files (empty path lists the inventory). It cannot inspect arbitrary
machine files or runtime `.env` credentials. Side chats do not get revision tools.

Dashboard, alert, and SLO-only creation or updates use `run_gcx` directly. Discover
the command syntax and inspect the live resource and telemetry, then submit an
inline `manifest` using the `@manifest` argument. The user approves the exact
command and payload in **Grafana actions**. Read back the resource after execution.
This does not need generated files, a prototype build, or Docker redeployment.
The revision tool redirects proposals containing only JSON/YAML manifests under
`dashboards/`, `alerts/`, `slos/`, or their `grafana/` equivalents to `run_gcx`
without creating a revision. Routing uses file paths, not words in the goal.

For mixed application and Grafana requests, propose the application code revision
separately from Grafana actions and explain any dependency on newly deployed
telemetry. Application code and ambiguous configuration paths remain eligible for
prototype revisions.

After inspecting code and any relevant gcx results, the model can call
`propose_prototype_revision`. The immutable proposal records the base iteration,
brief version, source digest, requested change, evidence, and exact editable paths.
This does **not** start a build.

The **Prototype revisions** panel shows the proposal with **Reject** and
**Approve and build** buttons. Approval is session-bound and single-use. A changed
brief or source invalidates the proposal; request a new one after reviewing the
changes. Approval cannot update the brief. Changes to confirmed requirements must
be confirmed through the brief workflow before proposing a revision.

Approval invokes the existing background prototype service. It snapshots the base
source into a new iteration, hands the approved request and current brief to the
builder, exposes a source-reading tool, and limits the write tool to approved
paths. It does not ask the planner to redesign the application. Validation must
follow the final edit. Existing iterations and running deployments stay untouched.

Build progress and results use the existing prototype UI. Revision receipts are
included in subsequent main-chat context; builder generations use the existing
Agent Observability instrumentation. The build button remains available for the
original full-generation workflow. Deployment and Grafana write approvals remain
separate. File deletions and automatic deployment are not part of this increment.
