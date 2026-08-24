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
	mode := flag.String("mode", "hello", "demo mode: hello | stream | tools | skills | skilltool | thinking | toolenv | chat | mcp")
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
	case "skilltool":
		userPrompt := defaultPrompt(*promptText, "Summarize this text using the summarize skill: agent-engine is a portable Go agent runtime.")
		runSkillTool(ctx, engine, modelConfig, resolveSkillsDir(*skillsDir), userPrompt)
	case "thinking":
		userPrompt := defaultPrompt(*promptText, "Explain in one sentence why the sky appears blue. Think step by step before answering.")
		runThinking(ctx, engine, modelConfig, userPrompt)
	case "toolenv":
		userPrompt := defaultPrompt(*promptText, "Call the echo tool with message hello.")
		runToolEnv(ctx, engine, modelConfig, userPrompt)
	case "chat":
		runChat(ctx, engine, modelConfig)
	case "mcp":
		userPrompt := defaultPrompt(*promptText, "List the tools you have available and briefly describe them.")
		runMCP(ctx, engine, modelConfig, resolveMCPConfig(*mcpConfig), userPrompt)
	default:
		fatal(fmt.Errorf("unknown mode %q (expected hello|stream|tools|skills|skilltool|thinking|toolenv|chat|mcp)", *mode))
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

func runThinking(ctx context.Context, engine agent.Engine, modelConfig agent.ModelConfig, userPrompt string) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	fmt.Print("assistant: ")
	var thinking strings.Builder
	response, err := engine.Stream(ctx, agent.StreamRequest{
		Request: agent.Request{
			ModelConfig:  modelConfig,
			SystemPrompt: "You are a demo assistant. Show your reasoning when the model supports it.",
			UserPrompt:   userPrompt,
		},
		StreamHandler: thinkingStreamHandler{
			onText: func(_ context.Context, chunk string) error {
				fmt.Print(chunk)
				return nil
			},
			onThinking: func(_ context.Context, chunk string) error {
				thinking.WriteString(chunk)
				return nil
			},
		},
	})
	fmt.Println()
	if err != nil {
		fatal(err)
	}
	if thinking.Len() > 0 {
		fmt.Fprintf(os.Stderr, "\nthinking (%d chars):\n%s\n", thinking.Len(), thinking.String())
	} else if response.Thinking != "" {
		fmt.Fprintf(os.Stderr, "\nthinking:\n%s\n", response.Thinking)
	}
	printUsage(response.Usage)
}

type thinkingStreamHandler struct {
	onText     func(context.Context, string) error
	onThinking func(context.Context, string) error
}

func (handler thinkingStreamHandler) OnText(ctx context.Context, chunk string) error {
	if handler.onText != nil {
		return handler.onText(ctx, chunk)
	}
	return nil
}

func (handler thinkingStreamHandler) OnThinking(ctx context.Context, chunk string) error {
	if handler.onThinking != nil {
		return handler.onThinking(ctx, chunk)
	}
	return nil
}

func runToolEnv(ctx context.Context, engine agent.Engine, modelConfig agent.ModelConfig, userPrompt string) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	ctx = agent.WithToolEnv(ctx, map[string]map[string]any{
		"echo": {"tenant_id": "demo-tenant", "region": "us-west"},
	})

	response, err := engine.Execute(ctx, agent.Request{
		ModelConfig:  modelConfig,
		SystemPrompt: "You are a demo assistant. Use the echo tool when asked.",
		UserPrompt:   userPrompt,
		Tools:        []agent.Tool{newEchoTool()},
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

func runSkillTool(ctx context.Context, engine agent.Engine, modelConfig agent.ModelConfig, skillsRoot, userPrompt string) {
	skills, err := skill.LoadSkills(skillsRoot)
	if err != nil {
		fatal(err)
	}
	if len(skills) == 0 {
		fatal(fmt.Errorf("no skills found under %s", skillsRoot))
	}

	systemPrompt := "You are a demo assistant.\n\n" + skill.BuildListing(skills)

	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	response, err := engine.Execute(ctx, agent.Request{
		ModelConfig:  modelConfig,
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
		Tools:        []agent.Tool{skill.NewTool(skillsRoot)},
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

func newEchoTool() agent.Tool {
	return agent.Tool{
		Name:        "echo",
		Description: "Echoes the message parameter back as JSON including any injected context fields.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"message": {"type": "string", "description": "Message to echo"}
			},
			"required": ["message"]
		}`),
		Handler: agent.ToolHandlerFunc(func(_ context.Context, input json.RawMessage) (string, error) {
			return string(input), nil
		}),
	}
}

func loadModelConfigFromEnv() (agent.ModelConfig, error) {
	provider := os.Getenv("AGENT_LLM_PROVIDER")
	model := os.Getenv("AGENT_LLM_MODEL")
	apiKey := os.Getenv("AGENT_LLM_API_KEY")
	baseURL := os.Getenv("AGENT_LLM_BASE_URL")
	disableThinking := os.Getenv("AGENT_LLM_DISABLE_THINKING") == "1" || strings.EqualFold(os.Getenv("AGENT_LLM_DISABLE_THINKING"), "true")
	if model == "" || apiKey == "" {
		return agent.ModelConfig{}, fmt.Errorf("set AGENT_LLM_MODEL and AGENT_LLM_API_KEY before running the demo")
	}
	return agent.ModelConfig{
		Provider:        provider,
		APIKey:          apiKey,
		BaseURL:         baseURL,
		Name:            model,
		DisableThinking: disableThinking,
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
	fmt.Fprintf(os.Stderr, "usage: input=%d output=%d total=%d", usage.InputTokens, usage.OutputTokens, usage.TotalTokens)
	if usage.ReasoningTokens > 0 {
		fmt.Fprintf(os.Stderr, " reasoning=%d", usage.ReasoningTokens)
	}
	fmt.Fprintln(os.Stderr)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
