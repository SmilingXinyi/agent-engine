package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	"gopkg.in/yaml.v3"
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

// DecodeYAML uses the existing configuration schema and validation hooks after
// decoding one YAML mapping. Runtime state continues to use DecodeJSON.
func DecodeYAML(content []byte, target any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	var document any
	if err := decoder.Decode(&document); err != nil {
		return err
	}
	if _, ok := document.(map[string]any); !ok {
		return fmt.Errorf("expected a YAML mapping")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err != nil {
			return err
		}
		return fmt.Errorf("expected exactly one YAML document")
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return err
	}
	return DecodeJSON(encoded, target)
}

// ReadYAMLConfig reads a real configuration file and reports legacy filenames
// only when the corresponding YAML file is absent.
func ReadYAMLConfig(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		legacy := strings.TrimSuffix(path, filepath.Ext(path)) + ".json"
		if _, legacyErr := os.Lstat(legacy); legacyErr == nil {
			return nil, fmt.Errorf("convert %s to YAML and save it as %s", legacy, path)
		} else if !errors.Is(legacyErr, os.ErrNotExist) {
			return nil, legacyErr
		}
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("configuration must be a regular file: %s", path)
	}
	return os.ReadFile(path)
}

func LoadConfig(path string) (Config, error) {
	content, err := ReadYAMLConfig(path)
	if err != nil {
		return Config{}, fmt.Errorf("read workspace config: %w", err)
	}
	var config Config
	if err := DecodeYAML(content, &config); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return config, config.Validate()
}

func (config Config) Validate() error {
	if config.MaxSteps < 0 || config.MaxHistoryRounds < 0 || config.TimeoutSeconds < 0 || int64(config.TimeoutSeconds) > math.MaxInt64/int64(time.Second) {
		return fmt.Errorf("workspace limits must be nonnegative and timeout must fit time.Duration")
	}
	if config.ModelMaxRetries != nil && *config.ModelMaxRetries < 0 {
		return fmt.Errorf("modelMaxRetries must be nonnegative")
	}
	if config.ReasoningEffort != "" && strings.TrimSpace(config.ReasoningEffort) == "" {
		return fmt.Errorf("reasoningEffort must not be blank")
	}
	if config.APIType != "" && config.APIType != "chat_completions" && config.APIType != "responses_api" {
		return fmt.Errorf("unsupported apiType %q", config.APIType)
	}
	if strings.ContainsAny(config.APIKeyEnv, "=\x00") {
		return fmt.Errorf("invalid API key environment variable name")
	}
	if config.Compression != nil {
		return config.Compression.Validate()
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
	if config.ModelMaxRetries == nil {
		retries := 2
		config.ModelMaxRetries = &retries
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

func (config Config) Reasoning() *aiprot.ReasoningOptions {
	if config.ReasoningEffort == "" {
		return nil
	}
	return &aiprot.ReasoningOptions{Effort: config.ReasoningEffort}
}
