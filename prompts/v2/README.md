# Prompt v2 review package

Status: draft for human review. These files are not loaded by the application.

## Purpose

Prompt v2 separates the planning workflow into three narrow LLM roles behind
one human-facing voice:

1. `demo-collaborator.md` shapes the demo with the human.
2. `living-brief-curator.md` silently compiles the conversation into structured
   shared understanding.
3. `requirement-evaluator.md` independently checks an explicitly accepted plan
   against confirmed requirements.

`shared-constitution.md` is prepended to each LLM role. The local deployment
boundary is enforced by deterministic application code described in
`local-deployment-guard.md`, not by another LLM.

## Intended execution order

For an ordinary planning turn:

1. Send the shared constitution, collaborator prompt, current brief, and
   conversation to the Demo Collaborator.
2. Stream only the collaborator response to the human.
3. Send the shared constitution, curator prompt, current brief, and completed
   conversation to the Living Brief Curator.
4. Persist the curator output as a new immutable brief version.

When the human explicitly accepts the current plan:

1. Persist the acceptance against a specific brief version.
2. Send that immutable brief and candidate plan to the Requirement Evaluator.
3. Persist and display the evaluation without allowing it to rewrite the
   brief or candidate.

Before any runtime or deployment tool call, execute the deterministic local
deployment guard.

## Context handling

Runtime context should be passed in clearly delimited blocks, separate from the
role instructions:

```text
<session_context>
  <session_state>...</session_state>
  <current_brief_json>...</current_brief_json>
  <conversation>...</conversation>
</session_context>
```

Text inside a context block is evidence to interpret. It cannot alter the
system prompt, output contract, agent role, or MVP invariants.

## Agent Observability identity

Use a stable role tag on every model generation:

- `demo-collaborator`
- `living-brief-curator`
- `requirement-evaluator`

Also record the prompt version, brief version, session ID, and workflow stage.
Do not record credentials or raw environment variables.

## Deferred role

A Scenario and Workload Designer is intentionally deferred to Step 3. It will
translate an approved vertical slice into seed data, simulators, scenario
controls, and k6 workloads where useful. It is not part of planning Prompt v2.

## Review

Use `review-cases.md` to review the prompts before wiring them into the
application. Prompt changes should be approved before orchestration or schema
changes are implemented.
