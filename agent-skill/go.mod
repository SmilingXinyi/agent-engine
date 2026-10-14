module github.com/SmilingXinyi/agent-engine/agent-skill

go 1.25.0

require (
	github.com/SmilingXinyi/agent-engine/ai-sdk v0.0.0
	github.com/SmilingXinyi/agent-engine/ai-tool-loop v0.0.0
	gopkg.in/yaml.v3 v3.0.1
)

replace github.com/SmilingXinyi/agent-engine/ai-sdk => ../ai-sdk

replace github.com/SmilingXinyi/agent-engine/ai-tool-loop => ../ai-tool-loop
