module github.com/SmilingXinyi/agent-engine/agent

go 1.25.0

require (
	github.com/SmilingXinyi/agent-engine/ai-sdk v0.0.0
	github.com/SmilingXinyi/agent-engine/tool-loop v0.0.0
)

replace github.com/SmilingXinyi/agent-engine/ai-sdk => ../ai-sdk

replace github.com/SmilingXinyi/agent-engine/tool-loop => ../tool-loop
