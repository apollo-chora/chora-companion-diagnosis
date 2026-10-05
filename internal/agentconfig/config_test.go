package agentconfig_test

import (
	"testing"

	"github.com/apollo-chora/chora-companion-diagnosis/internal/agentconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompanionExtractor_HighMultimodalTier(t *testing.T) {
	cfg, err := agentconfig.CompanionExtractor()
	require.NoError(t, err)
	assert.Equal(t, "companion_extractor", cfg.Agent)

	extractor, err := cfg.Sub("extractor")
	require.NoError(t, err)
	assert.Equal(t, "high", extractor.Tier)
	// gemini-2.5-pro is the GA multimodal flagship the transcription step needs.
	assert.Equal(t, "gemini-2.5-pro", extractor.PrimaryModel)
	assert.Equal(t, []string{"gemini-2.5-flash"}, extractor.FallbackModels)
	assert.Equal(t, "v1", extractor.PromptVersion)
}

func TestCompanionDiagnoser_HighTier(t *testing.T) {
	cfg, err := agentconfig.CompanionDiagnoser()
	require.NoError(t, err)
	assert.Equal(t, "companion_diagnoser", cfg.Agent)

	diagnoser, err := cfg.Sub("diagnoser")
	require.NoError(t, err)
	assert.Equal(t, "high", diagnoser.Tier)
	assert.Equal(t, "gemini-2.5-pro", diagnoser.PrimaryModel)
	assert.Equal(t, []string{"gemini-2.5-flash"}, diagnoser.FallbackModels)
	assert.Equal(t, "v1", diagnoser.PromptVersion)
}

func TestSub_MissingSubAgentFailsLoud(t *testing.T) {
	cfg, err := agentconfig.CompanionExtractor()
	require.NoError(t, err)
	_, err = cfg.Sub("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no sub-agent")
}
