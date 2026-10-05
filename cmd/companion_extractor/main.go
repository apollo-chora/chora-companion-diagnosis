// Command companion_extractor is the extractor of the companion_diagnosis crew
// (ADR-254 D2, ex weakness_extractor; ADR-205 WS-2 lineage). It serves the
// dispatch role companion_extract SUBSCRIBER-ONLY (ADR-254 D6): no ADK web
// launcher, a health port (/healthz, /readyz) and nothing else, and a
// subscriber that cannot start ends the process non-zero so the workload dies.
// The binary takes no arguments.
//
// Role (P1 single agent): faithfully TRANSCRIBE / DESCRIBE ONE uploaded
// learning artifact into PLAIN TEXT for the diagnoser. The dispatch payload
// carries source_blob_uri (object-store URI) + source_mime_type; the shared
// groundingplugin injects the artifact as a by-reference FileData part on the
// model call (the same path qgen grounds on through the gateway), so the model
// actually SEES the artifact. There is no inline-bytes variant on the bus: a
// payload without an object-store reference is a permanent FAILED. SafeSearch
// on the raw upload stays in the kennel before dispatch (ADR-254 D12).
//
// Env vars (NEVER inlined per feedback_no_inline_config):
//
//	CHORA_PROJECT_ID                       project label for the boot log (default chora-local)
//	CHORA_AGENT_APP_NAME                   session AppName label (optional)
//	COMPANION_EXTRACTOR_MODEL              primary model override (default: embedded agentconfig YAML)
//	CHORA_GATEWAY_ENDPOINT                 default gateway.chora.site:443
//	CHORA_GATEWAY_TENANT_ID / _GCID        required (ADR-163; per-request values from the dispatch win)
//	CHORA_ENV                              dev | staging | prod
//	AGENT_DISPATCH_ENABLED                 must be "true" (no other transport)
//	AGENT_DISPATCH_SUBSCRIPTION            request subscription (chora-companion-extractor.agent-dispatch-extract-requested)
//	AGENT_HEALTH_PORT                      health port (default 8080)
package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/apollo-chora/chora-companion-diagnosis/internal/boot"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	// SIGTERM (rollout, scale-down) cancels the context: the dispatch
	// subscriber stops receiving, in-flight work finishes, and the process
	// exits 0. Any other way out is an error and exits non-zero (ADR-254 D6).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := boot.RunExtractor(ctx, os.Args[1:]); err != nil {
		log.Fatalf("companion_extractor: %v", err)
	}
}
