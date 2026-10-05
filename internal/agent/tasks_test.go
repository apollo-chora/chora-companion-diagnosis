package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// ADR-254 D2/D6: companion_diagnose carries task_kind in {diagnose,
// study_aids, practice_test}; absent means diagnose (the kennel's live
// payload), anything else is a permanent unknown_task_kind failure.

func TestParseTaskKind(t *testing.T) {
	for raw, want := range map[string]TaskKind{"": TaskDiagnose, "diagnose": TaskDiagnose,
		"study_aids": TaskStudyAids, "practice_test": TaskPracticeTest, " Study_Aids ": TaskStudyAids} {
		got, err := ParseTaskKind(raw)
		if err != nil || got != want {
			t.Errorf("ParseTaskKind(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	_, err := ParseTaskKind("bogus")
	if err == nil || !strings.Contains(err.Error(), "unknown_task_kind") || !strings.Contains(err.Error(), "bogus") {
		t.Errorf("unknown task kind must fail with unknown_task_kind naming the value, got %v", err)
	}
}

const edgesFixture = `[{"concept_key":"photosynthesis.light","concept_label":"Light capture","descriptor_json":"{\"summary\":\"Where light energy is captured\",\"suggested_angles\":[\"chlorophyll\",\"chloroplasts\"]}"},
{"concept_key":"photosynthesis.inputs","concept_label":"Raw materials","descriptor_json":"{\"summary\":\"CO2 and water\"}"},
{"concept_label":"No key here","descriptor_json":""}]`

func TestParseEdges(t *testing.T) {
	edges, err := ParseEdges(edgesFixture)
	if err != nil || len(edges) != 3 {
		t.Fatalf("ParseEdges: %d edges, %v", len(edges), err)
	}
	if edges[0].ConceptKey != "photosynthesis.light" || edges[0].Summary() != "Where light energy is captured" ||
		strings.Join(edges[0].Angles(), ",") != "chlorophyll,chloroplasts" {
		t.Errorf("edge 0 not parsed: %+v", edges[0])
	}
	if edges[2].Summary() != "" {
		t.Errorf("an empty descriptor must read as an empty summary")
	}
	for _, bad := range []string{"", "not json", "{}", "[]"} {
		if _, err := ParseEdges(bad); err == nil {
			t.Errorf("ParseEdges(%q) must fail: the output tasks need at least one edge", bad)
		}
	}
}

func TestEdgeBriefAndKnownKeys(t *testing.T) {
	edges, _ := ParseEdges(edgesFixture)
	brief := EdgeBrief(edges)
	if !strings.Contains(brief, "- edge_key=photosynthesis.light: Light capture") ||
		!strings.Contains(brief, "Where light energy is captured") ||
		!strings.Contains(brief, "(angles: chlorophyll, chloroplasts)") {
		t.Errorf("brief missing a keyed edge line: %q", brief)
	}
	if strings.Contains(brief, "No key here") {
		t.Errorf("an edge without a concept_key must not appear in the brief (it cannot be targeted)")
	}
	keys := KnownKeys(edges)
	if len(keys) != 2 || !keys["photosynthesis.light"] || !keys["photosynthesis.inputs"] {
		t.Errorf("KnownKeys = %v", keys)
	}
}

func TestComposeStudyAidsInstruction(t *testing.T) {
	edges, _ := ParseEdges(edgesFixture)
	got := ComposeStudyAidsInstruction(edges)
	for _, want := range []string{`"advice"`, `"glossary"`, `"cheat_sheet"`, "STRICT JSON ONLY",
		"Growth Edges to support:", "- Light capture: Where light energy is captured", "- Raw materials: CO2 and water",
		"never mention protected attributes"} {
		if !strings.Contains(got, want) {
			t.Errorf("study aids instruction missing %q", want)
		}
	}
}

func TestComposeActorInstruction(t *testing.T) {
	edges, _ := ParseEdges(edgesFixture)
	got := ComposeActorInstruction(edges, 8, "")
	// n = min(max_questions, max(1, len(keys)) * 2) = min(8, 4) = 4
	for _, want := range []string{"Build up to 4 practice questions", "- edge_key=photosynthesis.light",
		"Use ONLY these edge_key values: [photosynthesis.inputs photosynthesis.light]", `"edge_key"`, "STRICT JSON ONLY"} {
		if !strings.Contains(got, want) {
			t.Errorf("actor instruction missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "previous attempt was rejected") {
		t.Errorf("no prior notes must not mention a previous attempt")
	}
	again := ComposeActorInstruction(edges, 1, "off-topic; ambiguous")
	if !strings.Contains(again, "Build up to 1 practice questions") ||
		!strings.Contains(again, "The previous attempt was rejected for these reasons") ||
		!strings.Contains(again, "off-topic; ambiguous") {
		t.Errorf("regenerate instruction must cap at max_questions and carry the critic notes:\n%s", again)
	}
}

func TestParseJSONObjectIsTolerant(t *testing.T) {
	for _, raw := range []string{
		`{"questions":[]}`,
		"```json\n{\"questions\":[]}\n```",
		"Here you go:\n{\"questions\":[]}\nHope this helps.",
	} {
		if m := ParseJSONObject(raw); m == nil || m["questions"] == nil {
			t.Errorf("ParseJSONObject(%q) = %v", raw, m)
		}
	}
	for _, raw := range []string{"", "not json", "[1,2]"} {
		if m := ParseJSONObject(raw); len(m) != 0 {
			t.Errorf("ParseJSONObject(%q) = %v, want empty", raw, m)
		}
	}
}

func TestNormaliseQuestions(t *testing.T) {
	known := map[string]bool{"k1": true, "k2": true}
	raw := []any{
		map[string]any{"stem": "Q1", "question_type": "MCQ", "options": []any{"a", "b", "c", "d"}, "answer": "a", "explanation": "e", "edge_key": "k1"},
		map[string]any{"stem": "Q2", "question_type": "weird", "answer": "x", "edge_key": "k2"},
		map[string]any{"stem": "off edge", "question_type": "oe", "answer": "x", "edge_key": "k9"},
		map[string]any{"stem": "", "edge_key": "k1"},
		"garbage",
		map[string]any{"stem": "Q5", "question_type": "oe", "answer": "y", "edge_key": "k1"},
	}
	got := NormaliseQuestions(raw, known, 2)
	if len(got) != 2 {
		t.Fatalf("want 2 (cap), got %d: %+v", len(got), got)
	}
	if got[0].QuestionType != "mcq" || len(got[0].Options) != 4 || got[0].EdgeKey != "k1" {
		t.Errorf("mcq not normalised: %+v", got[0])
	}
	if got[1].QuestionType != "oe" || got[1].Options != nil {
		t.Errorf("unknown type must default to oe without options: %+v", got[1])
	}
}

func TestComposeCriticInstructionAndParseVerdicts(t *testing.T) {
	qs := []Question{{Stem: "Q1", QuestionType: "mcq", Answer: "a", EdgeKey: "k1"}, {Stem: "Q2", QuestionType: "oe", Answer: "b", EdgeKey: "k2"}}
	inst := ComposeCriticInstruction(qs)
	for _, want := range []string{"Critique each candidate question", `"index":0`, `"stem":"Q1"`, `"edge_key":"k2"`, `"verdicts"`, "positively framed"} {
		if !strings.Contains(inst, want) {
			t.Errorf("critic instruction missing %q", want)
		}
	}
	v := ParseVerdicts(`{"verdicts":[{"index":0,"accepted":true,"note":"ok"},{"index":1,"accepted":false,"note":"ambiguous"},{"index":"x"}]}`)
	if len(v) != 2 || !v[0].Accepted || v[1].Accepted || v[1].Note != "ambiguous" {
		t.Errorf("verdicts = %+v", v)
	}
	if ParseVerdicts("not json") != nil {
		t.Errorf("unparseable verdicts must be nil (round failed), not an empty accept-all")
	}
}

func TestGateAndRender(t *testing.T) {
	qs := []Question{{Stem: "Q1", QuestionType: "mcq", Options: []string{"a", "b"}, Answer: "a", EdgeKey: "k1"}, {Stem: "Q2", QuestionType: "oe", Answer: "b", EdgeKey: "k2"}}
	accepted, notes := Gate(qs, map[int]Verdict{0: {Accepted: true}, 1: {Accepted: false, Note: "ambiguous"}})
	if len(accepted) != 1 || accepted[0].Stem != "Q1" || notes != "ambiguous" {
		t.Errorf("gate: accepted=%+v notes=%q", accepted, notes)
	}
	edges, _ := ParseEdges(edgesFixture)
	out := RenderPracticeTest(edges, accepted, "")
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("render is not JSON: %v\n%s", err, out)
	}
	if m["title"] != "Practice set: Light capture +2 more" {
		t.Errorf("title = %v", m["title"])
	}
	if qsOut, _ := m["questions"].([]any); len(qsOut) != 1 {
		t.Errorf("questions = %v", m["questions"])
	}
	if _, has := m["rejected_reason"]; has {
		t.Errorf("rejected_reason must be absent when questions were accepted")
	}
	empty := RenderPracticeTest(edges, nil, "critic rejected every candidate in 2 rounds")
	_ = json.Unmarshal([]byte(empty), &m)
	if qsOut, _ := m["questions"].([]any); len(qsOut) != 0 || m["rejected_reason"] == nil {
		t.Errorf("empty render must carry questions [] and rejected_reason: %s", empty)
	}
	if strings.Contains(out, "—") || strings.Contains(empty, "—") {
		t.Errorf("no em dashes in learner-facing output")
	}
}

func TestParseVerdicts_indexShapes(t *testing.T) {
	v := ParseVerdicts(`{"verdicts":[{"index":"1","accepted":true},{"index":2.0,"accepted":false,"note":7},{"index":true}]}`)
	if len(v) != 2 || !v[1].Accepted || v[2].Note != "7" {
		t.Errorf("verdicts = %+v", v)
	}
}

func TestNormaliseQuestions_coercesScalars(t *testing.T) {
	known := map[string]bool{"k1": true}
	got := NormaliseQuestions([]any{map[string]any{"stem": "Q", "question_type": "mcq", "options": []any{1, true, "x", map[string]any{"a": 1}}, "answer": 42, "explanation": false, "edge_key": "k1"}}, known, 5)
	if len(got) != 1 || got[0].Answer != "42" || got[0].Explanation != "false" || len(got[0].Options) != 4 {
		t.Errorf("coerced = %+v", got)
	}
	if MaxQuestionsFrom("") != DefaultMaxQuestions || MaxQuestionsFrom("-3") != DefaultMaxQuestions || MaxQuestionsFrom(" 3 ") != 3 {
		t.Errorf("MaxQuestionsFrom defaults wrong")
	}
}
