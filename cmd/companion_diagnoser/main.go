// Command companion_diagnoser is the diagnoser of the companion_diagnosis crew
// (ADR-254 D2, ex weakness_diagnoser; ADR-205 WS-2 lineage). It serves the
// dispatch role companion_diagnose SUBSCRIBER-ONLY (ADR-254 D6): no ADK web
// launcher, a health port (/healthz, /readyz) and nothing else, and a
// subscriber that cannot start ends the process non-zero so the workload dies.
// The binary takes no arguments; a stale "web -port ... agentengine" command is
// refused by name.
//
// Payload discriminator task_kind (ADR-254 D6 addendum): absent or "diagnose"
// builds the learner's positive Growth-Edge map from extracted_text (+ optional
// clues_block) and emits the FROZEN analysis.py JSON contract; "study_aids"
// emits the study-aids STRICT JSON from edges_json; "practice_test" runs the
// actor -> critic -> on-edge gate (one bounded regenerate) from edges_json and
// emits {title, questions[], rejected_reason?}; anything else is a permanent
// unknown_task_kind FAILED completion. Model calls go through
// chora-model-gateway (ADR-163) stamped surface=companion_diagnosis (D7).
//
// Env vars (NEVER inlined per feedback_no_inline_config):
//
//	CHORA_PROJECT_ID                       project label for the boot log (default chora-local)
//	CHORA_AGENT_APP_NAME                   session AppName label (optional)
//	COMPANION_DIAGNOSER_MODEL              primary model override (default: embedded agentconfig YAML)
//	CHORA_GATEWAY_ENDPOINT                 default gateway.chora.site:443
//	CHORA_GATEWAY_TENANT_ID / _GCID        required (ADR-163; per-request values from the dispatch win)
//	CHORA_ENV                              dev | staging | prod
//	AGENT_DISPATCH_ENABLED                 must be "true" (no other transport)
//	AGENT_DISPATCH_SUBSCRIPTION            request subscription (chora-companion-diagnoser.agent-dispatch-diagnose-requested)
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
	if err := boot.RunDiagnoser(ctx, os.Args[1:]); err != nil {
		log.Fatalf("companion_diagnoser: %v", err)
	}
}
