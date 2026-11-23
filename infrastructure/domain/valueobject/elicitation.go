package valueobject

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Elicitation is an agent asking the owner for input mid-turn: a form
// described by a JSON schema, or a URL to visit (a sign-in, a payment).
type Elicitation struct {
	id      string
	mode    string // form or url
	message string
	schema  json.RawMessage
	url     string
}

// NewElicitation validates a request: a form needs an object schema, a URL
// request an http(s) URL.
func NewElicitation(id, mode, message string, schema json.RawMessage, rawURL string) (Elicitation, error) {
	e := Elicitation{id: id, mode: mode, message: strings.TrimSpace(message), schema: schema, url: rawURL}
	switch mode {
	case "form":
		var s struct {
			Type string `json:"type"`
		}
		if len(schema) == 0 || json.Unmarshal(schema, &s) != nil || (s.Type != "" && s.Type != "object") {
			return Elicitation{}, fmt.Errorf("a form elicitation needs an object schema")
		}
	case "url":
		u, err := url.ParseRequestURI(rawURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return Elicitation{}, fmt.Errorf("a URL elicitation needs an http(s) URL")
		}
	default:
		return Elicitation{}, fmt.Errorf("unknown elicitation mode %q", mode)
	}
	return e, nil
}

func (e Elicitation) ID() string              { return e.id }
func (e Elicitation) Mode() string            { return e.mode }
func (e Elicitation) Message() string         { return e.message }
func (e Elicitation) Schema() json.RawMessage { return e.schema }
func (e Elicitation) URL() string             { return e.url }

// Check validates an accepted answer's content against the form schema:
// required fields present, no unknown fields, each value of its field's
// type, length, range, pattern, and choices. A URL request takes no
// content.
func (e Elicitation) Check(content json.RawMessage) error {
	if e.mode == "url" {
		return nil
	}
	values := map[string]any{}
	if len(content) > 0 && string(content) != "null" {
		if err := json.Unmarshal(content, &values); err != nil {
			return fmt.Errorf("content must be an object")
		}
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	_ = json.Unmarshal(e.schema, &schema)
	for _, key := range schema.Required {
		if _, ok := values[key]; !ok {
			return fmt.Errorf("%s is required", key)
		}
	}
	for key, value := range values {
		raw, ok := schema.Properties[key]
		if !ok {
			return fmt.Errorf("unknown field %s", key)
		}
		var spec struct {
			Type  string   `json:"type"`
			Enum  []string `json:"enum"`
			OneOf []struct {
				Const string `json:"const"`
			} `json:"oneOf"`
			MinLength *int     `json:"minLength"`
			MaxLength *int     `json:"maxLength"`
			Pattern   string   `json:"pattern"`
			Minimum   *float64 `json:"minimum"`
			Maximum   *float64 `json:"maximum"`
			MinItems  *int     `json:"minItems"`
			MaxItems  *int     `json:"maxItems"`
			Items     struct {
				Enum  []string `json:"enum"`
				AnyOf []struct {
					Const string `json:"const"`
				} `json:"anyOf"`
			} `json:"items"`
		}
		if err := json.Unmarshal(raw, &spec); err != nil {
			return fmt.Errorf("field %s has an invalid schema", key)
		}
		switch spec.Type {
		case "string":
			v, ok := value.(string)
			if !ok {
				return fmt.Errorf("%s must be text", key)
			}
			n := utf8.RuneCountInString(v)
			if spec.MinLength != nil && n < *spec.MinLength || spec.MaxLength != nil && n > *spec.MaxLength {
				return fmt.Errorf("%s has an invalid length", key)
			}
			if spec.Pattern != "" {
				re, err := regexp.Compile(spec.Pattern)
				if err != nil || !re.MatchString(v) {
					return fmt.Errorf("%s does not match the requested format", key)
				}
			}
			choices := append([]string(nil), spec.Enum...)
			for _, c := range spec.OneOf {
				choices = append(choices, c.Const)
			}
			if len(choices) > 0 && !slices.Contains(choices, v) {
				return fmt.Errorf("%s has an invalid choice", key)
			}
		case "number", "integer":
			v, ok := value.(float64)
			if !ok || spec.Type == "integer" && math.Trunc(v) != v {
				return fmt.Errorf("%s must be a number", key)
			}
			if spec.Minimum != nil && v < *spec.Minimum || spec.Maximum != nil && v > *spec.Maximum {
				return fmt.Errorf("%s is outside the requested range", key)
			}
		case "boolean":
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("%s must be true or false", key)
			}
		case "array":
			v, ok := value.([]any)
			if !ok {
				return fmt.Errorf("%s must be a list", key)
			}
			if spec.MinItems != nil && len(v) < *spec.MinItems || spec.MaxItems != nil && len(v) > *spec.MaxItems {
				return fmt.Errorf("%s has an invalid number of choices", key)
			}
			choices := append([]string(nil), spec.Items.Enum...)
			for _, c := range spec.Items.AnyOf {
				choices = append(choices, c.Const)
			}
			for _, item := range v {
				text, ok := item.(string)
				if !ok || !slices.Contains(choices, text) {
					return fmt.Errorf("%s has an invalid choice", key)
				}
			}
		default:
			return fmt.Errorf("unsupported field type %q", spec.Type)
		}
	}
	return nil
}

// ElicitationAnswer is the owner's reply: accept (with content for a
// form), decline, or cancel.
type ElicitationAnswer struct {
	Action  string          `json:"action"`
	Content json.RawMessage `json:"content,omitempty"`
}

// Valid reports a known action; only accept carries content.
func (a ElicitationAnswer) Valid() bool {
	switch a.Action {
	case "accept":
		return true
	case "decline", "cancel":
		return len(a.Content) == 0 || string(a.Content) == "null"
	}
	return false
}

// ElicitationChoice is a form that asks one thing with a fixed set of
// answers: a text field with choices, a list of choices, or a yes/no
// field. Surfaces without forms (chat polls) can ask it.
type ElicitationChoice struct {
	Field string
	// Options are the answers in the schema's order; a yes/no field
	// offers "Yes" and "No".
	Options []string
	// Max is how many options may be picked: 1 for a single answer.
	Max int
	// List says the answer is a list of the picks.
	List bool
	// Boolean says the answer is true for "Yes", false for "No".
	Boolean bool
}

// Choice reports the form as an ElicitationChoice when it has exactly one
// field and that field is answered by picking.
func (e Elicitation) Choice() (ElicitationChoice, bool) {
	if e.mode != "form" {
		return ElicitationChoice{}, false
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(e.schema, &schema) != nil || len(schema.Properties) != 1 {
		return ElicitationChoice{}, false
	}
	var c ElicitationChoice
	for field, raw := range schema.Properties {
		var spec struct {
			Type  string   `json:"type"`
			Enum  []string `json:"enum"`
			OneOf []struct {
				Const string `json:"const"`
			} `json:"oneOf"`
			MaxItems *int `json:"maxItems"`
			Items    struct {
				Enum  []string `json:"enum"`
				AnyOf []struct {
					Const string `json:"const"`
				} `json:"anyOf"`
			} `json:"items"`
		}
		if json.Unmarshal(raw, &spec) != nil {
			return ElicitationChoice{}, false
		}
		c = ElicitationChoice{Field: field, Max: 1}
		switch spec.Type {
		case "string":
			c.Options = spec.Enum
			for _, o := range spec.OneOf {
				c.Options = append(c.Options, o.Const)
			}
		case "boolean":
			c.Boolean, c.Options = true, []string{"Yes", "No"}
		case "array":
			c.List, c.Options = true, spec.Items.Enum
			for _, o := range spec.Items.AnyOf {
				c.Options = append(c.Options, o.Const)
			}
			c.Max = len(c.Options)
			if spec.MaxItems != nil && *spec.MaxItems > 0 && *spec.MaxItems < c.Max {
				c.Max = *spec.MaxItems
			}
		default:
			return ElicitationChoice{}, false
		}
	}
	c.Options = slices.DeleteFunc(slices.Clone(c.Options), func(o string) bool { return strings.TrimSpace(o) == "" })
	if len(c.Options) < 2 {
		return ElicitationChoice{}, false
	}
	return c, true
}

// Answer accepts the form with picks, which must be among Options.
func (c ElicitationChoice) Answer(picks []string) (ElicitationAnswer, error) {
	if len(picks) == 0 || len(picks) > c.Max {
		return ElicitationAnswer{}, fmt.Errorf("pick between 1 and %d answers", c.Max)
	}
	for _, p := range picks {
		if !slices.Contains(c.Options, p) {
			return ElicitationAnswer{}, fmt.Errorf("%q is not one of the answers", p)
		}
	}
	var value any = picks[0]
	switch {
	case c.Boolean:
		value = picks[0] == "Yes"
	case c.List:
		value = picks
	}
	content, err := json.Marshal(map[string]any{c.Field: value})
	if err != nil {
		return ElicitationAnswer{}, err
	}
	return ElicitationAnswer{Action: "accept", Content: content}, nil
}
