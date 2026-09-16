# Grafana Demo Compiler

Grafana Demo Compiler is a collaborative, local-first assistant for designing,
building, running, and validating short, story-led Grafana demos.

The MVP generates Go application services backed by MySQL and runs them locally
with Docker Compose. Remote deployment targets remain unsupported; deterministic
enforcement is deferred.
Grafana Alloy sends demo telemetry to a dedicated Grafana Cloud stack that
`gcx` creates and manages for each demo session.

The compiler sends its own agent telemetry to a separately provided central
Grafana operations stack for Agent Observability.

Development changes are made through pull requests. The initial repository
bootstrap is the only direct commit to `main`.

## How it works

The sequence below shows the persistent planning and focused-topic loops that
exist today, followed by the planned generation, local runtime, and verification
stages.

```mermaid
sequenceDiagram
    autonumber

    actor Human as Solutions Engineer
    participant UI as React UI
    participant API as Go Backend
    participant DB as SQLite
    participant AI as AI roles
    participant Local as Local workspace
    participant GCX as gcx
    participant Cloud as Per-session Grafana Cloud

    Human->>UI: Create or resume a demo session
    UI->>API: Send demo requirement
    API->>DB: Persist human message

    API->>AI: Collaborator: conversation + current brief
    AI-->>API: Stream narrative, scope and questions
    API-->>UI: Send response deltas over SSE
    API->>DB: Persist assistant response

    API->>AI: Curator: compile completed conversation
    AI-->>API: Structured living brief
    API->>DB: Save immutable brief revision
    API-->>UI: Display updated brief
    Note over API,AI: AI calls emit telemetry to central Agent Observability

    loop Main planning and iteration
        Human->>UI: Answer, correct or refine
        UI->>API: Send next message
        API->>AI: Collaborator: conversation + confirmed context
        AI-->>API: Stream revised proposal
        API-->>UI: Send response deltas over SSE
        API->>AI: Curator: update unconfirmed topics
        AI-->>API: Next brief revision
        API->>DB: Persist revision
    end

    opt Focused topic iteration
        Human->>UI: Open a brief topic
        UI->>API: Start focused side chat
        API->>DB: Persist isolated topic conversation
        API->>AI: Collaborator: topic context + focused messages
        AI-->>API: Stream candidate replacement
        API-->>UI: Send focused response deltas over SSE

        loop Refine candidate
            Human->>UI: Iterate on the topic
            UI->>API: Send focused message
            API->>AI: Continue focused conversation
            AI-->>API: Stream updated candidate
            API-->>UI: Send focused response deltas over SSE
        end

        Human->>UI: Confirm and apply
        UI->>API: Apply candidate
        API->>DB: Save locked topic in new brief revision
        API-->>UI: Refresh main session context
    end

    alt Accept a bounded prototype iteration
        Human->>UI: Prototype this bounded slice
        UI->>API: Authorize one prototype iteration
        API->>Local: Generate bounded local prototype
        Local-->>API: Return artifacts and check results
        API->>DB: Save iteration evidence and remain Draft
        API-->>UI: Show prototype for feedback
    else Accept the current plan
        Human->>UI: Accept plan
        UI->>API: Record plan acceptance
        API->>AI: Evaluator: requirement + brief + plan
        AI-->>API: Alignment result
        alt Evaluation is meets or partially_meets
            API->>DB: Move session to Ready
            Note over Local,Cloud: Planned generation and runtime stages
            API->>Local: Generate accepted revision
            Local-->>API: Artifacts pass minimum generation checks
            API->>DB: Record Generated state
            API->>GCX: Create dedicated demo stack
            GCX->>Cloud: Provision stack through Grafana Cloud APIs
            API->>GCX: Discover datasources and dry-run resources
            GCX-->>API: Resource change preview
            API-->>UI: Show create, update and delete preview
            Human->>UI: Approve selected Grafana resources
            UI->>API: Submit resource approval
            API->>GCX: Create only approved resources
            API->>Local: Start Docker Compose
            Local-->>API: Services and health endpoints are healthy
            API->>DB: Record Running state
            Local->>Cloud: Alloy exports metrics, logs and traces
            API->>Local: Exercise scenario and timed story
            API->>GCX: Verify telemetry and Grafana resources
            GCX-->>API: Validation evidence
            API->>AI: Evaluate completed demo against requirement
            AI-->>API: Completed-demo alignment result
            alt Application, telemetry, resources, story and evaluation pass
                API->>DB: Record Verified state
                API-->>UI: Show evidence-backed result
            else Any required verification fails
                API-->>UI: Show failures and keep current state
            end
        else Evaluation is does_not_meet
            API->>DB: Keep session in Draft
            API-->>UI: Show missing requirements
        end
    end

    Human->>UI: Request another revision
    UI->>API: Reopen the collaborative planning loop
```

The implemented lifecycle currently reaches `Draft` and `Ready`. The
`Generated`, `Running`, and `Verified` states and their transitions are planned
for later milestones. Conversation and focused-topic work can create many
immutable brief revisions before the human approves a bounded prototype.

```mermaid
stateDiagram-v2
    [*] --> Draft

    Draft --> Draft: Discuss, prototype or revise
    Draft --> Ready: Accepted plan passes evaluation
    Ready --> Draft: Human requests a change
    Ready --> Generated: Artifacts pass minimum checks
    Generated --> Running: Local Compose is healthy
    Running --> Generated: Environment is stopped
    Running --> Verified: Application, telemetry, resources and story pass
    Verified --> Draft: Create a new revision

    note right of Draft
        Persistent conversation
        Immutable brief revisions
        Confirmed topics are locked
    end note

    note right of Ready
        Accepted plan
        Requirement evaluation is sufficient
    end note

    note right of Generated
        Go services
        MySQL
        Alloy
        Docker Compose
        Grafana resources
    end note

    note right of Running
        Application runs locally
        Telemetry flows to the
        per-session Grafana Cloud stack
    end note

    note right of Verified
        Scenario and timed story pass
        gcx confirms telemetry and resources
        Completed demo answers requirement
    end note
```

## Step 1 development setup

Prerequisites:

- Go 1.26 or newer.
- Node.js 24 or newer.
- AWS credentials with access to the selected Amazon Bedrock model.

Install dependencies and build the React UI:

```bash
go mod download
cd web
npm install
npm run build
cd ..
```

Create local configuration. The application intentionally does not load dotenv
files implicitly, so export the file in the shell that starts the server:

```bash
cp .env.example .env
# Edit .env without committing credentials.
set -a
source .env
set +a
go run ./cmd/server
```

Open `http://localhost:8080`. The compiled UI is served by the Go backend.
During UI development, run `npm run dev` from `web/` and use the Vite address;
it proxies `/api` to the backend.

### Amazon Bedrock

`AWS_REGION` selects the region and `BEDROCK_MODEL_ID` selects the model or
inference profile. The Grafana AI SDK uses the standard AWS credential chain.
Changing the model requires updating `BEDROCK_MODEL_ID` and restarting the
compiler, not changing code.

### Agent Observability

The `democompiler` gcx context is used to inspect the central operations stack,
but gcx authentication is not the application's telemetry credential.

Get the `AGENTO11Y_*` ingest values and create an access-policy token from the
[Agent Observability Connection page](https://democompiler.grafana.net/plugins/grafana-agento11y-app).
The token needs `sigil:write`, `metrics:write`, `traces:write`, and `logs:write`.
Use the Connection page's OpenTelemetry link to copy the generated
`OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_EXPORTER_OTLP_HEADERS` values.

Keep all seven values from `.env.example` together. The health panel reports
exactly which values are missing and the assistant remains usable once Bedrock
is configured, even if telemetry export is not yet configured.
