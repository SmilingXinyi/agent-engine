module github.com/SmilingXinyi/agent-engine/ai-tools

go 1.25.0

require (
	github.com/SmilingXinyi/agent-engine/agent v0.0.0
	github.com/SmilingXinyi/agent-engine/ai-sdk v0.7.0
	github.com/SmilingXinyi/agent-engine/ai-tool-loop v0.1.0
)

require github.com/SmilingXinyi/agent-engine/compression v0.0.0 // indirect

replace github.com/SmilingXinyi/agent-engine/agent => ../agent

replace github.com/SmilingXinyi/agent-engine/ai-sdk => ../ai-sdk

replace github.com/SmilingXinyi/agent-engine/ai-tool-loop => ../ai-tool-loop

replace github.com/SmilingXinyi/agent-engine/compression => ../compression
