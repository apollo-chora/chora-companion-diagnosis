package agent

// tasks.go: the companion_diagnose payload discriminator (ADR-254 D2/D6) and
// the two output tasks folded into this crew from the kennel's weakness
// outputs (study aids, practice test). Pure and IO-free: prompt composition,
// tolerant JSON parsing, the on-edge gate and the final render are all
// testable without a model.
//
// Wire contract (binding, ADR-254 D6 addendum): input_payload.task_kind absent
// or "diagnose" reads extracted_text (+ optional clues_block); "study_aids" and
// "practice_test" read edges_json, a JSON array of the published edges
// ({concept_key, concept_label, descriptor_json}); practice_test also reads an
// optional max_questions (default 8). Any other value is a permanent
// unknown_task_kind failure reported on the wire as FAILED.

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// TaskKind is the companion_diagnose payload discriminator.
type TaskKind string

const (
	TaskDiagnose     TaskKind = "diagnose"
	TaskStudyAids    TaskKind = "study_aids"
	TaskPracticeTest TaskKind = "practice_test"
)

// ErrUnknownTaskKind is the permanent-failure reason token for a task_kind
// outside the contract; the wire carries "unknown_task_kind: <value>".
var ErrUnknownTaskKind = errors.New("unknown_task_kind")

// ParseTaskKind maps the raw payload value to a TaskKind. Absent means
// diagnose, which is what the kennel's live payload sends; anything outside
// the contract is refused by name.
func ParseTaskKind(raw string) (TaskKind, error) {
	switch v := strings.ToLower(strings.TrimSpace(raw)); v {
	case "", string(TaskDiagnose):
		return TaskDiagnose, nil
	case string(TaskStudyAids):
		return TaskStudyAids, nil
	case string(TaskPracticeTest):
		return TaskPracticeTest, nil
	default:
		return "", fmt.Errorf("%w: %s", ErrUnknownTaskKind, v)
	}
}

// Edge is one published Growth Edge as the kennel carries it.
type Edge struct {
	ConceptKey     string `json:"concept_key"`
	ConceptLabel   string `json:"concept_label"`
	DescriptorJSON string `json:"descriptor_json"`
}

type descriptor struct {
	Summary         string   `json:"summary"`
	SuggestedAngles []string `json:"suggested_angles"`
}

func (e Edge) descriptor() descriptor {
	var d descriptor
	if strings.TrimSpace(e.DescriptorJSON) == "" {
		return d
	}
	_ = json.Unmarshal([]byte(e.DescriptorJSON), &d) // malformed descriptor reads as empty, as in the kennel
	return d
}

// Summary is the edge's descriptor summary ("" when absent or malformed).
func (e Edge) Summary() string { return strings.TrimSpace(e.descriptor().Summary) }

// Angles are the edge's suggested angles (nil when absent).
func (e Edge) Angles() []string { return e.descriptor().SuggestedAngles }

// ParseEdges decodes edges_json. The output tasks need at least one edge, so
// an empty or malformed list is an error the caller reports as permanent.
func ParseEdges(edgesJSON string) ([]Edge, error) {
	raw := strings.TrimSpace(edgesJSON)
	if raw == "" {
		return nil, errors.New("edges_json is required for this task_kind")
	}
	var edges []Edge
	if err := json.Unmarshal([]byte(raw), &edges); err != nil {
		return nil, fmt.Errorf("edges_json is not a JSON array of edges: %w", err)
	}
	if len(edges) == 0 {
		return nil, errors.New("edges_json carries no edges")
	}
	return edges, nil
}

// KnownKeys is the set of concept keys a practice question may target.
func KnownKeys(edges []Edge) map[string]bool {
	keys := map[string]bool{}
	for _, e := range edges {
		if k := strings.TrimSpace(e.ConceptKey); k != "" {
			keys[k] = true
		}
	}
	return keys
}

// EdgeBrief renders the keyed edges for the actor prompt, exactly as the
// kennel's _edge_brief did: "- edge_key=<key>: <label> (summary) (angles)".
// Edges without a concept_key are skipped: a question cannot target them.
func EdgeBrief(edges []Edge) string {
	var lines []string
	for _, e := range edges {
		key := strings.TrimSpace(e.ConceptKey)
		if key == "" {
			continue
		}
		line := "- edge_key=" + key + ": " + strings.TrimSpace(e.ConceptLabel)
		if s := e.Summary(); s != "" {
			line += ", " + s
		}
		if a := e.Angles(); len(a) > 0 {
			line += " (angles: " + strings.Join(a, ", ") + ")"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// study_aids
// ---------------------------------------------------------------------------

// StudyAidsSystemPrompt is the kennel's _STUDY_AIDS_SYSTEM_PROMPT, ported
// verbatim so the STRICT JSON the kennel's outputs.py parses is unchanged.
const StudyAidsSystemPrompt = "You are an encouraging study coach producing concise, positive study aids " +
	"for a learner's 'Growth Edges' (concepts to grow next, never deficits). " +
	"Given the edges, return STRICT JSON ONLY (no prose, no fences): " +
	`{"advice": str (2-3 warm, actionable sentences), ` +
	`"glossary": [{"term": str, "definition": str}], ` +
	`"cheat_sheet": [str] (short ordered steps)}. ` +
	"Be specific to the edges, constructive, and motivating. Never demotivating, " +
	"never mention protected attributes / disability / medical conditions."

// ComposeStudyAidsInstruction folds the edges onto the study-aids prompt
// ("Growth Edges to support:" + one "- label: summary" line per edge).
func ComposeStudyAidsInstruction(edges []Edge) string {
	var b strings.Builder
	b.WriteString(StudyAidsSystemPrompt)
	b.WriteString("\n\nGrowth Edges to support:\n")
	for _, e := range edges {
		b.WriteString("- ")
		b.WriteString(strings.TrimSpace(e.ConceptLabel))
		b.WriteString(": ")
		b.WriteString(e.Summary())
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// ---------------------------------------------------------------------------
// practice_test: actor -> critic -> on-edge gate, one bounded regenerate
// ---------------------------------------------------------------------------

// DefaultMaxQuestions mirrors the kennel's DEFAULT_MAX_QUESTIONS.
const DefaultMaxQuestions = 8

// MaxRegenRounds: one regenerate after an all-rejected round (the kennel's
// DEFAULT_MAX_REGEN_ROUNDS), so at most two actor and two critic calls.
const MaxRegenRounds = 1

// ActorSystemPrompt is the kennel's _ACTOR_SYSTEM_PROMPT, ported verbatim.
const ActorSystemPrompt = "You are an encouraging assessment designer building a SHORT practice set " +
	"that helps a learner GROW the concepts below (their 'Growth Edges', frame " +
	"everything as growth, never as a deficit). Produce questions grounded ONLY " +
	"in the listed edges. Return STRICT JSON ONLY (no prose, no fences): " +
	`{"questions": [{"stem": str, "question_type": "mcq"|"oe", ` +
	`"options": [str] (4 for mcq; omit for oe), "answer": str, ` +
	`"explanation": str (1-2 supportive sentences), "edge_key": str ` +
	`(EXACTLY one of the provided concept keys)}]}. ` +
	"Each question must target one provided edge_key. Be constructive and " +
	"motivating. Never demotivating; never mention protected attributes, " +
	"disability, or medical conditions."

// CriticSystemPrompt is the kennel's _CRITIC_SYSTEM_PROMPT, ported verbatim.
const CriticSystemPrompt = "You are a strict assessment critic. For each candidate question, decide if " +
	"it is (a) grounded in its stated Growth Edge, (b) unambiguous with a single " +
	"defensible answer, (c) free of bias / protected-attribute inference, and " +
	"(d) positively framed. Return STRICT JSON ONLY (no prose, no fences): " +
	`{"verdicts": [{"index": int (0-based, matching the input order), ` +
	`"accepted": bool, "note": str (terse reason)}]}. ` +
	"Reject anything off-topic, ambiguous, unsafe, or demotivating."

// ComposeActorInstruction builds the actor's full instruction: the system
// prompt plus the kennel's user prompt (n = min(maxQuestions, keys*2), the
// edge brief, the allowed keys, and the critic notes on a regenerate).
func ComposeActorInstruction(edges []Edge, maxQuestions int, priorNotes string) string {
	if maxQuestions < 1 {
		maxQuestions = 1
	}
	keys := KnownKeys(edges)
	n := len(keys)
	if n < 1 {
		n = 1
	}
	n *= 2
	if n > maxQuestions {
		n = maxQuestions
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	var b strings.Builder
	b.WriteString(ActorSystemPrompt)
	b.WriteString("\n\n")
	// strings.Builder writes never fail (its Write is documented to always
	// return a nil error), so the discard is stated rather than implied.
	_, _ = fmt.Fprintf(&b, "Build up to %d practice questions across these Growth Edges:\n%s\n\n", n, EdgeBrief(edges))
	_, _ = fmt.Fprintf(&b, "Use ONLY these edge_key values: [%s].", strings.Join(sorted, " "))
	if notes := strings.TrimSpace(priorNotes); notes != "" {
		b.WriteString("\n\nThe previous attempt was rejected for these reasons, fix them this time: ")
		b.WriteString(notes)
	}
	return b.String()
}

// Question is one normalised practice question.
type Question struct {
	Stem         string   `json:"stem"`
	QuestionType string   `json:"question_type"`
	Options      []string `json:"options,omitempty"`
	Answer       string   `json:"answer"`
	Explanation  string   `json:"explanation"`
	EdgeKey      string   `json:"edge_key"`
}

var fenceRE = regexp.MustCompile("(?s)```(?:json)?\\s*(.+?)\\s*```")

// ParseJSONObject is the kennel's tolerant _parse_json_object: the whole text,
// a fenced block, or the outermost {...}; an empty map when none parses.
func ParseJSONObject(text string) map[string]any {
	t := strings.TrimSpace(text)
	if t == "" {
		return map[string]any{}
	}
	candidates := []string{t}
	if m := fenceRE.FindStringSubmatch(t); len(m) == 2 {
		candidates = append(candidates, m[1])
	}
	if first, last := strings.Index(t, "{"), strings.LastIndex(t, "}"); first != -1 && last > first {
		candidates = append(candidates, t[first:last+1])
	}
	for _, c := range candidates {
		var m map[string]any
		if err := json.Unmarshal([]byte(c), &m); err == nil && m != nil {
			return m
		}
	}
	return map[string]any{}
}

// NormaliseQuestions keeps well-formed ON-EDGE questions (edge_key must be a
// diagnosed key), lowercases the type (unknown defaults to oe), keeps options
// only for mcq, and caps the list. Mirrors the kennel's _normalise_questions.
func NormaliseQuestions(raw []any, known map[string]bool, capN int) []Question {
	var out []Question
	for _, item := range raw {
		q, ok := item.(map[string]any)
		if !ok {
			continue
		}
		stem := strings.TrimSpace(str(q["stem"]))
		edgeKey := strings.TrimSpace(str(q["edge_key"]))
		if stem == "" || !known[edgeKey] {
			continue
		}
		qtype := strings.ToLower(strings.TrimSpace(str(q["question_type"])))
		if qtype != "mcq" && qtype != "oe" {
			qtype = "oe"
		}
		n := Question{Stem: stem, QuestionType: qtype, Answer: str(q["answer"]),
			Explanation: str(q["explanation"]), EdgeKey: edgeKey}
		if qtype == "mcq" {
			if opts, ok := q["options"].([]any); ok {
				for _, o := range opts {
					if s := strings.TrimSpace(str(o)); s != "" {
						n.Options = append(n.Options, str(o))
					}
				}
			}
		}
		out = append(out, n)
		if capN > 0 && len(out) >= capN {
			break
		}
	}
	return out
}

// ParseCandidates extracts and normalises the actor's questions.
func ParseCandidates(actorText string, known map[string]bool, capN int) []Question {
	raw, _ := ParseJSONObject(actorText)["questions"].([]any)
	return NormaliseQuestions(raw, known, capN)
}

// ComposeCriticInstruction builds the critic's full instruction: the system
// prompt plus the kennel's candidate listing (index, stem, type, answer, key).
func ComposeCriticInstruction(candidates []Question) string {
	type row struct {
		Index        int    `json:"index"`
		Stem         string `json:"stem"`
		QuestionType string `json:"question_type"`
		Answer       string `json:"answer"`
		EdgeKey      string `json:"edge_key"`
	}
	rows := make([]row, 0, len(candidates))
	for i, q := range candidates {
		rows = append(rows, row{i, q.Stem, q.QuestionType, q.Answer, q.EdgeKey})
	}
	listing, _ := json.Marshal(rows)
	return CriticSystemPrompt + "\n\nCritique each candidate question and return a verdict per index.\nCandidates:\n" + string(listing)
}

// Verdict is the critic's decision for one candidate index.
type Verdict struct {
	Accepted bool
	Note     string
}

// ParseVerdicts reads {"verdicts":[{index, accepted, note}]}; nil when the
// critic's answer is unusable (the round failed, not an accept-all).
func ParseVerdicts(criticText string) map[int]Verdict {
	raw, ok := ParseJSONObject(criticText)["verdicts"].([]any)
	if !ok {
		return nil
	}
	out := map[int]Verdict{}
	for _, item := range raw {
		v, ok := item.(map[string]any)
		if !ok {
			continue
		}
		idx, ok := asInt(v["index"])
		if !ok {
			continue
		}
		accepted, _ := v["accepted"].(bool)
		out[idx] = Verdict{Accepted: accepted, Note: str(v["note"])}
	}
	return out
}

// Gate keeps the critic-accepted candidates in order and joins the rejection
// notes (the regenerate guidance) with "; ".
func Gate(candidates []Question, verdicts map[int]Verdict) (accepted []Question, notes string) {
	var reasons []string
	for i, q := range candidates {
		v, ok := verdicts[i]
		if ok && v.Accepted {
			accepted = append(accepted, q)
			continue
		}
		if n := strings.TrimSpace(v.Note); n != "" {
			reasons = append(reasons, n)
		}
	}
	return accepted, strings.Join(reasons, "; ")
}

// Title renders the practice set title from the edge labels.
func Title(edges []Edge) string {
	var labels []string
	for _, e := range edges {
		if l := strings.TrimSpace(e.ConceptLabel); l != "" {
			labels = append(labels, l)
		}
	}
	if len(labels) == 0 {
		return "Your practice set"
	}
	t := "Practice set: " + labels[0]
	if len(labels) > 1 {
		t += fmt.Sprintf(" +%d more", len(labels)-1)
	}
	if len(t) > 200 {
		t = t[:200]
	}
	return t
}

// RenderPracticeTest is the completion payload: {title, questions[],
// rejected_reason?}. An all-rejected set is a valid OK result with an empty
// list and the reason, never a FAILED (the output is opt-in, best-effort).
func RenderPracticeTest(edges []Edge, accepted []Question, rejectedReason string) string {
	out := map[string]any{"title": Title(edges), "questions": accepted}
	if accepted == nil {
		out["questions"] = []Question{}
	}
	if r := strings.TrimSpace(rejectedReason); r != "" && len(accepted) == 0 {
		out["rejected_reason"] = r
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// MaxQuestionsFrom parses the optional max_questions payload value.
func MaxQuestionsFrom(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 {
		return DefaultMaxQuestions
	}
	return n
}

func str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

func asInt(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		return int(t), true
	case int:
		return t, true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		return n, err == nil
	}
	return 0, false
}
