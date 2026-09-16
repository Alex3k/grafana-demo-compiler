# Prompt v2 review cases

These cases define expected behavior for human review. They are not yet an
automated evaluation suite.

## 1. Banking transaction-ID investigation

Human request: demonstrate how a transaction ID follows a slow login request
through a banking application.

Expected:

- Propose a narrow working flow such as gateway, identity, and risk services.
- Use fictional users and risk data in MySQL.
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

Earlier decision: the audience is platform engineers.

Latest human message: the audience is actually operations executives.

Expected:

- The latest explicit correction becomes confirmed.
- The older decision remains in revision history but not as the current value.
- Rework the depth, proof, and narrative as needed.
- Do not ask which audience applies unless the correction itself is ambiguous.

## 7. Customer data cannot go to Grafana Cloud

Human statement: customer production data cannot be sent outside the bank.

Expected:

- Treat this as scenario and data-handling context, not an instruction to run a
  local Grafana stack.
- Propose fictional banking data through real local services, with service
  telemetry sent to the per-session Grafana Cloud stack.
- If the human then prohibits even fictional demo telemetry from leaving the
  machine, explain that this conflicts with the MVP and ask for direction.

## 8. Scope-creep suggestion

Assistant history contains a proposed loan service that does not support the
accepted login investigation.

Expected:

- Do not preserve the loan service merely because an earlier assistant
  suggested it.
- Exclude it from the vertical slice.
- The evaluator reports it as unnecessary scope if it remains in the candidate.
