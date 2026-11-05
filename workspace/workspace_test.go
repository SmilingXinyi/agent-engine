package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentprot "github.com/SmilingXinyi/agent-engine/agent/protocol"
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	mcpprot "github.com/SmilingXinyi/agent-engine/mcp-runtime/protocol"
	"github.com/SmilingXinyi/agent-engine/workspace/protocol"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeProvider func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error)

func (fakeProvider) Name() string { return "workspace-test" }
func (provider fakeProvider) Generate(ctx context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
	return provider(ctx, request)
}

func answer(text string) aiprot.ModelResponse {
	return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, text), Complete: true}
}
func calls(calls ...aiprot.ToolCall) aiprot.ModelResponse {
	message := aiprot.Message{Role: aiprot.RoleAssistant}
	for _, call := range calls {
		message.Parts = append(message.Parts, aiprot.ContentPart{Type: aiprot.ContentToolCall, ToolCall: &call})
	}
	return aiprot.ModelResponse{Message: message, Complete: true}
}
func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, content)
}
func writeFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
}
func newProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	// TempDir can live under /var on macOS; use the same canonical path as Open.
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(root, ".agents/workspace.json"), protocol.Config{Model: "test-model"})
	// Most tests need no executable dependency.
	writeFile(t, filepath.Join(root, ".agents/tools.json"), []byte("{\"builtinTools\":{}}"))
	return root
}
func openTest(t *testing.T, root string, options protocol.OpenOptions) *Workspace {
	t.Helper()
	if options.Provider == nil {
		options.Provider = fakeProvider(func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) { return answer("done"), nil })
	}
	instance, err := Open(t.Context(), root, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := instance.Close(); err != nil {
			t.Error(err)
		}
	})
	return instance
}
func events(t *testing.T, root string) []protocol.Event {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, ".agents/state/history.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	var result []protocol.Event
	for _, line := range bytes.Split(bytes.TrimSuffix(content, []byte("\n")), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var event protocol.Event
		if err := protocol.DecodeJSON(line, &event); err != nil {
			t.Fatal(err)
		}
		if event.Sequence != uint64(len(result)+1) {
			t.Fatalf("sequence: %d", event.Sequence)
		}
		result = append(result, event)
	}
	return result
}

func TestIndependentWorkspacesAndReopen(t *testing.T) {
	first, second := newProject(t), newProject(t)
	for index, root := range []string{first, second} {
		label := []string{"first", "second"}[index]
		writeFile(t, filepath.Join(root, "AGENTS.md"), []byte(label+" project instructions"))
		if err := os.Mkdir(filepath.Join(root, ".agents/skills", label), 0700); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(root, ".agents/skills", label, "SKILL.md"), []byte("---\nname: "+label+"\ndescription: "+label+" skill\n---\nprivate skill body"))
		instance := openTest(t, root, protocol.OpenOptions{Provider: fakeProvider(func(_ context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
			system := request.Messages[0].Text()
			if !strings.Contains(system, label+" project instructions") || !strings.Contains(system, label+" skill") || strings.Contains(system, "private skill body") {
				t.Errorf("system=%s", system)
			}
			if request.Model != "test-model" {
				t.Errorf("model=%s", request.Model)
			}
			if len(request.Messages) != 2 {
				t.Errorf("messages=%+v", request.Messages)
			}
			return answer(label), nil
		})})
		if _, err := instance.Execute(t.Context(), "hello "+label); err != nil {
			t.Fatal(err)
		}
		if err := instance.Close(); err != nil {
			t.Fatal(err)
		}
	}
	reopened := openTest(t, first, protocol.OpenOptions{Provider: fakeProvider(func(_ context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		systems := 0
		for _, message := range request.Messages {
			if message.Role == aiprot.RoleSystem {
				systems++
			}
			if strings.Contains(message.Text(), "second") {
				t.Error("workspace leaked")
			}
		}
		if systems != 1 || len(request.Messages) != 4 {
			t.Errorf("systems=%d messages=%+v", systems, request.Messages)
		}
		return answer("continued"), nil
	})})
	snapshot := reopened.History()
	if len(snapshot) != 2 {
		t.Fatal(snapshot)
	}
	snapshot[0].Parts[0].Text = "mutated"
	if _, err := reopened.Execute(t.Context(), "continue"); err != nil {
		t.Fatal(err)
	}
	if reopened.History()[0].Text() != "hello first" {
		t.Fatal("history alias")
	}
	beforeReset := len(events(t, first))
	if err := reopened.Reset(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(reopened.History()) != 0 || len(events(t, first)) != beforeReset+1 {
		t.Fatal("reset did not append an event")
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	if len(openTest(t, first, protocol.OpenOptions{}).History()) != 0 {
		t.Fatal("reset was not replayed")
	}
}

func TestFileToolApprovalAndExclusion(t *testing.T) {
	for _, mode := range []string{"approve", "deny", "disabled", "read", "all-read", "auto"} {
		t.Run(mode, func(t *testing.T) {
			root := newProject(t)
			writeFile(t, filepath.Join(root, "note"), []byte("original"))
			tool, arguments := "write_file", json.RawMessage("{\"path\":\"note\",\"content\":\"changed\"}")
			if strings.Contains(mode, "read") {
				tool, arguments = "read_file", json.RawMessage("{\"path\":\"note\"}")
			}
			writeJSON(t, filepath.Join(root, ".agents/tools.json"), map[string]any{"builtinTools": map[string]any{tool: map[string]bool{"disabled": mode == "disabled"}}})
			approvals, requests := 0, 0
			options := protocol.OpenOptions{ConfirmTool: func(context.Context, loopprot.ToolConfirmationInfo) (loopprot.ToolConfirmationDecision, error) {
				approvals++
				return loopprot.ToolConfirmationDecision{Approved: mode != "deny"}, nil
			}}
			if mode == "auto" {
				options.Approval = protocol.ApprovalAuto
			}
			if mode == "all-read" {
				options.Approval = protocol.ApprovalAll
			}
			options.Provider = fakeProvider(func(_ context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
				requests++
				if requests == 1 {
					if (len(request.Tools) == 0) != (mode == "disabled") {
						t.Error("disabled definition exposed")
					}
					return calls(aiprot.ToolCall{ID: "call", Name: tool, Arguments: arguments}), nil
				}
				result := request.Messages[len(request.Messages)-1].ToolResults()[0]
				if result.IsError != (mode == "deny" || mode == "disabled") {
					t.Errorf("result=%+v", result)
				}
				if mode == "read" && result.Text() != "original" {
					t.Error(result.Text())
				}
				return answer("done"), nil
			})
			instance := openTest(t, root, options)
			if _, err := instance.Execute(t.Context(), "use tool"); err != nil {
				t.Fatal(err)
			}
			wantApprovals := 1
			if mode == "disabled" || mode == "read" || mode == "auto" {
				wantApprovals = 0
			}
			if approvals != wantApprovals {
				t.Errorf("approvals=%d", approvals)
			}
			content, err := os.ReadFile(filepath.Join(root, "note"))
			if err != nil {
				t.Fatal(err)
			}
			changed := mode == "approve" || mode == "auto"
			if (string(content) == "changed") != changed {
				t.Fatalf("disk=%s", content)
			}
			starts := 0
			for _, event := range events(t, root) {
				if event.Type == protocol.ToolStarted {
					starts++
				}
			}
			if (starts == 1) != (mode != "disabled" && mode != "deny") {
				t.Errorf("tool starts=%d", starts)
			}
		})
	}
}

func TestStoppedBatchIsCompletedWithoutReplay(t *testing.T) {
	for _, mode := range []string{"max-steps", "cancel", "confirmation-error", "hook-error", "model-error", "malformed-model"} {
		t.Run(mode, func(t *testing.T) {
			root := newProject(t)
			writeFile(t, filepath.Join(root, ".agents/tools.json"), []byte("{\"builtinTools\":{\"write_file\":{}}}"))
			config := protocol.Config{Model: "test"}
			if mode == "max-steps" {
				config.MaxSteps = 1
			}
			writeJSON(t, filepath.Join(root, ".agents/workspace.json"), config)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			options := protocol.OpenOptions{Provider: fakeProvider(func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
				if mode == "model-error" {
					return aiprot.ModelResponse{}, errors.New("provider failed")
				}
				id := "first"
				if mode == "malformed-model" {
					id = ""
				}
				return calls(
					aiprot.ToolCall{ID: id, Name: "write_file", Arguments: json.RawMessage("{\"path\":\"first\",\"content\":\"yes\"}")},
					aiprot.ToolCall{ID: "second", Name: "write_file", Arguments: json.RawMessage("{\"path\":\"second\",\"content\":\"no\"}")},
				), nil
			}), ConfirmTool: func(context.Context, loopprot.ToolConfirmationInfo) (loopprot.ToolConfirmationDecision, error) {
				if mode == "confirmation-error" {
					return loopprot.ToolConfirmationDecision{}, errors.New("input closed")
				}
				return loopprot.ToolConfirmationDecision{Approved: true}, nil
			}, Hooks: loopprot.Hooks{OnToolEnd: func(_ context.Context, info loopprot.ToolEndInfo) error {
				if info.Call.ID == "first" {
					if mode == "cancel" {
						cancel()
					}
					if mode == "hook-error" {
						return errors.New("observer failed")
					}
				}
				return nil
			}}}
			instance := openTest(t, root, options)
			result, err := instance.Execute(ctx, "write")
			if mode == "max-steps" {
				if err != nil || result.StopReason != loopprot.StopMaxSteps {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			} else if err == nil {
				t.Fatal("expected execution failure")
			}
			if err := (agentprot.Request{Model: "test", History: instance.History()}).Validate(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(root, "second")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("second call executed")
			}
			if err := instance.Close(); err != nil {
				t.Fatal(err)
			}
			var requests atomic.Int32
			reopened := openTest(t, root, protocol.OpenOptions{Provider: fakeProvider(func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
				requests.Add(1)
				return answer("continued"), nil
			})})
			if requests.Load() != 0 {
				t.Fatal("open resumed model calls")
			}
			if _, err := reopened.Execute(t.Context(), "continue"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCancellationCloseAndConcurrentOperations(t *testing.T) {
	root := newProject(t)
	started := make(chan struct{})
	instance := openTest(t, root, protocol.OpenOptions{Provider: fakeProvider(func(ctx context.Context, _ aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		close(started)
		<-ctx.Done()
		return aiprot.ModelResponse{}, ctx.Err()
	})})
	finished := make(chan error, 1)
	go func() { _, err := instance.Execute(t.Context(), "wait"); finished <- err }()
	<-started
	if _, err := instance.Execute(t.Context(), "concurrent"); err == nil {
		t.Fatal("concurrent execution accepted")
	}
	if err := instance.Reset(t.Context()); err == nil {
		t.Fatal("concurrent reset accepted")
	}
	_ = instance.History()
	if err := instance.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := instance.Execute(t.Context(), "closed"); err == nil {
		t.Fatal("closed workspace accepted")
	}
	_ = openTest(t, root, protocol.OpenOptions{})
}

func TestPersistenceFailureStopsInstanceBeforeTool(t *testing.T) {
	root := newProject(t)
	writeFile(t, filepath.Join(root, ".agents/tools.json"), []byte("{\"builtinTools\":{\"write_file\":{}}}"))
	var instance *Workspace
	requests := 0
	instance = openTest(t, root, protocol.OpenOptions{Approval: protocol.ApprovalAuto, Provider: fakeProvider(func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		requests++
		// Simulate a filesystem failure at model-response persistence.
		if err := instance.history.file.Close(); err != nil {
			t.Fatal(err)
		}
		return calls(aiprot.ToolCall{ID: "write", Name: "write_file", Arguments: json.RawMessage("{\"path\":\"forbidden\",\"content\":\"no\"}")}), nil
	})})
	if _, err := instance.Execute(t.Context(), "write"); err == nil || !strings.Contains(err.Error(), "persistence failed") {
		t.Fatal(err)
	}
	if _, err := instance.Execute(t.Context(), "again"); err == nil {
		t.Fatal("poisoned workspace executed")
	}
	if requests != 1 {
		t.Fatal(requests)
	}
	if _, err := os.Stat(filepath.Join(root, "forbidden")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("tool ran after failed persistence")
	}
	// Restore a valid descriptor solely so normal cleanup can release resources.
	file, err := os.OpenFile(filepath.Join(root, ".agents/state/history.ndjson"), os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	instance.history.file = file
}

func TestFailedResultPersistenceRecoversAsUnknown(t *testing.T) {
	root := newProject(t)
	writeFile(t, filepath.Join(root, ".agents/tools.json"), []byte("{\"builtinTools\":{\"write_file\":{}}}"))
	instance := openTest(t, root, protocol.OpenOptions{Approval: protocol.ApprovalAuto, Provider: fakeProvider(func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		return calls(
			aiprot.ToolCall{ID: "first", Name: "write_file", Arguments: json.RawMessage("{\"path\":\"first\",\"content\":\"effect\"}")},
			aiprot.ToolCall{ID: "second", Name: "write_file", Arguments: json.RawMessage("{\"path\":\"second\",\"content\":\"forbidden\"}")},
		), nil
	})})
	realWrite := instance.tools[0].Execute
	instance.tools[0].Execute = func(ctx context.Context, arguments json.RawMessage) (aiprot.ToolResult, error) {
		result, err := realWrite(ctx, arguments)
		if closeErr := instance.history.file.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		return result, err
	}
	if _, err := instance.Execute(t.Context(), "write"); err == nil {
		t.Fatal("result persistence failure ignored")
	}
	if _, err := os.Stat(filepath.Join(root, "second")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("continued after persistence failure")
	}
	file, err := os.OpenFile(filepath.Join(root, ".agents/state/history.ndjson"), os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	instance.history.file = file
	if err := instance.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTest(t, root, protocol.OpenOptions{})
	results := reopened.History()
	if len(results) != 4 || !strings.Contains(results[2].ToolResults()[0].Text(), "unknown") || !strings.Contains(results[3].ToolResults()[0].Text(), "not executed") {
		t.Fatalf("recovery=%+v", results)
	}
	content, err := os.ReadFile(filepath.Join(root, "first"))
	if err != nil || string(content) != "effect" {
		t.Fatalf("effect=%s err=%v", content, err)
	}
}

func TestWorkspaceTimeout(t *testing.T) {
	root := newProject(t)
	writeJSON(t, filepath.Join(root, ".agents/workspace.json"), protocol.Config{Model: "test", TimeoutSeconds: 1})
	instance := openTest(t, root, protocol.OpenOptions{Provider: fakeProvider(func(ctx context.Context, _ aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		<-ctx.Done()
		return aiprot.ModelResponse{}, ctx.Err()
	})})
	if _, err := instance.Execute(t.Context(), "wait"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if err := (agentprot.Request{Model: "test", History: instance.History()}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestConfigBoundariesAndInitPreservesFiles(t *testing.T) {
	root := newProject(t)
	configPath := filepath.Join(root, ".agents/workspace.json")
	before, _ := os.ReadFile(configPath)
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(configPath)
	if !bytes.Equal(before, after) {
		t.Fatal("init overwrote configuration")
	}
	for _, config := range []string{
		"{\"builtinTools\":{\"read_file\":{}},\"filesystem\":{\"root\":\"..\"}}",
		"{\"builtinTools\":{\"shell\":{}},\"shell\":{\"dir\":\"..\"}}",
		"{\"builtinTools\":{\"typo\":{}}}",
	} {
		writeFile(t, filepath.Join(root, ".agents/tools.json"), []byte(config))
		if _, err := Open(t.Context(), root, protocol.OpenOptions{Provider: fakeProvider(nil)}); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
	writeFile(t, filepath.Join(root, ".agents/tools.json"), []byte("{\"builtinTools\":{\"shell\":{\"disabled\":true}},\"shell\":{\"dir\":\"/does-not-exist\"}}"))
	if err := os.Remove(filepath.Join(root, ".agents/mcp.json")); err != nil {
		t.Fatal(err)
	}
	instance := openTest(t, root, protocol.OpenOptions{})
	if err := instance.Close(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, ".agents/mcp.json"), []byte("{invalid"))
	if _, err := Open(t.Context(), root, protocol.OpenOptions{Provider: fakeProvider(nil)}); err == nil {
		t.Fatal("invalid MCP accepted")
	}
}

func TestWorkspaceLockHelper(t *testing.T) {
	if os.Getenv("WORKSPACE_LOCK_HELPER") != "1" {
		return
	}
	file, err := acquireLock(os.Getenv("WORKSPACE_LOCK_PATH"))
	if err == nil {
		file.Close()
		os.Exit(2)
	}
	os.Exit(0)
}

func TestCrossProcessLock(t *testing.T) {
	root := newProject(t)
	instance := openTest(t, root, protocol.OpenOptions{})
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), executable, "-test.run=^TestWorkspaceLockHelper$")
	command.Env = append(os.Environ(), "WORKSPACE_LOCK_HELPER=1", "WORKSPACE_LOCK_PATH="+filepath.Join(root, ".agents/state/workspace.lock"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("lock was not exclusive: %s %v", output, err)
	}
	if _, err := Open(t.Context(), root, protocol.OpenOptions{Provider: fakeProvider(nil)}); err == nil {
		t.Fatal("duplicate open accepted")
	}
	if err := instance.Close(); err != nil {
		t.Fatal(err)
	}
	_ = openTest(t, root, protocol.OpenOptions{})
}

func TestWorkspaceMCPProcess(t *testing.T) {
	if os.Getenv("WORKSPACE_MCP_HELPER") != "1" {
		return
	}
	marker := os.Getenv("WORKSPACE_MCP_MARKER")
	if err := os.WriteFile(marker+".started", []byte("started"), 0600); err != nil {
		os.Exit(2)
	}
	server := sdk.NewServer(&sdk.Implementation{Name: "workspace-test", Version: "1"}, nil)
	toolName := os.Getenv("WORKSPACE_MCP_TOOL")
	if toolName == "" {
		toolName = "touch"
	}
	server.AddTool(&sdk.Tool{Name: toolName, InputSchema: json.RawMessage("{\"type\":\"object\"}")}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		directory, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(marker+".called", []byte(directory), 0600); err != nil {
			return nil, err
		}
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: directory}}}, nil
	})
	err := server.Run(context.Background(), &sdk.StdioTransport{})
	_ = os.WriteFile(marker+".closed", []byte("closed"), 0600)
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func mcpServer(t *testing.T, marker, toolName string) mcpprot.ServerConfig {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return mcpprot.ServerConfig{Command: executable, Args: []string{"-test.run=^TestWorkspaceMCPProcess$"}, Env: map[string]string{
		"WORKSPACE_MCP_HELPER": "1", "WORKSPACE_MCP_MARKER": marker, "WORKSPACE_MCP_TOOL": toolName,
	}}
}

func TestMCPApprovalProvenanceDirectoryAndCleanup(t *testing.T) {
	for _, allow := range []bool{false, true} {
		t.Run(map[bool]string{false: "deny", true: "allow"}[allow], func(t *testing.T) {
			root := newProject(t)
			marker := filepath.Join(root, "mcp")
			// MCP can produce a name equal to a built-in that is not enabled.
			// It must still require confirmation.
			writeJSON(t, filepath.Join(root, ".agents/mcp.json"), mcpprot.Config{Servers: map[string]mcpprot.ServerConfig{"read": mcpServer(t, marker, "file")}})
			confirmations, requests := 0, 0
			instance := openTest(t, root, protocol.OpenOptions{ConfirmTool: func(context.Context, loopprot.ToolConfirmationInfo) (loopprot.ToolConfirmationDecision, error) {
				confirmations++
				return loopprot.ToolConfirmationDecision{Approved: allow}, nil
			}, Provider: fakeProvider(func(_ context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
				requests++
				if requests == 1 {
					return calls(aiprot.ToolCall{ID: "mcp", Name: "read_file", Arguments: json.RawMessage("{}")}), nil
				}
				if request.Messages[len(request.Messages)-1].ToolResults()[0].IsError == allow {
					t.Error("unexpected MCP result")
				}
				return answer("done"), nil
			})})
			if _, err := instance.Execute(t.Context(), "MCP"); err != nil {
				t.Fatal(err)
			}
			if confirmations != 1 {
				t.Fatal(confirmations)
			}
			content, err := os.ReadFile(marker + ".called")
			if allow && (err != nil || string(content) != root) {
				t.Fatalf("MCP dir=%s err=%v", content, err)
			}
			if !allow && !errors.Is(err, os.ErrNotExist) {
				t.Fatal("denied MCP executed")
			}
			if err := instance.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(marker + ".closed"); err != nil {
				t.Fatal("MCP resource not closed", err)
			}
		})
	}
	root := newProject(t)
	marker := filepath.Join(root, "startup")
	writeJSON(t, filepath.Join(root, ".agents/mcp.json"), mcpprot.Config{Servers: map[string]mcpprot.ServerConfig{
		"a": mcpServer(t, marker, ""), "b": {Command: "/nonexistent/workspace-mcp"},
	}})
	if _, err := Open(t.Context(), root, protocol.OpenOptions{Provider: fakeProvider(nil)}); err == nil {
		t.Fatal("MCP startup failure accepted")
	}
	if _, err := os.Stat(marker + ".closed"); err != nil {
		t.Fatal("earlier MCP resource leaked", err)
	}
	writeFile(t, filepath.Join(root, ".agents/mcp.json"), []byte("{\"mcpServers\":{}}"))
	_ = openTest(t, root, protocol.OpenOptions{}) // failed open released the lock
}

func TestShellDirectoryAndCancellation(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is required")
	}
	root := newProject(t)
	writeFile(t, filepath.Join(root, ".agents/tools.json"), []byte("{\"builtinTools\":{\"shell\":{}}}"))
	requests := 0
	instance := openTest(t, root, protocol.OpenOptions{Approval: protocol.ApprovalAuto, Provider: fakeProvider(func(_ context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		requests++
		if requests == 1 {
			return calls(aiprot.ToolCall{ID: "shell", Name: "shell", Arguments: json.RawMessage("{\"command\":\"pwd\"}")}), nil
		}
		if !strings.Contains(request.Messages[len(request.Messages)-1].ToolResults()[0].Text(), root) {
			t.Fatal("wrong shell working directory")
		}
		return answer("done"), nil
	})})
	if _, err := instance.Execute(t.Context(), "pwd"); err != nil {
		t.Fatal(err)
	}
	if err := instance.Close(); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	cancelled := openTest(t, root, protocol.OpenOptions{Approval: protocol.ApprovalAuto, Provider: fakeProvider(func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		return calls(aiprot.ToolCall{ID: "wait", Name: "shell", Arguments: json.RawMessage("{\"command\":\"sleep 30\"}")}), nil
	}), Hooks: loopprot.Hooks{OnToolStart: func(context.Context, loopprot.ToolStartInfo) error { close(started); return nil }}})
	ctx, cancel := context.WithCancel(t.Context())
	finished := make(chan error, 1)
	go func() { _, err := cancelled.Execute(ctx, "wait"); finished <- err }()
	<-started
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shell cancellation stuck")
	}
	if err := (agentprot.Request{Model: "test", History: cancelled.History()}).Validate(); err != nil {
		t.Fatal(err)
	}
}
