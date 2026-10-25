package aitools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	ai "github.com/SmilingXinyi/agent-engine/ai-sdk"
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loop "github.com/SmilingXinyi/agent-engine/ai-tool-loop"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	aitools "github.com/SmilingXinyi/agent-engine/ai-tools"
	"github.com/SmilingXinyi/agent-engine/ai-tools/protocol"
)

func TestBuildOnlyExplicitlyEnabledTools(t *testing.T) {
	config := protocol.Config{
		BuiltinTools: map[string]protocol.ToolConfig{
			protocol.ReadFile: {}, protocol.ListDirectory: {}, protocol.WriteFile: {Disabled: true}, protocol.ApplyPatch: {Disabled: true}, protocol.Shell: {Disabled: true},
		},
		Filesystem: protocol.FilesystemOptions{Root: t.TempDir()},
		Shell:      protocol.ShellOptions{Dir: "/does/not/exist", Executable: "unavailable"},
	}
	tools, err := aitools.Build(config)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools {
		names = append(names, tool.Definition.Name)
	}
	if !reflect.DeepEqual(names, []string{protocol.ListDirectory, protocol.ReadFile}) {
		t.Fatalf("names=%v", names)
	}
	if !config.BuiltinTools[protocol.WriteFile].Disabled {
		t.Fatal("config mutated")
	}
	for _, config := range []protocol.Config{{}, {BuiltinTools: map[string]protocol.ToolConfig{protocol.Shell: {Disabled: true}}}} {
		tools, err := aitools.Build(config)
		if err != nil || len(tools) != 0 {
			t.Fatalf("tools=%v err=%v", tools, err)
		}
	}
	if _, err := aitools.Build(protocol.Config{BuiltinTools: map[string]protocol.ToolConfig{"typo": {Disabled: true}}}); err == nil {
		t.Fatal("unknown name accepted")
	}
	if _, err := aitools.Build(protocol.Config{BuiltinTools: map[string]protocol.ToolConfig{protocol.ReadFile: {}}}); err == nil {
		t.Fatal("missing root accepted")
	}
}

func TestConfiguredPatchThroughTheLoop(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		name := "enabled"
		if disabled {
			name = "disabled"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "note")
			if err := os.WriteFile(path, []byte("old\n"), 0600); err != nil {
				t.Fatal(err)
			}
			document, _ := json.Marshal(map[string]any{
				"builtinTools": map[string]any{protocol.ApplyPatch: map[string]bool{"disabled": disabled}},
				"filesystem":   map[string]string{"root": root},
			})
			config, err := protocol.ParseConfig(string(document))
			if err != nil {
				t.Fatal(err)
			}
			tools, err := aitools.Build(config)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			provider := scriptedProvider{generate: func(request aiprot.ModelRequest) aiprot.ModelResponse {
				calls++
				if disabled {
					if len(request.Tools) != 0 {
						t.Fatalf("disabled tool exposed: %+v", request.Tools)
					}
				} else if len(request.Tools) != 1 || request.Tools[0].Name != protocol.ApplyPatch {
					t.Fatalf("unexpected tools: %+v", request.Tools)
				}
				if calls == 1 {
					arguments, _ := json.Marshal(map[string]string{"path": "note", "patch": "@@ -1 +1 @@\n-old\n+new\n"})
					return aiprot.ModelResponse{Message: aiprot.Message{Role: aiprot.RoleAssistant, Parts: []aiprot.ContentPart{{Type: aiprot.ContentToolCall, ToolCall: &aiprot.ToolCall{ID: "patch-1", Name: protocol.ApplyPatch, Arguments: arguments}}}}}
				}
				results := request.Messages[len(request.Messages)-1].ToolResults()
				if len(results) != 1 || results[0].IsError != disabled {
					t.Fatalf("results=%+v", results)
				}
				if disabled {
					if !strings.Contains(results[0].Text(), "unknown tool") {
						t.Fatalf("disabled result=%+v", results[0])
					}
				} else {
					var summary protocol.PatchResult
					if err := json.Unmarshal([]byte(results[0].Text()), &summary); err != nil || summary.HunksApplied != 1 {
						t.Fatalf("summary=%+v err=%v", summary, err)
					}
				}
				return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, "done")}
			}}
			result, err := loop.New(ai.NewClient(provider, aiprot.Hooks{})).Run(context.Background(), loopprot.Request{
				Model: "test", Messages: []aiprot.Message{aiprot.TextMessage(aiprot.RoleUser, "patch note")}, Tools: tools,
			})
			if err != nil || calls != 2 || result.StopReason != loopprot.StopNatural {
				t.Fatalf("result=%+v calls=%d err=%v", result, calls, err)
			}
			want := "new\n"
			if disabled {
				want = "old\n"
			}
			actual, err := os.ReadFile(path)
			if err != nil || string(actual) != want {
				t.Fatalf("disk=%q want=%q err=%v", actual, want, err)
			}
		})
	}
}

type scriptedProvider struct {
	generate func(aiprot.ModelRequest) aiprot.ModelResponse
}

func (provider scriptedProvider) Name() string { return "scripted" }
func (provider scriptedProvider) Generate(_ context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
	return provider.generate(request), nil
}

func TestConfiguredToolsThroughTheLoop(t *testing.T) {
	for _, requestedTool := range []string{protocol.ReadFile, protocol.WriteFile} {
		t.Run(requestedTool, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "note")
			if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			configJSON, _ := json.Marshal(map[string]any{
				"builtinTools": map[string]any{protocol.ReadFile: map[string]bool{}, protocol.WriteFile: map[string]bool{"disabled": true}},
				"filesystem":   map[string]string{"root": root},
			})
			configPath := filepath.Join(t.TempDir(), "tools.json")
			if err := os.WriteFile(configPath, configJSON, 0600); err != nil {
				t.Fatal(err)
			}
			config, err := protocol.LoadConfig(configPath)
			if err != nil {
				t.Fatal(err)
			}
			tools, err := aitools.Build(config)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			provider := scriptedProvider{generate: func(request aiprot.ModelRequest) aiprot.ModelResponse {
				calls++
				if len(request.Tools) != 1 || request.Tools[0].Name != protocol.ReadFile {
					t.Fatalf("model-visible tools=%+v", request.Tools)
				}
				if calls == 1 {
					arguments := json.RawMessage(`{"path":"note"}`)
					if requestedTool == protocol.WriteFile {
						arguments = json.RawMessage(`{"path":"note","content":"changed"}`)
					}
					return aiprot.ModelResponse{Message: aiprot.Message{Role: aiprot.RoleAssistant, Parts: []aiprot.ContentPart{{Type: aiprot.ContentToolCall, ToolCall: &aiprot.ToolCall{ID: "call-1", Name: requestedTool, Arguments: arguments}}}}}
				}
				results := request.Messages[len(request.Messages)-1].ToolResults()
				if len(results) != 1 {
					t.Fatalf("results=%+v", results)
				}
				if requestedTool == protocol.ReadFile {
					if results[0].IsError || results[0].Text() != "original" {
						t.Fatalf("read=%+v", results[0])
					}
				} else if !results[0].IsError || !strings.Contains(results[0].Text(), "unknown tool") {
					t.Fatalf("disabled=%+v", results[0])
				}
				return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, "done")}
			}}
			result, err := loop.New(ai.NewClient(provider, aiprot.Hooks{})).Run(context.Background(), loopprot.Request{
				Model: "test", Messages: []aiprot.Message{aiprot.TextMessage(aiprot.RoleUser, "use tool")}, Tools: tools,
			})
			if err != nil || calls != 2 || result.StopReason != loopprot.StopNatural {
				t.Fatalf("result=%+v calls=%d err=%v", result, calls, err)
			}
			content, err := os.ReadFile(path)
			if err != nil || string(content) != "original" {
				t.Fatalf("disk=%q err=%v", content, err)
			}
		})
	}
}
