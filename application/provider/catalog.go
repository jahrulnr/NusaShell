package appprovider

import "github.com/jahrulnr/nusashell/infrastructure/domain/entity"

// MinListedContextWindow is the model selection floor. Unknown windows remain visible.
const MinListedContextWindow = 126_000

// ModelCatalogContains reports whether a provider advertises an id.
func ModelCatalogContains(models []entity.Model, id string) bool {
	for _, model := range models {
		if model.ID() == id {
			return true
		}
	}
	return false
}

// ModelListedInPicker applies the selection floor to a catalog entry: the
// model must serve a chat turn (entity.Model.ServesChat) and, when its
// window is declared, meet MinListedContextWindow. Unknown windows stay
// visible — many catalogs omit the declaration.
func ModelListedInPicker(m entity.Model) bool {
	if !m.ServesChat() {
		return false
	}
	w := m.ContextWindow()
	return w == 0 || w >= MinListedContextWindow
}

// ListedModels returns selectable catalog entries in provider order.
func ListedModels(models []entity.Model) []entity.Model {
	out := make([]entity.Model, 0, len(models))
	for _, m := range models {
		if ModelListedInPicker(m) {
			out = append(out, m)
		}
	}
	return out
}
