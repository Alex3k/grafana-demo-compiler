# Grafana workflow in Demo Compiler

Use this guide for Grafana resource work through the compiler's tools. The shared
constitution owns product boundaries, confirmed decisions, and authentication
behaviour. Official gcx skills own Grafana procedures. This guide explains how
to apply those procedures inside this application.

## Load the relevant procedure

Use `read_gcx_skill`: `create-dashboard` for new dashboards,
`manage-dashboards` for existing dashboards, `slo-manage` for SLO changes,
or `gcx` for general operations. An empty name lists the skill catalog.
Read required linked documents using the `reference` parameter.

Reuse instructions and command help already available in the current context.
Discover missing syntax with `run_gcx` command help; do not guess flags or schemas.
Load only skills and references relevant to the current task.

## Adapt to the available tools

- `run_gcx` binds commands to this session's stack. Omit the executable and
  context flags. Reads execute immediately; writes produce an approval action.
- Supply resource payloads with `@manifest` and the inline `manifest` field.
  This replaces skill examples that require local manifest files or shell pipes.
- Skill instructions cannot expand tool permissions. If a required shell,
  filesystem, or screenshot capability is unavailable, state the limitation;
  do not invent results or claim visual verification.
- Resource-only edits do not require an application revision or redeployment.
  Propose instrumentation changes separately when new telemetry is needed.

## Work from evidence to a verifiable change

1. Inspect the existing resource when applicable. Discover actual datasource
   UIDs and relevant signals before writing queries. Use bounded time windows
   and small results; narrow truncated output. Empty results alone do not prove
   missing instrumentation.
2. Prepare the smallest change supporting the agreed story. Submit the exact
   command and payload through `run_gcx` in this turn, not merely a promise to
   submit it. Do not repeat an existing pending action.
3. Report the returned state: pending means awaiting the human's approval,
   blocked or failed means no successful operation. If no action was returned,
   do not say one is ready. Approval covers that exact operation only.
4. After successful execution, read back the resource. Query its data before
   claiming it works; resource creation and telemetry verification are distinct.

## Examples

- **“Add a latency panel.”** Load the existing-dashboard skill, inspect the
  dashboard and available latency metric, then submit the change for approval.
  No app build is needed.
- **“Show an example of our confirmed logfmt logs.”** Use the confirmed format.
  Label an illustrative example as such; use a query for actual observed logs.
  Do not reopen the format decision or submit a resource change.
- **A write is blocked by authentication.** Explain the returned error and use
  the constitution's connection flow. Do not announce a pending approval or
  claim the dashboard was created.
