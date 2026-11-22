package compression

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	"github.com/SmilingXinyi/agent-engine/compression/protocol"
)

func trimTools(messages []aiprot.Message, limit int) ([]aiprot.Message, []protocol.ToolArchive) {
	trimmed := slices.Clone(messages)
	var archives []protocol.ToolArchive
	for index, message := range messages {
		if message.Role != aiprot.RoleTool {
			continue
		}
		copiedParts := false
		for partIndex, part := range message.Parts {
			result := part.ToolResult
			if part.Type != aiprot.ContentToolResult || result == nil {
				continue
			}
			text := result.Text()
			if textTokens(text) <= limit {
				continue
			}
			if !copiedParts {
				trimmed[index].Parts = slices.Clone(message.Parts)
				copiedParts = true
			}
			archives = append(archives, protocol.ToolArchive{
				MessageIndex: index, PartIndex: partIndex, Result: result.Clone(),
			})
			updated := *result
			updated.Content = replaceToolText(result.Content, toolExcerpt(text, limit))
			trimmed[index].Parts[partIndex].ToolResult = &updated
		}
	}
	return trimmed, archives
}

func replaceToolText(parts []aiprot.ContentPart, summary string) []aiprot.ContentPart {
	content := make([]aiprot.ContentPart, 0, len(parts))
	replaced := false
	for _, part := range parts {
		if part.Type != aiprot.ContentText {
			content = append(content, part)
		} else if !replaced {
			content = append(content, aiprot.ContentPart{Type: aiprot.ContentText, Text: summary})
			replaced = true
		}
	}
	return content
}

func toolExcerpt(text string, limit int) string {
	const marker = "[Tool result compacted]\n"
	// A smaller text budget cannot be reached when the full result already fits.
	budget := min(limit, textTokens(text)) * 4
	if budget <= len(marker)+4 {
		return headAndTail(text, budget)
	}
	remaining := budget - len(marker)
	if structured := structuredExcerpt(text, remaining); structured != "" {
		return marker + structured
	}
	if diagnostics := diagnosticExcerpt(text, remaining/3); diagnostics != "" {
		return marker + diagnostics + "\n" + headAndTail(text, remaining-textUnits(diagnostics)-1)
	}
	return marker + headAndTail(text, remaining)
}

func structuredExcerpt(text string, budget int) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(text), &fields) != nil || len(fields) == 0 {
		return ""
	}
	priority := []string{"exit_code", "status", "success", "is_error", "error", "stderr", "message", "path", "filename", "truncated", "count"}
	var keys []string
	for key := range fields {
		if !slices.Contains(priority, key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	keys = append(priority, keys...)
	var summary strings.Builder
	// Leave room for other fields when one field contains most of the result.
	fieldLimit := max(budget/2, 1)
	for _, key := range keys {
		value, exists := fields[key]
		label := key + ": "
		available := budget - textUnits(label) - 1
		if !exists || available < 4 {
			continue
		}
		var decoded string
		if json.Unmarshal(value, &decoded) != nil || len(value) == 0 || value[0] != '"' {
			decoded = string(value)
		}
		entry := label + headAndTail(decoded, min(available, fieldLimit)) + "\n"
		summary.WriteString(entry)
		budget -= textUnits(entry)
	}
	return strings.TrimSuffix(summary.String(), "\n")
}

func diagnosticExcerpt(text string, budget int) string {
	var diagnostics strings.Builder
	for line := range strings.SplitSeq(text, "\n") {
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "error") && !strings.Contains(lower, "fail") && !strings.Contains(lower, "warning") {
			continue
		}
		excerpt := headAndTail(strings.TrimSpace(line), budget-1)
		diagnostics.WriteString(excerpt)
		diagnostics.WriteByte('\n')
		budget -= textUnits(excerpt) + 1
		if budget < 12 {
			break
		}
	}
	return strings.TrimSuffix(diagnostics.String(), "\n")
}

func headAndTail(text string, budget int) string {
	if budget < 4 {
		return prefixWithin(text, budget)
	}
	if textUnits(text) <= budget {
		return text
	}
	const separator = "\n…\n"
	remaining := budget - textUnits(separator)
	if remaining <= 0 {
		// This marker also fits a one-token budget.
		return "…"
	}
	head := remaining * 2 / 3
	return prefixWithin(text, head) + separator + suffixWithin(text, remaining-head)
}
