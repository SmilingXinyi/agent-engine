package schema

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// GenerateJSONSchema generates a JSON schema for a Go struct type.
func GenerateJSONSchema(v any) (json.RawMessage, error) {
	t := reflect.TypeOf(v)
	if t == nil {
		return json.RawMessage(`{"type":"object"}`), nil
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("expected struct, got %s", t.Kind())
	}

	properties := make(map[string]any)
	var required []string

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}

		jsonTag := field.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}

		name := field.Name
		isOptional := false
		if jsonTag != "" {
			parts := strings.Split(jsonTag, ",")
			if parts[0] != "" {
				name = parts[0]
			}
			for _, p := range parts[1:] {
				if p == "omitempty" {
					isOptional = true
				}
			}
		}

		desc := field.Tag.Get("description")
		fieldSchema := schemaForType(field.Type, desc)
		properties[name] = fieldSchema

		if !isOptional {
			required = append(required, name)
		}
	}

	result := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		result["required"] = required
	}

	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func schemaForType(t reflect.Type, description string) map[string]any {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	schema := make(map[string]any)
	if description != "" {
		schema["description"] = description
	}

	switch t.Kind() {
	case reflect.String:
		schema["type"] = "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		schema["type"] = "integer"
	case reflect.Float32, reflect.Float64:
		schema["type"] = "number"
	case reflect.Bool:
		schema["type"] = "boolean"
	case reflect.Slice, reflect.Array:
		schema["type"] = "array"
		schema["items"] = schemaForType(t.Elem(), "")
	case reflect.Map:
		schema["type"] = "object"
	case reflect.Struct:
		schema["type"] = "object"
		props := make(map[string]any)
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			fn := f.Name
			tag := f.Tag.Get("json")
			if tag == "-" {
				continue
			}
			if tag != "" {
				parts := strings.Split(tag, ",")
				if parts[0] != "" {
					fn = parts[0]
				}
			}
			props[fn] = schemaForType(f.Type, f.Tag.Get("description"))
		}
		schema["properties"] = props
	default:
		schema["type"] = "string"
	}

	return schema
}
