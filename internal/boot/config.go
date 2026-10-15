// Package boot holds the resolved boot wiring for the companion_diagnosis
// crew's two subscriber-only binaries, companion_diagnoser and
// companion_extractor (ADR-254 D2/D6; ADR-205 WS-2 lineage). Same split as the
// OE crew: config.go resolves env + embedded agentconfig into a Config and
// returns errors instead of exiting; agents.go builds the agents with the LLM
// injected; plugins.go pins the plugin chain identities; run.go is the thin
// glue that needs the network and the blocking receive loop.
package boot

import (
	"fmt"
	"os"
	"strings"

	"github.com/apollo-chora/chora-companion-diagnosis/internal/agent"
	"github.com/apollo-chora/chora-companion-diagnosis/internal/agentconfig"
)

// Crew kinds: the ADK agent names, the termination AgentIDs and the gateway
// agent_id (ADR-254 D7). CrewSurface is the crew id stamped as
// InvokeRequest.surface on every model call.
const (
	CrewKindDiagnoser = agent.DiagnoserAgentName
	CrewKindExtractor = agent.ExtractorAgentName
	CrewSurface       = "companion_diagnosis"
)

// Model env vars. Every *_MODEL var is REQUIRED — the model id is env-only,
// so a deploy that forgets it dies at boot rather than routing to a literal
// name. The *_FALLBACK_MODELS vars are optional comma-separated chains: unset
// (the default) declares no fallback.
const (
	envDiagnoserModel    = "COMPANION_DIAGNOSER_MODEL"
	envDiagnoserFallback = "COMPANION_DIAGNOSER_FALLBACK_MODELS"
	envExtractorModel    = "COMPANION_EXTRACTOR_MODEL"
	envExtractorFallback = "COMPANION_EXTRACTOR_FALLBACK_MODELS"
)

// Config is one binary's fully resolved boot configuration.
type Config struct {
	CrewKind string

	// ProjectID is the project label stamped into the boot log line. It is
	// informational only — no call is scoped by it.
	ProjectID string
	// AppName is the ADK session AppName label (the session grouping key).
	// Empty lets agentdispatch fall back to the dispatch role.
	AppName string

	GatewayEndpoint string
	// GatewayAudience is the ID-token audience claim minted for the gateway
	// call. Read here rather than left to modelgatewayclient's internal
	// default, which lives in TWO places (client.go and image.go) and which
	// nothing could previously state or override. The default below is the
	// value the library already used, so this is not a behaviour change.
	GatewayAudience string
	GatewayTenantID string
	GatewayGCID     string

	Model          string
	FallbackModels []string
	PromptVersion  string

	Env string
}

// EnvOr returns os.Getenv(name) if non-empty, else fallback.
func EnvOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// requiredModel returns the env-provided gateway logical_model_id, failing
// loud when the var is unset. Model ids are env-only: there is no literal
// default to fall back to.
func requiredModel(name string) (string, error) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return "", fmt.Errorf("%s required (the gateway logical_model_id; env-only, no default)", name)
	}
	return v, nil
}

// fallbackModels parses a comma-separated env list into the declared fallback
// chain. Unset / blank (the default) declares no chain, so the gateway is
// asked for the primary target only.
func fallbackModels(name string) []string {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// LoadDiagnoserConfig resolves the companion_diagnoser boot config.
func LoadDiagnoserConfig() (Config, error) {
	cfg, err := agentconfig.CompanionDiagnoser()
	if err != nil {
		return Config{}, fmt.Errorf("%s: load agent config: %w", CrewKindDiagnoser, err)
	}
	sub, err := cfg.Sub("diagnoser")
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", CrewKindDiagnoser, err)
	}
	return loadConfig(CrewKindDiagnoser, envDiagnoserModel, envDiagnoserFallback, sub)
}

// LoadExtractorConfig resolves the companion_extractor boot config.
func LoadExtractorConfig() (Config, error) {
	cfg, err := agentconfig.CompanionExtractor()
	if err != nil {
		return Config{}, fmt.Errorf("%s: load agent config: %w", CrewKindExtractor, err)
	}
	sub, err := cfg.Sub("extractor")
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", CrewKindExtractor, err)
	}
	return loadConfig(CrewKindExtractor, envExtractorModel, envExtractorFallback, sub)
}

func loadConfig(crewKind, modelEnv, fallbackEnv string, sub agentconfig.SubAgentConfig) (Config, error) {
	model, err := requiredModel(modelEnv)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", crewKind, err)
	}
	gatewayTenantID := os.Getenv("CHORA_GATEWAY_TENANT_ID")
	gatewayGCID := os.Getenv("CHORA_GATEWAY_GCID")
	if gatewayTenantID == "" || gatewayGCID == "" {
		return Config{}, fmt.Errorf("%s: CHORA_GATEWAY_TENANT_ID + CHORA_GATEWAY_GCID required "+
			"(ADR-163; no in-memory fallback per feedback_no_stubs_real_wiring)", crewKind)
	}
	return Config{
		CrewKind:        crewKind,
		ProjectID:       EnvOr("CHORA_PROJECT_ID", "chora-local"),
		AppName:         os.Getenv("CHORA_AGENT_APP_NAME"),
		GatewayEndpoint: EnvOr("CHORA_GATEWAY_ENDPOINT", "gateway.chora.site:443"),
		GatewayAudience: EnvOr("CHORA_GATEWAY_AUDIENCE", "https://gateway.chora.site"),
		GatewayTenantID: gatewayTenantID,
		GatewayGCID:     gatewayGCID,
		Model:           model,
		FallbackModels:  fallbackModels(fallbackEnv),
		PromptVersion:   sub.PromptVersion,
		Env:             EnvOr("CHORA_ENV", "dev"),
	}, nil
}

// LogAttrs renders the boot line's slog key/value pairs (GCID truncated).
func (c Config) LogAttrs() []any {
	gcid := c.GatewayGCID
	if len(gcid) > 8 {
		gcid = gcid[:8] + "..."
	}
	return []any{
		"project", c.ProjectID, "app_name", c.AppName,
		"model", c.Model, "fallback", c.FallbackModels, "prompt_version", c.PromptVersion,
		"gateway_endpoint", c.GatewayEndpoint, "gateway_tenant_id", c.GatewayTenantID, "gateway_gcid_prefix", gcid,
		"chora_env", c.Env, "surface", CrewSurface,
	}
}
