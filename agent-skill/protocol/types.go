// Package protocol defines a local skill's instructions and source.
package protocol

// Skill is a snapshot of one SKILL.md. Path is absolute for loaded files;
// callers may leave it empty when constructing an in-memory skill.
type Skill struct {
	Name        string
	Description string
	Path        string
	Body        string
}

const ToolName = "get_skill"
