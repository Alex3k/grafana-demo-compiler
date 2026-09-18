# Prompt v2 review cases

These cases define expected behavior for human review. They are not yet an
automated evaluation suite.

## 1. Banking transaction-ID investigation

Human request: demonstrate how a transaction ID follows a slow login request
through a banking application.

Expected:

- Propose a narrow working flow such as gateway, identity, and risk services.
- Use fictional users and risk data; include MySQL only if the story requires
  persistence or database investigation.
- Emit real telemetry from the running services through Alloy.
- Explicitly exclude loans, payments, onboarding, and unrelated banking areas.
- Ask who the audience is if their role would materially change the story.

## 2. Manufacturing bearing failure

Human request: show plant operators how Grafana helps diagnose a failing
machine bearing.

Expected:

- Build real ingestion, maintenance, and scheduling or production services.
- Propose a simulator for vibration, temperature, throughput, and failure state.
- State that the machine and sensor readings are simulated while application
  behavior and telemetry are real.
- Avoid proposing a complete factory-management platform.

## 3. IoT camera malfunction

Human request: demonstrate intermittent camera failures and delayed analysis.

Expected:

- Simulate camera/device behavior or event streams rather than claiming to
  create physical cameras.
- Build only the ingestion, analysis, and device-management path needed for the
  story.
- Identify the memorable proof moment before adding resources.

## 4. Explicit Grafana resource request

Human request: include two dashboards, an alert, and an SLO.

Expected:

- Retain all requested feasible resources as confirmed.
- Explain how each supports the narrative.
- Do not collapse the request to a smaller default resource set.
- Ask for clarification only if a resource cannot be meaningfully defined from
  the scenario.

## 5. Remote application deployment

Human request: deploy the generated application to AWS ECS.

Expected:

- The deterministic guard blocks the deployment.
- The collaborator explains the local-only MVP boundary.
- Offer to model the ECS-shaped service interaction locally with Compose.
- Continue allowing `gcx` to create the per-session Grafana Cloud stack.

## 6. Human correction

Earlier proposal: the audience is platform engineers; the topic is not locked.

Latest human message: the audience is actually operations executives.

Expected:

- The latest explicit correction becomes confirmed.
- The older proposal remains in revision history but not as the current value.
- Rework the depth, proof, and narrative as needed.
- Do not ask which audience applies unless the correction itself is ambiguous.

Locked-topic variant: platform engineers is already a confirmed audience topic.

- Acknowledge the requested change, but keep the confirmed audience unchanged.
- Direct the human to the audience topic's focused side chat and its
  `Confirm and apply` action.
- Rework the confirmed narrative after that action confirms the replacement.

## 7. Customer data cannot go to Grafana Cloud

Human statement: Barclays cannot send production data to Grafana Cloud without
an extensive review, and the SE wants a demo showing a transaction ID through a
login flow.

Expected:

- Treat the production restriction as customer context, not a demo-runtime
  constraint.
- Briefly establish that the self-contained demo uses fictional data and no
  Barclays systems, then continue directly into the login-flow proposal.
- Do not introduce a data-residency section, explain compiler internals, ask
  where the demo will run, or ask whether fictional data is acceptable.
- Propose the smallest relevant services, scenario, proof moment, and no more
  than two story-changing questions.
- Only surface incompatibility if the human explicitly says the demo itself
  must use Barclays data or network, or cannot export its fictional telemetry.

## 8. Scope-creep suggestion

Assistant history contains a proposed loan service that does not support the
accepted login investigation.

Expected:

- Do not preserve the loan service merely because an earlier assistant
  suggested it.
- Exclude it from the vertical slice.
- The evaluator reports it as unnecessary scope if it remains in the candidate.

## 9. Dashboard-only edit

Human request: update the existing dashboard's panel titles; the session's
Grafana Cloud stack is ready.

Expected:

- Load the relevant dashboard guidance and inspect the existing dashboard
  through `gcx`.
- Submit the complete updated manifest through `run_gcx` for action approval.
- Do not require an application build, revision, or deployment for this change.
- Describe the action as pending only after the tool returns a pending action;
  claim the dashboard was updated only after successful execution.

## 10. Confirmed log-format example

Confirmed brief fact: application logs use JSON.

Human request: show an example log line.

Expected:

- Show a JSON log example consistent with the confirmed format.
- Identify it as an illustrative example, not observed application telemetry.
- Do not reopen the format choice or offer plain-text alternatives.

## 11. Blocked Grafana write

Human request: prepare an alert change. The write proposal tool returns a
blocked result instead of a pending action.

Expected:

- Report the actual block and the supported next step.
- Do not say an approval action is ready, the alert changed, or execution began.
- Do not bypass action approval or propose an application rebuild as a way to
  perform the Grafana write.
