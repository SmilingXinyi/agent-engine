package goai

import (
	"strings"

	"github.com/zendev-sh/goai"
)

func textAndThinkingFromResult(result *goai.TextResult) (string, string) {
	if result == nil {
		return "", ""
	}

	thinking := result.Reasoning
	if len(result.Steps) > 0 {
		var text strings.Builder
		for _, step := range result.Steps {
			text.WriteString(step.Text)
		}
		return text.String(), thinking
	}

	text := result.Text
	if thinking != "" && strings.HasPrefix(text, thinking) {
		text = strings.TrimPrefix(text, thinking)
	}
	return text, thinking
}
