package workspace

import (
	"encoding/json"
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

// Init creates a workspace without overwriting existing configuration. The
// target project directory must already exist. No providers or tools are run.
func Init(directory string) error {
	root, err := projectRoot(directory)
	if err != nil {
		return err
	}
	agents := filepath.Join(root, ".agents")
	for _, path := range []string{agents, filepath.Join(agents, "skills"), filepath.Join(agents, "state")} {
		if err := ensureDirectory(path); err != nil {
			return err
		}
	}
	configs := []struct {
		name  string
		value any
	}{
		{"workspace.json", protocol.Config{MaxSteps: 40, MaxHistoryRounds: 10, TimeoutSeconds: 900}},
		{"tools.json", toolsprot.Config{BuiltinTools: map[string]toolsprot.ToolConfig{
			toolsprot.ReadFile: {}, toolsprot.ListDirectory: {}, toolsprot.WriteFile: {}, toolsprot.ApplyPatch: {}, toolsprot.Shell: {},
		}}},
		{"mcp.json", mcpprot.Config{Servers: map[string]mcpprot.ServerConfig{}}},
	}
	for _, config := range configs {
		content, err := json.MarshalIndent(config.value, "", "  ")
		if err != nil {
			return err
		}
		path := filepath.Join(agents, config.name)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return err
		}
		_, writeErr := file.Write(append(content, '\n'))
		err = errors.Join(writeErr, file.Sync(), file.Close())
		if err != nil {
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

func loadResources(root string) ([]loopprot.Tool, mcpprot.Config, string, error) {
	agents := filepath.Join(root, ".agents")
	config, err := toolsprot.LoadConfig(filepath.Join(agents, "tools.json"))
	if err != nil {
		return nil, mcpprot.Config{}, "", err
	}
	// Only validate dependencies of enabled tools, retaining ai-tools semantics.
	for _, name := range config.EnabledNames() {
		if name == toolsprot.Shell {
			if err := bindRoot(root, &config.Shell.Dir); err != nil {
				return nil, mcpprot.Config{}, "", err
			}
		} else {
			if err := bindRoot(root, &config.Filesystem.Root); err != nil {
				return nil, mcpprot.Config{}, "", err
			}
		}
	}
	tools, err := aitools.Build(config)
	if err != nil {
		return nil, mcpprot.Config{}, "", err
	}
	mcpConfig := mcpprot.Config{Servers: map[string]mcpprot.ServerConfig{}}
	content, err := os.ReadFile(filepath.Join(agents, "mcp.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, mcpConfig, "", err
	}
	if err == nil {
		mcpConfig, err = mcpprot.ParseConfig(string(content))
		if err != nil {
			return nil, mcpConfig, "", err
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
	instructions, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, mcpConfig, "", err
	}
	listing := agentskill.BuildListing(skills)
	return tools, mcpConfig, string(instructions) + "\n\n" + listing, nil
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
