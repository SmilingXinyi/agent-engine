package agentskill

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/SmilingXinyi/agent-engine/agent-skill/protocol"
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
)

// BuildListing includes only metadata. The model decides relevance and requests
// instructions through get_skill; no keyword scoring or body injection occurs.
func BuildListing(skills []protocol.Skill) string {
	if len(skills) == 0 {
		return ""
	}
	ordered := append([]protocol.Skill(nil), skills...)
	sort.Slice(ordered, func(first, second int) bool { return ordered[first].Name < ordered[second].Name })
	var listing strings.Builder
	listing.WriteString("# Available Skills\n\nUse `get_skill` with the exact name to read a skill's instructions before applying it.\n\n")
	for _, skill := range ordered {
		fmt.Fprintf(&listing, "- %s: %s\n", skill.Name, strings.Join(strings.Fields(skill.Description), " "))
	}
	return listing.String()
}

// NewTool captures an immutable catalog snapshot. Executions perform an exact
// name lookup without rescanning disk or accepting arbitrary file paths.
func NewTool(skills []protocol.Skill) (loopprot.Tool, error) {
	catalog := make(map[string]protocol.Skill, len(skills))
	for _, skill := range skills {
		if err := skill.Validate(); err != nil {
			return loopprot.Tool{}, fmt.Errorf("skill %q: %w", skill.Name, err)
		}
		if _, exists := catalog[skill.Name]; exists {
			return loopprot.Tool{}, fmt.Errorf("duplicate skill %q", skill.Name)
		}
		catalog[skill.Name] = skill
	}
	return loopprot.Tool{
		Definition: aiprot.ToolDefinition{
			Name:        protocol.ToolName,
			Description: "Read the full instructions of an available skill by its exact name.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"],"additionalProperties":false}`),
		},
		Execute: func(ctx context.Context, arguments json.RawMessage) (aiprot.ToolResult, error) {
			if err := ctx.Err(); err != nil {
				return aiprot.ToolResult{}, err
			}
			var input struct {
				Name string `json:"name"`
			}
			decoder := json.NewDecoder(strings.NewReader(string(arguments)))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err != nil {
				return aiprot.ToolResult{}, fmt.Errorf("invalid skill input: %w", err)
			}
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				return aiprot.ToolResult{}, fmt.Errorf("skill input must be one JSON object")
			}
			skill, exists := catalog[input.Name]
			if !exists {
				return aiprot.ToolResult{}, fmt.Errorf("skill %q not found", input.Name)
			}
			instructions := skill.Body
			if skill.Path != "" {
				instructions = fmt.Sprintf("Skill: %s\nSource: %s\nBase directory: %s\n\n%s", skill.Name, skill.Path, filepath.Dir(skill.Path), skill.Body)
			}
			return aiprot.TextToolResult(instructions), nil
		},
	}, nil
}
