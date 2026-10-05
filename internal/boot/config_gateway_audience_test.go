// RED-first for D6 step 1 (owner ruling R21, sequenced).
//
// CHORA_GATEWAY_AUDIENCE must be READ HERE and defaulted EXPLICITLY, so the
// audience becomes a stateable value instead of one the shared library invents.
// Today `modelgatewayclient` defaults it in TWO places, client.go:181-182 and
// image.go:110-111, and no caller anywhere sets it.
//
// ⚠ This test asserts the value is CARRIED, not that the library stops
// defaulting. Removing those two defaults is step 4 and is gated on the
// manifests reaching the live cluster first: with 12 constructor sites across 9
// agents and no CHORA_GATEWAY_AUDIENCE in any manifest, a removal today fails
// every agent at construction.
package boot

import "testing"

func TestGatewayAudience_defaultsExplicitly_LoadDiagnoserConfig(t *testing.T) {
	minimalBootEnv(t)
	cfg, err := LoadDiagnoserConfig()
	if err != nil {
		t.Fatalf("LoadDiagnoserConfig: %v", err)
	}
	if cfg.GatewayAudience != "https://gateway.chora.site" {
		t.Fatalf("GatewayAudience = %q, want the explicit default %q",
			cfg.GatewayAudience, "https://gateway.chora.site")
	}
}

func TestGatewayAudience_honoursEnv_LoadDiagnoserConfig(t *testing.T) {
	minimalBootEnv(t)
	t.Setenv("CHORA_GATEWAY_AUDIENCE", "https://gateway.example.invalid")
	cfg, err := LoadDiagnoserConfig()
	if err != nil {
		t.Fatalf("LoadDiagnoserConfig: %v", err)
	}
	if cfg.GatewayAudience != "https://gateway.example.invalid" {
		t.Fatalf("GatewayAudience = %q, want the env value", cfg.GatewayAudience)
	}
}

func TestGatewayAudience_defaultsExplicitly_LoadExtractorConfig(t *testing.T) {
	minimalBootEnv(t)
	cfg, err := LoadExtractorConfig()
	if err != nil {
		t.Fatalf("LoadExtractorConfig: %v", err)
	}
	if cfg.GatewayAudience != "https://gateway.chora.site" {
		t.Fatalf("GatewayAudience = %q, want the explicit default %q",
			cfg.GatewayAudience, "https://gateway.chora.site")
	}
}

func TestGatewayAudience_honoursEnv_LoadExtractorConfig(t *testing.T) {
	minimalBootEnv(t)
	t.Setenv("CHORA_GATEWAY_AUDIENCE", "https://gateway.example.invalid")
	cfg, err := LoadExtractorConfig()
	if err != nil {
		t.Fatalf("LoadExtractorConfig: %v", err)
	}
	if cfg.GatewayAudience != "https://gateway.example.invalid" {
		t.Fatalf("GatewayAudience = %q, want the env value", cfg.GatewayAudience)
	}
}
