package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"
)

// DecodeJSON rejects unknown fields, extra documents and null, retaining JSON
// numbers in protocol extension maps without a float64 round trip.
func DecodeJSON(content []byte, target any) error {
	if bytes.Equal(bytes.TrimSpace(content), []byte("null")) {
		return fmt.Errorf("expected a JSON object, got null")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("expected exactly one JSON document")
	}
	return nil
}

func LoadConfig(path string) (Config, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read workspace config: %w", err)
	}
	var config Config
	if err := DecodeJSON(content, &config); err != nil {
		return Config{}, fmt.Errorf("parse workspace config: %w", err)
	}
	return config, config.Validate()
}

func (config Config) Validate() error {
	if config.MaxSteps < 0 || config.MaxHistoryRounds < 0 || config.TimeoutSeconds < 0 || int64(config.TimeoutSeconds) > math.MaxInt64/int64(time.Second) {
		return fmt.Errorf("workspace limits must be nonnegative and timeout must fit time.Duration")
	}
	if config.APIType != "" && config.APIType != "chat_completions" && config.APIType != "responses_api" {
		return fmt.Errorf("unsupported apiType %q", config.APIType)
	}
	if strings.ContainsAny(config.APIKeyEnv, "=\x00") {
		return fmt.Errorf("invalid API key environment variable name")
	}
	return nil
}

// WithDefaults returns a snapshot. It does not mutate process environment.
func (config Config) WithDefaults() Config {
	if config.Model == "" {
		config.Model = os.Getenv("AGENT_MODEL")
		if config.Model == "" {
			config.Model = os.Getenv("AGENT_LLM_MODEL")
		}
	}
	if config.BaseURL == "" {
		config.BaseURL = os.Getenv("AGENT_LLM_BASE_URL")
	}
	if config.APIType == "" {
		config.APIType = "chat_completions"
	}
	if config.MaxSteps == 0 {
		config.MaxSteps = 40
	}
	if config.MaxHistoryRounds == 0 {
		config.MaxHistoryRounds = 10
	}
	if config.TimeoutSeconds == 0 {
		config.TimeoutSeconds = 900
	}
	return config
}

func (mode ApprovalMode) Validate() error {
	switch mode {
	case "", ApprovalWrites, ApprovalAll, ApprovalAuto:
		return nil
	default:
		return fmt.Errorf("invalid approval mode %q: expected writes, all or auto", mode)
	}
}
