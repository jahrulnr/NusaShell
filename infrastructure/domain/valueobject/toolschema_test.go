package valueobject

import (
	"encoding/json"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

func TestNewToolSchema(t *testing.T) {
	if _, err := NewToolSchema("", "d", nil, true, enum.ToolPermissionGeneral); err == nil {
		t.Fatal("expected error for empty name")
	}
	s, err := NewToolSchema("search", "d", nil, true, enum.ToolPermissionGeneral)
	if err != nil {
		t.Fatalf("NewToolSchema: %v", err)
	}
	if s.Name() != "search" || !s.Enabled() {
		t.Fatalf("unexpected schema: %+v", s)
	}
}

// Parameters must be an object schema: providers 400 on a bare string or
// array on the wire, while an absent schema normalizes to an empty object.
func TestNewToolSchemaParameters(t *testing.T) {
	for name, params := range map[string]json.RawMessage{
		"array":      json.RawMessage(`[]`),
		"string":     json.RawMessage(`"x"`),
		"bad json":   json.RawMessage(`{oops`),
		"wrong type": json.RawMessage(`{"type":"array","items":{}}`),
	} {
		if _, err := NewToolSchema("t", "d", params, true, enum.ToolPermissionGeneral); err == nil {
			t.Errorf("%s: parameters %s accepted", name, params)
		}
	}
	for name, tc := range map[string]struct {
		params json.RawMessage
		want   string
	}{
		"nil":    {nil, `{"type":"object","properties":{}}`},
		"null":   {json.RawMessage(`null`), `{"type":"object","properties":{}}`},
		"object": {json.RawMessage(` {"type":"object"} `), `{"type":"object"}`},
		"ref":    {json.RawMessage(`{"$ref":"#/defs/x"}`), `{"$ref":"#/defs/x"}`},
	} {
		s, err := NewToolSchema("t", "d", tc.params, true, enum.ToolPermissionGeneral)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := string(s.Parameters()); got != tc.want {
			t.Errorf("%s: parameters = %s, want %s", name, got, tc.want)
		}
	}
}

// CallableBy* must fail closed for disabled schemas and unknown permissions.
func TestToolSchemaCallable(t *testing.T) {
	cases := []struct {
		name       string
		enabled    bool
		permission enum.ToolPermission
		agent      bool
		system     bool
	}{
		{"disabled general", false, enum.ToolPermissionGeneral, false, false},
		{"system", true, enum.ToolPermissionSystem, false, true},
		{"agent", true, enum.ToolPermissionAgent, true, false},
		{"general", true, enum.ToolPermissionGeneral, true, true},
		{"unknown permission", true, enum.ToolPermission("bogus"), false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := NewToolSchema("t", "", nil, c.enabled, c.permission)
			if err != nil {
				t.Fatal(err)
			}
			if got := s.CallableByAgent(); got != c.agent {
				t.Fatalf("CallableByAgent = %v, want %v", got, c.agent)
			}
			if got := s.CallableBySystem(); got != c.system {
				t.Fatalf("CallableBySystem = %v, want %v", got, c.system)
			}
		})
	}
}
