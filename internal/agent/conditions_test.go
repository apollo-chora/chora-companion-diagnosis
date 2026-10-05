package agent

// RED-first tests for the ADR-197 M-A condition extractors for the weakness-
// analyser crew (mirrors qgen_adk_go/internal/agent/conditions_test.go). These
// surface the prompt-shaping DISCRIMINANTS that vary the composed prompt — never
// the learner's artifact content / clues / PII — so O+ can render "which
// conditions produced this Growth-Edge map" (IMDA D2 explainability). They mirror
// ComposeDiagnoserInstruction / ComposeExtractorInstruction so they cannot drift
// from what actually drives the prompt.

import (
	"strings"
	"testing"
)

// mapState is a minimal in-package stateGetter fake (mirrors the qgen test
// fake). Get returns (nil, errNotFound) on miss, matching session.ReadonlyState.
type mapState struct{ m map[string]any }

func (s *mapState) Get(k string) (any, error) {
	v, ok := s.m[k]
	if !ok {
		return nil, errNotFound
	}
	return v, nil
}

var errNotFound = &stateKeyMissing{}

type stateKeyMissing struct{}

func (*stateKeyMissing) Error() string { return "state key not found" }

// --- weakness_diagnoser ---------------------------------------------------- //

// A threaded LEARNER CLUES block is the ONLY discriminant that changes the
// diagnoser's composed prompt (ComposeDiagnoserInstruction appends it only when
// present), so has_clues must be "true".
func TestDiagnoserConditions_withClues(t *testing.T) {
	st := &mapState{m: map[string]any{
		"extracted_text": "Q1 [WRONG]: 2+2=5.",
		"clues_block":    "--- BEGIN LEARNER CLUES ---\nsubject: arithmetic\n--- END LEARNER CLUES ---",
	}}
	c := DiagnoserConditions(st)
	if c["has_clues"] != "true" {
		t.Errorf("has_clues: got %q, want \"true\"", c["has_clues"])
	}
}

// No clues block ⇒ has_clues="false" (the diagnoser works from the extracted
// text alone). The required extracted_text is content, never a condition.
func TestDiagnoserConditions_withoutClues(t *testing.T) {
	st := &mapState{m: map[string]any{"extracted_text": "Q1 [WRONG]: factoring quadratics."}}
	c := DiagnoserConditions(st)
	if c["has_clues"] != "false" {
		t.Errorf("has_clues: got %q, want \"false\"", c["has_clues"])
	}
}

// A whitespace-only clues_block is NOT a real block (TrimSpace) ⇒ has_clues="false",
// matching ComposeDiagnoserInstruction which skips a blank cluesBlock.
func TestDiagnoserConditions_blankCluesIsFalse(t *testing.T) {
	st := &mapState{m: map[string]any{
		"extracted_text": "x",
		"clues_block":    "   \n  ",
	}}
	if c := DiagnoserConditions(st); c["has_clues"] != "false" {
		t.Errorf("blank clues_block must be has_clues=false; got %q", c["has_clues"])
	}
}

// The artifact content + clues body are PII/content and must NEVER appear in a
// condition value (conditions are discriminants, not content — ADR-197 §3).
func TestDiagnoserConditions_neverLeaksContent(t *testing.T) {
	st := &mapState{m: map[string]any{
		"extracted_text": "SECRET-ARTIFACT-TEXT",
		"clues_block":    "SECRET-CLUES-BODY",
	}}
	for k, v := range DiagnoserConditions(st) {
		if strings.Contains(v, "SECRET-ARTIFACT-TEXT") || strings.Contains(v, "SECRET-CLUES-BODY") {
			t.Errorf("condition %q leaked content/PII: %q", k, v)
		}
	}
}

// --- weakness_extractor ---------------------------------------------------- //

// The transcription approach keys off the artifact MIME type (marked test vs
// notes vs scribble vs source material), so source_mime_type is surfaced; a
// gs:// blob renders artifact_source="blob".
func TestExtractorConditions_blobWithMime(t *testing.T) {
	st := &mapState{m: map[string]any{
		"source_blob_uri":  "gs://bucket/upload.png",
		"source_mime_type": "image/png",
	}}
	c := ExtractorConditions(st)
	if c["source_mime_type"] != "image/png" {
		t.Errorf("source_mime_type: got %q, want \"image/png\"", c["source_mime_type"])
	}
	if c["artifact_source"] != "blob" {
		t.Errorf("artifact_source: got %q, want \"blob\"", c["artifact_source"])
	}
	// The blob URI is an identifier — it must NOT ride in any condition value.
	for k, v := range c {
		if strings.Contains(v, "gs://bucket/upload.png") {
			t.Errorf("condition %q leaked the blob URI: %q", k, v)
		}
	}
}

// Inline base64 with no MIME ⇒ artifact_source="inline", source_mime_type
// defaults to "unspecified" (never a blank condition value).
func TestExtractorConditions_inlineUnspecifiedMime(t *testing.T) {
	st := &mapState{m: map[string]any{"artifact_b64": "QUJD"}}
	c := ExtractorConditions(st)
	if c["source_mime_type"] != "unspecified" {
		t.Errorf("source_mime_type: got %q, want \"unspecified\"", c["source_mime_type"])
	}
	if c["artifact_source"] != "inline" {
		t.Errorf("artifact_source: got %q, want \"inline\"", c["artifact_source"])
	}
}

// No artifact reference at all (the InstructionProvider would already fail-loud)
// ⇒ both conditions default safely rather than erroring.
func TestExtractorConditions_noArtifact(t *testing.T) {
	c := ExtractorConditions(&mapState{m: map[string]any{}})
	if c["source_mime_type"] != "unspecified" {
		t.Errorf("source_mime_type: got %q, want \"unspecified\"", c["source_mime_type"])
	}
	if c["artifact_source"] != "unspecified" {
		t.Errorf("artifact_source: got %q, want \"unspecified\"", c["artifact_source"])
	}
}
