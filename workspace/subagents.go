package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/SmilingXinyi/agent-engine/agent"
	agentprot "github.com/SmilingXinyi/agent-engine/agent/protocol"
	ai "github.com/SmilingXinyi/agent-engine/ai-sdk"
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	toolsprot "github.com/SmilingXinyi/agent-engine/ai-tools/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-tools/subagent"
	compressionprot "github.com/SmilingXinyi/agent-engine/compression/protocol"
	"github.com/SmilingXinyi/agent-engine/workspace/protocol"
)

const (
	subagentsDirectory    = ".subagents"
	maxDelegationsPerTurn = 2
	subagentMaxSteps      = 6
	subagentTimeout       = 60 * time.Second
)

const delegationInstructions = `Use the registered subagent tools for independent tasks that take multiple read-only steps. Choose a role by its description.
Send a self-contained task and only the needed paths, constraints, and facts as context. For a simple lookup, use the relevant tool directly.
A subagent starts with fresh history each time. If its answer identifies missing context, supply that context on the next delegation or ask the user if the information is unavailable.`

// The complete tool name, including the prefix, must fit OpenAI's 64 characters.
var subagentNamePattern = regexp.MustCompile(`^[a-z0-9_-]{1,55}$`)

type subagentRole struct {
	name        string
	description string
	request     agentprot.Request
}

func loadSubagentRoles(root, model, instructions string, localTools []loopprot.Tool) ([]subagentRole, error) {
	directory := filepath.Join(root, ".agents", subagentsDirectory)
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("subagent directory must be a real directory: %s", directory)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	available := make(map[string]loopprot.Tool)
	for _, tool := range localTools {
		switch tool.Definition.Name {
		case toolsprot.ReadFile, toolsprot.ListDirectory:
			available[tool.Definition.Name] = tool
		}
	}
	var roles []subagentRole
	for _, entry := range entries {
		path := filepath.Join(directory, entry.Name())
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("subagent entries must not be symlinks: %s", path)
		}
		if !entry.IsDir() {
			continue
		}
		if !subagentNamePattern.MatchString(entry.Name()) {
			return nil, fmt.Errorf("invalid subagent directory name %q: %s", entry.Name(), path)
		}
		role, err := loadSubagentRole(path, model, root, instructions, available)
		if err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, nil
}

func loadSubagentRole(directory, model, root, instructions string, available map[string]loopprot.Tool) (subagentRole, error) {
	path := filepath.Join(directory, "workspace.json")
	var config struct {
		Description string `json:"description"`
		Model       string `json:"model"`
	}
	if err := loadRoleJSON(path, &config); err != nil {
		return subagentRole{}, err
	}
	description := strings.TrimSpace(config.Description)
	if description == "" {
		return subagentRole{}, fmt.Errorf("subagent description is required: %s", path)
	}
	if override := strings.TrimSpace(config.Model); override != "" {
		model = override
	}
	path = filepath.Join(directory, "tools.json")
	var selection struct {
		BuiltinTools map[string]toolsprot.ToolConfig `json:"builtinTools"`
	}
	if err := loadRoleJSON(path, &selection); err != nil {
		return subagentRole{}, err
	}
	toolConfig := toolsprot.Config{BuiltinTools: selection.BuiltinTools}
	if err := toolConfig.Validate(); err != nil {
		return subagentRole{}, fmt.Errorf("%s: %w", path, err)
	}
	var tools []loopprot.Tool
	for _, name := range toolConfig.EnabledNames() {
		tool, exists := available[name]
		if !exists {
			return subagentRole{}, fmt.Errorf("%s: tool %q is not an enabled local read-only workspace tool", path, name)
		}
		tools = append(tools, tool)
	}
	if len(tools) == 0 {
		return subagentRole{}, fmt.Errorf("select at least one local read-only tool: %s", path)
	}
	path = filepath.Join(directory, "prompt.md")
	if err := regularOrMissing(path); err != nil {
		return subagentRole{}, err
	}
	prompt, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return subagentRole{}, err
	}
	system := fmt.Sprintf("You are a read-only subagent. Workspace root: %s\nFile paths are relative to this root. Return concise findings with supporting evidence. If essential context is missing, state what you need.\n\nRole: %s", root, description)
	for _, part := range []string{instructions, string(prompt)} {
		if part != "" {
			system += "\n\n" + part
		}
	}
	return subagentRole{
		name: "subagent_" + filepath.Base(directory), description: description,
		request: agentprot.Request{
			Model: model, SystemPrompt: system, Tools: tools,
			Options: loopprot.RunOptions{
				MaxSteps: subagentMaxSteps, MaxHistoryRounds: subagentMaxSteps, Compression: &compressionprot.Config{},
			},
		},
	}, nil
}

func loadRoleJSON(path string, target any) error {
	if err := regularOrMissing(path); err != nil {
		return err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := protocol.DecodeJSON(content, target); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func (workspace *Workspace) registerSubagents(provider aiprot.Provider, roles []subagentRole) error {
	if len(roles) == 0 {
		return nil
	}
	// The child client has no hooks that write into the parent's pending turn.
	child := agent.New(ai.NewClient(provider, aiprot.Hooks{}))
	workspace.subagents = make(map[string]bool, len(roles))
	for _, role := range roles {
		role.request.Options.ConfirmTool = workspace.confirm
		tool, err := subagent.NewTool(child, role.name, role.description, role.request, subagentTimeout)
		if err != nil {
			return fmt.Errorf("register %s: %w", role.name, err)
		}
		workspace.tools = append(workspace.tools, tool)
		workspace.subagents[role.name] = true
		workspace.readOnly[role.name] = true
	}
	return nil
}
