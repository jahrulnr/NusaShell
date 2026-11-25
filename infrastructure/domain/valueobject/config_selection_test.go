package valueobject

import (
	"encoding/json"
	"testing"
)

// The pick list is ordered: storage and the wire read both the array form
// written now and the {id: value} map rooms and payloads carried before —
// the map decodes in id order so the read is deterministic.
func TestConfigSelectionsDecodeBothForms(t *testing.T) {
	var list ConfigSelections
	if err := json.Unmarshal([]byte(`[{"id":"model","value":"a"},{"id":"effort","value":"high"}]`), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0] != (ConfigSelection{ID: "model", Value: "a"}) || list[1] != (ConfigSelection{ID: "effort", Value: "high"}) {
		t.Fatalf("array form = %v", list)
	}
	var old ConfigSelections
	if err := json.Unmarshal([]byte(`{"zeta":"1","mode":"plan","alpha":"2"}`), &old); err != nil {
		t.Fatal(err)
	}
	want := ConfigSelections{{ID: "alpha", Value: "2"}, {ID: "mode", Value: "plan"}, {ID: "zeta", Value: "1"}}
	if len(old) != 3 || old[0] != want[0] || old[1] != want[1] || old[2] != want[2] {
		t.Fatalf("map form = %v, want %v", old, want)
	}
	out, _ := json.Marshal(old)
	if string(out) != `[{"id":"alpha","value":"2"},{"id":"mode","value":"plan"},{"id":"zeta","value":"1"}]` {
		t.Fatalf("marshal = %s", out)
	}
}
