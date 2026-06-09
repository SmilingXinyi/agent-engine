//go:build ignore

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/SmilingXinyi/agent-engine/pkg/agent"
	"github.com/SmilingXinyi/agent-engine/pkg/model/goai"
)

func main() {
	_ = godotenv.Load(".env", "../../../.env")
	modelConfig, err := loadModelConfigFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ctx := context.Background()
	engine := agent.NewEngineWithClientFactory(goai.NewClientFactory())
	scanner := bufio.NewScanner(os.Stdin)
	history := []agent.Message{}
	tools := []agent.Tool{newDatetimeTool()}
	systemPrompt := strings.Join([]string{
		"你是一名温和、专业、有边界感的 AI 心理医生。",
		"你需要用共情的方式回应来访者，帮助对方澄清感受、识别压力来源，并提出安全、可执行的小建议。",
		"你不能声称自己能替代真实心理医生，也不能做医学诊断。",
		"如果用户表达自伤、伤害他人或紧急危险信号，你必须建议用户立刻联系当地紧急服务、可信赖的人或专业心理危机热线。",
	}, "\n")

	fmt.Println("AI 心理医生多轮对话脚本")
	fmt.Println("输入内容开始对话；输入 exit 或 quit 退出。")
	for {
		fmt.Print("\n你：")
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

		ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
		var assistant strings.Builder
		fmt.Print("AI：")
		response, err := engine.Stream(ctx, agent.StreamRequest{
			Request: agent.Request{
				ModelConfig:  modelConfig,
				SystemPrompt: systemPrompt,
				UserPrompt:   userPrompt,
				History:      history,
				Tools:        tools,
				Options: agent.RunOptions{
					MaxSteps: 5,
					EventHandler: agent.EventHandlerFunc{
						ToolCallStart: func(ctx context.Context, event agent.ToolCallStartEvent) error {
							_ = ctx
							fmt.Printf("\n[tool %s start]\n", event.ToolName)
							return nil
						},
						ToolCallFinish: func(ctx context.Context, event agent.ToolCallFinishEvent) error {
							_ = ctx
							fmt.Printf("\n[tool %s end]\n", event.ToolName)
							return nil
						},
					},
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
			fmt.Fprintf(os.Stderr, "请求失败：%v\n", err)
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
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
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
		return agent.ModelConfig{}, fmt.Errorf("set AGENT_LLM_MODEL and AGENT_LLM_API_KEY before running this script")
	}
	return agent.ModelConfig{Provider: provider, APIKey: apiKey, BaseURL: baseURL, Name: model}, nil
}
