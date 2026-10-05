package boot

import (
	"context"
	"errors"
	"fmt"
	"iter"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/artifact"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

type fakeState struct{ data map[string]any }

func newFakeState(data map[string]any) *fakeState {
	if data == nil {
		data = map[string]any{}
	}
	return &fakeState{data: data}
}
func (f *fakeState) Get(k string) (any, error) {
	v, ok := f.data[k]
	if !ok {
		return nil, fmt.Errorf("key %q not found", k)
	}
	return v, nil
}
func (f *fakeState) Set(k string, v any) error { f.data[k] = v; return nil }
func (f *fakeState) All() iter.Seq2[string, any] {
	return func(yield func(string, any) bool) {
		for k, v := range f.data {
			if !yield(k, v) {
				return
			}
		}
	}
}

var _ session.State = (*fakeState)(nil)

type fakeReadonlyContext struct {
	context.Context
	state session.ReadonlyState
}

func (f *fakeReadonlyContext) UserContent() *genai.Content          { return nil }
func (f *fakeReadonlyContext) InvocationID() string                 { return "test-invocation" }
func (f *fakeReadonlyContext) AgentName() string                    { return "test_agent" }
func (f *fakeReadonlyContext) ReadonlyState() session.ReadonlyState { return f.state }
func (f *fakeReadonlyContext) UserID() string                       { return "test-user" }
func (f *fakeReadonlyContext) AppName() string                      { return "companion_diagnosis" }
func (f *fakeReadonlyContext) SessionID() string                    { return "test-session" }
func (f *fakeReadonlyContext) Branch() string                       { return "" }

var _ agent.ReadonlyContext = (*fakeReadonlyContext)(nil)

func newFakeCtx(state map[string]any) *fakeReadonlyContext {
	return &fakeReadonlyContext{Context: context.Background(), state: newFakeState(state)}
}

type fakeLLM struct{ name string }

func (f fakeLLM) Name() string { return f.name }
func (f fakeLLM) GenerateContent(context.Context, *adkmodel.LLMRequest, bool) iter.Seq2[*adkmodel.LLMResponse, error] {
	return func(yield func(*adkmodel.LLMResponse, error) bool) {
		yield(nil, errors.New("fakeLLM: GenerateContent must not be called by a boot-wiring test"))
	}
}

var _ adkmodel.LLM = fakeLLM{}

func minimalBootEnv(t interface{ Setenv(string, string) }) {
	t.Setenv("CHORA_PROJECT_ID", "chora-test")
	t.Setenv("CHORA_GATEWAY_TENANT_ID", "11111111-1111-7111-8111-111111111111")
	t.Setenv("CHORA_GATEWAY_GCID", "00000000-0000-7000-8000-000000001999")
	t.Setenv("COMPANION_DIAGNOSER_MODEL", "")
	t.Setenv("COMPANION_EXTRACTOR_MODEL", "")
}

// fakeCallbackContext adds the mutable-state + artifacts surface that
// agent.CallbackContext requires (what a BeforeAgentCallback receives).
type fakeCallbackContext struct {
	*fakeReadonlyContext
	state *fakeState
}

func (f *fakeCallbackContext) State() session.State       { return f.state }
func (f *fakeCallbackContext) Artifacts() agent.Artifacts { return noopArtifacts{} }

var _ agent.CallbackContext = (*fakeCallbackContext)(nil)

func newFakeCallbackCtx(state map[string]any) *fakeCallbackContext {
	s := newFakeState(state)
	return &fakeCallbackContext{fakeReadonlyContext: &fakeReadonlyContext{Context: context.Background(), state: s}, state: s}
}

type noopArtifacts struct{}

var errNoArtifacts = errors.New("fake: artifact store not wired for this test")

func (noopArtifacts) Save(context.Context, string, *genai.Part) (*artifact.SaveResponse, error) {
	return nil, errNoArtifacts
}
func (noopArtifacts) List(context.Context) (*artifact.ListResponse, error) {
	return nil, errNoArtifacts
}
func (noopArtifacts) Load(context.Context, string) (*artifact.LoadResponse, error) {
	return nil, errNoArtifacts
}
func (noopArtifacts) LoadVersion(context.Context, string, int) (*artifact.LoadResponse, error) {
	return nil, errNoArtifacts
}
