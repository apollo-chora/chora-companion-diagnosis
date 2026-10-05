package boot

import (
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/adk/agent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"

	"github.com/apollo-chora/chora-adk-common/agentdispatch"
	"github.com/apollo-chora/chora-adk-common/modelgatewayclient"
	"github.com/apollo-chora/chora-adk-common/tracing"
)

type newAgentFunc func(cfg Config, llm adkmodel.LLM) (agent.Agent, error)

// Process-level collaborators, as package variables so a unit test can prove
// the boot composition (which identities reach the subscriber) without ADC, a
// network dial or a broker. Production never reassigns them.
var (
	initTracing     = tracing.Init
	newGatewayLLM   = NewGatewayLLM
	serveSubscriber = agentdispatch.RunSubscriberOnly
)

// RunDiagnoser boots and serves companion_diagnoser subscriber-only (ADR-254
// D6). It returns rather than exits; a non-nil error means the pod must die.
func RunDiagnoser(ctx context.Context, args []string) error {
	if err := refuseArgs(CrewKindDiagnoser, args); err != nil {
		return err
	}
	cfg, err := LoadDiagnoserConfig()
	if err != nil {
		return err
	}
	return run(ctx, cfg, NewDiagnoserAgent)
}

// RunExtractor boots and serves companion_extractor, same contract.
func RunExtractor(ctx context.Context, args []string) error {
	if err := refuseArgs(CrewKindExtractor, args); err != nil {
		return err
	}
	cfg, err := LoadExtractorConfig()
	if err != nil {
		return err
	}
	return run(ctx, cfg, NewExtractorAgent)
}

// refuseArgs rejects any command-line argument: the ADK web launcher and its
// "web -port ... agentengine" command line are gone (ADR-254 D6); a Deployment
// whose command was not updated dies with the cause in its exit line.
func refuseArgs(crewKind string, args []string) error {
	if len(args) == 0 {
		return nil
	}
	return fmt.Errorf("%s takes no arguments, got %q: the ADK web launcher "+
		"(\"web -port ... agentengine\") was removed by ADR-254 D6 and this binary "+
		"is subscriber-only; update the Deployment command", crewKind, args)
}

func run(ctx context.Context, cfg Config, newAgent newAgentFunc) error {
	traceShutdown, err := initTracing(ctx, cfg.CrewKind)
	if err != nil {
		return fmt.Errorf("tracing.Init: %w", err)
	}
	defer func() {
		if err := traceShutdown(context.Background()); err != nil {
			slog.Error("trace shutdown error", "err", err)
		}
	}()
	slog.Info(cfg.CrewKind+" boot", cfg.LogAttrs()...)

	llm, err := newGatewayLLM(ctx, cfg)
	if err != nil {
		return err
	}
	root, err := newAgent(cfg, llm)
	if err != nil {
		return err
	}
	plugins, err := NewPlugins(cfg.CrewKind)
	if err != nil {
		return err
	}
	serveCfg, err := dispatchServeConfig(cfg, root, plugins)
	if err != nil {
		return err
	}
	return serveSubscriber(ctx, serveCfg, agentdispatch.RunOptions{})
}

// dispatchServeConfig derives the lane identity from the crew kind; an unknown
// crew kind is refused rather than guessed.
func dispatchServeConfig(cfg Config, root agent.Agent, plugins []*plugin.Plugin) (agentdispatch.ServeConfig, error) {
	role := DispatchRole(cfg.CrewKind)
	if role == "" {
		return agentdispatch.ServeConfig{}, fmt.Errorf("crew kind %q has no dispatch role: refusing to guess one", cfg.CrewKind)
	}
	return agentdispatch.ServeConfig{
		AgentRole:   role,
		ServiceName: ServiceName(cfg.CrewKind),
		AppName:     cfg.AppName,
		RootAgent:   root,
		Sessions:    session.InMemoryService(),
		Plugins:     runner.PluginConfig{Plugins: plugins},
	}, nil
}

// gatewayConfig is the gateway client configuration: the crew kind is the
// agent id (ADR-254 D7), the surface is the crew id. Pure, so it is assertable.
func gatewayConfig(cfg Config) modelgatewayclient.Config {
	return modelgatewayclient.Config{
		Endpoint:         cfg.GatewayEndpoint,
		LogicalModelID:   cfg.Model,
		FallbackModelIDs: cfg.FallbackModels,
		AgentID:          cfg.CrewKind,
		CrewKind:         CrewSurface,
		Surface:          CrewSurface,
		TenantID:         cfg.GatewayTenantID,
		GCID:             cfg.GatewayGCID,
		Audience:         cfg.GatewayAudience,
	}
}

// NewGatewayLLM builds the chora-model-gateway-fronted adkmodel.LLM (ADR-163):
// the single un-bypassable LLM chokepoint.
func NewGatewayLLM(ctx context.Context, cfg Config) (adkmodel.LLM, error) {
	m, err := modelgatewayclient.New(ctx, gatewayConfig(cfg))
	if err != nil {
		return nil, fmt.Errorf("modelgatewayclient.New(%s, %s @ %s): %w", cfg.CrewKind, cfg.Model, cfg.GatewayEndpoint, err)
	}
	return m, nil
}
