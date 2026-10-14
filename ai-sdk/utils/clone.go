package utils

import "encoding/json"

// CloneMap returns a shallow copy of source, preserving nil.
func CloneMap[K comparable, V any](source map[K]V) map[K]V {
	if source == nil {
		return nil
	}
	result := make(map[K]V, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

// CloneJSONMap copies JSON-shaped maps, slices and byte payloads recursively.
// Non-JSON objects (for example application-owned pointers) remain shared.
func CloneJSONMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = cloneJSONValue(value)
	}
	return result
}

func cloneJSONValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return CloneJSONMap(value)
	case []any:
		result := make([]any, len(value))
		for index := range value {
			result[index] = cloneJSONValue(value[index])
		}
		return result
	case json.RawMessage:
		return append(json.RawMessage(nil), value...)
	case []byte:
		return append([]byte(nil), value...)
	case []string:
		return append([]string(nil), value...)
	default:
		return value
	}
}
