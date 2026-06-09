package prompt

import (
	"strings"

	"github.com/SmilingXinyi/agent-engine/pkg/tools/skill"
)

func BuildSystemPrompt(agentsContent string, skills []skill.Skill, userPrompt string) string {
	var builder strings.Builder
	builder.WriteString("# Agent Instructions\n\n")
	builder.WriteString(agentsContent)
	builder.WriteString("\n\n# Skills\n")
	for _, s := range skill.SelectSkills(userPrompt, skills) {
		appendSkill(&builder, s)
	}
	return builder.String()
}

func appendSkill(builder *strings.Builder, s skill.Skill) {
	builder.WriteString("\n## Skill: ")
	builder.WriteString(s.Name)
	builder.WriteString("\n\n")
	builder.WriteString(strings.TrimSpace(s.Body))
	builder.WriteString("\n")
}
