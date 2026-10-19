package agentskill_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	agentskill "github.com/SmilingXinyi/agent-engine/agent-skill"
	"github.com/SmilingXinyi/agent-engine/agent-skill/protocol"
	ai "github.com/SmilingXinyi/agent-engine/ai-sdk"
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loop "github.com/SmilingXinyi/agent-engine/tool-loop"
	loopprot "github.com/SmilingXinyi/agent-engine/tool-loop/protocol"
)

func TestToolUsesCatalogSnapshotAndIncludesResourceDirectory(t *testing.T) {
	source := filepath.Join(t.TempDir(), "demo", "SKILL.md")
	skills := []protocol.Skill{{Name: "demo", Description: "description", Path: source, Body: "original instructions"}}
	tool, err := agentskill.NewTool(skills)
	if err != nil {
		t.Fatal(err)
	}
	skills[0].Body = "mutated"
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			result, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"demo"}`))
			if err != nil || !strings.Contains(result.Text(), "original instructions") || !strings.Contains(result.Text(), filepath.Dir(source)) || strings.Contains(result.Text(), "mutated") {
				t.Errorf("result=%#v error=%v", result, err)
			}
		}()
	}
	workers.Wait()
	for _, input := range []string{`{}`, `null`, `[]`, `{"name":"missing"}`, `{"name":"../demo"}`, `{"name":" demo "}`, `{"name":1}`, `{"name":"demo","path":"/tmp"}`, `{"name":"demo"} {}`} {
		if _, err := tool.Execute(context.Background(), json.RawMessage(input)); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tool.Execute(ctx, json.RawMessage(`{"name":"demo"}`)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := agentskill.NewTool(append(skills, skills[0])); err == nil {
		t.Fatal("accepted duplicate")
	}
	if _, err := agentskill.NewTool([]protocol.Skill{{Name: "demo"}}); err == nil {
		t.Fatal("accepted malformed skill")
	}
}

type skillProvider struct {
	calls int
	t     *testing.T
}

func (*skillProvider) Name() string { return "local-skill" }
func (provider *skillProvider) Generate(_ context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
	provider.calls++
	if provider.calls == 1 {
		if strings.Contains(request.Messages[0].Text(), "full instructions") {
			provider.t.Fatal("body was eagerly injected")
		}
		call := aiprot.ToolCall{ID: "skill-call", Name: protocol.ToolName, Arguments: json.RawMessage(`{"name":"demo"}`)}
		return aiprot.ModelResponse{Message: aiprot.Message{Role: aiprot.RoleAssistant, Parts: []aiprot.ContentPart{{Type: aiprot.ContentToolCall, ToolCall: &call}}}}, nil
	}
	results := request.Messages[len(request.Messages)-1].ToolResults()
	if len(results) != 1 || results[0].Text() != "full instructions" {
		provider.t.Fatalf("results=%#v", results)
	}
	return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, "done")}, nil
}

func TestSkillToolCompletesLocalLoop(t *testing.T) {
	skills := []protocol.Skill{{Name: "demo", Description: "demo description", Body: "full instructions"}}
	tool, err := agentskill.NewTool(skills)
	if err != nil {
		t.Fatal(err)
	}
	provider := &skillProvider{t: t}
	result, err := loop.New(ai.NewClient(provider, aiprot.Hooks{})).Run(context.Background(), loopprot.Request{
		Model: "local", Messages: []aiprot.Message{aiprot.TextMessage(aiprot.RoleSystem, agentskill.BuildListing(skills)), aiprot.TextMessage(aiprot.RoleUser, "use demo")}, Tools: []loopprot.Tool{tool},
	})
	if err != nil || provider.calls != 2 || len(result.ToolCalls) != 1 || result.LastResponse.Message.Text() != "done" || result.StopReason != loopprot.StopNatural {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}
