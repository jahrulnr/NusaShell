package domain

import "testing"

func boolPtr(b bool) *bool { return &b }
func intPtr(i int) *int    { return &i }

func TestModelOverrideApplyDisablesVision(t *testing.T) {
	m := &Model{ID: "deepseek-v4-flash", Vision: true, Context: 200000}
	o := &ModelOverride{Provider: "tokenrouter", Model: "deepseek-v4-flash", Vision: boolPtr(false)}
	if changed := o.Apply(m); !changed {
		t.Fatal("expected change")
	}
	if m.Vision {
		t.Error("vision should be disabled")
	}
	if m.Context != 200000 {
		t.Errorf("untouched context changed: %d", m.Context)
	}
}

func TestModelOverrideApplyRaisesContext(t *testing.T) {
	m := &Model{ID: "deepseek-v4-flash", Context: 200000}
	o := &ModelOverride{Provider: "tokenrouter", Model: "deepseek-v4-flash", Context: intPtr(1000000)}
	if changed := o.Apply(m); !changed {
		t.Fatal("expected change")
	}
	if m.Context != 1000000 {
		t.Errorf("context = %d, want 1000000 (override raises)", m.Context)
	}
}

func TestModelOverrideApplyLowersContext(t *testing.T) {
	m := &Model{ID: "m", Context: 1000000}
	o := &ModelOverride{Provider: "p", Model: "m", Context: intPtr(128000)}
	o.Apply(m)
	if m.Context != 128000 {
		t.Errorf("context = %d, want 128000 (override lowers)", m.Context)
	}
}

func TestModelOverrideApplyNilFieldsUntouched(t *testing.T) {
	m := &Model{ID: "m", Vision: true, Audio: false, Context: 500000, MaxOutput: 8192}
	o := &ModelOverride{Provider: "p", Model: "m", Reasoning: boolPtr(true)}
	changed := o.Apply(m)
	if !changed {
		t.Fatal("reasoning set should report change")
	}
	if !m.Reasoning {
		t.Error("reasoning should be enabled")
	}
	if !m.Vision || m.Audio || m.Context != 500000 || m.MaxOutput != 8192 {
		t.Errorf("nil override fields must not touch model: %+v", m)
	}
}

func TestModelOverrideApplyNoChangeWhenSame(t *testing.T) {
	m := &Model{ID: "m", Vision: false}
	o := &ModelOverride{Provider: "p", Model: "m", Vision: boolPtr(false)}
	if changed := o.Apply(m); changed {
		t.Error("applying identical value should report no change")
	}
}

func TestModelOverrideApplyNilModel(t *testing.T) {
	o := &ModelOverride{Provider: "p", Model: "m", Vision: boolPtr(true)}
	if o.Apply(nil) {
		t.Error("nil model must return false")
	}
}

func TestModelOverrideRegistrySetMerge(t *testing.T) {
	r := NewModelOverrideRegistry()
	if err := r.Set(&ModelOverride{Provider: "tokenrouter", Model: "deepseek-v4-flash", Vision: boolPtr(false)}); err != nil {
		t.Fatal(err)
	}
	if err := r.Set(&ModelOverride{Provider: "tokenrouter", Model: "deepseek-v4-flash", Context: intPtr(1000000)}); err != nil {
		t.Fatal(err)
	}
	o := r.Get("tokenrouter", "deepseek-v4-flash")
	if o == nil {
		t.Fatal("override missing after merge")
	}
	if o.Vision == nil || *o.Vision != false {
		t.Error("first field (vision) lost on merge")
	}
	if o.Context == nil || *o.Context != 1000000 {
		t.Error("second field (context) not merged")
	}
	if r.Len() != 1 {
		t.Errorf("merge must not create a second entry, len=%d", r.Len())
	}
}

func TestModelOverrideRegistrySetReplacesField(t *testing.T) {
	r := NewModelOverrideRegistry()
	_ = r.Set(&ModelOverride{Provider: "p", Model: "m", Vision: boolPtr(true)})
	_ = r.Set(&ModelOverride{Provider: "p", Model: "m", Vision: boolPtr(false)})
	o := r.Get("p", "m")
	if o.Vision == nil || *o.Vision != false {
		t.Error("later set must replace the field value")
	}
}

func TestModelOverrideRegistryKeyCaseInsensitive(t *testing.T) {
	r := NewModelOverrideRegistry()
	_ = r.Set(&ModelOverride{Provider: "TokenRouter", Model: "DeepSeek-V4", Vision: boolPtr(true)})
	if r.Get("tokenrouter", "deepseek-v4") == nil {
		t.Error("lookup must be case-insensitive")
	}
}

func TestModelOverrideRegistryRemove(t *testing.T) {
	r := NewModelOverrideRegistry()
	_ = r.Set(&ModelOverride{Provider: "p", Model: "m", Vision: boolPtr(true)})
	if !r.Remove("p", "m") {
		t.Error("remove existing should return true")
	}
	if r.Get("p", "m") != nil {
		t.Error("entry should be gone")
	}
	if r.Remove("p", "m") {
		t.Error("remove missing should return false")
	}
}

func TestModelOverrideRegistryApply(t *testing.T) {
	r := NewModelOverrideRegistry()
	_ = r.Set(&ModelOverride{Provider: "p", Model: "m", Context: intPtr(999000)})
	m := &Model{ID: "m", Context: 1000}
	if changed := r.Apply(m, "p", "m"); !changed {
		t.Fatal("expected apply to change model")
	}
	if m.Context != 999000 {
		t.Errorf("context = %d, want 999000", m.Context)
	}
	// No entry → no change.
	m2 := &Model{ID: "other", Context: 1000}
	if r.Apply(m2, "p", "other") {
		t.Error("no override for this model, must not change")
	}
}

func TestValidateModelOverride(t *testing.T) {
	cases := []struct {
		name    string
		o       *ModelOverride
		wantErr bool
	}{
		{"nil", nil, true},
		{"empty provider", &ModelOverride{Model: "m", Vision: boolPtr(true)}, true},
		{"empty model", &ModelOverride{Provider: "p", Vision: boolPtr(true)}, true},
		{"no fields", &ModelOverride{Provider: "p", Model: "m"}, true},
		{"zero context", &ModelOverride{Provider: "p", Model: "m", Context: intPtr(0)}, true},
		{"negative maxoutput", &ModelOverride{Provider: "p", Model: "m", MaxOutput: intPtr(-5)}, true},
		{"valid bool only", &ModelOverride{Provider: "p", Model: "m", Vision: boolPtr(false)}, false},
		{"valid context only", &ModelOverride{Provider: "p", Model: "m", Context: intPtr(1000000)}, false},
	}
	for _, tc := range cases {
		err := ValidateModelOverride(tc.o)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err=%v wantErr=%v", tc.name, err, tc.wantErr)
		}
	}
}

func TestModelOverrideSetRejectsInvalid(t *testing.T) {
	r := NewModelOverrideRegistry()
	if err := r.Set(&ModelOverride{Provider: "p", Model: "m"}); err == nil {
		t.Error("set with no fields must be rejected")
	}
	if r.Len() != 0 {
		t.Error("rejected set must not store anything")
	}
}
