package agent

import "testing"

func TestSkillAnnouncementNameResolvesDeleteID(t *testing.T) {
	svc := New(Deps{
		ResolveSkillName: func(id, ownedBy string) string {
			if id != "learned-tool-mapping" || ownedBy != "learned" {
				t.Fatalf("resolver args = %q/%q", id, ownedBy)
			}
			return "tool-mapping"
		},
	})
	if got := svc.skillAnnouncementName(`{"op":"delete","id":"learned-tool-mapping","owned_by":"learned"}`); got != "tool-mapping" {
		t.Fatalf("skill announcement name = %q, want tool-mapping", got)
	}
}

func TestSkillAnnouncementNameUsesSaveName(t *testing.T) {
	called := false
	svc := New(Deps{
		ResolveSkillName: func(string, string) string {
			called = true
			return "wrong"
		},
	})
	if got := svc.skillAnnouncementName(`{"op":"save","name":"tool-mapping"}`); got != "tool-mapping" {
		t.Fatalf("skill announcement name = %q, want tool-mapping", got)
	}
	if called {
		t.Fatal("save name should not need a store lookup")
	}
}
