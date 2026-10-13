// Run from the workspace root: go run ./example/verify-mcp.go
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	mcpruntime "github.com/SmilingXinyi/agent-engine/mcp-runtime"
	"github.com/SmilingXinyi/agent-engine/mcp-runtime/protocol"
)

func main() {
	configPath := flag.String("config", "example/.agents/mcp.json", "MCP JSON configuration file")
	device := flag.String("device", "", "device ID for a screen-element check")
	flag.Parse()
	if err := verify(*configPath, *device); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func verify(configPath, device string) (err error) {
	config, err := protocol.LoadConfig(configPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	runtime, err := mcpruntime.Start(ctx, config)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := runtime.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	fmt.Printf("MCP connected; discovered %d tools\n", len(runtime.Tools))
	for _, tool := range runtime.Tools {
		fmt.Println(tool.Definition.Name)
	}
	result, err := callTool(ctx, runtime.Tools, "mobile-mcp_mobile_list_available_devices", map[string]string{})
	if err != nil {
		return err
	}
	var devices struct {
		Devices []struct {
			ID string `json:"id"`
		} `json:"devices"`
	}
	if err := json.Unmarshal([]byte(result.Text()), &devices); err != nil {
		return fmt.Errorf("decode device list: %w", err)
	}
	if len(devices.Devices) == 0 {
		return fmt.Errorf("MCP is connected but no mobile device is available; check adb and USB debugging authorization")
	}
	if device != "" {
		_, err := callTool(ctx, runtime.Tools, "mobile-mcp_mobile_list_elements_on_screen", map[string]string{"device": device})
		return err
	}
	return nil
}

func callTool(ctx context.Context, tools []loopprot.Tool, name string, arguments map[string]string) (aiprot.ToolResult, error) {
	for _, tool := range tools {
		if tool.Definition.Name != name {
			continue
		}
		fmt.Printf("\n%s\nInput schema: %s\n", name, tool.Definition.InputSchema)
		input, err := json.Marshal(arguments)
		if err != nil {
			return aiprot.ToolResult{}, err
		}
		result, err := tool.Execute(ctx, input)
		if err != nil {
			return result, err
		}
		fmt.Println(result.Text())
		if result.IsError {
			return result, fmt.Errorf("MCP tool %q returned an error", name)
		}
		return result, nil
	}
	return aiprot.ToolResult{}, fmt.Errorf("MCP tool %q was not discovered", name)
}
