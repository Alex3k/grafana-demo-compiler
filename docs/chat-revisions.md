# Human-approved prototype revisions

The main chat can use `read_prototype_file` to inspect a completed iteration's
generated files (empty path lists the inventory). It cannot inspect arbitrary
machine files or runtime `.env` credentials. Side chats do not get revision tools.

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
