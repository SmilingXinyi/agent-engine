package agent

import "context"

type toolEnvKey struct{}

// WithToolEnv attaches a tool parameter injection map to the context.
// The map key is either a tool name or "*" for all tools.
// Each value is a set of parameters to inject when the model omitted them.
func WithToolEnv(ctx context.Context, env map[string]map[string]any) context.Context {
	if len(env) == 0 {
		return ctx
	}
	cloned := make(map[string]map[string]any, len(env))
	for toolName, values := range env {
		clonedValues := make(map[string]any, len(values))
		for key, value := range values {
			clonedValues[key] = value
		}
		cloned[toolName] = clonedValues
	}
	return context.WithValue(ctx, toolEnvKey{}, cloned)
}

// ToolEnvFromContext retrieves the tool env map from the context.
func ToolEnvFromContext(ctx context.Context) map[string]map[string]any {
	env, _ := ctx.Value(toolEnvKey{}).(map[string]map[string]any)
	return env
}

// ApplyToolEnv merges env entries into params for the given tool.
// Wildcard "*" entries are applied first, then tool-specific entries.
// Model-supplied values take priority — env only fills in parameters the model
// did not provide.
func ApplyToolEnv(toolName string, params map[string]any, env map[string]map[string]any) {
	for key, value := range env["*"] {
		if _, exists := params[key]; !exists {
			params[key] = value
		}
	}
	for key, value := range env[toolName] {
		if _, exists := params[key]; !exists {
			params[key] = value
		}
	}
}

// HasToolEnv reports whether env contains entries for toolName or the wildcard "*".
func HasToolEnv(toolName string, env map[string]map[string]any) bool {
	return len(env["*"]) > 0 || len(env[toolName]) > 0
}
