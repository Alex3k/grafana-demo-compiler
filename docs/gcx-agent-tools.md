# gcx agent tools (MVP)

The conversational agent has two capabilities: `run_gcx` and `read_guidance`.
There is no per-dashboard/alert/SLO tool registry. Commands and flags are discovered
from the installed gcx catalog; execution remains direct argv, never a shell.

## One-time login

Create/deploy the demo through the existing button. In **Grafana actions**, expand
**One-time gcx login** and run its command in your terminal. The context name must
match the session stack slug and point to its exact Grafana Cloud URL. The tool
checks this before each stack operation and ignores ambient endpoint/token env
overrides. It never uses the central operations context as a fallback.

## Reads and writes

Ask the main chat to inspect telemetry or propose a dashboard. Known read commands
run immediately. Writes and unclassified operations create an immutable pending
action. Review target, exact argument array, purpose, and manifest in **Grafana
actions**, then **Approve and execute** or **Reject**. Natural-language "yes" is
not an approval. Each approval is consumed once; changing anything requires a new
proposal. Commands have a 45-second timeout and bounded output.

Resource file inputs must reference `@manifest`; the supplied payload is persisted
with the approval and materialized in a private temporary directory only when run.
External paths, secret references, endpoint overrides, auth/config changes, Cloud
account commands, raw API passthrough, arbitrary shell, and other agent invocation
are not enabled. Existing deployment/deletion buttons retain their current scope.
The read classification and allowed argument policy live in
`internal/gcxtool/runner.go`, separately from the model's tool schema. Unsupported
flags fail explicitly rather than silently changing command semantics.

Receipts persist in SQLite and are polled by the panel; recent receipts are loaded
into both main and focused chat context. The agent's native tool calls/results use
the existing Agent Observability middleware; approved execution gets a separate
`run_gcx_approved` tool span correlated to the session conversation.

After execution, ask the agent to verify the change or continue. Approval does not
automatically launch another model turn in this MVP. A failed/interrupted write may
have partially succeeded: inspect before requesting another write. A server restart
does not retry running actions automatically.

## Adding best practices

Edit the Markdown files in `guidance/`, or add another `.md` file and rebuild. The
catalog is generated automatically. The agent reads relevant guidance on demand;
it is not all pasted into every request. Start with `demo-storytelling.md` and
`grafana-workflow.md`. Storytelling guidance provides adaptable advice; the
Grafana workflow adapts official gcx skills to the compiler's tools and approval
flow. Keep product constraints in the existing constitution and enforce execution
boundaries in code. No vector database, new orchestration framework, or provider
migration is involved.
