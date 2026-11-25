package valueobject

import (
	"encoding/json"
	"maps"
	"slices"
)

// ConfigSelection is one stored pick of an agent's session option: {id,
// value} in the position it must apply.
type ConfigSelection struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

// ConfigSelections is the ordered pick list — an agent's defaults first,
// then the room's own picks, each replacing the default at its position.
// Order is what a map cannot keep: a dependent option (the effort a model
// pick materializes, for one) must apply after the pick that makes it
// real.
//
// Records written before picks were ordered stored a {id: value} object.
// That form still decodes — into the list in id order — while everything
// written now is the array.
type ConfigSelections []ConfigSelection

func (s *ConfigSelections) UnmarshalJSON(b []byte) error {
	var list []ConfigSelection
	if err := json.Unmarshal(b, &list); err == nil {
		*s = list
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	list = make([]ConfigSelection, 0, len(m))
	for _, id := range slices.Sorted(maps.Keys(m)) {
		list = append(list, ConfigSelection{ID: id, Value: m[id]})
	}
	*s = list
	return nil
}

// Value is the stored pick for id, when one exists.
func (s ConfigSelections) Value(id string) (string, bool) {
	i := slices.IndexFunc(s, func(c ConfigSelection) bool { return c.ID == id })
	if i < 0 {
		return "", false
	}
	return s[i].Value, true
}
