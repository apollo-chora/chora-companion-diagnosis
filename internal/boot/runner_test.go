package boot

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"strings"
	"testing"

	"google.golang.org/adk/agent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	"github.com/apollo-chora/chora-adk-common/agentdispatch"
)

// scriptedLLM answers each GenerateContent call with the next scripted text
// and records the request, so a runner-driven test can prove the task router,
// the practice loop (actor -> critic -> gate, one regenerate) and the critic
// skip without a network. The plugin chain is not under test here (it is
// pinned by TestNewPlugins_chainAndIdentity); the agent tree is.
type scriptedLLM struct {
	answers []string
	calls   []*adkmodel.LLMRequest
}

func (s *scriptedLLM) Name() string { return "scripted" }
func (s *scriptedLLM) GenerateContent(_ context.Context, req *adkmodel.LLMRequest, _ bool) iter.Seq2[*adkmodel.LLMResponse, error] {
	return func(yield func(*adkmodel.LLMResponse, error) bool) {
		s.calls = append(s.calls, req)
		if len(s.answers) == 0 {
			yield(nil, errors.New("scriptedLLM: no answer scripted for this call"))
			return
		}
		text := s.answers[0]
		s.answers = s.answers[1:]
		yield(&adkmodel.LLMResponse{Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: text}}}}, nil)
	}
}

func systemInstruction(req *adkmodel.LLMRequest) string {
	if req == nil || req.Config == nil || req.Config.SystemInstruction == nil {
		return ""
	}
	var b strings.Builder
	for _, p := range req.Config.SystemInstruction.Parts {
		if p != nil {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// runRoot runs the root agent once over a fresh session seeded with state and
// returns the last non-partial text event (the dispatch terminal text) and the
// run error, mirroring agentdispatch.RunnerAgentRun.
func runRoot(t *testing.T, root agent.Agent, state map[string]any) (string, error) {
	t.Helper()
	ctx := context.Background()
	svc := session.InMemoryService()
	const app, user, sid = "companion_diagnosis_test", "u1", "s1"
	if _, err := svc.Create(ctx, &session.CreateRequest{AppName: app, UserID: user, SessionID: sid, State: state}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	r, err := runner.New(runner.Config{AppName: app, Agent: root, SessionService: svc})
	if err != nil {
		t.Fatalf("runner.New: %v", err)
	}
	var events []*session.Event
	for ev, err := range r.Run(ctx, user, sid, &genai.Content{Role: "user", Parts: []*genai.Part{{Text: "BEGIN"}}}, agent.RunConfig{}) {
		if err != nil {
			return "", err
		}
		events = append(events, ev)
	}
	text, err := agentdispatch.TerminalText(events, agentdispatch.TerminalAuthor(DispatchRoleDiagnose))
	return text, err
}

const runnerEdges = `[{"concept_key":"k1","concept_label":"One","descriptor_json":"{\"summary\":\"s1\"}"},{"concept_key":"k2","concept_label":"Two","descriptor_json":""}]`

func TestRunner_diagnoseAndStudyAidsAreSingleShot(t *testing.T) {
	llm := &scriptedLLM{answers: []string{`{"edges":[]}`}}
	root, err := NewDiagnoserAgent(Config{PromptVersion: "v1"}, llm)
	if err != nil {
		t.Fatal(err)
	}
	got, err := runRoot(t, root, map[string]any{"extracted_text": "Q1 [WRONG]", "tenant_id": "t", "user_gcid": "g"})
	if err != nil || got != `{"edges":[]}` || len(llm.calls) != 1 {
		t.Fatalf("diagnose: text=%q err=%v calls=%d", got, err, len(llm.calls))
	}
	if si := systemInstruction(llm.calls[0]); !strings.Contains(si, "EXTRACTED ARTIFACT TEXT") || !strings.Contains(si, "Q1 [WRONG]") {
		t.Errorf("diagnose instruction not composed from state: %q", si[:min(len(si), 200)])
	}

	llm = &scriptedLLM{answers: []string{`{"advice":"go","glossary":[],"cheat_sheet":[]}`}}
	root, _ = NewDiagnoserAgent(Config{PromptVersion: "v1"}, llm)
	got, err = runRoot(t, root, map[string]any{"task_kind": "study_aids", "edges_json": runnerEdges})
	if err != nil || !strings.Contains(got, `"advice"`) || len(llm.calls) != 1 {
		t.Fatalf("study_aids: text=%q err=%v calls=%d", got, err, len(llm.calls))
	}
	if si := systemInstruction(llm.calls[0]); !strings.Contains(si, "Growth Edges to support:") || !strings.Contains(si, "- One: s1") {
		t.Errorf("study aids instruction: %q", si[:min(len(si), 200)])
	}
}

func TestRunner_practiceTestAcceptedInRoundOne(t *testing.T) {
	actor := `{"questions":[{"stem":"Q1","question_type":"mcq","options":["a","b","c","d"],"answer":"a","explanation":"e","edge_key":"k1"}]}`
	llm := &scriptedLLM{answers: []string{actor, `{"verdicts":[{"index":0,"accepted":true,"note":"ok"}]}`}}
	root, _ := NewDiagnoserAgent(Config{PromptVersion: "v1"}, llm)
	got, err := runRoot(t, root, map[string]any{"task_kind": "practice_test", "edges_json": runnerEdges, "max_questions": "4"})
	if err != nil {
		t.Fatal(err)
	}
	if len(llm.calls) != 2 {
		t.Fatalf("want actor + critic = 2 model calls, got %d", len(llm.calls))
	}
	if si := systemInstruction(llm.calls[0]); !strings.Contains(si, "Build up to 4 practice questions") {
		t.Errorf("actor instruction: %q", si[:min(len(si), 160)])
	}
	if si := systemInstruction(llm.calls[1]); !strings.Contains(si, "Critique each candidate question") || !strings.Contains(si, `"stem":"Q1"`) {
		t.Errorf("critic instruction: %q", si[:min(len(si), 160)])
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("terminal text is not JSON: %v %q", err, got)
	}
	if qs, _ := m["questions"].([]any); len(qs) != 1 || m["title"] != "Practice set: One +1 more" {
		t.Errorf("practice result = %s", got)
	}
}

func TestRunner_practiceTestRegeneratesOnceThenSucceeds(t *testing.T) {
	actor := `{"questions":[{"stem":"Q1","question_type":"oe","answer":"a","edge_key":"k1"}]}`
	llm := &scriptedLLM{answers: []string{
		actor, `{"verdicts":[{"index":0,"accepted":false,"note":"ambiguous"}]}`, // round 1: rejected
		actor, `{"verdicts":[{"index":0,"accepted":true}]}`, // round 2: accepted
	}}
	root, _ := NewDiagnoserAgent(Config{PromptVersion: "v1"}, llm)
	got, err := runRoot(t, root, map[string]any{"task_kind": "practice_test", "edges_json": runnerEdges})
	if err != nil {
		t.Fatal(err)
	}
	if len(llm.calls) != 4 {
		t.Fatalf("want 4 model calls (actor, critic, actor, critic), got %d", len(llm.calls))
	}
	if si := systemInstruction(llm.calls[2]); !strings.Contains(si, "The previous attempt was rejected") || !strings.Contains(si, "ambiguous") {
		t.Errorf("the regenerate actor turn must carry the critic notes: %q", si[len(si)-200:])
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(got), &m)
	if qs, _ := m["questions"].([]any); len(qs) != 1 || m["rejected_reason"] != nil {
		t.Errorf("regenerated result = %s", got)
	}
}

func TestRunner_practiceTestExhaustedRoundsEndsEmptyWithReason(t *testing.T) {
	actor := `{"questions":[{"stem":"Q1","question_type":"oe","answer":"a","edge_key":"k1"}]}`
	llm := &scriptedLLM{answers: []string{
		actor, `{"verdicts":[{"index":0,"accepted":false,"note":"off"}]}`,
		actor, `{"verdicts":[{"index":0,"accepted":false,"note":"still off"}]}`,
	}}
	root, _ := NewDiagnoserAgent(Config{PromptVersion: "v1"}, llm)
	got, err := runRoot(t, root, map[string]any{"task_kind": "practice_test", "edges_json": runnerEdges})
	if err != nil || len(llm.calls) != 4 {
		t.Fatalf("err=%v calls=%d", err, len(llm.calls))
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(got), &m)
	if qs, _ := m["questions"].([]any); len(qs) != 0 || !strings.Contains(m["rejected_reason"].(string), "2 rounds") {
		t.Errorf("exhausted result = %s", got)
	}
}

func TestRunner_nonProducingActorSkipsTheCritic(t *testing.T) {
	llm := &scriptedLLM{answers: []string{`{"questions":[{"stem":"x","edge_key":"k9"}]}`}}
	root, _ := NewDiagnoserAgent(Config{PromptVersion: "v1"}, llm)
	got, err := runRoot(t, root, map[string]any{"task_kind": "practice_test", "edges_json": runnerEdges})
	if err != nil {
		t.Fatal(err)
	}
	if len(llm.calls) != 1 {
		t.Fatalf("the critic must be skipped when there is nothing to critique; model calls = %d", len(llm.calls))
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(got), &m)
	if m["rejected_reason"] == nil {
		t.Errorf("non-producing actor result = %s", got)
	}
}

func TestRunner_unknownTaskKindIsPermanentBeforeAnyModelCall(t *testing.T) {
	llm := &scriptedLLM{answers: []string{`never`}}
	root, _ := NewDiagnoserAgent(Config{PromptVersion: "v1"}, llm)
	_, err := runRoot(t, root, map[string]any{"task_kind": "bogus", "edges_json": runnerEdges})
	var perm *agentdispatch.PermanentError
	if !errors.As(err, &perm) || !strings.Contains(err.Error(), "unknown_task_kind: bogus") {
		t.Fatalf("want a permanent unknown_task_kind error, got %v", err)
	}
	if len(llm.calls) != 0 {
		t.Errorf("no model call may happen for an unknown task_kind, got %d", len(llm.calls))
	}
}

func TestRunner_extractorTranscribesFromAGsReference(t *testing.T) {
	llm := &scriptedLLM{answers: []string{"1. What is 2+2? Learner: 5 [WRONG]"}}
	root, err := NewExtractorAgent(Config{PromptVersion: "v1"}, llm)
	if err != nil {
		t.Fatal(err)
	}
	got, err := runRoot(t, root, map[string]any{"source_blob_uri": "gs://chora-consumption-growth-uploads-dev/u/1.png", "source_mime_type": "image/png"})
	if err != nil || got != "1. What is 2+2? Learner: 5 [WRONG]" || len(llm.calls) != 1 {
		t.Fatalf("extractor: text=%q err=%v calls=%d", got, err, len(llm.calls))
	}
	if si := systemInstruction(llm.calls[0]); !strings.Contains(si, "gs://chora-consumption-growth-uploads-dev/u/1.png") || !strings.Contains(si, "image/png") {
		t.Errorf("extractor instruction lacks the artifact reference: %q", si[len(si)-200:])
	}
	_, err = runRoot(t, root, map[string]any{"artifact_b64": "AAAA"})
	var perm *agentdispatch.PermanentError
	if !errors.As(err, &perm) || !strings.Contains(err.Error(), "unsupported_artifact_source") {
		t.Errorf("inline bytes must be a permanent refusal, got %v", err)
	}
}

func TestDispatchServeConfigAndLogAttrs(t *testing.T) {
	root, _ := NewExtractorAgent(Config{PromptVersion: "v1"}, &scriptedLLM{})
	sc, err := dispatchServeConfig(Config{CrewKind: CrewKindExtractor, AppName: "app-1"}, root, nil)
	if err != nil || sc.AgentRole != "companion_extract" || sc.ServiceName != "chora-companion-extractor" || sc.AppName != "app-1" || sc.Sessions == nil {
		t.Errorf("serve config = %+v, %v", sc, err)
	}
	if _, err := dispatchServeConfig(Config{CrewKind: "nope"}, root, nil); err == nil {
		t.Errorf("unknown crew kind must be refused")
	}
	attrs := Config{CrewKind: CrewKindDiagnoser, GatewayGCID: "0190a1b2-c3d4-7e5f"}.LogAttrs()
	joined := strings.Join(func() []string {
		var s []string
		for _, a := range attrs {
			s = append(s, strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.Trim(strings.Trim(strings.Trim(strings.Trim(strings.Trim(strings.Trim(strings.TrimSpace(toString(a)), "["), "]"), " "), "\""), "'"), "`"), "\n", " "), "\t", " "), "  ", " ")))
		}
		return s
	}(), " ")
	if !strings.Contains(joined, "0190a1b2...") || strings.Contains(joined, "0190a1b2-c3d4-7e5f") || !strings.Contains(joined, "companion_diagnosis") {
		t.Errorf("LogAttrs must truncate the GCID and name the surface: %s", joined)
	}
}

func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []string:
		return strings.Join(t, ",")
	}
	b, _ := json.Marshal(v)
	return string(b)
}
