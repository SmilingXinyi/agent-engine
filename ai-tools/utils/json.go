// Package utils contains type-independent helpers for tool implementations.
package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// DecodeObject rejects non-object inputs, unknown fields and trailing documents.
func DecodeObject(content []byte, target any) error {
	trimmed := bytes.TrimSpace(content)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("expected a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return fmt.Errorf("expected exactly one JSON object")
	}
	return nil
}
