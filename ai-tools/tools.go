// Package aitools builds an explicitly selected set of built-in tools.
// Capability packages can also be used independently of this assembly helper.
package aitools

import (
	"fmt"

	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-tools/filesystem"
	"github.com/SmilingXinyi/agent-engine/ai-tools/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-tools/shell"
)

// Build constructs enabled tools without running them. It has no global registry
// and no dependency on Agent or MCP. Rebuild after changing configuration.
func Build(config protocol.Config) ([]loopprot.Tool, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	tools := make([]loopprot.Tool, 0, len(config.BuiltinTools))
	for _, name := range config.EnabledNames() {
		var tool loopprot.Tool
		var err error
		switch name {
		case protocol.ReadFile:
			tool, err = filesystem.NewReadTool(config.Filesystem)
		case protocol.WriteFile:
			tool, err = filesystem.NewWriteTool(config.Filesystem)
		case protocol.ApplyPatch:
			tool, err = filesystem.NewPatchTool(config.Filesystem)
		case protocol.ListDirectory:
			tool, err = filesystem.NewListTool(config.Filesystem)
		case protocol.Shell:
			tool, err = shell.NewTool(config.Shell)
		}
		if err != nil {
			return nil, fmt.Errorf("build built-in tool %q: %w", name, err)
		}
		tools = append(tools, tool)
	}
	return tools, nil
}
