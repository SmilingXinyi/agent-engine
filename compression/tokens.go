package compression

import (
	"encoding/json"
	"unicode/utf8"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	"github.com/SmilingXinyi/agent-engine/compression/protocol"
)

// Measure estimates model-visible message tokens. A zero window reports only
// the token count. Percent can exceed 100.
func Measure(messages []aiprot.Message, contextWindow int) protocol.ContextUsage {
	return protocol.NewContextUsage(estimateTokens(messages), contextWindow)
}

func estimateTokens(messages []aiprot.Message) int {
	tokens := 0
	for _, message := range messages {
		tokens += 4 + textTokens(string(message.Role)) + textTokens(message.Name)
		tokens += contentTokens(message.Parts)
		if len(message.ProviderOptions) > 0 {
			options, _ := json.Marshal(message.ProviderOptions)
			tokens += textTokens(string(options))
		}
	}
	return tokens
}

func contentTokens(parts []aiprot.ContentPart) int {
	tokens := 0
	for _, part := range parts {
		tokens += textTokens(part.Text) + textTokens(string(part.Data))
		if call := part.ToolCall; call != nil {
			tokens += 4 + textTokens(call.ID) + textTokens(call.Name) + textTokens(string(call.Arguments))
		}
		if result := part.ToolResult; result != nil {
			tokens += 4 + textTokens(result.ToolCallID) + textTokens(result.ToolName) + contentTokens(result.Content)
		}
		if part.Media != nil {
			// The provider determines the actual media cost. Inline base64 is
			// a transport encoding and is not counted as text.
			tokens += 1024
		}
	}
	return tokens
}

func textTokens(text string) int {
	return (textUnits(text) + 3) / 4
}

// An ASCII character costs one unit and a non-ASCII rune costs four units.
func textUnits(text string) int {
	units := 0
	for _, character := range text {
		units += runeUnits(character)
	}
	return units
}

func runeUnits(character rune) int {
	if character < utf8.RuneSelf {
		return 1
	}
	return 4
}

func prefixWithin(text string, units int) string {
	for index, character := range text {
		units -= runeUnits(character)
		if units < 0 {
			return text[:index]
		}
	}
	return text
}

func suffixWithin(text string, units int) string {
	end := len(text)
	for end > 0 {
		character, width := utf8.DecodeLastRuneInString(text[:end])
		units -= runeUnits(character)
		if units < 0 {
			break
		}
		end -= width
	}
	return text[end:]
}
