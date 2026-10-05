package agent

// conditions.go — ADR-197 M-A prompt-composition condition extractors for the
// weakness-analyser crew (mirrors qgen_adk_go/internal/agent/conditions.go).
//
// These surface the prompt-shaping DISCRIMINANTS that VARY the composed prompt —
// NOT the learner's artifact content, clues, or PII — so O+ can render "which
// conditions produced this Growth-Edge map" (IMDA D2 explainability). They are
// pure + best-effort: a key the orchestrator did not seed renders as its safe
// default (never an error); the per-turn InstructionProvider already fail-loud-
// guards the REQUIRED keys (extracted_text / an artifact reference) before
// promptstamping.WithStamping ever calls these.
//
// The parameter is the crew-local stateGetter (Get-only); google.golang.org/adk/
// session.ReadonlyState satisfies it, so each cmd/*/main.go wraps these into a
// promptstamping.ConditionExtractor.

import "strings"

// stateGetter is the minimal Get-only view of ADK session state these extractors
// need. session.ReadonlyState satisfies it (it has Get(string) (any, error)).
type stateGetter interface {
	Get(string) (any, error)
}

// DiagnoserConditions extracts the weakness_diagnoser prompt discriminants. The
// ONLY discriminant that changes the composed prompt is whether a LEARNER CLUES
// block was threaded in — ComposeDiagnoserInstruction appends it only when
// present. The required extracted_text + the clues body are CONTENT/PII and are
// deliberately NOT surfaced as conditions.
func DiagnoserConditions(state stateGetter) map[string]string {
	return map[string]string{
		"has_clues": boolStr(stateNonEmpty(state, "clues_block")),
	}
}

// ExtractorConditions extracts the weakness_extractor prompt discriminants. The
// transcription approach keys off the artifact's MIME type (marked test vs notes
// vs scribble vs source material), so source_mime_type is surfaced; whether the
// artifact arrives as a gs:// blob or inline base64 is the artifact_source
// discriminant (ComposeExtractorInstruction renders the blob URI vs the literal
// "<inline base64 artifact>"). The blob URI / inline bytes themselves are
// identifiers/content and are NOT surfaced.
func ExtractorConditions(state stateGetter) map[string]string {
	mime := strings.TrimSpace(stateStr(state, "source_mime_type"))
	if mime == "" {
		mime = "unspecified"
	}
	return map[string]string{
		"source_mime_type": mime,
		"artifact_source":  artifactSource(state),
	}
}

// artifactSource reports whether the artifact arrives as a gs:// blob or inline
// base64 — "blob", "inline", or "unspecified" (no reference present, in which
// case the InstructionProvider has already failed the turn loud).
func artifactSource(state stateGetter) string {
	switch {
	case stateNonEmpty(state, "source_blob_uri"):
		return "blob"
	case stateNonEmpty(state, "artifact_b64"):
		return "inline"
	default:
		return "unspecified"
	}
}

// stateStr reads a string-valued state key, returning "" on miss / type mismatch
// — the same defensive posture as cmd/*/main.go stateString.
func stateStr(state stateGetter, key string) string {
	v, err := state.Get(key)
	if err != nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// stateNonEmpty reports whether a string-valued state key is present and not
// blank (whitespace trimmed) — mirrors the TrimSpace guards the composers use.
func stateNonEmpty(state stateGetter, key string) bool {
	return strings.TrimSpace(stateStr(state, key)) != ""
}

// boolStr renders a bool as the "true"/"false" string condition values O+ reads.
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
