package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/SmilingXinyi/agent-engine/ai-sdk/model"
)

// Handler handles tool execution.
type Handler func(ctx context.Context, input json.RawMessage) (any, error)

// Tool represents an executable tool for LLMs.
type Tool interface {
	Definition() model.ToolDefinition
	Execute(ctx context.Context, input json.RawMessage) (any, error)
}

// BaseTool provides a convenient implementation of Tool.
type BaseTool struct {
	Def     model.ToolDefinition
	Handler Handler
}

func (b *BaseTool) Definition() model.ToolDefinition {
	return b.Def
}

func (b *BaseTool) Execute(ctx context.Context, input json.RawMessage) (any, error) {
	if b.Handler == nil {
		return nil, fmt.Errorf("tool %q has no handler", b.Def.Name)
	}
	return b.Handler(ctx, input)
}

// New creates a new BaseTool.
func New(name, description string, params json.RawMessage, handler Handler) *BaseTool {
	if len(params) == 0 {
		params = json.RawMessage(`{"type":"object"}`)
	}
	return &BaseTool{
		Def: model.ToolDefinition{
			Name:        name,
			Description: description,
			Parameters:  params,
		},
		Handler: handler,
	}
}

// Registry manages tool registration and lookup.
type Registry interface {
	Register(tool Tool) error
	Get(name string) (Tool, bool)
	List() []Tool
	Definitions() []model.ToolDefinition
}

type memoryRegistry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry creates a new in-memory tool registry.
func NewRegistry() Registry {
	return &memoryRegistry{
		tools: make(map[string]Tool),
	}
}

func (r *memoryRegistry) Register(tool Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := tool.Definition().Name
	if name == "" {
		return fmt.Errorf("tool name cannot be empty")
	}
	r.tools[name] = tool
	return nil
}

func (r *memoryRegistry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

func (r *memoryRegistry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		list = append(list, t)
	}
	return list
}

func (r *memoryRegistry) Definitions() []model.ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]model.ToolDefinition, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, t.Definition())
	}
	return defs
}
