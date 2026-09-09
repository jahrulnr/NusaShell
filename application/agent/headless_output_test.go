package agent

import (
	"strings"
	"testing"
)

func TestValidateHeadlessOutputAcceptsJSONMatchingSchema(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status": map[string]any{"type": "string"},
		},
		"required":             []any{"status"},
		"additionalProperties": false,
	}

	if err := validateHeadlessOutput(`{"status":"ok"}`, schema); err != nil {
		t.Fatalf("valid output rejected: %v", err)
	}
}

func TestValidateHeadlessOutputRejectsJSONViolatingSchema(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status": map[string]any{"type": "string"},
		},
		"required": []any{"status"},
	}

	err := validateHeadlessOutput(`{"status":42}`, schema)
	if err == nil || !strings.Contains(err.Error(), "does not match output_schema") {
		t.Fatalf("invalid output error = %v, want schema mismatch", err)
	}
}

func TestValidateHeadlessOutputAcceptsPlainTextStringSchema(t *testing.T) {
	schema := map[string]any{"type": "string", "minLength": 3}

	if err := validateHeadlessOutput("done", schema); err != nil {
		t.Fatalf("plain text output rejected by string schema: %v", err)
	}
}

func TestValidateHeadlessOutputRejectsInvalidSchema(t *testing.T) {
	err := validateHeadlessOutput(`{"status":"ok"}`, map[string]any{"type": "not-a-json-type"})
	if err == nil || !strings.Contains(err.Error(), "invalid output_schema") {
		t.Fatalf("invalid schema error = %v, want invalid-schema diagnostic", err)
	}
}

func TestValidateHeadlessOutputAcceptsDoubleEncodedReply(t *testing.T) {
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"reply": map[string]any{"type": "string"}},
		"required":   []any{"reply"},
	}
	if err := validateHeadlessOutput(`"{\"reply\":\"hai\"}"`, schema); err != nil {
		t.Fatalf("double-encoded JSON string should validate: %v", err)
	}
	if err := validateHeadlessOutput(`{"reply":"hai"}`, schema); err != nil {
		t.Fatalf("plain object should validate: %v", err)
	}
	if err := validateHeadlessOutput(`plain text`, schema); err == nil {
		t.Fatal("non-JSON plain text must fail a structured schema")
	}
}
