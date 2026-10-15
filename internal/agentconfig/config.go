// Package agentconfig holds the build-time, per-agent tier + prompt
// configuration for the companion_diagnosis crew (ADR-205 WS-2). The Growth-Edge
// analyser graduates from the pre-graduation single-shot multimodal crew into
// two ADK Go P1 agents — companion_extractor (faithful artifact transcription)
// -> companion_diagnoser (structured Growth-Edge map). Each agent owns its OWN
// embedded YAML (separation of concern) declaring tier / prompt_version.
//
// AGENT-DRIVEN tiering (same convention as the qgen crews, CR qgen 2026-06-01):
// the YAML is the single source of truth for tier + prompt version. The model
// ids are env-only: boot reads the REQUIRED per-binary model env var and the
// optional *_FALLBACK_MODELS comma-separated chain (empty = no fallback) and:
//   - sends the model id as the chora-model-gateway logical_model_id,
//   - sends the fallback chain as InvokeRequest.fallback_logical_model_ids (the
//     gateway honours the declared chain rather than hardcoding a per-tier
//     ladder),
//   - tags the prompt_version (WS-3 will resolve this against the ADR-197
//     prompt registry; today it labels the embedded default).
//
// A blank model env var is a config error, never a silent default. Model
// selection stays AGENT-DRIVEN via the tier label, never mana-derived (mana is
// a token-budget QUOTA system, see
// feedback_mana_is_quota_not_model_selector).
package agentconfig

import (
	_ "embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

//go:embed companion_extractor.yaml
var companionExtractorYAML []byte

//go:embed companion_diagnoser.yaml
var companionDiagnoserYAML []byte

// SubAgentConfig is one sub-agent's resolved tier + prompt selection. The
// model id is not declared here: boot reads it from the binary's REQUIRED
// model env var.
type SubAgentConfig struct {
	Tier          string `yaml:"tier"`
	PromptVersion string `yaml:"prompt_version"`
}

// AgentConfig is one agent binary's full per-sub-agent config.
type AgentConfig struct {
	Agent     string                    `yaml:"agent"`
	SubAgents map[string]SubAgentConfig `yaml:"sub_agents"`
}

// Sub returns the named sub-agent config, failing loud if the YAML omits it or
// leaves its prompt_version blank — a missing sub-agent or an unversioned
// prompt is a build/config error, never a silent default (per
// feedback_no_stubs_real_wiring). The model id is not declared here: boot
// reads it from the binary's REQUIRED model env var.
func (c AgentConfig) Sub(name string) (SubAgentConfig, error) {
	sc, ok := c.SubAgents[name]
	if !ok {
		return SubAgentConfig{}, fmt.Errorf("agentconfig: agent %q has no sub-agent %q", c.Agent, name)
	}
	if sc.PromptVersion == "" {
		return SubAgentConfig{}, fmt.Errorf("agentconfig: agent %q sub-agent %q has empty prompt_version", c.Agent, name)
	}
	return sc, nil
}

// CompanionExtractor parses the embedded companion_extractor config.
func CompanionExtractor() (AgentConfig, error) { return parse(companionExtractorYAML) }

// CompanionDiagnoser parses the embedded companion_diagnoser config.
func CompanionDiagnoser() (AgentConfig, error) { return parse(companionDiagnoserYAML) }

func parse(raw []byte) (AgentConfig, error) {
	var c AgentConfig
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return AgentConfig{}, fmt.Errorf("agentconfig: unmarshal: %w", err)
	}
	if c.Agent == "" {
		return AgentConfig{}, fmt.Errorf("agentconfig: missing top-level agent name")
	}
	if len(c.SubAgents) == 0 {
		return AgentConfig{}, fmt.Errorf("agentconfig: agent %q declares no sub_agents", c.Agent)
	}
	return c, nil
}
