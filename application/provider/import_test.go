package provider

import (
	"testing"

	"nusashell/domain"
)

func TestImportSeedCodexImageModelsDoesNotDuplicate(t *testing.T) {
	in := []domain.Model{{ID: "gpt-image-2", Kind: domain.ModelKindChat}}
	out := seedCodexImageModels(in)
	count := 0
	for _, m := range out {
		if m.ID == "gpt-image-2" {
			count++
			if m.Kind != domain.ModelKindImage {
				t.Fatalf("kind = %q", m.Kind)
			}
			if !m.Vision {
				t.Fatal("gpt-image-2 must be tagged Vision for i2i edits")
			}
		}
	}
	if count != 1 {
		t.Fatalf("duplicates = %d", count)
	}
}

func TestImportSeedCodexImageModelsAddsMissing(t *testing.T) {
	out := seedCodexImageModels([]domain.Model{{ID: "gpt-5.4"}})
	got := map[string]domain.ModelKind{}
	gotVision := map[string]bool{}
	for _, m := range out {
		got[m.ID] = m.Kind
		gotVision[m.ID] = m.Vision
	}
	if got["gpt-5.4"] == domain.ModelKindImage {
		t.Fatal("chat model must not be retagged image")
	}
	if got["gpt-image-2"] != domain.ModelKindImage || got["gpt-image-1.5"] != domain.ModelKindImage {
		t.Fatalf("seeded kinds = %v", got)
	}
	if !gotVision["gpt-image-2"] || !gotVision["gpt-image-1.5"] {
		t.Fatalf("seeded vision flags = %v, want both true (i2i-capable)", gotVision)
	}
}
