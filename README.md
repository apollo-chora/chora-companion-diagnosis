# chora-companion-diagnosis

> Two subscriber-only agents for the companion diagnosis workflow: one turns an
> uploaded artefact into text, the other turns that text into a Growth-Edge map,
> study aids, or a practice test.

Go, one image, two binaries. No database — both agents are stateless and receive
work through the agent-dispatch event bus, sending model requests to the platform's
model gateway.

## What it is

The repository builds two agents that occupy two roles in the AI kernel's
dispatch roster:

| Binary | Role | What it does |
| --- | --- | --- |
| `companion_extractor` | `companion_extract` | reads one uploaded learning artefact and produces a plain-text transcription or description |
| `companion_diagnoser` | `companion_diagnose` | turns the extracted text into a structured Growth-Edge map, and can also produce study aids or a practice test |

Neither agent serves HTTP traffic in the usual sense and neither is on the request
path of a web feature directly: the AI kernel dispatches to them over NATS and
collects their result through the same bus.

The diagnoser handles three task kinds, chosen by the dispatch payload:

| `task_kind` | Reads | Produces |
| --- | --- | --- |
| `diagnose` (default) | `extracted_text` | the Growth-Edge map |
| `study_aids` | `edges_json` | study aids |
| `practice_test` | `edges_json`, optional `max_questions` | a practice test |

An unknown `task_kind` is treated as a **permanent** failure — it will not succeed
on redelivery, so it should not be retried.

For a practice test the actor and critic are followed by an on-edge quality gate,
with at most one regeneration round.

The extractor expects the dispatch session state to carry `source_blob_uri` and
`source_mime_type`, and the blob URI must use the `gs://` scheme.

### In the stack, this repository runs two services

`compose.yml` builds the same image twice and overrides the entrypoint:

| Service | Entrypoint | Role |
| --- | --- | --- |
| `companion-diagnoser` | `/app/companion_diagnoser` | `companion_diagnose` |
| `companion-extractor` | `/app/companion_extractor` | `companion_extract` |

The image's default entrypoint is the diagnoser; the extractor is selected by
overriding the command. Both services `expose` `8080` on the Compose network and
publish no host port.

## Prerequisites

| Route | What you need |
| --- | --- |
| In the stack | A working `chora-stack` deployment — NATS and the model gateway come from it. |
| Standalone | Go **1.26.6** (`go.mod` declares it), a reachable NATS JetStream broker, and a reachable model gateway. |
| Either | The model gateway must be running: this service has no fallback to a direct provider API. |

## Credentials

This service holds **one** credential, and it is not a provider key you obtain — it
is an internal caller credential for the platform's model gateway.

| Value | Where it comes from | Variable |
| --- | --- | --- |
| Gateway caller token | the stack's `GATEWAY_API_KEY` (`.env`) | `CHORA_GATEWAY_TOKEN` |
| Gateway tenant | the stack's `CHORA_DEFAULT_TENANT_ID` (defaults to the seeded tenant) | `CHORA_GATEWAY_TENANT_ID` |
| Gateway GCID | the stack's `CHORA_DEFAULT_GCID` | `CHORA_GATEWAY_GCID` |
| NATS endpoint | the stack's NATS service; no credential in the local configuration | `NATS_URL` |

`compose.yml` marks `GATEWAY_API_KEY` as required (`${GATEWAY_API_KEY:?...}`), so the
stack will not start either companion service without it. In the local stack the
value is inert — `GATEWAY_AUTH_ENABLED=false` — but it must be non-empty, and it must
equal the `<key>` half of `GATEWAY_API_KEYS` before gateway authentication is turned
on.

There is **no** model provider key here. The agents name a model and the gateway
owns the provider credential; see
[`chora-stack/README.md`](../chora-stack/README.md#credentials) for where those come
from and how to obtain them.

## Run it in the stack

`compose.yml` does three things for each of the two services:

1. Builds the image from the sibling checkout at `../chora-companion-diagnosis`.
2. Starts the service once `nats` is healthy, `nats-init` has completed, and
   `model-gateway` has started.
3. Publishes no host port; it `expose`s `8080` only.

```sh
cd chora-stack
docker compose up -d companion-diagnoser companion-extractor
docker compose ps companion-diagnoser companion-extractor
```

Both should read `Up`. What Compose sets, beyond the credentials above:

| Variable | Stack value |
| --- | --- |
| `CHORA_GATEWAY_ENDPOINT` | `model-gateway:9090` (gRPC, in-network) |
| `CHORA_GATEWAY_AUDIENCE` | `chora-local` |
| `CHORA_GATEWAY_INSECURE` | `1` — plaintext gRPC inside the Compose network |
| `CHORA_ENV` | `local` |
| `AGENT_DISPATCH_ENABLED` | `true` |
| `AGENT_DISPATCH_SUBSCRIPTION` | see the note below |
| `AGENT_HEALTH_PORT` | `8080` |
| `COMPANION_DIAGNOSER_MODEL` / `COMPANION_EXTRACTOR_MODEL` | `LongCat-2.5-Preview` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `otel-collector:4317` |

**The stack overrides the subscription names.** The binaries' defaults are
`chora-companion-diagnoser.agent-dispatch-diagnose-requested` and
`chora-companion-extractor.agent-dispatch-extract-requested`, but `compose.yml` sets:

- `chora-companion-diagnoser.agent-dispatch-companion-diagnose-requested`
- `chora-companion-extractor.agent-dispatch-companion-extract-requested`

The extra `companion-` infix means anything published to the binaries' documented
default subjects will never be picked up in this stack. Use the Compose values when
you are checking a subscription.

## Run it standalone

```sh
git clone https://github.com/apollo-chora/chora-companion-diagnosis.git
cd chora-companion-diagnosis
go build ./...
```

Either agent needs dispatch enabled and the gateway identity set before it will do
anything:

```sh
export CHORA_GATEWAY_TENANT_ID=your-tenant-id
export CHORA_GATEWAY_GCID=your-gcid
export CHORA_GATEWAY_TOKEN=<gateway caller token>
export AGENT_DISPATCH_ENABLED=true
export NATS_URL=nats://localhost:4222
export COMPANION_DIAGNOSER_MODEL=<logical model id>
export COMPANION_EXTRACTOR_MODEL=<logical model id>
```

Then run one:

```sh
go run ./cmd/companion_diagnoser
go run ./cmd/companion_extractor
```

Both take no command-line arguments. Container build:

```sh
docker build -t chora-companion-diagnosis .
docker run --rm chora-companion-diagnosis                       # diagnoser (default entrypoint)
docker run --rm chora-companion-diagnosis /app/companion_extractor   # extractor
```

Development checks, as CI runs them:

```sh
gofmt -l .
go mod tidy
go vet ./...
go test ./...
```

CI also fails if `go mod tidy` modifies `go.mod` or `go.sum`. The unit suite runs
without a broker, database or network.

## Configuration

Model configuration is environment-only. `internal/agentconfig` declares each
agent's tier and prompt version but **no model name** — the model id is a required
environment variable and boot fails loud when it is unset.

| Variable | Default | Purpose |
| --- | --- | --- |
| `COMPANION_DIAGNOSER_MODEL` | **required** (stack: `LongCat-2.5-Preview`) | the diagnoser's gateway `logical_model_id` |
| `COMPANION_EXTRACTOR_MODEL` | **required** (stack: `LongCat-2.5-Preview`) | the extractor's gateway `logical_model_id` |
| `COMPANION_DIAGNOSER_FALLBACK_MODELS` | unset | comma-separated fallback chain; unset declares none |
| `COMPANION_EXTRACTOR_FALLBACK_MODELS` | unset | comma-separated fallback chain |
| `CHORA_GATEWAY_ENDPOINT` | `gateway.chora.site:443` | model gateway endpoint (stack: `model-gateway:9090`) |
| `CHORA_GATEWAY_TENANT_ID` | **required** | gateway tenant id |
| `CHORA_GATEWAY_GCID` | **required** | gateway caller GCID |
| `CHORA_GATEWAY_TOKEN` | unset in code; **required by the stack** | gateway caller token |
| `CHORA_GATEWAY_AUDIENCE` | `https://gateway.chora.site` | ID-token audience (stack: `chora-local`) |
| `CHORA_GATEWAY_INSECURE` | unset | plaintext gRPC when truthy (stack: `1`) |
| `CHORA_ENV` | `dev` | environment name (stack: `local`) |
| `CHORA_PROJECT_ID` | `chora-local` | project label in boot logs |
| `CHORA_AGENT_APP_NAME` | unset | ADK session AppName label |
| `AGENT_DISPATCH_ENABLED` | unset | enables the dispatch subscriber; must be `true` |
| `AGENT_DISPATCH_SUBSCRIPTION` | derived per role | request-subscription override |
| `AGENT_DISPATCH_MAX_DELIVERY_ATTEMPTS` | `5` | redelivery ceiling before the message is dead-lettered |
| `AGENT_HEALTH_PORT` | `8080` | the port the health listener is configured for — see [Verify it works](#verify-it-works) |
| `NATS_URL` | unset | NATS JetStream broker |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | stdout | OTLP/gRPC trace endpoint |
| `CHORA_SERVICE_VERSION` | `dev` | OTLP `service.version` attribute |

The two `*_MODEL` variables are the ones people get wrong: they are model ids the
**gateway** understands, not provider model names, and an empty value is a boot
failure rather than a silent default.

## Verify it works

### Be careful with the health port

Both binaries document a health/readiness port on `AGENT_HEALTH_PORT` (default
`8080`), and `chora-adk-common` logs a `health_addr` when the subscriber boots:

```text
"agentdispatch.subscriber_only_boot","role":"companion_diagnose",...,"health_addr":"[::]:8080"
"agentdispatch.serving","role":"companion_diagnose",...
```

**In the running stack nothing answers on that port.** Observed from three
directions — a request from another container by service name, and requests to
`127.0.0.1:8080` and `[::1]:8080` from a container sharing the diagnoser's network
namespace — all refused the connection, while the log still reaches
`agentdispatch.serving`.

Be precise about what that is: a runtime observation, and the cause is the pinned
framework version, not this repository's code:

```text
chora-adk-common v0.0.0-20261005110720-56af53fbecf8   ← this repo, no listener
chora-adk-common v0.0.0-20261018001513-336709b355a0   ← chora-qgen, binds correctly
```

The listener lives in `agentdispatch.RunSubscriberOnly`, and the October 18
pseudo-version fixed the bind. `chora-kg-explorer` and `chora-oe-grading` share the
stale pin and fail the same way; `chora-qgen` answers on `8080` normally. Bumping
the pin is the fix. Until then, do not build a readiness gate on
`curl http://…:8080/healthz` for these services — it will fail even when the agent
is healthy.

### What to check instead

Confirm the subscriber came up, from the logs:

```sh
docker compose logs companion-diagnoser --tail=20 | grep agentdispatch.serving
docker compose logs companion-extractor --tail=20 | grep agentdispatch.serving
```

A line naming the role and subscription is the pass. Its absence, or a
`subscriber ... stopped` line, is the failure — the most common cause is a NATS
connect error at boot.

Confirm the durable consumers exist on the broker. The `nats` container ships no
`nats` CLI, so read the monitoring endpoint instead — it is published on the host:

```sh
curl -s 'http://localhost:8222/jsz?consumers=1' \
  | grep -o 'chora-companion-[a-z-]*-agent-dispatch[a-z-]*' | sort -u
```

Both of these should appear:

```text
chora-companion-diagnoser-agent-dispatch-companion-diagnose-requested
chora-companion-extractor-agent-dispatch-companion-extract-requested
```

That is the strongest available proof that the agent actually subscribed — it is the
broker's own view, not the process's claim about itself.

### It has no database

There is nothing to migrate and no schema to check. If a deployment suggests
otherwise, it is describing a different service.

## Troubleshooting

- **The agent starts but the health probe fails** — expected in this build; no
  listener exists on `AGENT_HEALTH_PORT`. Use the log check above.
- **`boot fails loud` naming a model variable** — `COMPANION_DIAGNOSER_MODEL` or
  `COMPANION_EXTRACTOR_MODEL` is unset. The stack supplies `LongCat-2.5-Preview`;
  a standalone run must supply a logical model id the gateway knows.
- **The service exits or restarts immediately** — start with what this repository
  validates itself: an unset `COMPANION_DIAGNOSER_MODEL` / `COMPANION_EXTRACTOR_MODEL`,
  or a missing `CHORA_GATEWAY_TENANT_ID` / `CHORA_GATEWAY_GCID`, is rejected during
  config loading and the process exits non-zero. A disabled `AGENT_DISPATCH_ENABLED`
  is a different case: dispatch enablement is handled inside `chora-adk-common`, so
  this repository alone does not guarantee that path exits immediately.
- **`subscriber ... stopped ... nats connect: lookup nats: i/o timeout`** — the
  broker was not resolvable when the subscriber booted. Common right after the stack
  starts, or after a daemon restart; the container restarts and reconnects.
- **Dispatches are published but never consumed** — the publisher is using the
  binary's default subject while `compose.yml` overrides it with the extra
  `companion-` infix. Compare against `AGENT_DISPATCH_SUBSCRIPTION` in the running
  container.
- **An unknown `task_kind` keeps being redelivered** — `internal/boot/agents.go`
  marks it a permanent failure via `agentdispatch.Permanent`, so it will never
  succeed. The retry ceiling and the dead-letter routing belong to the dispatch
  dependency, governed by `AGENT_DISPATCH_MAX_DELIVERY_ATTEMPTS`; expect a bounded
  number of attempts and then a dead letter, not an immediate stop.
- **A practice test never converges** — the on-edge gate allows at most one
  regeneration round; a test that fails it twice is returned as rejected rather than
  looping.
- **The extractor rejects a dispatch** — the session state must carry
  `source_blob_uri` (a `gs://` URI) and `source_mime_type`. A blob at any other
  scheme is refused.

## API surface

No inbound HTTP API. Both agents are subscriber-only: they consume agent-dispatch
requests from NATS JetStream and return results through the same bus. The only HTTP
surface the code claims is the health listener described above, which does not bind
in this build.

The dispatch contract, per role:

| Role | Consumes | Requires in session state |
| --- | --- | --- |
| `companion_extract` | `chora-companion-extractor.agent-dispatch-companion-extract-requested` | `source_blob_uri` (`gs://`), `source_mime_type` |
| `companion_diagnose` | `chora-companion-diagnoser.agent-dispatch-companion-diagnose-requested` | `extracted_text`, or `edges_json` for `study_aids` / `practice_test` |

## Architecture

```text
cmd/
  companion_diagnoser/   diagnoser binary: launcher and signal handling
  companion_extractor/   extractor binary: the same, for the other role
internal/
  agent/                 prompt composition, task parsing, output handling
  agentconfig/           embedded model and prompt configuration (tier, prompt version)
  boot/                  runtime wiring, dispatch and gateway setup
Dockerfile               one image, both binaries
```

The design keeps the role logic in `internal/agent` as pure functions — prompt
composition, task-kind parsing, output shaping — with `internal/boot` doing the
wiring, so the unit suite runs with no broker and no gateway. `internal/agentconfig`
embeds each agent's tier and prompt version in YAML, deliberately without a model
name, which is why the model id has to arrive through the environment.

`main.go` blocks on the signal context after starting the subscriber. That is not
decoration: the pinned `chora-adk-common` returns from its serve call once the
subscription is registered, so without that block the process would exit 0 and the
restart policy would loop it forever.

## Events

Dispatch is NATS JetStream, one durable consumer per role, with the request
subscription set by `AGENT_DISPATCH_SUBSCRIPTION` (or derived from the role when
unset).

| Concern | Setting |
| --- | --- |
| Request subscription | `AGENT_DISPATCH_SUBSCRIPTION`, derived per role by default |
| Redelivery ceiling | `AGENT_DISPATCH_MAX_DELIVERY_ATTEMPTS`, default `5` |
| Streams | `CHORA_EVENTS` for normal dispatch, `CHORA_DLQ` for dead letters |
| Ack behaviour | in-flight work finishes on SIGTERM; the subscriber then stops and the process exits 0 |

`SIGTERM` (a rollout or scale-down) cancels the context, and both entrypoints wait on
that context after starting the subscriber. A boot error goes through `log.Fatalf`
and exits non-zero — that is the path the restart policy keys on.

The graceful half is not locally enforced: that in-flight work is allowed to finish
before the subscriber returns is behaviour of `agentdispatch.RunSubscriberOnly` in
`chora-adk-common`, not of this repository. `main.go` blocking on the context
afterwards is what keeps the process alive either way — the pinned dependency returns
once the subscription is registered, so without the block the process would exit 0
and the restart policy would loop it.

## Migrations

**None.** This service has no database, ships no `migrations/` directory and is not
part of the stack's migrate one-shot set. All state it needs arrives in the dispatch
session; nothing it produces is persisted here.

If you are auditing a deployment and expect a `companion-diagnosis-migrate`
container, there isn't one and there should not be.
