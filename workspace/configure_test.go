package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	"github.com/SmilingXinyi/agent-engine/workspace/protocol"
)

func TestConfigureFromProjectWithoutOpeningWorkspace(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), []byte("module example.org/configure-fixture\n"))
	t.Setenv("AGENT_MODEL", "configure-model")
	calls := 0
	options := protocol.ConfigureOptions{
		Provider: fakeProvider(func(_ context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
			calls++
			if request.Model != "configure-model" || len(request.Tools) != 0 ||
				!strings.Contains(request.Messages[1].Text(), "module example.org/configure-fixture") {
				t.Fatalf("unexpected configure request %+v", request)
			}
			return answer(`{"files":{"workspace.yaml":"model: configured\n","tools.yaml":"builtinTools: {}\n","prompt.md":"Use the Go workspace."},"missing":["Provide the MCP address."]}`), nil
		}),
	}
	for _, approve := range []bool{false, true} {
		options.Review = func(_ context.Context, changes []protocol.ConfigChange, missing []string) (bool, error) {
			if len(changes) != 4 || len(missing) != 1 {
				t.Fatalf("changes=%+v missing=%v", changes, missing)
			}
			if _, err := os.Stat(filepath.Join(root, ".agents")); !os.IsNotExist(err) {
				t.Fatalf("configure wrote before review: %v", err)
			}
			changes[0].After = "modified by review callback"
			return approve, nil
		}
		applied, err := Configure(t.Context(), root, options)
		if err != nil || applied != approve {
			t.Fatalf("applied=%t err=%v", applied, err)
		}
	}
	if calls != 2 {
		t.Fatalf("requests=%d", calls)
	}
	config, err := protocol.LoadConfig(filepath.Join(root, ".agents", workspaceConfigName))
	if err != nil || config.Model != "configured" {
		t.Fatalf("config=%+v err=%v", config, err)
	}
	session := openTest(t, root, protocol.OpenOptions{})
	if len(session.History()) != 0 {
		t.Fatal("configure created chat messages")
	}
}

func TestConfigureRejectsInvalidOrUnreviewedWrites(t *testing.T) {
	for _, test := range []struct {
		name           string
		proposal       map[string]string
		concurrentEdit bool
	}{
		{name: "unknown file", proposal: map[string]string{"../outside": "oops"}},
		{name: "invalid YAML", proposal: map[string]string{toolsConfigName: "builtinTools: {unknown: {}}"}},
		{name: "concurrent edit", proposal: map[string]string{promptName: "suggestion"}, concurrentEdit: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := newProject(t)
			prompt := filepath.Join(root, ".agents", promptName)
			writeFile(t, prompt, []byte("original"))
			proposal, err := json.Marshal(map[string]any{"files": test.proposal})
			if err != nil {
				t.Fatal(err)
			}
			reviewed := false
			applied, err := Configure(t.Context(), root, protocol.ConfigureOptions{
				Provider: fakeProvider(func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
					return answer(string(proposal)), nil
				}),
				Review: func(context.Context, []protocol.ConfigChange, []string) (bool, error) {
					reviewed = true
					if test.concurrentEdit {
						writeFile(t, prompt, []byte("concurrent"))
					}
					return true, nil
				},
			})
			if err == nil || applied || reviewed != test.concurrentEdit {
				t.Fatalf("applied=%t reviewed=%t err=%v", applied, reviewed, err)
			}
			content, err := os.ReadFile(prompt)
			want := "original"
			if test.concurrentEdit {
				want = "concurrent"
			}
			if err != nil || string(content) != want {
				t.Fatalf("prompt=%q err=%v", content, err)
			}
		})
	}
}
