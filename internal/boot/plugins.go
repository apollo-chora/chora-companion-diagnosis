package boot

import (
	"fmt"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/plugin"
	"google.golang.org/genai"

	"github.com/apollo-chora/chora-adk-common/groundingplugin"
	"github.com/apollo-chora/chora-adk-common/modelgatewayclient"
	"github.com/apollo-chora/chora-adk-common/terminationplugin"
	"github.com/apollo-chora/chora-adk-common/tracing"
)

// Pinned termination identity (D6 P3 trace-emission contract).
const (
	terminationRuntime     = "AGENT_EXECUTION_RUNTIME_ADK_GO"
	terminationCrewPattern = "P1_SINGLE_AGENT"
	// MaxIterations caps model calls per invocation. The extractor and the
	// diagnose / study_aids tasks are single-shot; practice_test is actor +
	// critic with one bounded regenerate, four calls at most.
	terminationMaxIterationsExtractor = 3
	terminationMaxIterationsDiagnoser = 6
)

// StateString reads a string value from ADK session state ("" on miss).
func StateString(state interface {
	Get(string) (any, error)
}, key string) string {
	v, err := state.Get(key)
	if err != nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// NewInboundTracePlugin links the agent's local trace to the dispatch
// envelope's W3C traceparent (set in session state by agentdispatch).
func NewInboundTracePlugin(crewKind string) (*plugin.Plugin, error) {
	p, err := plugin.New(plugin.Config{
		Name: "chora_inbound_trace_" + crewKind,
		BeforeAgentCallback: func(ctx agent.CallbackContext) (*genai.Content, error) {
			traceparent := StateString(ctx.State(), "traceparent")
			if traceparent == "" {
				return nil, nil
			}
			tracing.AddInboundLink(ctx, traceparent, StateString(ctx.State(), "tracestate"))
			return nil, nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("inbound-trace plugin.New: %w", err)
	}
	return p, nil
}

// TerminationConfig is the pinned termination-plugin configuration for a crew
// kind: AgentID is the binary's agent name, CrewKind the crew id.
func TerminationConfig(crewKind string) terminationplugin.Config {
	maxIter := terminationMaxIterationsExtractor
	if crewKind == CrewKindDiagnoser {
		maxIter = terminationMaxIterationsDiagnoser
	}
	return terminationplugin.Config{
		Publisher:     &terminationplugin.LoggingPublisher{},
		AgentID:       crewKind,
		Runtime:       terminationRuntime,
		CrewKind:      CrewSurface,
		CrewPattern:   terminationCrewPattern,
		MaxIterations: maxIter,
	}
}

// NewPlugins builds the crew's plugin chain in runtime order:
// [inboundTrace, tenantProp, (grounding, extractor only), termination]. No
// agent-side mana gate: the gateway meters (ADR-177 / ADR-254 A3).
// The grounding plugin is what makes the extractor SEE the artifact: it appends
// the object-store FileData part from source_blob_uri to the model call. The
// diagnoser never grounds (its inputs are text).
func NewPlugins(crewKind string) ([]*plugin.Plugin, error) {
	inboundTraceP, err := NewInboundTracePlugin(crewKind)
	if err != nil {
		return nil, err
	}
	tenantPropP, err := modelgatewayclient.NewTenantPropagationPlugin(crewKind)
	if err != nil {
		return nil, fmt.Errorf("modelgatewayclient.NewTenantPropagationPlugin: %w", err)
	}
	chain := []*plugin.Plugin{inboundTraceP, tenantPropP}
	if crewKind == CrewKindExtractor {
		groundingP, err := groundingplugin.New(crewKind)
		if err != nil {
			return nil, fmt.Errorf("groundingplugin.New: %w", err)
		}
		chain = append(chain, groundingP)
	}
	terminationP, err := terminationplugin.New(TerminationConfig(crewKind))
	if err != nil {
		return nil, fmt.Errorf("terminationplugin.New: %w", err)
	}
	return append(chain, terminationP), nil
}
