// Package workspace assembles a local project's tools and owns its append-only
// conversation history. Agent and its existing sequential loop remain stateless.
package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/SmilingXinyi/agent-engine/agent"
	agentprot "github.com/SmilingXinyi/agent-engine/agent/protocol"
	ai "github.com/SmilingXinyi/agent-engine/ai-sdk"
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-sdk/providers/openai"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	compressionprot "github.com/SmilingXinyi/agent-engine/compression/protocol"
	mcpruntime "github.com/SmilingXinyi/agent-engine/mcp-runtime"
	"github.com/SmilingXinyi/agent-engine/workspace/protocol"
)

type Workspace struct {
	mu            sync.Mutex
	root          string
	config        protocol.Config
	options       protocol.OpenOptions
	system        string
	tools         []loopprot.Tool
	readOnly      map[string]bool
	subagents     map[string]bool
	agent         *agent.Agent
	history       *history
	lock          *os.File
	runtime       *mcpruntime.Runtime
	lifetime      context.Context
	stopLifetime  context.CancelFunc
	runCancel     context.CancelFunc
	runDone       chan struct{}
	checkpoint    *contextCheckpoint
	checkpointErr error
	closed        bool
	closeOnce     sync.Once
	closeErr      error
}

// Open loads a fixed configuration snapshot and explicitly starts its MCP
// resources. It never executes model/tool calls or resumes an interrupted run.
// Close releases resources and the process lock, including on cancelled runs.
func Open(ctx context.Context, directory string, options protocol.OpenOptions) (_ *Workspace, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := options.Approval.Validate(); err != nil {
		return nil, err
	}
	root, err := projectRoot(directory)
	if err != nil {
		return nil, err
	}
	agents := filepath.Join(root, ".agents")
	info, err := os.Lstat(agents)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("workspace is not initialized; run agent-engine init (metadata must be a real directory)")
	}
	lock, err := lockWorkspace(root)
	if err != nil {
		return nil, err
	}
	defer func() {
		if lock != nil {
			err = errors.Join(err, lock.Close())
		}
	}()
	config, err := protocol.LoadConfig(filepath.Join(agents, workspaceConfigName))
	if err != nil {
		return nil, err
	}
	config = config.WithDefaults()
	if strings.TrimSpace(config.Model) == "" {
		return nil, fmt.Errorf("configure workspace model or AGENT_LLM_MODEL")
	}
	provider, err := workspaceProvider(config, options.Provider)
	if err != nil {
		return nil, err
	}
	projectInstructions, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	tools, mcpConfig, instructions, err := loadResources(root, string(projectInstructions), options.AskUser)
	if err != nil {
		return nil, err
	}
	roles, err := loadSubagentRoles(root, config.Model, string(projectInstructions), tools)
	if err != nil {
		return nil, err
	}
	state := filepath.Join(agents, "state")
	lifetime, stop := context.WithCancel(ctx)
	workspace := &Workspace{root: root, config: config, options: options, tools: tools, lock: lock, lifetime: lifetime, stopLifetime: stop}
	lock = nil // Workspace now owns the lock.
	workspace.readOnly = make(map[string]bool)
	for _, tool := range tools {
		switch tool.Definition.Name {
		case "read_file", "list_directory", "get_skill", "ask_user":
			workspace.readOnly[tool.Definition.Name] = true
		}
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, workspace.Close())
		}
	}()
	if err := workspace.registerSubagents(provider, roles); err != nil {
		return nil, err
	}
	workspace.history, err = openHistory(filepath.Join(state, "history.ndjson"), options.Warn)
	if err != nil {
		return nil, err
	}
	if runID := workspace.history.runID(); runID != "" {
		workspace.warn("Recovered an interrupted run. No calls were replayed; enter a new prompt to continue.")
		if err := workspace.history.completePending(options.Warn); err != nil {
			return nil, err
		}
		if err := workspace.history.append(protocol.RunEnded, runID, protocol.EventData{StopReason: loopprot.StopError, Error: "interrupted before run completion"}); err != nil {
			return nil, err
		}
	}
	workspace.checkpoint, workspace.checkpointErr = workspace.loadContextCheckpoint()
	if workspace.checkpointErr != nil {
		workspace.warn(workspace.checkpointErr.Error())
	}
	workspace.runtime, err = mcpruntime.Start(lifetime, mcpConfig)
	if err != nil {
		return nil, err
	}
	workspace.tools = append(workspace.tools, workspace.runtime.Tools...)
	seen := make(map[string]bool)
	for _, tool := range workspace.tools {
		if seen[tool.Definition.Name] {
			return nil, fmt.Errorf("duplicate workspace tool %q", tool.Definition.Name)
		}
		seen[tool.Definition.Name] = true
	}
	workspace.system = fmt.Sprintf("You are a local workspace assistant. Workspace root: %s\nFile tool paths are relative to this root. Shell and MCP are not sandboxed. Verify tool outcomes before reporting success.", root)
	if instructions != "" {
		workspace.system += "\n\n" + instructions
	}
	if len(workspace.subagents) > 0 {
		workspace.system += "\n\n" + delegationInstructions
	}
	guardRequest := func(ctx context.Context, _ aiprot.RequestInfo) error {
		return errors.Join(ctx.Err(), workspace.history.healthy())
	}
	recordResponse := func(_ context.Context, info aiprot.ResponseInfo) error {
		if info.Error != nil || len(info.Response.Message.Parts) == 0 ||
			info.Request.Metadata[compressionprot.MetadataPurpose] == compressionprot.PurposeSummary {
			return nil
		}
		message := info.Response.Message.Clone()
		if message.Role == "" {
			message.Role = aiprot.RoleAssistant
		}
		return workspace.history.append(protocol.ModelMessage, workspace.history.runID(), protocol.EventData{Message: &message})
	}
	client := ai.NewClient(provider, aiprot.Hooks{
		OnRequest: []aiprot.RequestHook{guardRequest},
		OnResponse: []aiprot.ResponseHook{recordResponse, func(ctx context.Context, info aiprot.ResponseInfo) error {
			if options.OnModelResponse == nil {
				return nil
			}
			return options.OnModelResponse(ctx, protocol.ModelResponseInfo{ResponseInfo: info, APIType: config.APIType})
		}},
	})
	workspace.agent = agent.New(client)
	return workspace, nil
}

// Execute serializes one user turn through the existing Agent. Concurrent
// Execute/Reset calls fail; Close cancels a running turn and waits for cleanup.
func (workspace *Workspace) Execute(ctx context.Context, prompt string) (result loopprot.Result, err error) {
	workspace.mu.Lock()
	if err := workspace.ready(); err != nil {
		workspace.mu.Unlock()
		return result, err
	}
	if workspace.checkpointErr != nil {
		workspace.mu.Unlock()
		return result, workspace.checkpointErr
	}
	if strings.TrimSpace(prompt) == "" {
		workspace.mu.Unlock()
		return result, fmt.Errorf("user prompt is required")
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(workspace.config.TimeoutSeconds)*time.Second)
	stop := context.AfterFunc(workspace.lifetime, cancel)
	done := make(chan struct{})
	workspace.runCancel, workspace.runDone = cancel, done
	workspace.mu.Unlock()
	defer func() {
		stop()
		cancel()
		workspace.mu.Lock()
		workspace.runCancel, workspace.runDone = nil, nil
		close(done)
		workspace.mu.Unlock()
	}()
	var currentCallID string
	delegations := 0
	tools := make([]loopprot.Tool, len(workspace.tools))
	for index, tool := range workspace.tools {
		execute := tool.Execute
		isSubagent := workspace.subagents[tool.Definition.Name]
		tools[index] = tool
		tools[index].Execute = func(ctx context.Context, arguments json.RawMessage) (aiprot.ToolResult, error) {
			if err := ctx.Err(); err != nil {
				return aiprot.ToolResult{}, err
			}
			if err := workspace.history.append(protocol.ToolStarted, workspace.history.runID(), protocol.EventData{ToolCallID: currentCallID}); err != nil {
				return aiprot.ToolResult{}, err
			}
			if isSubagent {
				if delegations == maxDelegationsPerTurn {
					result := aiprot.TextToolResult(fmt.Sprintf("Status: delegation_limit\nThis turn has used its %d subagent delegations.", maxDelegationsPerTurn))
					result.IsError = true
					return result, nil
				}
				delegations++
			}
			return execute(ctx, arguments)
		}
	}
	hooks := workspace.options.Hooks
	onStart, onToolStart, onToolEnd := hooks.OnRunStart, hooks.OnToolStart, hooks.OnToolEnd
	hooks.OnRunStart = func(ctx context.Context, info loopprot.RunStartInfo) error {
		if err := workspace.history.append(protocol.RunStarted, info.RunID, protocol.EventData{}); err != nil {
			return err
		}
		message := aiprot.TextMessage(aiprot.RoleUser, prompt)
		if err := workspace.history.append(protocol.UserMessage, info.RunID, protocol.EventData{Message: &message}); err != nil {
			return err
		}
		if onStart != nil {
			return onStart(ctx, info)
		}
		return nil
	}
	hooks.OnToolStart = func(ctx context.Context, info loopprot.ToolStartInfo) error {
		currentCallID = info.Call.ID
		if onToolStart != nil {
			return onToolStart(ctx, info)
		}
		return nil
	}
	hooks.OnToolEnd = func(ctx context.Context, info loopprot.ToolEndInfo) error {
		message := resultMessage(info.Result)
		if err := workspace.history.append(protocol.ToolResult, info.RunID, protocol.EventData{Message: &message}); err != nil {
			return err
		}
		if onToolEnd != nil {
			return onToolEnd(ctx, info)
		}
		return nil
	}
	result, err = workspace.agent.Execute(runCtx, agentprot.Request{
		Model: workspace.config.Model, SystemPrompt: workspace.system, History: workspace.modelHistory(), UserPrompt: prompt, Tools: tools,
		Reasoning: workspace.config.Reasoning(), ProviderOptions: workspace.config.ProviderOptions,
		Options: loopprot.RunOptions{
			MaxSteps: workspace.config.MaxSteps, MaxHistoryRounds: workspace.config.MaxHistoryRounds,
			Compression: workspace.config.Compression, ConfirmTool: workspace.confirm, Hooks: hooks,
		},
	})
	if runID := workspace.history.runID(); runID != "" {
		if recoveryErr := workspace.history.completePending(workspace.options.Warn); recoveryErr != nil {
			return result, errors.Join(err, recoveryErr)
		}
		data := protocol.EventData{StopReason: result.StopReason}
		if err != nil {
			data.Error = "execution failed or was cancelled"
			data.StopReason = loopprot.StopError
		}
		if endErr := workspace.history.append(protocol.RunEnded, runID, data); endErr != nil {
			return result, errors.Join(err, endErr)
		}
		if err == nil && result.StopReason == loopprot.StopNatural {
			err = workspace.saveContextCheckpoint(runID, result.ContextMessages)
		}
	}
	return result, err
}

func (workspace *Workspace) confirm(ctx context.Context, info loopprot.ToolConfirmationInfo) (loopprot.ToolConfirmationDecision, error) {
	if err := errors.Join(ctx.Err(), workspace.history.healthy()); err != nil {
		return loopprot.ToolConfirmationDecision{}, err
	}
	mode := workspace.options.Approval
	if mode == protocol.ApprovalAuto {
		return loopprot.ToolConfirmationDecision{Approved: true}, nil
	}
	// Classification comes from local tool provenance, not an MCP-chosen name.
	if mode != protocol.ApprovalAll && workspace.readOnly[info.Call.Name] {
		return loopprot.ToolConfirmationDecision{Approved: true}, nil
	}
	if workspace.options.ConfirmTool == nil {
		return loopprot.ToolConfirmationDecision{Reason: "no approval callback supplied"}, nil
	}
	return workspace.options.ConfirmTool(ctx, info)
}

// History returns an owned snapshot of messages since the latest reset. During
// execution it may include a tool batch whose results are still pending.
func (workspace *Workspace) History() []aiprot.Message { return workspace.history.snapshot() }

func (workspace *Workspace) Reset(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	workspace.mu.Lock()
	defer workspace.mu.Unlock()
	if err := workspace.ready(); err != nil {
		return err
	}
	if err := workspace.history.append(protocol.ContextReset, "", protocol.EventData{}); err != nil {
		return err
	}
	workspace.checkpoint = nil
	workspace.checkpointErr = workspace.removeContextCheckpoint()
	return workspace.checkpointErr
}

func (workspace *Workspace) ready() error {
	if workspace.closed {
		return fmt.Errorf("workspace is closed")
	}
	if workspace.runDone != nil {
		return fmt.Errorf("workspace is executing a turn")
	}
	if err := workspace.lifetime.Err(); err != nil {
		return err
	}
	return workspace.history.healthy()
}

// Close is idempotent and safe to call concurrently, but must not be invoked
// synchronously from execution hooks (it waits for that execution to finish).
func (workspace *Workspace) Close() error {
	workspace.closeOnce.Do(func() {
		workspace.mu.Lock()
		workspace.closed = true
		if workspace.runCancel != nil {
			workspace.runCancel()
		}
		done := workspace.runDone
		workspace.mu.Unlock()
		if done != nil {
			<-done
		}
		if workspace.runtime != nil {
			workspace.closeErr = errors.Join(workspace.closeErr, workspace.runtime.Close())
		}
		if workspace.stopLifetime != nil {
			workspace.stopLifetime()
		}
		if workspace.history != nil {
			workspace.closeErr = errors.Join(workspace.closeErr, workspace.history.file.Close())
		}
		if workspace.lock != nil {
			workspace.closeErr = errors.Join(workspace.closeErr, workspace.lock.Close())
		}
	})
	return workspace.closeErr
}

func (workspace *Workspace) warn(message string) {
	if workspace.options.Warn != nil {
		workspace.options.Warn(message)
	}
}

func workspaceProvider(config protocol.Config, provider aiprot.Provider) (aiprot.Provider, error) {
	if provider != nil {
		return provider, nil
	}
	key := ""
	if config.APIKeyEnv != "" {
		key = os.Getenv(config.APIKeyEnv)
	} else {
		key = os.Getenv("AGENT_LLM_TOKEN")
		if key == "" {
			key = os.Getenv("AGENT_LLM_API_KEY")
		}
	}
	return openai.New(openai.Config{
		BaseURL: config.BaseURL, APIKey: key, APIType: openai.APIType(config.APIType),
		Retry: openai.RetryPolicy{MaxRetries: *config.ModelMaxRetries},
	})
}
