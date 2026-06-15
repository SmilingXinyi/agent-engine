package goai

import (
	"testing"

	"github.com/zendev-sh/goai"
)

func TestTextAndThinkingFromResultUsesStepText(t *testing.T) {
	text, thinking := textAndThinkingFromResult(&goai.TextResult{
		Text:      "thoughtanswer",
		Reasoning: "thought",
		Steps: []goai.StepResult{
			{Text: "answer", Reasoning: "thought"},
		},
	})
	if text != "answer" || thinking != "thought" {
		t.Fatalf("unexpected split text=%q thinking=%q", text, thinking)
	}
}

func TestTextAndThinkingFromResultFallsBackToTrimmedText(t *testing.T) {
	text, thinking := textAndThinkingFromResult(&goai.TextResult{
		Text:      "thoughtanswer",
		Reasoning: "thought",
	})
	if text != "answer" || thinking != "thought" {
		t.Fatalf("unexpected split text=%q thinking=%q", text, thinking)
	}
}

func TestTextAndThinkingFromResultHandlesNil(t *testing.T) {
	text, thinking := textAndThinkingFromResult(nil)
	if text != "" || thinking != "" {
		t.Fatalf("expected empty result, got text=%q thinking=%q", text, thinking)
	}
}
