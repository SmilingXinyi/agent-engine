package test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/joho/godotenv"

	"github.com/SmilingXinyi/agent-engine/pkg/agent"
	"github.com/SmilingXinyi/agent-engine/pkg/model/goai"
)

// TestAgentEngineExecuteIntegration verifies the real LLM adapter when explicitly enabled by environment variables.
func TestAgentEngineExecuteIntegration(t *testing.T) {
	_ = godotenv.Load("../.env")

	if os.Getenv("AGENT_ENGINE_INTEGRATION_TEST") != "1" {
		t.Skip("set AGENT_ENGINE_INTEGRATION_TEST=1 to run agent-engine integration tests")
	}

	modelConfig := loadModelConfigFromEnv(t)
	t.Logf("agent engine execute integration: provider=%s model=%s base_url=%s", modelConfig.Provider, modelConfig.Name, modelConfig.BaseURL)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	engine := agent.NewEngineWithClientFactory(goai.NewClientFactory())
	start := time.Now()
	response, err := engine.Execute(ctx, agent.Request{
		ModelConfig:  modelConfig,
		SystemPrompt: "你是一个严格按要求输出的测试助手。只输出用户要求的内容，不要添加解释。",
		UserPrompt:   "请只输出：pong",
	})
	if err != nil {
		t.Fatalf("execute request failed after %s: %v", time.Since(start), err)
	}
	t.Logf("agent engine execute integration completed: duration=%s text=%q usage=%+v tool_calls=%d messages=%d", time.Since(start), response.Text, response.Usage, len(response.ToolCalls), len(response.Messages))
	if !strings.Contains(strings.ToLower(strings.TrimSpace(response.Text)), "pong") {
		t.Fatalf("expected response to contain pong, got %q", response.Text)
	}
}

// TestAgentEngineStreamIntegration verifies real streaming output when explicitly enabled by environment variables.
func TestAgentEngineStreamIntegration(t *testing.T) {
	_ = godotenv.Load("../.env")

	if os.Getenv("AGENT_ENGINE_INTEGRATION_TEST") != "1" {
		t.Skip("set AGENT_ENGINE_INTEGRATION_TEST=1 to run agent-engine integration tests")
	}

	modelConfig := loadModelConfigFromEnv(t)
	t.Logf("agent engine stream integration: provider=%s model=%s base_url=%s", modelConfig.Provider, modelConfig.Name, modelConfig.BaseURL)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	engine := agent.NewEngineWithClientFactory(goai.NewClientFactory())
	var chunks strings.Builder
	chunkCount := 0
	start := time.Now()
	response, err := engine.Stream(ctx, agent.StreamRequest{
		Request: agent.Request{
			ModelConfig:  modelConfig,
			SystemPrompt: "你是一个严格按要求输出的测试助手。只输出用户要求的内容，不要添加解释。",
			UserPrompt:   "请只输出：pong",
		},
		StreamHandler: agent.StreamHandlerFunc(func(ctx context.Context, chunk string) error {
			_ = ctx
			chunkCount++
			chunks.WriteString(chunk)
			return nil
		}),
	})
	if err != nil {
		t.Fatalf("stream request failed after %s: %v", time.Since(start), err)
	}
	t.Logf("agent engine stream integration completed: duration=%s chunk_count=%d chunk_text=%q response_text=%q usage=%+v tool_calls=%d messages=%d", time.Since(start), chunkCount, chunks.String(), response.Text, response.Usage, len(response.ToolCalls), len(response.Messages))
	if chunks.String() == "" {
		t.Fatal("expected non-empty stream chunks")
	}
	if !strings.Contains(strings.ToLower(strings.TrimSpace(response.Text)), "pong") {
		t.Fatalf("expected response to contain pong, got %q", response.Text)
	}
}

func loadModelConfigFromEnv(t *testing.T) agent.ModelConfig {
	t.Helper()
	provider := os.Getenv("AGENT_LLM_PROVIDER")
	model := os.Getenv("AGENT_LLM_MODEL")
	apiKey := os.Getenv("AGENT_LLM_API_KEY")
	baseURL := os.Getenv("AGENT_LLM_BASE_URL")
	if model == "" || apiKey == "" {
		t.Fatal("set AGENT_LLM_MODEL and AGENT_LLM_API_KEY to run integration tests")
	}
	return agent.ModelConfig{
		Provider: provider,
		APIKey:   apiKey,
		BaseURL:  baseURL,
		Name:     model,
	}
}
