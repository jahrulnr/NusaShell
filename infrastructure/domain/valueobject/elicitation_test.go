package valueobject

import (
	"encoding/json"
	"testing"
)

func TestNewElicitation(t *testing.T) {
	for _, c := range []struct {
		mode, schema, url string
		ok                bool
	}{
		{"form", `{"type":"object","properties":{}}`, "", true},
		{"form", `{"properties":{}}`, "", true},
		{"form", `{"type":"array"}`, "", false},
		{"form", ``, "", false},
		{"url", ``, "https://example.com/login", true},
		{"url", ``, "javascript:alert(1)", false},
		{"url", ``, "/relative", false},
		{"other", ``, "", false},
	} {
		_, err := NewElicitation("eli_1", c.mode, "m", json.RawMessage(c.schema), c.url)
		if (err == nil) != c.ok {
			t.Errorf("%s %s %s: err = %v", c.mode, c.schema, c.url, err)
		}
	}
}

func TestElicitationCheck(t *testing.T) {
	schema := `{"type":"object","required":["env","n"],"properties":{
		"env":{"type":"string","enum":["dev","prod"]},
		"n":{"type":"integer","minimum":1,"maximum":5},
		"note":{"type":"string","maxLength":4,"pattern":"^[a-z]*$"},
		"on":{"type":"boolean"},
		"tags":{"type":"array","items":{"anyOf":[{"const":"a"},{"const":"b"}]},"maxItems":2}}}`
	e, err := NewElicitation("eli_1", "form", "", json.RawMessage(schema), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		content string
		ok      bool
	}{
		{`{"env":"dev","n":2}`, true},
		{`{"env":"dev","n":2,"note":"abc","on":true,"tags":["a","b"]}`, true},
		{`{"env":"dev"}`, false},                        // required n
		{`{"env":"qa","n":2}`, false},                   // not a choice
		{`{"env":"dev","n":2.5}`, false},                // not an integer
		{`{"env":"dev","n":9}`, false},                  // over maximum
		{`{"env":"dev","n":2,"note":"toolong"}`, false}, // over maxLength
		{`{"env":"dev","n":2,"note":"AB"}`, false},      // pattern
		{`{"env":"dev","n":2,"on":"yes"}`, false},       // not a boolean
		{`{"env":"dev","n":2,"tags":["c"]}`, false},     // not an item choice
		{`{"env":"dev","n":2,"extra":1}`, false},        // unknown field
		{`[1]`, false},
	} {
		if err := e.Check(json.RawMessage(c.content)); (err == nil) != c.ok {
			t.Errorf("%s: err = %v", c.content, err)
		}
	}
	u, _ := NewElicitation("eli_2", "url", "", nil, "https://example.com")
	if err := u.Check(nil); err != nil {
		t.Fatalf("a URL request takes no content: %v", err)
	}
}

func TestElicitationAnswerValid(t *testing.T) {
	for _, c := range []struct {
		a  ElicitationAnswer
		ok bool
	}{
		{ElicitationAnswer{Action: "accept", Content: json.RawMessage(`{}`)}, true},
		{ElicitationAnswer{Action: "decline"}, true},
		{ElicitationAnswer{Action: "cancel", Content: json.RawMessage(`{"x":1}`)}, false},
		{ElicitationAnswer{Action: ""}, false},
	} {
		if c.a.Valid() != c.ok {
			t.Errorf("%+v valid = %v", c.a, !c.ok)
		}
	}
}

// A one-field form answered by picking is a choice; its answer takes the
// field's shape and passes the form's own check.
func TestElicitationChoice(t *testing.T) {
	for _, c := range []struct {
		name, schema string
		ok           bool
		max          int
		picks        []string
		content      string
	}{
		{"enum", `{"type":"object","properties":{"env":{"type":"string","enum":["staging","prod"]}}}`, true, 1, []string{"prod"}, `{"env":"prod"}`},
		{"oneOf", `{"type":"object","properties":{"env":{"type":"string","oneOf":[{"const":"a"},{"const":"b"}]}}}`, true, 1, []string{"b"}, `{"env":"b"}`},
		{"boolean", `{"type":"object","properties":{"go":{"type":"boolean"}}}`, true, 1, []string{"No"}, `{"go":false}`},
		{"array", `{"type":"object","properties":{"tags":{"type":"array","maxItems":2,"items":{"enum":["x","y","z"]}}}}`, true, 2, []string{"x", "z"}, `{"tags":["x","z"]}`},
		{"free text", `{"type":"object","properties":{"name":{"type":"string"}}}`, false, 0, nil, ""},
		{"one choice", `{"type":"object","properties":{"env":{"type":"string","enum":["only"]}}}`, false, 0, nil, ""},
		{"two fields", `{"type":"object","properties":{"a":{"type":"boolean"},"b":{"type":"boolean"}}}`, false, 0, nil, ""},
	} {
		e, err := NewElicitation("e", "form", "q", json.RawMessage(c.schema), "")
		if err != nil {
			t.Fatal(err)
		}
		choice, ok := e.Choice()
		if ok != c.ok {
			t.Fatalf("%s: choice = %v", c.name, ok)
		}
		if !ok {
			continue
		}
		if choice.Max != c.max {
			t.Fatalf("%s: max = %d", c.name, choice.Max)
		}
		ans, err := choice.Answer(c.picks)
		if err != nil || string(ans.Content) != c.content || ans.Action != "accept" {
			t.Fatalf("%s: answer = %+v, %v", c.name, ans, err)
		}
		if err := e.Check(ans.Content); err != nil {
			t.Fatalf("%s: the answer fails the form: %v", c.name, err)
		}
		if _, err := choice.Answer([]string{"nope"}); err == nil {
			t.Fatalf("%s: an unknown pick must be refused", c.name)
		}
	}
}
