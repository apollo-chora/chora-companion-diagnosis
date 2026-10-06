# chora-companion-diagnosis

## About

chora-companion-diagnosis is a Go service that runs two subscriber-only agents for the Chora companion diagnosis workflow. `companion_extractor` reads one uploaded learning artifact and produces plain-text transcription or description; `companion_diagnoser` turns that text into a structured Growth-Edge map and can also generate study aids or a practice test. Both agents receive work through the Chora agent-dispatch event bus and send model requests through `chora-model-gateway`.

## Quick start

Prerequisites: Go 1.26.6 and, when running the subscriber, a reachable NATS JetStream broker and Chora model gateway.

Clone and build the repository:

```sh
git clone https://github.com/apollo-chora/chora-companion-diagnosis.git
cd chora-companion-diagnosis
go build ./...
```

Before starting either subscriber, set the required gateway credentials and enable dispatch:

```sh
export CHORA_GATEWAY_TENANT_ID=your-tenant-id
export CHORA_GATEWAY_GCID=your-gcid
export AGENT_DISPATCH_ENABLED=true
export NATS_URL=nats://localhost:4222
```

The repository also contains a Dockerfile that builds both binaries into one image:

```sh
docker build -t chora-companion-diagnosis .
```

## Usage

The repository builds two binaries:

```sh
go run ./cmd/companion_extractor
go run ./cmd/companion_diagnoser
```

Both binaries are subscriber-only. They take no command-line arguments and expose health and readiness endpoints on `AGENT_HEALTH_PORT`, which defaults to port 8080. The dispatch request subscriptions are `chora-companion-extractor.agent-dispatch-extract-requested` and `chora-companion-diagnoser.agent-dispatch-diagnose-requested`; `AGENT_DISPATCH_SUBSCRIPTION` can override the request subscription name.

The extractor expects dispatch session state containing `source_blob_uri` and `source_mime_type`. The blob URI must use the `gs://` scheme. It produces plain text for marked tests, notes or scribbles, and source material.

The diagnoser reads `extracted_text` for the default `diagnose` task. It also accepts `study_aids`, which reads `edges_json`, and `practice_test`, which reads `edges_json` and optionally `max_questions`. An unknown `task_kind` is treated as a permanent failure. For practice tests, the actor and critic are followed by an on-edge gate, with at most one regeneration round.

Model configuration is embedded in `internal/agentconfig`. Both agents use `gemini-2.5-pro` with `gemini-2.5-flash` as the fallback by default. `COMPANION_DIAGNOSER_MODEL` and `COMPANION_EXTRACTOR_MODEL` override the primary model. The remaining runtime settings are controlled through environment variables:

| Variable | Purpose | Default |
| --- | --- | --- |
| `CHORA_PROJECT_ID` | Project label used in boot logs | `chora-local` |
| `CHORA_AGENT_APP_NAME` | ADK session AppName label | unset |
| `CHORA_GATEWAY_ENDPOINT` | Model gateway endpoint | `gateway.chora.site:443` |
| `CHORA_GATEWAY_TENANT_ID` | Gateway tenant ID; required | unset |
| `CHORA_GATEWAY_GCID` | Gateway GCID; required | unset |
| `CHORA_GATEWAY_AUDIENCE` | ID-token audience for the gateway | `https://gateway.chora.site` |
| `CHORA_ENV` | Environment name | `dev` |
| `AGENT_DISPATCH_ENABLED` | Enables the dispatch subscriber; must be `true` | unset |
| `AGENT_DISPATCH_SUBSCRIPTION` | Request subscription override | derived |
| `AGENT_DISPATCH_MAX_DELIVERY_ATTEMPTS` | Redelivery ceiling | `5` |
| `AGENT_HEALTH_PORT` | Health/readiness port | `8080` |
| `NATS_URL` | NATS JetStream broker | unset |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP/gRPC trace endpoint | stdout |
| `CHORA_SERVICE_VERSION` | OTLP `service.version` attribute | `dev` |

The default Docker entrypoint is the diagnoser. The extractor binary is also present in the image and can be selected by overriding the container command:

```sh
docker run --rm chora-companion-diagnosis /app/companion_extractor
```

## Development

The module path is `github.com/apollo-chora/chora-companion-diagnosis` and the module declares Go 1.26.6.

Run the same checks used by CI:

```sh
gofmt -l .
go mod tidy
go vet ./...
go test ./...
```

CI also verifies that `go mod tidy` does not modify `go.mod` or `go.sum`.

The repository is split into:

```
cmd/
  companion_diagnoser/     diagnoser binary
  companion_extractor/     extractor binary
internal/
  agent/                   prompt composition, task parsing and output handling
  agentconfig/             embedded model and prompt configuration
  boot/                    runtime wiring, dispatch and gateway setup
```

The pure logic under `internal/agent` and the configuration and boot packages have unit tests. The repository's test suite is designed to run without a broker, database, or network connection.
