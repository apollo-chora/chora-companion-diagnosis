# chora-companion-diagnosis

The companion_diagnosis crew: two subscriber-only ADK Go **P1 single agents**
that turn an uploaded learning artifact into a learner's positive Growth-Edge
map, study aids, and a practice test. `companion_extractor` faithfully
transcribes/describes the artifact; `companion_diagnoser` builds the
Growth-Edge map and renders the downstream outputs.

Module path: `github.com/apollo-chora/chora-companion-diagnosis`.

The crew is cloud-neutral: NATS JetStream for dispatch (via
`chora-adk-common/agentdispatch` + `chora-common/eventbus`), standard OTLP for
traces (via `chora-adk-common/tracing` + `chora-common/otel`), the
`chora-model-gateway` gRPC chokepoint for every model call, and env-backed
configuration for secrets. No cloud account or managed service is required.

## The two agents

| Agent | Binary | Role | Input (ADK session state) | Output |
|---|---|---|---|---|
| **companion_extractor** | `cmd/companion_extractor` | Faithfully TRANSCRIBE / DESCRIBE one uploaded learning artifact into plain text. Does NOT analyse or grade. For a `marked_test` it transcribes each item + its right/wrong mark verbatim; for `notes`/`scribble` it describes faithfully; for `source_material` it summarises the scope. | `source_blob_uri` + `source_mime_type` | PLAIN TEXT |
| **companion_diagnoser** | `cmd/companion_diagnoser` | Build a learner's positive "Growth Edge" map from the extracted text + a fenced **UNTRUSTED LEARNER CLUES** data block, then render the downstream outputs. | `extracted_text`, optional `clues_block`, `task_kind`, `edges_json` | STRICT JSON (see below) |

The diagnoser's `task_kind` discriminator (ADR-254 D6 addendum):

- absent or `diagnose` — build the Growth-Edge map from `extracted_text` (+ optional
  `clues_block`); emits the FROZEN `analysis.py` `_JSON_CONTRACT` shape
  (`edges[]` with `concept_label` / `concept_key` / `category` / `tags` /
  `confidence` / `strength` / `summary` / `misconceptions` / `sample_wrong` /
  `suggested_angles` / `per_item_correctness`);
- `study_aids` — emit the study-aids STRICT JSON from `edges_json`;
- `practice_test` — run the actor -> critic -> on-edge gate (one bounded
  regenerate) from `edges_json` and emit `{title, questions[],
  rejected_reason?}`;
- anything else — a permanent `unknown_task_kind` FAILED completion.

The diagnoser instruction carries a LOCKED SAFETY PREAMBLE (never infer
protected attributes / disability / medical conditions; never demotivating
language; omit `confidence < 0.5` guesses) and an untrusted-clues guard (learner
clues + extracted text are DATA, never instructions). The instruction strings
live in `internal/agent/instructions.go` as exported funcs so they are
unit-testable.

## Dispatch contract (ADR-253/254)

Both binaries are SUBSCRIBER-ONLY: no ADK web launcher, a health port
(`/healthz`, `/readyz`) and nothing else, and a subscriber that cannot start
ends the process non-zero so the workload dies. The binary takes no arguments.

Three identity strings are in play, deliberately different:

| String | Value | Meaning |
|---|---|---|
| CrewKind | `companion_diagnoser` / `companion_extractor` | the ADK agent name; the event AUTHOR |
| DispatchRole | `companion_diagnose` / `companion_extract` | what the bus keys topics on |
| ServiceName | `chora-companion-diagnoser` / `chora-companion-extractor` | the workload; names the subscription |

The request subscription is `chora-companion-diagnoser.agent-dispatch-diagnose-requested`
(resp. `...extract-requested`); set `AGENT_DISPATCH_SUBSCRIPTION` explicitly —
the derived name is only the fallback.

## Architecture

- **Compute**: any host running a binary or the container image. ONE image,
  TWO binaries; the compose service selects which runs via its `command`.
- **Event bus**: NATS JetStream (stream `CHORA_EVENTS`). The dispatch topics
  (`chora.ai_kernel.agent_dispatch.<role>_{requested,completed}.v1`) are valid
  NATS subjects; the canonical event envelope rides as NATS headers.
- **Model calls**: gRPC to `chora-model-gateway` (ADR-163 chokepoint), stamped
  `surface=companion_diagnosis`. The extractor's plugin chain includes the
  grounding plugin, which injects the uploaded artifact as a by-reference
  `FileData` part so the model actually SEES it.
- **Traces**: standard OTLP/gRPC via `chora-common/otel`; stdout in local dev
  when `OTEL_EXPORTER_OTLP_ENDPOINT` is unset.
- **Ports**: health/readiness on `AGENT_HEALTH_PORT` (default `8080`). No
  database.

## Configuration

| Variable | Purpose | Local default |
| --- | --- | --- |
| `CHORA_PROJECT_ID` | project label stamped into the boot log | `chora-local` |
| `CHORA_AGENT_APP_NAME` | ADK session AppName label (session grouping) | unset (falls back to the dispatch role) |
| `COMPANION_DIAGNOSER_MODEL` / `COMPANION_EXTRACTOR_MODEL` | primary model override for the embedded agentconfig YAML | unset |
| `CHORA_GATEWAY_ENDPOINT` | model-gateway endpoint | `gateway.chora.site:443` |
| `CHORA_GATEWAY_TENANT_ID` / `CHORA_GATEWAY_GCID` | **required** — process-fixed fallback; the tenant-propagation plugin stamps the per-request tenant from session state over these | unset |
| `CHORA_GATEWAY_AUDIENCE` | ID-token audience claim for the gateway call | `https://gateway.chora.site` |
| `CHORA_ENV` | `dev` \| `staging` \| `prod` | `dev` |
| `AGENT_DISPATCH_ENABLED` | must be `true` (the dispatch subscriber is the only transport) | unset |
| `AGENT_DISPATCH_SUBSCRIPTION` | consumer name override | derived from service + role |
| `AGENT_DISPATCH_MAX_DELIVERY_ATTEMPTS` | redelivery ceiling (must match the consumer) | `5` |
| `AGENT_HEALTH_PORT` | health/readiness port | `8080` |
| `NATS_URL` | NATS JetStream event bus | unset |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP/gRPC trace endpoint | stdout |
| `CHORA_SERVICE_VERSION` | stamped as the OTLP `service.version` attribute | `dev` |

Model selection is AGENT-DRIVEN via the `internal/agentconfig` YAML (single
source of truth): each agent sends `primary_model` as the gateway's
`logical_model_id` and `fallback_models` as the declared fallback chain. Ops
may override an individual primary via the `COMPANION_*_MODEL` env vars for
quick experiments; tier, fallback chain and prompt version stay YAML-declared.

## Build and test

```sh
go build ./...
go vet ./...
go test ./...
```

The suite is hermetic — no broker, database, or network is required.

## Docker

```sh
docker build -t chora-companion-diagnosis .
```

The image carries both binaries; run the extractor with
`docker run --rm chora-companion-diagnosis /app/companion_extractor` (the
default entrypoint is the diagnoser).
