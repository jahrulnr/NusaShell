package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const headlessOutputSchemaResource = "headless-output.json"

// validateHeadlessOutput validates the final assistant content against an
// automation agent's JSON Schema. JSON content is validated as its decoded
// value; non-JSON content remains a string so schemas with type=string can
// keep accepting concise natural-language step results.
func validateHeadlessOutput(content string, schema map[string]any) error {
	if len(schema) == 0 {
		return nil
	}

	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(headlessOutputSchemaResource, schema); err != nil {
		return fmt.Errorf("invalid output_schema: %w", err)
	}
	compiled, err := compiler.Compile(headlessOutputSchemaResource)
	if err != nil {
		return fmt.Errorf("invalid output_schema: %w", err)
	}

	instance := any(content)
	var decoded any
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &decoded); err == nil {
		// Models sometimes double-encode structured output as a JSON string
		// that contains the object (e.g. `"{\"reply\":\"hai\"}"`). Unwrap one
		// level so a structured schema sees the object, not a scalar string.
		if s, ok := decoded.(string); ok {
			var nested any
			if err := json.Unmarshal([]byte(s), &nested); err == nil {
				decoded = nested
			}
		}
		instance = decoded
	}
	if err := compiled.Validate(instance); err != nil {
		return fmt.Errorf("headless output does not match output_schema: %w", err)
	}
	return nil
}
