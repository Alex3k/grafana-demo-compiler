# Prompt v2 review package

Status: implemented as the application's versioned Prompt v2 runtime package.

## Purpose

Prompt v2 separates the planning workflow into three narrow LLM roles behind
one human-facing voice:

1. `demo-collaborator.md` shapes the demo with the human.
2. `living-brief-curator.md` silently compiles the conversation into structured
   shared understanding.
3. `requirement-evaluator.md` independently checks an explicitly accepted plan
   against confirmed requirements.

`shared-constitution.md` is prepended to each LLM role. The local deployment
boundary remains part of the MVP prompt context. Its planned deterministic
application guard is deferred and described in `local-deployment-guard.md`.

## Intended execution order

For an ordinary planning turn:

1. Send the shared constitution, collaborator prompt, confirmed and proposed
   brief facts, compact operational state, and bounded recent conversation to
   the Demo Collaborator.
2. Stream only the collaborator response to the human.
3. Send the shared constitution, curator prompt, current brief content, and
   completed messages since the previous brief cursor to the Living Brief
   Curator.
4. Persist the curator output as a new immutable brief version.

When the human explicitly accepts the current plan:

1. Persist the acceptance against a specific brief version.
2. Send that immutable brief and candidate plan to the Requirement Evaluator.
3. Persist and display the evaluation without allowing it to rewrite the
   brief or candidate.

Deterministic enforcement before runtime or deployment tool calls is deferred.

## Context handling

Runtime context should be passed in clearly delimited blocks, separate from the
role instructions:

```text
<session_context>
  <session_state>...</session_state>
  <selected_brief_facts>...</selected_brief_facts>
  <recent_conversation>...</recent_conversation>
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
