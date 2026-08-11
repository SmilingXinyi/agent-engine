package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/SmilingXinyi/agent-engine/pkg/agent"
	"github.com/SmilingXinyi/agent-engine/pkg/model/goai"
	"github.com/SmilingXinyi/agent-engine/pkg/tools/mcp"
	"github.com/SmilingXinyi/agent-engine/pkg/tools/prompt"
	"github.com/SmilingXinyi/agent-engine/pkg/tools/skill"
)

func main() {
	mode := flag.String("mode", "hello", "demo mode: hello | stream | tools | skills | chat | mcp")
	promptText := flag.String("prompt", "", "user prompt override for non-chat modes")
	skillsDir := flag.String("skills", "", "skills root directory (default: ./skills next to this demo)")
	mcpConfig := flag.String("mcp-config", "", "optional MCP servers JSON config path")
	flag.Parse()

	_ = godotenv.Load(".env", "../../.env")

	modelConfig, err := loadModelConfigFromEnv()
	if err != nil {
		fatal(err)
	}

	engine := agent.NewEngineWithClientFactory(goai.NewClientFactory())
	ctx := context.Background()

	switch strings.ToLower(strings.TrimSpace(*mode)) {
	case "hello":
		userPrompt := defaultPrompt(*promptText, "Say hello in one short sentence.")
		runHello(ctx, engine, modelConfig, userPrompt)
	case "stream":
		userPrompt := defaultPrompt(*promptText, "Write a 3-line welcome message for agent-engine.")
		runStream(ctx, engine, modelConfig, userPrompt)
	case "tools":
		userPrompt := defaultPrompt(*promptText, "What is the current datetime? Use the get_datetime tool.")
		runTools(ctx, engine, modelConfig, userPrompt)
	case "skills":
		userPrompt := defaultPrompt(*promptText, "Please summarize this article: agent-engine is a portable Go agent runtime.")
		runSkills(ctx, engine, modelConfig, resolveSkillsDir(*skillsDir), userPrompt)
	case "chat":
		runChat(ctx, engine, modelConfig)
	case "mcp":
		userPrompt := defaultPrompt(*promptText, "List the tools you have available and briefly describe them.")
		runMCP(ctx, engine, modelConfig, resolveMCPConfig(*mcpConfig), userPrompt)
	default:
		fatal(fmt.Errorf("unknown mode %q (expected hello|stream|tools|skills|chat|mcp)", *mode))
	}
}

func runHello(ctx context.Context, engine agent.Engine, modelConfig agent.ModelConfig, userPrompt string) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	response, err := engine.Execute(ctx, agent.Request{
		ModelConfig:  modelConfig,
		SystemPrompt: "You are a concise demo assistant for agent-engine.",
		UserPrompt:   userPrompt,
	})
	if err != nil {
		fatal(err)
	}
	fmt.Println(response.Text)
	printUsage(response.Usage)
}

func runStream(ctx context.Context, engine agent.Engine, modelConfig agent.ModelConfig, userPrompt string) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	fmt.Print("assistant: ")
	response, err := engine.Stream(ctx, agent.StreamRequest{
		Request: agent.Request{
			ModelConfig:  modelConfig,
			SystemPrompt: "You are a concise demo assistant for agent-engine.",
			UserPrompt:   userPrompt,
		},
		StreamHandler: agent.StreamHandlerFunc(func(ctx context.Context, chunk string) error {
			_ = ctx
			fmt.Print(chunk)
			return nil
		}),
	})
	fmt.Println()
	if err != nil {
		fatal(err)
	}
	printUsage(response.Usage)
}

func runTools(ctx context.Context, engine agent.Engine, modelConfig agent.ModelConfig, userPrompt string) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	response, err := engine.Execute(ctx, agent.Request{
		ModelConfig:  modelConfig,
		SystemPrompt: "You are a demo assistant. Prefer calling tools when they are useful.",
		UserPrompt:   userPrompt,
		Tools:        []agent.Tool{newDatetimeTool()},
		Options: agent.RunOptions{
			MaxSteps:     5,
			EventHandler: agent.StdoutToolEventHandler(),
		},
	})
	if err != nil {
		fatal(err)
	}
	fmt.Println(response.Text)
	printUsage(response.Usage)
}

func runSkills(ctx context.Context, engine agent.Engine, modelConfig agent.ModelConfig, skillsRoot, userPrompt string) {
	skills, err := skill.LoadSkills(skillsRoot)
	if err != nil {
		fatal(err)
	}
	if len(skills) == 0 {
		fatal(fmt.Errorf("no skills found under %s", skillsRoot))
	}

	systemPrompt := prompt.BuildSystemPrompt(
		"You are a demo assistant that follows matched skills carefully.",
		skills,
		userPrompt,
	)

	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	response, err := engine.Execute(ctx, agent.Request{
		ModelConfig:  modelConfig,
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
	})
	if err != nil {
		fatal(err)
	}
	fmt.Println(response.Text)
	printUsage(response.Usage)
}

func runChat(ctx context.Context, engine agent.Engine, modelConfig agent.ModelConfig) {
	scanner := bufio.NewScanner(os.Stdin)
	history := []agent.Message{}
	tools := []agent.Tool{newDatetimeTool()}
	systemPrompt := "You are a helpful demo assistant for agent-engine. Keep replies short."

	fmt.Println("agent-engine demo chat")
	fmt.Println("Type a message to chat. Type exit or quit to leave.")
	for {
		fmt.Print("\nyou: ")
		if !scanner.Scan() {
			break
		}
		userPrompt := strings.TrimSpace(scanner.Text())
		if userPrompt == "" {
			continue
		}
		if userPrompt == "exit" || userPrompt == "quit" {
			break
		}

		turnCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		var assistant strings.Builder
		fmt.Print("assistant: ")
		response, err := engine.Stream(turnCtx, agent.StreamRequest{
			Request: agent.Request{
				ModelConfig:  modelConfig,
				SystemPrompt: systemPrompt,
				UserPrompt:   userPrompt,
				History:      history,
				Tools:        tools,
				Options: agent.RunOptions{
					MaxSteps:     5,
					EventHandler: agent.StdoutToolEventHandler(),
				},
			},
			StreamHandler: agent.StreamHandlerFunc(func(ctx context.Context, chunk string) error {
				_ = ctx
				fmt.Print(chunk)
				assistant.WriteString(chunk)
				return nil
			}),
		})
		cancel()
		fmt.Println()
		if err != nil {
			fmt.Fprintf(os.Stderr, "request failed: %v\n", err)
			if response.Text == "" && assistant.Len() == 0 {
				continue
			}
		}

		assistantText := response.Text
		if assistantText == "" {
			assistantText = assistant.String()
		}
		history = append(history,
			agent.TextMessage{Role: agent.RoleUser, Content: userPrompt},
			agent.TextMessage{Role: agent.RoleAssistant, Content: assistantText},
		)
	}

	if err := scanner.Err(); err != nil {
		fatal(err)
	}
}

func runMCP(ctx context.Context, engine agent.Engine, modelConfig agent.ModelConfig, configPath, userPrompt string) {
	config, err := mcp.LoadMCPConfigFile(configPath)
	if err != nil {
		fatal(err)
	}
	if len(config.Servers) == 0 {
		fatal(fmt.Errorf("no MCP servers configured in %s (copy mcp.example.json and set -mcp-config)", configPath))
	}

	runtime, err := mcp.StartMCPRuntime(ctx, config)
	if err != nil {
		fatal(err)
	}
	defer func() {
		_ = runtime.Close()
	}()

	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	response, err := engine.Execute(ctx, agent.Request{
		ModelConfig:  modelConfig,
		SystemPrompt: "You are a demo assistant with MCP tools. Prefer tools when helpful.",
		UserPrompt:   userPrompt,
		Tools:        runtime.Tools,
		Options: agent.RunOptions{
			MaxSteps:     8,
			EventHandler: agent.StdoutToolEventHandler(),
		},
	})
	if err != nil {
		fatal(err)
	}
	fmt.Println(response.Text)
	printUsage(response.Usage)
}

func newDatetimeTool() agent.Tool {
	return agent.Tool{
		Name:        "get_datetime",
		Description: "Returns the current datetime as a string in YYYYMMDDHHMMSS format.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: agent.ToolHandlerFunc(func(ctx context.Context, input json.RawMessage) (string, error) {
			_ = ctx
			_ = input
			return time.Now().Format("20060102150405"), nil
		}),
	}
}

func loadModelConfigFromEnv() (agent.ModelConfig, error) {
	provider := os.Getenv("AGENT_LLM_PROVIDER")
	model := os.Getenv("AGENT_LLM_MODEL")
	apiKey := os.Getenv("AGENT_LLM_API_KEY")
	baseURL := os.Getenv("AGENT_LLM_BASE_URL")
	if model == "" || apiKey == "" {
		return agent.ModelConfig{}, fmt.Errorf("set AGENT_LLM_MODEL and AGENT_LLM_API_KEY before running the demo")
	}
	return agent.ModelConfig{
		Provider: provider,
		APIKey:   apiKey,
		BaseURL:  baseURL,
		Name:     model,
	}, nil
}

func resolveSkillsDir(override string) string {
	if strings.TrimSpace(override) != "" {
		return override
	}
	return filepath.Join(demoRoot(), "skills")
}

func resolveMCPConfig(override string) string {
	if strings.TrimSpace(override) != "" {
		return override
	}
	return filepath.Join(demoRoot(), "mcp.example.json")
}

func demoRoot() string {
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Dir(exe))
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			cwd,
			filepath.Join(cwd, "examples", "demo"),
		)
	}
	for _, start := range candidates {
		if root := findDemoRoot(start); root != "" {
			return root
		}
	}
	return filepath.Join("examples", "demo")
}

func findDemoRoot(start string) string {
	dir := start
	for i := 0; i < 6; i++ {
		skillsPath := filepath.Join(dir, "skills")
		mcpPath := filepath.Join(dir, "mcp.example.json")
		if fileExists(skillsPath) || fileExists(mcpPath) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func defaultPrompt(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func printUsage(usage *agent.Usage) {
	if usage == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "usage: input=%d output=%d total=%d\n", usage.InputTokens, usage.OutputTokens, usage.TotalTokens)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
