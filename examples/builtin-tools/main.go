// Run from the workspace root: go run ./examples/builtin-tools/main.go
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"

	aitools "github.com/SmilingXinyi/agent-engine/ai-tools"
	"github.com/SmilingXinyi/agent-engine/ai-tools/protocol"
)

func main() {
	configPath := flag.String("config", "ai-tools/tools.example.json", "built-in tool configuration file")
	toolName := flag.String("tool", protocol.ListDirectory, "enabled tool name to execute")
	arguments := flag.String("arguments", `{"path":"."}`, "tool arguments as a JSON object")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, *configPath, *toolName, json.RawMessage(*arguments)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, configPath, toolName string, arguments json.RawMessage) error {
	config, err := protocol.LoadConfig(configPath)
	if err != nil {
		return err
	}
	tools, err := aitools.Build(config)
	if err != nil {
		return err
	}
	for _, tool := range tools {
		if tool.Definition.Name != toolName {
			continue
		}
		result, err := tool.Execute(ctx, arguments)
		if err != nil {
			return err
		}
		fmt.Println(result.Text())
		if result.IsError {
			return fmt.Errorf("tool %q reported an execution failure", toolName)
		}
		return nil
	}
	return fmt.Errorf("tool %q is disabled or not configured", toolName)
}
