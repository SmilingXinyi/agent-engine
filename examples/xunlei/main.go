// Run from the workspace root: go run ./examples/xunlei/{main,desktop,logging,confirmation}.go
package main

import (
	"bufio"
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"mime"
	"net/url"
	"os"
	"os/signal"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/SmilingXinyi/agent-engine/agent"
	agentprot "github.com/SmilingXinyi/agent-engine/agent/protocol"
	ai "github.com/SmilingXinyi/agent-engine/ai-sdk"
	"github.com/SmilingXinyi/agent-engine/ai-sdk/providers/openai"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	mcpruntime "github.com/SmilingXinyi/agent-engine/mcp-runtime"
	mcpprot "github.com/SmilingXinyi/agent-engine/mcp-runtime/protocol"
)

//go:embed prompt.md
var systemPrompt string

type options struct {
	configPath      string
	envPath         string
	urlPath         string
	logPath         string
	maxSteps        int
	historyMessages int
	display         int
	timeout         time.Duration
}

func main() {
	var settings options
	flag.StringVar(&settings.configPath, "config", "examples/.agents/mcp.json", "MCP configuration file")
	flag.StringVar(&settings.envPath, "env", ".env", "model environment file")
	flag.StringVar(&settings.urlPath, "url-file", "examples/xunlei/download-url.txt", "file containing the exact download URL")
	flag.StringVar(&settings.logPath, "log-file", "examples/xunlei/run.log", "append complete execution logs to this file; empty disables the file")
	flag.IntVar(&settings.maxSteps, "max-steps", 40, "maximum model calls")
	flag.IntVar(&settings.historyMessages, "history-messages", 10, "maximum messages sent to the vision model, including system, task and screenshots")
	flag.IntVar(&settings.display, "display", 0, "remote display index to observe")
	flag.DurationVar(&settings.timeout, "timeout", 10*time.Minute, "total execution timeout")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, settings); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, settings options) (err error) {
	output, closeLog, err := openRunLog(settings.logPath)
	if err != nil {
		return err
	}
	started := time.Now()
	fmt.Fprintf(output, "\n[execution start] time=%s log_file=%q\n", started.Format(time.RFC3339), settings.logPath)
	defer func() {
		fmt.Fprintf(output, "[execution end] duration=%s error=%v\n", time.Since(started).Round(time.Millisecond), err)
		err = errors.Join(err, closeLog())
	}()
	if settings.maxSteps <= 0 || settings.timeout <= 0 || settings.display < 0 {
		return errors.New("max-steps and timeout must be positive; display must not be negative")
	}
	if settings.historyMessages < 5 {
		return errors.New("history-messages must be at least 5 to retain system, task and a complete screenshot tool exchange")
	}
	downloadURL, filenames, err := loadDownloadURL(settings.urlPath)
	if err != nil {
		return err
	}
	if err := loadEnv(settings.envPath); err != nil {
		return err
	}
	// Resolve application configuration once, before connecting MCP servers.
	runOptions, err := executionOptions(os.Getenv("AGENT_TOOL_CONFIRMATION"), os.Stdin, output, settings.maxSteps)
	if err != nil {
		return err
	}
	model := firstEnv("AGENT_LLM_MODEL", "AGENT_MODEL")
	baseURL := os.Getenv("AGENT_LLM_BASE_URL")
	if model == "" || baseURL == "" {
		return errors.New("请设置 AGENT_LLM_MODEL 和 AGENT_LLM_BASE_URL；AGENT_LLM_API_KEY 可选")
	}
	provider, err := openai.New(openai.Config{BaseURL: baseURL, APIKey: firstEnv("AGENT_LLM_API_KEY", "AGENT_LLM_TOKEN")})
	if err != nil {
		return err
	}
	config, err := mcpprot.LoadConfig(settings.configPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, settings.timeout)
	defer cancel()
	var enabledServers []string
	for _, name := range config.ServerNames() {
		if !config.Servers[name].Disabled {
			enabledServers = append(enabledServers, name)
		}
	}
	fmt.Fprintf(output, "[mcp start] servers=%s\n", strings.Join(enabledServers, ","))
	runtime, err := mcpruntime.Start(ctx, config)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, runtime.Close()) }()
	for _, tool := range runtime.Tools {
		fmt.Fprintf(output, "[tool registered] %s\n", tool.Definition.Name)
	}
	fmt.Fprintf(output, "迅雷视觉 Agent：model=%s tools=%d display=%d max_steps=%d history_messages=%d\n", model, len(runtime.Tools), settings.display, settings.maxSteps, settings.historyMessages)
	client := ai.NewClient(visionProvider{Provider: provider, maxMessages: settings.historyMessages, output: output}, modelHooks(output))
	result, err := agent.New(client).Execute(ctx, agentprot.Request{
		Model: model, SystemPrompt: systemPrompt,
		UserPrompt: fmt.Sprintf("目标显示器索引：%d\n预期文件名：%s\n下载 URL：\n%s", settings.display, strings.Join(filenames, "、"), downloadURL),
		Tools:      runtime.Tools,
		Options:    runOptions,
	})
	if err != nil {
		return err
	}
	if result.StopReason != loopprot.StopNatural {
		return fmt.Errorf("任务尚未完成：stop=%s steps=%d", result.StopReason, len(result.Steps))
	}
	fmt.Fprintf(output, "Agent 执行结束：stop=%s steps=%d loop_tokens=%d\n", result.StopReason, len(result.Steps), result.Usage.TotalTokens)
	return nil
}

func loadDownloadURL(filename string) (string, []string, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return "", nil, err
	}
	raw := strings.TrimSpace(string(data))
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || strings.ContainsAny(raw, "\r\n\t ") {
		return "", nil, errors.New("url-file must contain exactly one valid HTTP(S) URL")
	}
	var names []string
	for _, key := range []string{"b2ContentDisposition", "response-content-disposition"} {
		if _, params, err := mime.ParseMediaType(parsed.Query().Get(key)); err == nil && params["filename"] != "" {
			names = append(names, params["filename"])
			break
		}
	}
	if basename := path.Base(parsed.Path); basename != "." && basename != "/" && basename != "" && (len(names) == 0 || names[0] != basename) {
		names = append(names, basename)
	}
	if len(names) == 0 {
		return "", nil, errors.New("cannot determine the expected download filename from the URL")
	}
	// Preserve the original bytes. Re-encoding signed query parameters may
	// change their meaning to the download server.
	return raw, names, nil
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}

// Read assignments without running a shell. Existing process variables win.
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
				return fmt.Errorf("invalid quoted value for %s", key)
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
