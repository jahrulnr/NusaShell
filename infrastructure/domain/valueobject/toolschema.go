package valueobject

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

// ToolSchema describes a tool the agent may call. Adapters translate it into
// each provider's tool definition wire format. enabled toggles the schema;
// permission restricts who may invoke it (see CallableByAgent /
// CallableBySystem).
type ToolSchema struct {
	name        string
	description string
	parameters  json.RawMessage
	enabled     bool
	permission  enum.ToolPermission
}

// defaultParameters is the schema a declaration with no parameters
// resolves to — providers require an object shape on the wire.
var defaultParameters = json.RawMessage(`{"type":"object","properties":{}}`)

func NewToolSchema(name, description string, parameters json.RawMessage, enabled bool, permission enum.ToolPermission) (ToolSchema, error) {
	s := ToolSchema{
		name:        name,
		description: description,
		enabled:     enabled,
		permission:  permission,
	}
	trimmed := bytes.TrimSpace(parameters)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		// A server omitting inputSchema declares no parameters — every
		// provider still wants an object on the wire.
		s.parameters = defaultParameters
	} else {
		var head struct {
			Type json.RawMessage `json:"type"`
		}
		if trimmed[0] != '{' || json.Unmarshal(trimmed, &head) != nil {
			return s, errors.New("parameters must be a JSON object")
		}
		if len(head.Type) > 0 && string(head.Type) != `"object"` {
			return s, errors.New("parameters must be an object schema")
		}
		s.parameters = trimmed
	}
	return s, s.validate()
}

func (s ToolSchema) validate() error {
	if s.name == "" {
		return errors.New("name is required")
	}
	return nil
}

func (s ToolSchema) Name() string                { return s.name }
func (s ToolSchema) Description() string         { return s.description }
func (s ToolSchema) Parameters() json.RawMessage { return s.parameters }
func (s ToolSchema) Enabled() bool               { return s.enabled }
func (s ToolSchema) Permission() enum.ToolPermission {
	return s.permission
}

// CallableByAgent reports whether the schema may be offered to or invoked by
// the model. Disabled or unknown permission fails closed.
func (s ToolSchema) CallableByAgent() bool {
	return s.enabled && (s.permission == enum.ToolPermissionAgent || s.permission == enum.ToolPermissionGeneral)
}

// CallableBySystem reports whether the application itself may invoke it.
// Disabled or unknown permission fails closed.
func (s ToolSchema) CallableBySystem() bool {
	return s.enabled && (s.permission == enum.ToolPermissionSystem || s.permission == enum.ToolPermissionGeneral)
}
