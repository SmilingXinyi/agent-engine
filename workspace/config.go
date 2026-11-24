package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agentskill "github.com/SmilingXinyi/agent-engine/agent-skill"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	aitools "github.com/SmilingXinyi/agent-engine/ai-tools"
	toolsprot "github.com/SmilingXinyi/agent-engine/ai-tools/protocol"
	mcpprot "github.com/SmilingXinyi/agent-engine/mcp-runtime/protocol"
	"github.com/SmilingXinyi/agent-engine/workspace/protocol"
)

const (
	workspaceConfigName = "workspace.yaml"
	toolsConfigName     = "tools.yaml"
	mcpConfigName       = "mcp.yaml"
	promptName          = "prompt.md"
)

var configTemplates = []struct{ name, content string }{
	{workspaceConfigName, `# Set model and baseURL here or export AGENT_LLM_MODEL and AGENT_LLM_BASE_URL.
# apiKeyEnv names an environment variable. Keep credentials in the environment.
# apiType: chat_completions
# reasoningEffort: medium
modelMaxRetries: 2
maxSteps: 40
maxHistoryRounds: 10
timeoutSeconds: 900
`},
	{toolsConfigName, `# List the built-in tools this workspace can use.
builtinTools:
  read_file: {}
  list_directory: {}
  write_file: {}
  apply_patch: {}
  shell: {}
  # ask_user: {}
shell:
  timeoutSeconds: 30
`},
	{mcpConfigName, `# Add named MCP servers under mcpServers.
mcpServers: {}
`},
	{promptName, ""},
}

// Init creates a workspace without overwriting existing configuration. The
// target project directory must already exist. No providers or tools are run.
func Init(directory string) (err error) {
	root, err := projectRoot(directory)
	if err != nil {
		return err
	}
	agents := filepath.Join(root, ".agents")
	for _, path := range []string{agents, filepath.Join(agents, "skills"), filepath.Join(agents, subagentsDirectory)} {
		if err := ensureDirectory(path); err != nil {
			return err
		}
	}
	lock, err := lockWorkspace(root)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	// Check legacy files before creating templates that would shadow them.
	for _, config := range configTemplates {
		if config.name == promptName {
			continue
		}
		if _, err := protocol.ReadYAMLConfig(filepath.Join(agents, config.name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	for _, config := range configTemplates {
		path := filepath.Join(agents, config.name)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return err
		}
		_, writeErr := file.WriteString(config.content)
		if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
			return err
		}
	}
	return syncDirectory(agents)
}

func projectRoot(directory string) (string, error) {
	if directory == "" {
		directory = "."
	}
	root, err := filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace root must be a directory")
	}
	return root, nil
}

func ensureDirectory(path string) error {
	if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace metadata directory must not be a symlink: %s", path)
	}
	return nil
}

func lockWorkspace(root string) (*os.File, error) {
	state := filepath.Join(root, ".agents", "state")
	if err := ensureDirectory(state); err != nil {
		return nil, err
	}
	lock, err := acquireLock(filepath.Join(state, "workspace.lock"))
	if err != nil {
		return nil, fmt.Errorf("lock workspace %s: %w", root, err)
	}
	return lock, nil
}

func loadResources(root, instructions string, askUser toolsprot.AskUserFunc) ([]loopprot.Tool, mcpprot.Config, string, error) {
	agents := filepath.Join(root, ".agents")
	content, err := protocol.ReadYAMLConfig(filepath.Join(agents, toolsConfigName))
	if err != nil {
		return nil, mcpprot.Config{}, "", err
	}
	tools, err := buildTools(root, content, askUser)
	if err != nil {
		return nil, mcpprot.Config{}, "", fmt.Errorf("%s: %w", toolsConfigName, err)
	}
	mcpConfig := mcpprot.Config{Servers: map[string]mcpprot.ServerConfig{}}
	content, err = protocol.ReadYAMLConfig(filepath.Join(agents, mcpConfigName))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, mcpConfig, "", err
	}
	if err == nil {
		mcpConfig, err = decodeMCPConfig(content)
		if err != nil {
			return nil, mcpConfig, "", fmt.Errorf("%s: %w", mcpConfigName, err)
		}
	}
	for name, server := range mcpConfig.Servers {
		if server.Disabled {
			continue
		}
		if server.Command != "" {
			if server.Dir == "" {
				server.Dir = root
			} else if !filepath.IsAbs(server.Dir) {
				server.Dir = filepath.Join(root, server.Dir)
			}
			// Relative executable paths are workspace-relative; bare names still
			// use PATH. Relative arguments are interpreted by the subprocess.
			if strings.ContainsRune(server.Command, filepath.Separator) && !filepath.IsAbs(server.Command) {
				server.Command = filepath.Join(root, server.Command)
			}
		}
		mcpConfig.Servers[name] = server
	}
	skills, err := agentskill.Load(filepath.Join(agents, "skills"))
	if err != nil {
		return nil, mcpConfig, "", err
	}
	if len(skills) > 0 {
		tool, err := agentskill.NewTool(skills)
		if err != nil {
			return nil, mcpConfig, "", err
		}
		tools = append(tools, tool)
	}
	prompt, err := os.ReadFile(filepath.Join(agents, promptName))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, mcpConfig, "", fmt.Errorf("read workspace prompt: %w", err)
	}
	listing := agentskill.BuildListing(skills)
	contextParts := []string{}
	for _, part := range []string{string(prompt), instructions, listing} {
		if part != "" {
			contextParts = append(contextParts, part)
		}
	}
	return tools, mcpConfig, strings.Join(contextParts, "\n\n"), nil
}

func bindRoot(root string, configured *string) error {
	if *configured != "" {
		path := *configured
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		if resolved != root {
			return fmt.Errorf("built-in tool directory must match workspace root: %s", path)
		}
	}
	*configured = root
	return nil
}

func buildTools(root string, content []byte, askUser toolsprot.AskUserFunc) ([]loopprot.Tool, error) {
	var config toolsprot.Config
	if err := protocol.DecodeYAML(content, &config); err != nil {
		return nil, err
	}
	if config.BuiltinTools == nil {
		return nil, fmt.Errorf("expected a builtinTools mapping")
	}
	for _, name := range config.EnabledNames() {
		switch name {
		case toolsprot.Shell:
			if err := bindRoot(root, &config.Shell.Dir); err != nil {
				return nil, err
			}
		case toolsprot.ReadFile, toolsprot.ListDirectory, toolsprot.WriteFile, toolsprot.ApplyPatch:
			if err := bindRoot(root, &config.Filesystem.Root); err != nil {
				return nil, err
			}
		}
	}
	return aitools.BuildWithDependencies(config, toolsprot.Dependencies{AskUser: askUser})
}

func decodeMCPConfig(content []byte) (mcpprot.Config, error) {
	var config mcpprot.Config
	if err := protocol.DecodeYAML(content, &config); err != nil {
		return config, err
	}
	if config.Servers == nil {
		return config, fmt.Errorf("expected an mcpServers mapping")
	}
	return config, config.Validate()
}
