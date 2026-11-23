package appprovider

import (
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

// The auto-selection rule picks the only possible answer — the sole
// usable provider, then its sole listed model — and says which fields it
// filled so the transport can announce them.
func TestResolveSelectionAutoSelects(t *testing.T) {
	app, _, _ := newApp(t)
	off := false
	if _, err := app.Upsert(t.Context(), ProviderInput{
		ID: "gw1", Name: "n", Style: enum.StyleMessages, Endpoint: "https://x",
		Stream: &off, Models: []ModelInput{{ID: "m1"}},
	}); err != nil {
		t.Fatal(err)
	}
	// A broken sibling must not count against "the only usable provider".
	if _, err := app.Upsert(t.Context(), ProviderInput{
		ID: "sick", Name: "n", Style: enum.StyleMessages, Endpoint: "https://x",
		ApiKey: new("env:DEFINITELY_UNSET_RESOLVE"),
	}); err != nil {
		t.Fatal(err)
	}

	sel, err := app.ResolveSelection(t.Context(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if sel.ProviderID != "gw1" || sel.ModelID != "m1" || !sel.AutoProvider || !sel.AutoModel {
		t.Fatalf("selection = %+v, want gw1/m1 both auto", sel)
	}

	// Explicit picks pass through untouched — validation is the send
	// path's job, not this rule's.
	sel, err = app.ResolveSelection(t.Context(), "gw1", "m9")
	if err != nil {
		t.Fatal(err)
	}
	if sel.ProviderID != "gw1" || sel.ModelID != "m9" || sel.AutoProvider || sel.AutoModel {
		t.Fatalf("explicit selection = %+v", sel)
	}

	// An explicit healthy provider still gets its sole listed model filled.
	sel, err = app.ResolveSelection(t.Context(), "gw1", "")
	if err != nil {
		t.Fatal(err)
	}
	if sel.ProviderID != "gw1" || sel.ModelID != "m1" || sel.AutoProvider || !sel.AutoModel {
		t.Fatalf("explicit provider + sole model = %+v, want gw1/m1 model-only auto", sel)
	}

	// A broken provider offers no model to auto-fill.
	sel, err = app.ResolveSelection(t.Context(), "sick", "")
	if err != nil {
		t.Fatal(err)
	}
	if sel.ProviderID != "sick" || sel.ModelID != "" || sel.AutoModel {
		t.Fatalf("broken provider selection = %+v", sel)
	}
}

// Ambiguity is not an error and not a guess: multiple usable providers or
// multiple listed models leave the field empty for the caller to ask.
func TestResolveSelectionAmbiguous(t *testing.T) {
	app, _, _ := newApp(t)
	off := false
	for _, id := range []string{"a", "b"} {
		if _, err := app.Upsert(t.Context(), ProviderInput{
			ID: id, Name: "n", Style: enum.StyleMessages, Endpoint: "https://x",
			Stream: &off, Models: []ModelInput{{ID: "m1"}, {ID: "m2"}},
		}); err != nil {
			t.Fatal(err)
		}
	}

	sel, err := app.ResolveSelection(t.Context(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if sel.ProviderID != "" || sel.ModelID != "" || sel.AutoProvider || sel.AutoModel {
		t.Fatalf("ambiguous setup must not auto-select: %+v", sel)
	}

	// A bound provider with two listed models leaves the model open.
	sel, err = app.ResolveSelection(t.Context(), "a", "")
	if err != nil {
		t.Fatal(err)
	}
	if sel.ProviderID != "a" || sel.ModelID != "" || sel.AutoProvider || sel.AutoModel {
		t.Fatalf("two-model provider must not auto-select a model: %+v", sel)
	}
}
