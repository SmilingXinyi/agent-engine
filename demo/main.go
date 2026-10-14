package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("LLM_BASE_URL")), "/")
	apiKey := strings.TrimSpace(os.Getenv("LLM_API_KEY"))
	model := strings.TrimSpace(os.Getenv("LLM_MODEL"))
	if baseURL == "" || apiKey == "" || model == "" {
		return fmt.Errorf("set LLM_BASE_URL, LLM_API_KEY and LLM_MODEL")
	}

	payload, err := json.Marshal(map[string]any{
		"model":  model,
		"stream": true,
		"messages": []map[string]string{
			{"role": "user", "content": "请用一句话介绍自己。"},
		},
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
		if err != nil {
			return fmt.Errorf("HTTP %s: read error body: %w", response.Status, err)
		}
		return fmt.Errorf("HTTP %s: %s", response.Status, body)
	}
	return printSSE(response.Body, os.Stdout)
}

// printSSE writes each complete SSE frame unchanged, including comments and [DONE].
func printSSE(body io.Reader, output io.Writer) error {
	reader := bufio.NewReader(body)
	var frame bytes.Buffer
	var dataLines []string
	for {
		line, readErr := reader.ReadString('\n')
		if len(line) > 0 {
			frame.WriteString(line)
			content := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
			if content == "" {
				if _, err := output.Write(frame.Bytes()); err != nil {
					return err
				}
				done := strings.TrimSpace(strings.Join(dataLines, "\n")) == "[DONE]"
				frame.Reset()
				dataLines = dataLines[:0]
				if done {
					return nil
				}
			} else if field, value, _ := strings.Cut(content, ":"); field == "data" {
				dataLines = append(dataLines, strings.TrimPrefix(value, " "))
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				return readErr
			}
			// Keep any final bytes even when the upstream omits the closing blank line.
			if frame.Len() > 0 {
				_, err := output.Write(frame.Bytes())
				return err
			}
			return nil
		}
	}
}
