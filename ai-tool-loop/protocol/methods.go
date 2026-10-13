package protocol

import "github.com/SmilingXinyi/agent-engine/ai-tool-loop/utils"

// Clone copies mutable request data, preserving executors and callbacks.
func (request Request) Clone() Request { return utils.Clone(request) }

// Clone copies mutable execution records independently of the original result.
func (result Result) Clone() Result { return utils.Clone(result) }
