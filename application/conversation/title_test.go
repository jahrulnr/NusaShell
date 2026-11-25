package appconversation

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	appjob "github.com/jahrulnr/nusashell/application/job"
)

// titleHandler answers a turn with "pong" and the title request with a
// title wrapped in the quotes and period models tend to add.
func titleHandler(calls *int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		content := "pong"
		if strings.Contains(string(raw), "You name chat threads") {
			*calls++
			content = "\"Ping pong check.\""
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}},
		})
	}
}

func TestAutoTitle(t *testing.T) {
	calls := 0
	app, _ := fixture(t, titleHandler(&calls))
	ctx := t.Context()
	title := func(id string) string {
		conv, err := app.deps.Convs.Load(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return conv.Title()
	}
	if _, err := app.Send(ctx, "c_one", "gw1", "m1", "ping", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}

	// No utility model: nothing is asked and the default title stays.
	if err := app.AutoTitle(ctx, "c_one"); err != nil || calls != 0 || title("c_one") != "ping" {
		t.Fatalf("without a utility model: %v, %d calls, %q", err, calls, title("c_one"))
	}

	changed := 0
	app.OnChanged(func(string) { changed++ })
	app.UseUtility(func() (string, string) { return "gw1", "m1" })
	if err := app.AutoTitle(ctx, "c_one"); err != nil || title("c_one") != "Ping pong check" || changed != 1 {
		t.Fatalf("named: %v, %q, %d changes", err, title("c_one"), changed)
	}

	// Past the first turn, nothing more is asked.
	if _, err := app.Send(ctx, "c_one", "", "", "again", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := app.AutoTitle(ctx, "c_one"); err != nil || calls != 1 {
		t.Fatalf("second turn: %v, %d calls", err, calls)
	}

	// A title the owner set wins.
	if _, err := app.Send(ctx, "c_two", "gw1", "m1", "hello", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	mine := "My name"
	if _, err := app.Annotate(ctx, "c_two", Annotation{Title: &mine}); err != nil {
		t.Fatal(err)
	}
	if err := app.AutoTitle(ctx, "c_two"); err != nil || calls != 1 || title("c_two") != "My name" {
		t.Fatalf("renamed: %v, %d calls, %q", err, calls, title("c_two"))
	}
}

func TestCleanTitle(t *testing.T) {
	for in, want := range map[string]string{
		"**Fixing the login bug.**":    "Fixing the login bug",
		"# Title: “Deploy plan”\nmore": "Deploy plan",
		"  ":                           "",
	} {
		if got := cleanTitle(in); got != want {
			t.Fatalf("cleanTitle(%q) = %q, want %q", in, got, want)
		}
	}
	if got := cleanTitle(strings.Repeat("a", 200)); len([]rune(got)) != titleMaxRunes {
		t.Fatalf("long title has %d runes", len([]rune(got)))
	}
}
