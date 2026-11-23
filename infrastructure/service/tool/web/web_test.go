package toolweb

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jahrulnr/searchwire"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

func stubDef(name string, out string, runErr error) searchwire.ToolDef {
	return searchwire.ToolDef{
		Name:        name,
		Description: "stub " + name,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`),
		Run: func(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
			if runErr != nil {
				return nil, runErr
			}
			return json.RawMessage(out), nil
		},
	}
}

func TestNewPrefixesWebName(t *testing.T) {
	tool, err := New(stubDef("search", "{}", nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if tool.Schema().Name() != "web_search" {
		t.Fatalf("name = %q", tool.Schema().Name())
	}
	if !tool.Schema().CallableByAgent() {
		t.Fatal("expected agent-callable schema")
	}
}

func TestExecutePassesInputThroughAndReturnsText(t *testing.T) {
	var gotArgs json.RawMessage
	def := stubDef("fetch", "", nil)
	def.Run = func(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
		gotArgs = args
		return json.RawMessage(`{"text":"page"}`), nil
	}
	tool, err := New(def, nil)
	if err != nil {
		t.Fatal(err)
	}
	call, err := entity.NewToolCall("c1", "web_fetch", `{"url":"https://example.com"}`, enum.TooltypeOrigin)
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool.Execute(context.Background(), call, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotArgs) != `{"url":"https://example.com"}` {
		t.Fatalf("args = %s", gotArgs)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(result.Text), &doc); err != nil {
		t.Fatalf("text = %q: %v", result.Text, err)
	}
	if doc["text"] != "page" || doc["backend"] != "searchwire" {
		t.Fatalf("doc = %#v", doc)
	}
}

func TestExecuteSurfacesRunErrorAsToolError(t *testing.T) {
	runErr := errors.New("upstream failed")
	tool, err := New(stubDef("search", "", runErr), nil)
	if err != nil {
		t.Fatal(err)
	}
	call, err := entity.NewToolCall("c1", "web_search", `{"query":"x"}`, enum.TooltypeOrigin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), call, nil); !errors.Is(err, runErr) {
		t.Fatalf("err = %v", err)
	}
}

func TestToolsFiltersNamedCapabilities(t *testing.T) {
	tools, err := Tools(nil, nil, searchwire.ToolSearch, searchwire.ToolFetch)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Schema().Name()] = true
	}
	if !names["web_search"] || !names["web_fetch"] || names["web_answer"] {
		t.Fatalf("names = %#v", names)
	}
}

// A provider error body can echo the Authorization header back — the
// fallback note lands in tool output and model context, so the key must
// be scrubbed alongside the endpoint and model id.
func TestSanitizeFallbackNoteRedactsAPIKey(t *testing.T) {
	p := service.WebProvider{
		Endpoint: "https://api.example.com",
		APIKey:   "sk-live-secret",
		Model:    "m1",
	}
	note := sanitizeFallbackNote(errors.New(`401 from https://api.example.com: {"key":"sk-live-secret"} for m1`), p)
	for _, leak := range []string{"sk-live-secret", "https://api.example.com", "m1"} {
		if strings.Contains(note, leak) {
			t.Fatalf("note leaks %q: %q", leak, note)
		}
	}
}
