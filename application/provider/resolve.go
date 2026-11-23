package appprovider

import "context"

// Selection is ResolveSelection's answer: the provider/model the next turn
// should use plus which of them the auto-selection rule picked itself,
// so a transport can announce the pick in its own words instead of the
// rule carrying CLI phrasing.
type Selection struct {
	ProviderID   string
	ModelID      string
	AutoProvider bool // providerID was unbound; the sole usable provider filled it
	AutoModel    bool // model was unbound; the providerID's sole listed entry filled it
}

// ResolveSelection applies the provider/model selection policy: explicit
// picks pass through untouched (the send path validates them), while an
// unbound provider fills from the sole usable provider and an unbound model
// fills from the provider's sole listed model — an unambiguous setup
// selects itself rather than making the operator retype the only
// possible answer. Ambiguity is not an error: the field comes back
// empty and the caller decides how to ask.
func (app *ProviderApp) ResolveSelection(ctx context.Context, providerID, modelID string) (Selection, error) {
	sel := Selection{ProviderID: providerID, ModelID: modelID}
	if sel.ProviderID == "" {
		providers, err := app.List(ctx)
		if err != nil {
			return Selection{}, err
		}
		var ready []ProviderInfo
		for _, g := range providers {
			if !g.Broken {
				ready = append(ready, g)
			}
		}
		if len(ready) == 1 {
			sel.ProviderID = ready[0].ID
			sel.AutoProvider = true
		}
	}
	if sel.ModelID == "" && sel.ProviderID != "" {
		// A provider that won't load has nothing to offer — leave the
		// model unbound rather than pinning one on a dead provider.
		if prov, err := app.Get(ctx, sel.ProviderID); err == nil && !prov.Broken {
			if listed := prov.ListedModels(); len(listed) == 1 {
				sel.ModelID = listed[0].ID()
				sel.AutoModel = true
			}
		}
	}
	return sel, nil
}
