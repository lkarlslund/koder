package tools

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"
)

type parameterSchema struct {
	Properties map[string]integerProperty `json:"properties"`
}

type integerProperty struct {
	Type    string       `json:"type"`
	Minimum *json.Number `json:"minimum"`
	Maximum *json.Number `json:"maximum"`
}

// normalizeSchemaIntegers canonicalizes every argument declared as an integer
// before tool-specific normalization, persistence, preview, or execution.
func normalizeSchemaIntegers(spec ToolSpec, args map[string]string) (map[string]string, error) {
	if len(args) == 0 || strings.TrimSpace(spec.Parameters) == "" {
		return args, nil
	}
	var schema parameterSchema
	if err := json.Unmarshal([]byte(spec.Parameters), &schema); err != nil {
		return nil, fmt.Errorf("decode %s tool parameter schema: %w", spec.Title, err)
	}
	var normalized map[string]string
	for name, property := range schema.Properties {
		if property.Type != "integer" {
			continue
		}
		raw := strings.TrimSpace(args[name])
		if raw == "" {
			continue
		}
		value, err := ParseToolInt(raw)
		if err != nil {
			return nil, integerArgumentError(name, property)
		}
		if property.Minimum != nil {
			minimum, parseErr := ParseToolInt(property.Minimum.String())
			if parseErr != nil {
				return nil, fmt.Errorf("decode %s minimum: %w", name, parseErr)
			}
			if value < minimum {
				return nil, integerArgumentError(name, property)
			}
		}
		if property.Maximum != nil {
			maximum, parseErr := ParseToolInt(property.Maximum.String())
			if parseErr != nil {
				return nil, fmt.Errorf("decode %s maximum: %w", name, parseErr)
			}
			if value > maximum {
				return nil, fmt.Errorf("%s must be at most %s", name, maximum.String())
			}
		}
		if normalized == nil {
			normalized = maps.Clone(args)
		}
		normalized[name] = value.String()
	}
	if normalized != nil {
		return normalized, nil
	}
	return args, nil
}

func integerArgumentError(name string, property integerProperty) error {
	if property.Minimum != nil {
		switch property.Minimum.String() {
		case "0":
			return fmt.Errorf("%s must be a non-negative integer", name)
		case "1":
			return fmt.Errorf("%s must be a positive integer", name)
		}
	}
	return fmt.Errorf("%s must be an integer", name)
}
