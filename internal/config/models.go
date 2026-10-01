package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ModelPins is a providers.<p> block that carries nothing but the model
// picker's pins.
type ModelPins struct {
	Models []ModelPin `yaml:"models"`
}

// ModelPin is one model the operator pins to the picker's list under
// providers.<p>.models. It is written either as the id alone,
//
//   - claude-opus-4-5-20251101
//
// or with the words the picker should show for it,
//
//   - id: claude-opus-4-5-20251101
//     label: Opus 4.5
//
// A pin is offered whether or not the login has been seen to have it: it
// is the operator's word, not a discovery.
type ModelPin struct {
	ID    string `yaml:"id"`
	Label string `yaml:"label"`
}

// UnmarshalYAML takes the bare-string form as well as the mapping.
func (m *ModelPin) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		*m = ModelPin{ID: node.Value}
		return nil
	case yaml.MappingNode:
		// node.Decode does not carry the decoder's KnownFields, so the
		// keys are checked here: a misspelt label is an error, as it is
		// everywhere else in the file.
		*m = ModelPin{}
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, val := node.Content[i], node.Content[i+1]
			if val.Kind != yaml.ScalarNode {
				return fmt.Errorf("line %d: model pin %s must be a string", val.Line, key.Value)
			}
			switch key.Value {
			case "id":
				m.ID = val.Value
			case "label":
				m.Label = val.Value
			default:
				return fmt.Errorf("line %d: field %s not found in a model pin (id, label)", key.Line, key.Value)
			}
		}
		return nil
	default:
		return fmt.Errorf("line %d: a model pin is an id or {id, label}", node.Line)
	}
}

// PinnedModels is providers.<provider>.models with blank ids dropped and
// every id and label trimmed, in the order written. An unknown provider,
// or a nil Config, pins nothing.
func (c *Config) PinnedModels(provider string) []ModelPin {
	if c == nil {
		return nil
	}
	var in []ModelPin
	switch provider {
	case "claude":
		in = c.Providers.Claude.Models
	case "codex":
		in = c.Providers.Codex.Models
	case "openai":
		in = c.Providers.OpenAI.Models
	case "acp":
		in = c.Providers.ACP.Models
	case "qwen":
		in = c.Providers.Qwen.Models
	case "cursor":
		in = c.Providers.Cursor.Models
	}
	var out []ModelPin
	for _, m := range in {
		m.ID, m.Label = strings.TrimSpace(m.ID), strings.TrimSpace(m.Label)
		if m.ID != "" {
			out = append(out, m)
		}
	}
	return out
}
