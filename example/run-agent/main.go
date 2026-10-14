// Run from the workspace root: go run ./example/run-agent/main.go
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/SmilingXinyi/agent-engine/agent"
	agentskill "github.com/SmilingXinyi/agent-engine/agent-skill"
	agentprot "github.com/SmilingXinyi/agent-engine/agent/protocol"
	ai "github.com/SmilingXinyi/agent-engine/ai-sdk"
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-sdk/providers/openai"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	mcpruntime "github.com/SmilingXinyi/agent-engine/mcp-runtime"
	mcpprot "github.com/SmilingXinyi/agent-engine/mcp-runtime/protocol"
)

func main() {
	promptPath := flag.String("prompt", "example/prompt.md", "user prompt file")
	configPath := flag.String("config", "example/.agents/mcp.json", "MCP configuration file")
	envPath := flag.String("env", ".env", "model environment file")
	skillsPath := flag.String("skills", "", "optional directory containing SKILL.md files")
	maxSteps := flag.Int("max-steps", 40, "maximum model calls")
	timeout := flag.Duration("timeout", 15*time.Minute, "execution timeout")
	flag.Parse()
	if err := run(*promptPath, *configPath, *envPath, *skillsPath, *maxSteps, *timeout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(promptPath, configPath, envPath, skillsPath string, maxSteps int, timeout time.Duration) (err error) {
	if err := loadEnv(envPath); err != nil {
		return err
	}
	model := firstEnv("AGENT_MODEL", "AGENT_LLM_MODEL")
	token := firstEnv("AGENT_LLM_TOKEN", "AGENT_LLM_API_KEY")
	baseURL := os.Getenv("AGENT_LLM_BASE_URL")
	if model == "" || token == "" || baseURL == "" {
		return errors.New("configure AGENT_LLM_MODEL, AGENT_LLM_API_KEY and AGENT_LLM_BASE_URL in .env or process environment")
	}
	prompt, err := os.ReadFile(promptPath)
	if err != nil {
		return err
	}
	systemPrompt := "你是通过 MCP 工具操作手机的执行助手。根据实际工具结果完成用户任务，确认结果后再报告。"
	var skillTools []loopprot.Tool
	if skillsPath != "" {
		skills, err := agentskill.Load(skillsPath)
		if err != nil {
			return err
		}
		if len(skills) > 0 {
			tool, err := agentskill.NewTool(skills)
			if err != nil {
				return err
			}
			skillTools = append(skillTools, tool)
			systemPrompt += "\n\n" + agentskill.BuildListing(skills)
		}
	}
	config, err := mcpprot.LoadConfig(configPath)
	if err != nil {
		return err
	}
	signalContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	ctx, cancel := context.WithTimeout(signalContext, timeout)
	defer cancel()
	runtime, err := mcpruntime.Start(ctx, config)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, runtime.Close()) }()
	fmt.Printf("Model: %s; MCP tools: %d; max steps: %d\n", model, len(runtime.Tools), maxSteps)
	provider, err := openai.New(openai.Config{BaseURL: baseURL, APIKey: token, Name: "configured-provider"})
	if err != nil {
		return err
	}
	step := 0
	client := ai.NewClient(provider, aiprot.Hooks{
		OnRequest: []aiprot.RequestHook{func(_ context.Context, info aiprot.RequestInfo) error {
			step++
			fmt.Printf("[model %d] request=%s messages=%d tools=%d\n", step, info.RequestID, len(info.Request.Messages), len(info.Request.Tools))
			return nil
		}},
		OnResponse: []aiprot.ResponseHook{func(_ context.Context, info aiprot.ResponseInfo) error {
			fmt.Printf("[model %d result] duration=%s finish=%s toolCalls=%d\n", step, info.Duration.Round(time.Millisecond), info.Response.FinishReason, len(info.Response.Message.ToolCalls()))
			if text := info.Response.Message.Text(); text != "" {
				fmt.Println(text)
			}
			return nil
		}},
	})
	response, err := agent.New(client).Execute(ctx, agentprot.Request{
		Model:        model,
		SystemPrompt: systemPrompt,
		UserPrompt:   string(prompt), Tools: append(runtime.Tools, skillTools...),
		Options: loopprot.RunOptions{MaxSteps: maxSteps, Hooks: executionHooks()},
	})
	fmt.Printf("\nFinal response:\n%s\nStop reason: %s; model calls: %d; total tokens: %d\n", response.LastResponse.Message.Text(), response.StopReason, step, response.Usage.TotalTokens)
	if err == nil && response.StopReason == loopprot.StopMaxSteps {
		return errors.New("model call limit reached; execution remains incomplete")
	}
	return err
}

// Execution hooks observe loop snapshots; the permission policy belongs only to
// this mobile example, not to the generic loop.
func executionHooks() loopprot.Hooks {
	return loopprot.Hooks{
		OnRunStart: func(_ context.Context, info loopprot.RunStartInfo) error {
			fmt.Printf("[run start] id=%s\n", info.RunID)
			return nil
		},
		OnRunEnd: func(_ context.Context, info loopprot.RunEndInfo) error {
			fmt.Printf("[run end] id=%s stop=%s duration=%s error=%v\n", info.RunID, info.Result.StopReason, info.Duration.Round(time.Millisecond), info.Error)
			return nil
		},
		OnStepStart: func(_ context.Context, info loopprot.StepStartInfo) error {
			fmt.Printf("[step start] step=%d request=%s\n", info.Step, info.RequestID)
			return nil
		},
		OnStepEnd: func(_ context.Context, info loopprot.StepEndInfo) error {
			usage := info.Result.Response.Usage
			fmt.Printf("[step end] step=%d input=%d output=%d total=%d tools=%d duration=%s error=%v\n", info.Step, usage.InputTokens, usage.OutputTokens, usage.TotalTokens, len(info.ToolCalls), info.Duration.Round(time.Millisecond), info.Error)
			return nil
		},
		OnToolStart: func(_ context.Context, info loopprot.ToolStartInfo) error {
			fmt.Printf("[tool] step=%d request=%s %s %s\n", info.Step, info.RequestID, info.Call.Name, info.Call.Arguments)
			return nil
		},
		OnToolEnd: func(_ context.Context, info loopprot.ToolEndInfo) error {
			fmt.Printf("[tool result] %s isError=%v chars=%d duration=%s\n", info.Call.Name, info.Result.IsError, len([]rune(info.Result.Text())), info.Duration.Round(time.Millisecond))
			details := info.Result.Text()
			if info.Error != nil {
				fmt.Printf("[tool error] %v\n", info.Error)
				details += info.Error.Error()
			} else if info.Result.IsError {
				fmt.Printf("[tool error] %s\n", details)
			}
			if strings.Contains(details, "INJECT_EVENTS") && (info.Error != nil || info.Result.IsError) {
				return errors.New("mobile input permission denied: enable USB debugging (Security settings) before retrying")
			}
			return nil
		},
	}
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}

// loadEnv reads simple KEY=value assignments, optional export prefixes and
// quoted values. Existing process variables take precedence; no shell is run.
func loadEnv(path string) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if !ok {
			return fmt.Errorf("invalid assignment in environment file %q", path)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if strings.HasPrefix(value, "\"") {
			value, err = strconv.Unquote(value)
			if err != nil {
				return fmt.Errorf("invalid quoted value for environment variable %s", key)
			}
		} else if len(value) >= 2 && strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") {
			value = value[1 : len(value)-1]
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	return scanner.Err()
}
