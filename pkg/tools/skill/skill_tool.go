package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SmilingXinyi/agent-engine/pkg/agent"
)

const skillToolName = "get_skill"

type skillInput struct {
	Name string `json:"name"`
}

// NewTool returns an agent.Tool that loads a skill's full instructions by name on demand.
func NewTool(skillsRoot string) agent.Tool {
	return agent.Tool{
		Name:        skillToolName,
		Description: "Load the full instructions for a skill by its name. Call this when you decide to apply a skill listed in the system prompt.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {
					"type": "string",
					"description": "The skill name as listed in the system prompt."
				}
			},
			"required": ["name"]
		}`),
		Handler: agent.ToolHandlerFunc(func(_ context.Context, input json.RawMessage) (string, error) {
			var request skillInput
			if err := json.Unmarshal(input, &request); err != nil {
				return "", fmt.Errorf("invalid skill tool input: %w", err)
			}
			if strings.TrimSpace(request.Name) == "" {
				return "", fmt.Errorf("skill name is required")
			}
			skills, err := LoadSkills(skillsRoot)
			if err != nil {
				return "", fmt.Errorf("load skills: %w", err)
			}
			for _, skill := range skills {
				if skill.Name == request.Name {
					return strings.TrimSpace(skill.Body), nil
				}
			}
			return "", fmt.Errorf("skill %q not found", request.Name)
		}),
	}
}

// BuildListing returns a markdown section listing all skill names and descriptions
// for injection into the system prompt.
func BuildListing(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("# Available Skills\n\n")
	builder.WriteString("Use the `get_skill` tool to load a skill's full instructions when relevant.\n\n")
	for _, skill := range skills {
		builder.WriteString("- **")
		builder.WriteString(skill.Name)
		builder.WriteString("**: ")
		builder.WriteString(skill.Metadata.Description)
		builder.WriteString("\n")
	}
	return builder.String()
}
