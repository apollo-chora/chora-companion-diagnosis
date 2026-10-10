package boot

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	adkmodel "google.golang.org/adk/model"

	"github.com/apollo-chora/chora-adk-common/agentdispatch"
	dagent "github.com/apollo-chora/chora-companion-diagnosis/internal/agent"
)

// --- config ---------------------------------------------------------------

func TestLoadConfig_requiresGatewayScopeAndDefaultsTheProject(t *testing.T) {
	minimalBootEnv(t)
	t.Setenv("CHORA_PROJECT_ID", "")
	d, err := LoadDiagnoserConfig()
	if err != nil {
		t.Fatalf("diagnoser without a project id must default, got %v", err)
	}
	if d.ProjectID != "chora-local" {
		t.Errorf("diagnoser project default = %q, want %q", d.ProjectID, "chora-local")
	}
	t.Setenv("CHORA_PROJECT_ID", "chora-prod")
	if d, err = LoadDiagnoserConfig(); err != nil || d.ProjectID != "chora-prod" {
		t.Errorf("diagnoser project override: %+v %v", d, err)
	}
	minimalBootEnv(t)
	t.Setenv("CHORA_GATEWAY_GCID", "")
	if _, err := LoadExtractorConfig(); err == nil || !strings.Contains(err.Error(), "CHORA_GATEWAY_TENANT_ID + CHORA_GATEWAY_GCID required") {
		t.Errorf("extractor without gcid: %v", err)
	}
}

func TestLoadConfig_readsTheEmbeddedYAMLAndTheCompanionOverrides(t *testing.T) {
	minimalBootEnv(t)
	d, err := LoadDiagnoserConfig()
	if err != nil {
		t.Fatal(err)
	}
	if d.CrewKind != "companion_diagnoser" || d.Model != "longcat-2.5-preview" ||
		len(d.FallbackModels) != 1 || d.FallbackModels[0] != "longcat-2.5-preview" || d.PromptVersion != "v1" {
		t.Errorf("diagnoser config = %+v", d)
	}
	// The env override must win over the YAML default, so it uses a value the
	// YAML never declares.
	t.Setenv("COMPANION_EXTRACTOR_MODEL", "test-model-override")
	e, err := LoadExtractorConfig()
	if err != nil {
		t.Fatal(err)
	}
	if e.CrewKind != "companion_extractor" || e.Model != "test-model-override" {
		t.Errorf("extractor override not honoured: %+v", e)
	}
	// The old WEAKNESS_* override names are DELETED (ADR-254 D9), not aliased.
	t.Setenv("COMPANION_EXTRACTOR_MODEL", "")
	t.Setenv("WEAKNESS_EXTRACTOR_MODEL", "test-model-ignored")
	e, _ = LoadExtractorConfig()
	if e.Model != "longcat-2.5-preview" {
		t.Errorf("WEAKNESS_EXTRACTOR_MODEL must be dead, got model %q", e.Model)
	}
}

// --- dispatch identity -----------------------------------------------------

func TestDispatchIdentity(t *testing.T) {
	for crew, want := range map[string][2]string{
		CrewKindDiagnoser: {"companion_diagnose", "chora-companion-diagnoser"},
		CrewKindExtractor: {"companion_extract", "chora-companion-extractor"},
	} {
		if DispatchRole(crew) != want[0] || ServiceName(crew) != want[1] {
			t.Errorf("%s: role/service = %q/%q", crew, DispatchRole(crew), ServiceName(crew))
		}
	}
	if DispatchRole("weakness_diagnoser") != "" {
		t.Errorf("the old crew kind must map to no role (the lane is retired, never aliased)")
	}
	if CrewSurface != "companion_diagnosis" {
		t.Errorf("CrewSurface = %q", CrewSurface)
	}
	gw := gatewayConfig(Config{CrewKind: CrewKindExtractor, GatewayEndpoint: "g:443", Model: "m", GatewayTenantID: "t", GatewayGCID: "g"})
	if gw.Surface != "companion_diagnosis" || gw.AgentID != "companion_extractor" || gw.CrewKind != "companion_diagnosis" {
		t.Errorf("gateway config identity: %+v", gw)
	}
}

func TestRun_refusesArgsBeforeConfig(t *testing.T) {
	minimalBootEnv(t)
	if err := RunDiagnoser(context.Background(), []string{"web", "-port", "8080", "agentengine"}); err == nil || !strings.Contains(err.Error(), "takes no arguments") {
		t.Errorf("want the args refusal first, got %v", err)
	}
	minimalBootEnv(t)
	t.Setenv("CHORA_GATEWAY_GCID", "")
	if err := RunExtractor(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "CHORA_GATEWAY_TENANT_ID + CHORA_GATEWAY_GCID required") {
		t.Errorf("want the config guard with no args, got %v", err)
	}
}

// --- plugins ----------------------------------------------------------------

func TestNewPlugins_chainAndIdentity(t *testing.T) {
	d, err := NewPlugins(CrewKindDiagnoser)
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewPlugins(CrewKindExtractor)
	if err != nil {
		t.Fatal(err)
	}
	if len(d) != 3 || len(e) != 4 {
		t.Fatalf("chain lengths: diagnoser %d (want 3), extractor %d (want 4, with grounding)", len(d), len(e))
	}
	names := func(ps []any) []string { return nil }
	_ = names
	if !strings.Contains(e[2].Name(), "grounding") {
		t.Errorf("the extractor's 3rd plugin must be the grounding plugin (FileData injection), got %q", e[2].Name())
	}
	for _, p := range d {
		if strings.Contains(p.Name(), "grounding") {
			t.Errorf("the diagnoser must not ground: %q", p.Name())
		}
	}
	tc := TerminationConfig(CrewKindDiagnoser)
	if tc.AgentID != "companion_diagnoser" || tc.CrewKind != "companion_diagnosis" || tc.MaxIterations != 6 {
		t.Errorf("diagnoser termination config = %+v", tc)
	}
	if te := TerminationConfig(CrewKindExtractor); te.AgentID != "companion_extractor" || te.MaxIterations != 3 {
		t.Errorf("extractor termination config = %+v", te)
	}
}

// --- agents: structure + providers ------------------------------------------

func TestNewAgents_treeShape(t *testing.T) {
	cfg := Config{PromptVersion: "v1"}
	ex, err := NewExtractorAgent(cfg, fakeLLM{"fake"})
	if err != nil || ex.Name() != "companion_extractor" {
		t.Fatalf("extractor: %v %v", ex, err)
	}
	dg, err := NewDiagnoserAgent(cfg, fakeLLM{"fake"})
	if err != nil || dg.Name() != "companion_diagnoser" {
		t.Fatalf("diagnoser: %v %v", dg, err)
	}
	subs := dg.SubAgents()
	if len(subs) != 2 || subs[0].Name() != singleTaskAgentName || subs[1].Name() != practiceAgentName {
		t.Fatalf("diagnoser sub-agents = %v", subs)
	}
	loop := subs[1].SubAgents()
	if len(loop) != 3 || loop[0].Name() != actorAgentName || loop[1].Name() != criticAgentName || loop[2].Name() != gateAgentName {
		t.Errorf("practice loop = %v", loop)
	}
}

func TestExtractorProvider_requiresAnObjectStoreReference(t *testing.T) {
	p := extractorInstructionProvider()
	var perm *agentdispatch.PermanentError
	if _, err := p(newFakeCtx(nil)); !errors.As(err, &perm) || !strings.Contains(err.Error(), "missing_source_blob_uri") {
		t.Errorf("no reference: want permanent missing_source_blob_uri, got %v", err)
	}
	if _, err := p(newFakeCtx(map[string]any{"artifact_b64": "AAAA"})); !errors.As(err, &perm) || !strings.Contains(err.Error(), "unsupported_artifact_source") {
		t.Errorf("inline bytes: want permanent unsupported_artifact_source, got %v", err)
	}
	if _, err := p(newFakeCtx(map[string]any{"source_blob_uri": "https://x/y.png"})); !errors.As(err, &perm) || !strings.Contains(err.Error(), "invalid_source_blob_uri") {
		t.Errorf("non gs uri: got %v", err)
	}
	got, err := p(newFakeCtx(map[string]any{"source_blob_uri": "gs://bucket/obj.png", "source_mime_type": "image/png"}))
	if err != nil || !strings.Contains(got, "gs://bucket/obj.png") || !strings.Contains(got, "image/png") {
		t.Errorf("gs reference: %v / %q", err, got)
	}
}

func TestSingleTaskProvider_routesByTaskKind(t *testing.T) {
	p := singleTaskInstructionProvider()
	var perm *agentdispatch.PermanentError
	if _, err := p(newFakeCtx(map[string]any{"task_kind": "bogus"})); !errors.As(err, &perm) || !strings.Contains(err.Error(), "unknown_task_kind: bogus") {
		t.Errorf("unknown task_kind: got %v", err)
	}
	if _, err := p(newFakeCtx(map[string]any{})); !errors.As(err, &perm) || !strings.Contains(err.Error(), "missing_extracted_text") {
		t.Errorf("diagnose without text: got %v", err)
	}
	got, err := p(newFakeCtx(map[string]any{"extracted_text": "item 1 [WRONG]", "clues_block": "BEGIN LEARNER CLUES\nx\nEND LEARNER CLUES"}))
	if err != nil || !strings.Contains(got, "item 1 [WRONG]") || !strings.Contains(got, "BEGIN LEARNER CLUES") || !strings.Contains(got, `"edges"`) {
		t.Errorf("diagnose prompt: %v / %q", err, got)
	}
	got, err = p(newFakeCtx(map[string]any{"task_kind": "study_aids", "edges_json": `[{"concept_key":"k","concept_label":"Label","descriptor_json":"{\"summary\":\"S\"}"}]`}))
	if err != nil || !strings.Contains(got, `"cheat_sheet"`) || !strings.Contains(got, "- Label: S") {
		t.Errorf("study aids prompt: %v / %q", err, got)
	}
	if _, err := p(newFakeCtx(map[string]any{"task_kind": "study_aids"})); !errors.As(err, &perm) || !strings.Contains(err.Error(), "invalid_edges_json") {
		t.Errorf("study aids without edges: got %v", err)
	}
	if _, err := p(newFakeCtx(map[string]any{"task_kind": "practice_test"})); !errors.As(err, &perm) {
		t.Errorf("practice_test must never be served by the single-shot agent: got %v", err)
	}
}

const edgesJSON = `[{"concept_key":"k1","concept_label":"One","descriptor_json":"{\"summary\":\"s1\"}"},{"concept_key":"k2","concept_label":"Two","descriptor_json":""}]`

func TestPracticeGate_decisions(t *testing.T) {
	actor := `{"questions":[{"stem":"Q1","question_type":"mcq","options":["a","b","c","d"],"answer":"a","edge_key":"k1"},{"stem":"Q2","question_type":"oe","answer":"b","edge_key":"k2"}]}`

	// accepted -> final render, escalate
	out, err := decidePractice(newFakeState(map[string]any{"edges_json": edgesJSON, "practice_actor_raw": actor,
		"practice_critic_raw": `{"verdicts":[{"index":0,"accepted":true},{"index":1,"accepted":false,"note":"vague"}]}`}))
	if err != nil || !out.Escalate || out.Text == "" {
		t.Fatalf("accepted: %+v %v", out, err)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(out.Text), &m)
	if qs, _ := m["questions"].([]any); len(qs) != 1 {
		t.Errorf("accepted render = %s", out.Text)
	}

	// all rejected, first round -> regenerate with notes, no text, no escalate
	out, err = decidePractice(newFakeState(map[string]any{"edges_json": edgesJSON, "practice_actor_raw": actor,
		"practice_critic_raw": `{"verdicts":[{"index":0,"accepted":false,"note":"off-topic"},{"index":1,"accepted":false,"note":"vague"}]}`}))
	if err != nil || out.Escalate || out.Text != "" || out.StateDelta["practice_prior_notes"] != "off-topic; vague" || out.StateDelta["practice_round"] != 1 {
		t.Errorf("regenerate: %+v %v", out, err)
	}

	// all rejected, budget exhausted -> empty result with reason, escalate
	out, err = decidePractice(newFakeState(map[string]any{"edges_json": edgesJSON, "practice_actor_raw": actor, "practice_round": 1,
		"practice_critic_raw": `{"verdicts":[{"index":0,"accepted":false,"note":"still off"},{"index":1,"accepted":false}]}`}))
	_ = json.Unmarshal([]byte(out.Text), &m)
	if err != nil || !out.Escalate || m["rejected_reason"] == nil || !strings.Contains(m["rejected_reason"].(string), "2 rounds") {
		t.Errorf("exhausted: %+v %v", out, err)
	}

	// actor produced nothing on-edge -> end at once (no regenerate)
	out, err = decidePractice(newFakeState(map[string]any{"edges_json": edgesJSON, "practice_actor_raw": `{"questions":[{"stem":"x","edge_key":"k9"}]}`}))
	_ = json.Unmarshal([]byte(out.Text), &m)
	if err != nil || !out.Escalate || m["rejected_reason"] == nil {
		t.Errorf("non-producing actor: %+v %v", out, err)
	}

	// unreadable critic, first round -> regenerate with the unreadable note
	out, _ = decidePractice(newFakeState(map[string]any{"edges_json": edgesJSON, "practice_actor_raw": actor, "practice_critic_raw": "not json"}))
	if out.Escalate || out.StateDelta["practice_prior_notes"] != "critic verdict unreadable" {
		t.Errorf("unreadable critic: %+v", out)
	}

	// bad edges -> permanent
	var perm *agentdispatch.PermanentError
	if _, err := decidePractice(newFakeState(map[string]any{"edges_json": "nope"})); !errors.As(err, &perm) {
		t.Errorf("bad edges must be permanent: %v", err)
	}
	_ = dagent.MaxRegenRounds
}

func TestDiagnoserConditions_carryTaskKind(t *testing.T) {
	c := diagnoserConditions(newFakeState(map[string]any{"task_kind": "study_aids", "clues_block": "x"}))
	if c["task_kind"] != "study_aids" || c["has_clues"] != "true" {
		t.Errorf("conditions = %v", c)
	}
	if c := diagnoserConditions(newFakeState(map[string]any{"task_kind": "bogus"})); c["task_kind"] != "unknown" {
		t.Errorf("unknown kind must surface as unknown, got %v", c)
	}
}

func TestNewGatewayLLM_refusesAnIncompleteConfigAndNamesTheBinary(t *testing.T) {
	// modelgatewayclient validates before minting a token, so no credentials
	// are needed; the wrap must name the binary, model and endpoint.
	_, err := NewGatewayLLM(context.Background(), Config{CrewKind: CrewKindExtractor, GatewayEndpoint: "gateway.chora.site:443", Model: "longcat-2.5-preview"})
	if err == nil {
		t.Fatal("NewGatewayLLM built a client with no tenant and no GCID")
	}
	for _, want := range []string{"modelgatewayclient.New", CrewKindExtractor, "longcat-2.5-preview", "gateway.chora.site:443"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestInboundTracePlugin_isANoOpWithoutATraceparent(t *testing.T) {
	p, err := NewInboundTracePlugin(CrewKindDiagnoser)
	if err != nil || !strings.Contains(p.Name(), CrewKindDiagnoser) {
		t.Fatalf("plugin: %v %v", p, err)
	}
	// With a traceparent the callback links the inbound span; without one it
	// must do nothing. Both paths return no content and no error: a trace
	// link is observability, never a reason to fail a run.
	for _, st := range []map[string]any{{}, {"traceparent": "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01", "tracestate": "chora=1"}} {
		content, err := p.BeforeAgentCallback()(newFakeCallbackCtx(st))
		if content != nil || err != nil {
			t.Errorf("state %v: content=%v err=%v", st, content, err)
		}
	}
}

// --- run(): the composition, with the process collaborators faked ----------

func withFakedProcess(t *testing.T, serve func(context.Context, agentdispatch.ServeConfig, agentdispatch.RunOptions) error) {
	t.Helper()
	oldTracing, oldLLM, oldServe := initTracing, newGatewayLLM, serveSubscriber
	initTracing = func(context.Context, string) (func(context.Context) error, error) {
		return func(context.Context) error { return nil }, nil
	}
	newGatewayLLM = func(context.Context, Config) (adkmodel.LLM, error) { return fakeLLM{"fake"}, nil }
	serveSubscriber = serve
	t.Cleanup(func() {
		initTracing, newGatewayLLM, serveSubscriber = oldTracing, oldLLM, oldServe
	})
}

func TestRun_composesTheLaneIdentityAndReturnsTheSubscriberError(t *testing.T) {
	minimalBootEnv(t)
	var got agentdispatch.ServeConfig
	errStop := errors.New("subscription gone")
	withFakedProcess(t, func(_ context.Context, cfg agentdispatch.ServeConfig, _ agentdispatch.RunOptions) error {
		got = cfg
		return errStop
	})
	err := RunDiagnoser(context.Background(), nil)
	if !errors.Is(err, errStop) {
		t.Fatalf("the subscriber's error must reach main (pod death), got %v", err)
	}
	if got.AgentRole != "companion_diagnose" || got.ServiceName != "chora-companion-diagnoser" ||
		got.RootAgent == nil || got.RootAgent.Name() != "companion_diagnoser" || got.Sessions == nil || len(got.Plugins.Plugins) != 3 {
		t.Errorf("diagnoser serve config = role %q service %q root %v plugins %d", got.AgentRole, got.ServiceName, got.RootAgent, len(got.Plugins.Plugins))
	}
	withFakedProcess(t, func(_ context.Context, cfg agentdispatch.ServeConfig, _ agentdispatch.RunOptions) error {
		got = cfg
		return nil
	})
	if err := RunExtractor(context.Background(), nil); err != nil {
		t.Fatalf("clean subscriber exit must return nil, got %v", err)
	}
	if got.AgentRole != "companion_extract" || got.ServiceName != "chora-companion-extractor" || got.RootAgent.Name() != "companion_extractor" || len(got.Plugins.Plugins) != 4 {
		t.Errorf("extractor serve config = role %q service %q root %v plugins %d", got.AgentRole, got.ServiceName, got.RootAgent.Name(), len(got.Plugins.Plugins))
	}
}

func TestRun_failsBeforeServingWhenACollaboratorCannotStart(t *testing.T) {
	minimalBootEnv(t)
	served := false
	withFakedProcess(t, func(context.Context, agentdispatch.ServeConfig, agentdispatch.RunOptions) error {
		served = true
		return nil
	})
	boom := errors.New("no gateway")
	newGatewayLLM = func(context.Context, Config) (adkmodel.LLM, error) { return nil, boom }
	if err := RunDiagnoser(context.Background(), nil); !errors.Is(err, boom) || served {
		t.Errorf("gateway failure: err=%v served=%v", err, served)
	}
	newGatewayLLM = func(context.Context, Config) (adkmodel.LLM, error) { return fakeLLM{"fake"}, nil }
	initTracing = func(context.Context, string) (func(context.Context) error, error) {
		return nil, errors.New("no trace exporter")
	}
	if err := RunExtractor(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "tracing.Init") || served {
		t.Errorf("tracing failure: err=%v served=%v", err, served)
	}
}
