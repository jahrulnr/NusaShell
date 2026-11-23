package appprovider

import (
	"context"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// catalogListTimeout bounds one provider catalog fetch inside a
// management call. The catalog client's own http.Client deadline stays
// the outer ceiling, but a hung provider must not hold a provider save
// or an import pass hostage for that long.
const catalogListTimeout = 15 * time.Second

// AutoImportResult reports one provider's refresh outcome to the caller's
// diagnostics sink. Added lists newly imported model ids; Error carries a
// per-provider failure — one bad credential never aborts the pass.
type AutoImportResult struct {
	Provider string   `json:"provider"`
	Added    []string `json:"added,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// StartAutoImport refreshes every provider's model catalog immediately and
// then on interval until ctx ends. Newly discovered ids merge into the
// stored record so the picker's catalog stays current without a manual
// import; existing entries keep their tuned parameters, and the record's
// apiKey ref rides along untouched — Inspect hydrates it as a reference,
// never as resolved material, so a refresh can never write a key into a
// provider file.
func (app *ProviderApp) StartAutoImport(ctx context.Context, interval time.Duration, report func(AutoImportResult)) {
	if app.catalog == nil || interval <= 0 {
		return
	}
	// A nil report means the caller wants the refresh without
	// diagnostics. The first pass runs before the select, so a
	// cancelled ctx does not skip it — the guard has to be on the call.
	emit := func(r AutoImportResult) {
		if report != nil {
			report(r)
		}
	}
	for _, r := range app.RefreshModels(ctx) {
		emit(r)
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			for _, r := range app.RefreshModels(ctx) {
				emit(r)
			}
		}
	}
}

// RefreshModels runs a single import pass over every stored provider and
// returns a result per provider attempted. A provider whose record cannot
// decode (Inspect tolerates it as a nil-Provider stub), whose key cannot
// resolve, or whose provider rejects the catalog call reports an Error and
// leaves its record untouched.
func (app *ProviderApp) RefreshModels(ctx context.Context) []AutoImportResult {
	if app.catalog == nil {
		return nil
	}
	snaps, err := app.providers.InspectList(ctx)
	if err != nil {
		return []AutoImportResult{{Error: err.Error()}}
	}
	var results []AutoImportResult
	for _, snap := range snaps {
		if snap.Provider == nil {
			continue // corrupt record — nothing safe to merge into
		}
		r := AutoImportResult{Provider: snap.Provider.ID()}
		if added, err := app.importModels(ctx, snap.Provider.ID()); err != nil {
			r.Error = err.Error()
		} else {
			r.Added = added
		}
		results = append(results, r)
	}
	return results
}

// ImportModels reruns the catalog merge for one stored provider without
// the Upsert write first — the refresh path behind the import-models
// endpoint. A missing record propagates its 404 (the provider itself is
// absent, not the catalog); every other import-side failure reports
// through CatalogError because the record is intact. catalog == nil is
// a wiring error.
func (app *ProviderApp) ImportModels(ctx context.Context, id string) (UpsertOutcome, error) {
	if app.catalog == nil {
		return UpsertOutcome{}, apperror.NewInternalError("model import unavailable", nil)
	}
	added, err := app.importModels(ctx, id)
	if err != nil {
		if apperror.As(err).StatusCode() == apperror.StatusNotFound {
			return UpsertOutcome{}, err
		}
		return UpsertOutcome{CatalogError: err.Error()}, nil
	}
	return UpsertOutcome{Added: added}, nil
}

// importModels fetches the provider catalog for one provider and merges it
// into the record: unknown ids append, known ids backfill the metadata
// fields they leave unknown. The provider call happens outside mu (network
// latency must not serialize management ops); the inspect→merge→save window
// holds it so a concurrent Upsert can't clobber the merged set. The merged
// record goes through Save, which writes apiKey verbatim — safe only
// because Inspect's snapshot carries the raw ref, never resolved material.
func (app *ProviderApp) importModels(ctx context.Context, id string) ([]string, error) {
	// The ref is the stored credential identity. Load resolves it, so a
	// later compare of Provider.ApiKey alone cannot see that the record
	// still names the same "secret:" slot.
	before, err := app.providers.Inspect(ctx, id)
	if err != nil {
		return nil, err
	}
	loaded, err := app.providers.Load(ctx, id)
	if err != nil {
		return nil, err
	}
	listCtx, cancel := context.WithTimeout(ctx, catalogListTimeout)
	catalog, err := app.catalog.ListModels(listCtx, loaded)
	cancel()
	if err != nil {
		return nil, err
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	snap, err := app.providers.Inspect(ctx, id)
	if err != nil {
		return nil, err
	}
	if snap.Provider == nil {
		return nil, nil // became unreadable between list and merge — skip
	}
	// Resolved material catches a rotation that keeps the same secret
	// ref: the vault value changed, the stored "secret:" string did not.
	now, err := app.providers.Load(ctx, id)
	if err != nil {
		return nil, err
	}
	// The catalog was fetched against the record as it was — an Upsert
	// that replaced the endpoint, the style, or the credential since is
	// a different provider surface, and its models would land on a
	// provider they cannot serve.
	if snap.Provider.Endpoint() != loaded.Endpoint() || snap.Provider.Style() != loaded.Style() ||
		snap.ApiKeyRef != before.ApiKeyRef || now.ApiKey() != loaded.ApiKey() {
		return nil, nil
	}
	models := snap.Provider.Models()
	index := make(map[string]int, len(models))
	for i, m := range models {
		index[m.ID()] = i
	}
	var added []string
	changed := false
	for _, cm := range catalog {
		if i, ok := index[cm.ID]; ok {
			if applyCatalogModel(&models[i], cm, snap.Provider.Style()) {
				changed = true
			}
			continue
		}
		m, err := entity.NewModel(cm.ID)
		if err != nil {
			continue // a malformed provider id is not fatal to the pass
		}
		applyCatalogModel(&m, cm, snap.Provider.Style())
		models = append(models, m)
		added = append(added, cm.ID)
		index[cm.ID] = len(models) - 1
		changed = true
	}
	if !changed {
		return nil, nil
	}
	snap.Provider.SetModels(models)
	// Inspect hydrates with an empty apiKey — the record's credential field
	// only survives in ApiKeyRef. Reattach the raw ref verbatim before Save
	// or the refresh would wipe the "secret:"/"env:" indirection — and never
	// Load's resolved material, which Save would persist as plaintext.
	snap.Provider.SetApiKey(snap.ApiKeyRef)
	if err := app.providers.Save(ctx, snap.Provider); err != nil {
		return nil, err
	}
	return added, nil
}

// applyCatalogModel backfills provider-declared metadata onto a stored
// entry: a field fills only while the record leaves it unknown, so an
// explicit value — user-set or previously declared — stays authoritative.
// Clearing a field to zero on Upsert re-derives it at the next import.
// The style pin follows the same rule: the catalog's recommended API fills
// an unset pin, but only when it differs from the provider's style — a pin
// equal to the provider's own style would just freeze today's default.
// Returns whether anything changed.
func applyCatalogModel(m *entity.Model, cm dservice.CatalogModel, provStyle enum.Style) bool {
	changed := false
	if m.Style() == "" && cm.Style != "" && cm.Style != provStyle {
		m.SetStyle(cm.Style)
		changed = true
	}
	if m.Name() == "" && cm.Name != "" {
		m.SetName(cm.Name)
		changed = true
	}
	if m.Canonical() == "" && cm.Canonical != "" {
		m.SetCanonical(cm.Canonical)
		changed = true
	}
	if m.AliasOf() == "" && cm.AliasOf != "" {
		m.SetAliasOf(cm.AliasOf)
		changed = true
	}
	// The request cap (maxOutputTokens) is a third side of the same
	// triangle the write path validates: a window or ceiling below it
	// pairs into a request the provider can never honor — skip the
	// claim that would create it.
	maxOut := 0
	if v := m.MaxOutputTokens(); v != nil {
		maxOut = *v
	}
	if m.ContextWindow() == 0 && cm.ContextWindow > 0 {
		// A window smaller than the declared output ceiling would pair
		// into an impossible request — skip a claim that conflicts with
		// what the record already says.
		if (m.OutputLimit() == 0 || cm.ContextWindow >= m.OutputLimit()) &&
			(maxOut == 0 || cm.ContextWindow >= maxOut) {
			m.SetContextWindow(cm.ContextWindow)
			changed = true
		}
	}
	if m.OutputLimit() == 0 && cm.OutputLimit > 0 {
		if (m.ContextWindow() == 0 || cm.OutputLimit <= m.ContextWindow()) &&
			(maxOut == 0 || cm.OutputLimit >= maxOut) {
			m.SetOutputLimit(cm.OutputLimit)
			changed = true
		}
	}
	if m.ModelType() == "" && cm.ModelType != "" {
		m.SetModelType(cm.ModelType)
		changed = true
	}
	if len(m.InputModalities()) == 0 && len(cm.InputModalities) > 0 {
		m.SetInputModalities(cm.InputModalities)
		changed = true
	}
	if len(m.OutputModalities()) == 0 && len(cm.OutputModalities) > 0 {
		m.SetOutputModalities(cm.OutputModalities)
		changed = true
	}
	if len(m.SupportedEfforts()) == 0 && len(cm.SupportedEfforts) > 0 {
		m.SetSupportedEfforts(cm.SupportedEfforts)
		changed = true
	}
	if m.Pricing() == (valueobject.Pricing{}) && cm.Pricing != nil {
		m.SetPricing(*cm.Pricing)
		changed = true
	}
	return changed
}
