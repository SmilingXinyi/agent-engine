package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	"github.com/SmilingXinyi/agent-engine/compression"
	mcpprot "github.com/SmilingXinyi/agent-engine/mcp-runtime/protocol"
	"github.com/SmilingXinyi/agent-engine/workspace/protocol"
)

func writeRole(t *testing.T, root, name, model, tools string) string {
	t.Helper()
	directory := filepath.Join(root, ".agents", subagentsDirectory, name)
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(directory, "workspace.yaml"), map[string]string{"description": "Investigate " + name, "model": model})
	writeFile(t, filepath.Join(directory, "tools.yaml"), []byte(tools))
	return directory
}

func subagentCall(id, name, arguments string) aiprot.ToolCall {
	return aiprot.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(arguments)}
}

func TestWorkspaceSubagents(t *testing.T) {
	root := newProject(t)
	writeFile(t, filepath.Join(root, ".agents/tools.yaml"), []byte(`{"builtinTools":{"read_file":{},"list_directory":{}}}`))
	writeFile(t, filepath.Join(root, "AGENTS.md"), []byte("shared repository rules"))
	writeFile(t, filepath.Join(root, ".agents/prompt.md"), []byte("parent-only instructions"))
	writeFile(t, filepath.Join(root, "entry.txt"), []byte("the entry point is Start\n"))
	firstRole := writeRole(t, root, "locate", "locator-model", `{"builtinTools":{"list_directory":{},"read_file":{"disabled":true}}}`)
	writeFile(t, filepath.Join(firstRole, "prompt.md"), []byte("locator prompt"))
	writeRole(t, root, "verify", "", `{"builtinTools":{"read_file":{}}}`)
	requests := 0
	var confirmations []string
	instance := openTest(t, root, protocol.OpenOptions{
		Approval: protocol.ApprovalAll,
		ConfirmTool: func(_ context.Context, info loopprot.ToolConfirmationInfo) (loopprot.ToolConfirmationDecision, error) {
			confirmations = append(confirmations, info.Call.Name)
			return loopprot.ToolConfirmationDecision{Approved: true}, nil
		},
		Provider: fakeProvider(func(ctx context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
			requests++
			system := request.Messages[0].Text()
			last := request.Messages[len(request.Messages)-1]
			switch requests {
			case 1:
				if len(request.Tools) != 4 || request.Tools[2].Name != "subagent_locate" || request.Tools[3].Name != "subagent_verify" ||
					!strings.Contains(system, delegationInstructions) || request.Tools[3].Description != "Investigate verify" {
					t.Fatalf("unexpected parent request: %+v", request)
				}
				return calls(subagentCall("first", "subagent_locate", `{"task":"Find the entry point"}`)), nil
			case 2:
				if request.Model != "locator-model" || len(request.Tools) != 1 || request.Tools[0].Name != "list_directory" || len(request.Messages) != 2 ||
					last.Text() != "Task:\nFind the entry point" || !strings.Contains(system, "locator prompt") || !strings.Contains(system, "shared repository rules") ||
					strings.Contains(system, "parent-only instructions") || strings.Contains(system, "changed prompt") {
					t.Fatalf("unexpected locator request: %+v", request)
				}
				if deadline, ok := ctx.Deadline(); !ok || deadline.IsZero() {
					t.Fatal("no child deadline")
				}
				return answer("Need the file path."), nil
			case 3:
				if results := last.ToolResults(); len(results) != 1 || !strings.Contains(results[0].Text(), "Need the file path.") {
					t.Fatalf("parent did not receive the first result: %+v", last)
				}
				return calls(subagentCall("second", "subagent_verify", `{"task":"Verify the entry point","context":"Read entry.txt"}`),
					subagentCall("third", "subagent_locate", `{"task":"Do not run this"}`)), nil
			case 4:
				if request.Model != "test-model" || len(request.Tools) != 1 || request.Tools[0].Name != "read_file" || len(request.Messages) != 2 ||
					last.Text() != "Task:\nVerify the entry point\n\nContext:\nRead entry.txt" || strings.Contains(system, "locator prompt") {
					t.Fatalf("unexpected verifier request: %+v", request)
				}
				return calls(subagentCall("child-read", "read_file", `{"path":"entry.txt"}`)), nil
			case 5:
				if results := last.ToolResults(); len(results) != 1 || !strings.Contains(results[0].Text(), "the entry point is Start") {
					t.Fatalf("child did not observe real file: %+v", last)
				}
				response := answer("The entry point is Start (entry.txt:1).")
				response.Usage = aiprot.Usage{TotalTokens: 12}
				return response, nil
			case 6:
				if results := last.ToolResults(); len(results) != 1 || !results[0].IsError || !strings.Contains(results[0].Text(), "delegation_limit") {
					t.Fatalf("parent did not observe the shared limit: %+v", last)
				}
				return answer("Start is confirmed."), nil
			default:
				t.Fatalf("unexpected model request %d", requests)
				return aiprot.ModelResponse{}, nil
			}
		}),
	})
	writeFile(t, filepath.Join(firstRole, "prompt.md"), []byte("changed prompt"))
	result, err := instance.Execute(t.Context(), "parent-only request")
	if err != nil || requests != 6 || len(result.ToolCalls) != 3 || result.Usage.TotalTokens != 0 {
		t.Fatalf("result=%+v requests=%d err=%v", result, requests, err)
	}
	if want := []string{"subagent_locate", "subagent_verify", "read_file", "subagent_locate"}; !reflect.DeepEqual(confirmations, want) {
		t.Fatalf("approval order %v, want %v", confirmations, want)
	}
	metadata := result.ToolCalls[1].Result.Metadata
	if metadata["subagent_run_id"] == nil || metadata["subagent_run_id"] == result.RunID || metadata["subagent_usage"].(map[string]any)["total_tokens"] != 12 {
		t.Fatalf("unexpected child metadata %+v", metadata)
	}
	journal, err := os.ReadFile(filepath.Join(root, ".agents/state/history.ndjson"))
	if err != nil || strings.Contains(string(journal), "child-read") || !strings.Contains(string(journal), "entry.txt:1") {
		t.Fatalf("unexpected parent journal: %s, err=%v", journal, err)
	}
	if err := instance.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTest(t, root, protocol.OpenOptions{})
	if _, err := reopened.Execute(t.Context(), "continue after reopening"); err != nil {
		t.Fatal(err)
	}
}

func TestSubagentConfiguration(t *testing.T) {
	options := protocol.OpenOptions{Provider: fakeProvider(func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		return aiprot.ModelResponse{}, errors.New("opening a workspace must not call the provider")
	})}
	for _, scenario := range []struct{ name, tools, want string }{
		{"disabled_parent", `{"builtinTools":{"read_file":{}}}`, "not an enabled local read-only"},
		{"write_tool", `{"builtinTools":{"write_file":{}}}`, "not an enabled local read-only"},
		{"empty_selection", `{"builtinTools":{"list_directory":{"disabled":true}}}`, "select at least one"},
		{"own_root", `{"builtinTools":{"list_directory":{}},"filesystem":{"root":"/"}}`, "unknown field"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			root := newProject(t)
			writeFile(t, filepath.Join(root, ".agents/tools.yaml"), []byte(`{"builtinTools":{"read_file":{"disabled":true},"list_directory":{},"write_file":{}}}`))
			directory := writeRole(t, root, "repo", "", scenario.tools)
			if _, err := Open(t.Context(), root, options); err == nil || !strings.Contains(err.Error(), scenario.want) ||
				!strings.Contains(err.Error(), filepath.Join(directory, "tools.yaml")) {
				t.Fatalf("unexpected Open error: %v", err)
			}
		})
	}
	t.Run("legacy_workspace", func(t *testing.T) {
		root := newProject(t)
		if err := os.Remove(filepath.Join(root, ".agents", subagentsDirectory)); err != nil {
			t.Fatal(err)
		}
		instance := openTest(t, root, protocol.OpenOptions{})
		if len(instance.subagents) != 0 {
			t.Fatal("registered subagents for a legacy workspace")
		}
	})
	t.Run("mcp_name_collision", func(t *testing.T) {
		root := newProject(t)
		writeFile(t, filepath.Join(root, ".agents/tools.yaml"), []byte(`{"builtinTools":{"list_directory":{}}}`))
		writeRole(t, root, "repo", "", `{"builtinTools":{"list_directory":{}}}`)
		writeJSON(t, filepath.Join(root, ".agents/mcp.yaml"), mcpprot.Config{Servers: map[string]mcpprot.ServerConfig{
			"subagent": mcpServer(t, filepath.Join(root, "mcp"), "repo"),
		}})
		if _, err := Open(t.Context(), root, options); err == nil || !strings.Contains(err.Error(), `duplicate workspace tool "subagent_repo"`) {
			t.Fatalf("unexpected Open error: %v", err)
		}
	})
}

func TestWorkspaceSubagentFailures(t *testing.T) {
	for _, scenario := range []string{"deny_parent", "deny_child", "timeout", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			root := newProject(t)
			writeFile(t, filepath.Join(root, ".agents/tools.yaml"), []byte(`{"builtinTools":{"read_file":{}}}`))
			writeFile(t, filepath.Join(root, "private.txt"), []byte("must remain unread"))
			writeRole(t, root, "repo", "child", `{"builtinTools":{"read_file":{}}}`)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			childRequests, parentRequests := 0, 0
			instance := openTest(t, root, protocol.OpenOptions{
				Approval: protocol.ApprovalAll,
				ConfirmTool: func(_ context.Context, info loopprot.ToolConfirmationInfo) (loopprot.ToolConfirmationDecision, error) {
					deny := scenario == "deny_parent" || (scenario == "deny_child" && info.Call.Name == "read_file")
					return loopprot.ToolConfirmationDecision{Approved: !deny}, nil
				},
				Provider: fakeProvider(func(callCtx context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
					if request.Model != "child" {
						parentRequests++
						if parentRequests == 1 {
							return calls(subagentCall("parent", "subagent_repo", `{"task":"inspect"}`)), nil
						}
						return answer("parent continued"), nil
					}
					childRequests++
					switch scenario {
					case "timeout":
						return aiprot.ModelResponse{}, context.DeadlineExceeded
					case "cancel":
						cancel()
						<-callCtx.Done()
						return aiprot.ModelResponse{}, callCtx.Err()
					case "deny_child":
						if childRequests == 1 {
							return calls(subagentCall("child", "read_file", `{"path":"private.txt"}`)), nil
						}
						results := request.Messages[len(request.Messages)-1].ToolResults()
						if len(results) != 1 || !results[0].IsError || strings.Contains(results[0].Text(), "must remain unread") {
							t.Fatalf("inner denial did not prevent execution: %+v", results)
						}
						return answer("Read denied."), nil
					default:
						t.Fatal("denied delegation started a child")
						return aiprot.ModelResponse{}, nil
					}
				}),
			})
			result, err := instance.Execute(ctx, "delegate")
			if scenario == "cancel" {
				if !errors.Is(err, context.Canceled) || parentRequests != 1 || childRequests != 1 {
					t.Fatalf("cancel: parent=%d child=%d error=%v", parentRequests, childRequests, err)
				}
				return
			}
			if err != nil || parentRequests != 2 || len(result.ToolCalls) != 1 {
				t.Fatalf("parent did not continue: requests=%d result=%+v error=%v", parentRequests, result, err)
			}
			toolResult := result.ToolCalls[0].Result
			if scenario == "timeout" && (!toolResult.IsError || !strings.Contains(toolResult.Text(), "Status: timeout")) {
				t.Fatalf("unexpected timeout result %+v", toolResult)
			}
		})
	}
}

func TestRepositoryResearchComparison(t *testing.T) {
	const evidence = "Run calls Confirm (entry.go:2), which defaults to deny (approval.go:2)."
	for _, delegated := range []bool{false, true} {
		name := "direct"
		if delegated {
			name = "delegated"
		}
		t.Run(name, func(t *testing.T) {
			root := newProject(t)
			const config = `{"builtinTools":{"read_file":{}}}`
			writeFile(t, filepath.Join(root, ".agents/tools.yaml"), []byte(config))
			padding := strings.Repeat("// unrelated implementation detail\n", 76)
			writeFile(t, filepath.Join(root, "entry.go"), []byte("package example\nfunc Run() bool { return Confirm() }\n"+padding))
			writeFile(t, filepath.Join(root, "approval.go"), []byte("package example\nfunc Confirm() bool { return false }\n"+padding))
			if delegated {
				role := writeRole(t, root, "repo_research", "researcher", config)
				writeFile(t, filepath.Join(role, "prompt.md"), []byte("Cite evidence with file paths and line numbers."))
			}
			peakParent, estimatedTotal := 0, 0
			instance := openTest(t, root, protocol.OpenOptions{Provider: fakeProvider(func(_ context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
				inputTokens := compression.Measure(request.Messages, 0).Tokens
				isChild := request.Model == "researcher"
				if !isChild {
					peakParent = max(peakParent, inputTokens)
				}
				var response aiprot.ModelResponse
				switch {
				case delegated && !isChild && request.Metadata[loopprot.MetadataStep] == "1":
					response = calls(subagentCall("research", "subagent_repo_research", `{"task":"Trace Run and Confirm","context":"Read entry.go and approval.go"}`))
				case delegated && !isChild:
					results := request.Messages[len(request.Messages)-1].ToolResults()
					if len(results) != 1 || !strings.Contains(results[0].Text(), evidence) || strings.Contains(results[0].Text(), "unrelated implementation") {
						t.Fatalf("expected evidence without raw file output: %+v", results)
					}
					response = answer(evidence)
				case request.Metadata[loopprot.MetadataStep] == "1":
					response = calls(subagentCall("entry", "read_file", `{"path":"entry.go"}`), subagentCall("approval", "read_file", `{"path":"approval.go"}`))
				default:
					messages := request.Messages
					entry := messages[len(messages)-2].ToolResults()
					approval := messages[len(messages)-1].ToolResults()
					if len(entry) != 1 || len(approval) != 1 || !strings.Contains(entry[0].Text(), "func Run() bool { return Confirm() }") ||
						!strings.Contains(approval[0].Text(), "func Confirm() bool { return false }") {
						t.Fatalf("missing file evidence: %+v %+v", entry, approval)
					}
					response = answer(evidence)
				}
				estimatedTotal += inputTokens + compression.Measure([]aiprot.Message{response.Message}, 0).Tokens
				return response, nil
			})})
			started := time.Now()
			result, err := instance.Execute(t.Context(), "Trace the approval decision and cite both lines.")
			if err != nil || result.LastResponse.Message.Text() != evidence {
				t.Fatalf("answer=%q err=%v", result.LastResponse.Message.Text(), err)
			}
			t.Logf("verified evidence, peak parent context=%d estimated tokens, total=%d estimated tokens, in-process time=%v", peakParent, estimatedTotal, time.Since(started))
		})
	}
}
