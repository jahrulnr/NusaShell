package domain

import "testing"

func TestMemoryEntryMergeFrom(t *testing.T) {
	t.Run("unions tags without duplicates", func(t *testing.T) {
		survivor := &MemoryEntry{
			ID:      "m1",
			Content: "User prefers Indonesian.",
			Tags:    []string{"language", "preference"},
		}
		absorbed := &MemoryEntry{
			ID:      "m2",
			Content: "User lives in Jakarta.",
			Tags:    []string{"location", "preference"},
		}
		survivor.MergeFrom(absorbed)
		wantTags := map[string]bool{"language": true, "preference": true, "location": true}
		for _, tag := range survivor.Tags {
			if !wantTags[tag] {
				t.Errorf("unexpected tag %q", tag)
			}
			delete(wantTags, tag)
		}
		if len(wantTags) > 0 {
			t.Errorf("missing tags: %v", wantTags)
		}
	})

	t.Run("appends different content with merge marker", func(t *testing.T) {
		survivor := &MemoryEntry{ID: "m1", Content: "User prefers Indonesian."}
		absorbed := &MemoryEntry{ID: "m2", Content: "User lives in Jakarta."}
		survivor.MergeFrom(absorbed)
		want := "User prefers Indonesian.\n— merged: User lives in Jakarta."
		if survivor.Content != want {
			t.Fatalf("Content = %q, want %q", survivor.Content, want)
		}
	})

	t.Run("does not append identical content", func(t *testing.T) {
		survivor := &MemoryEntry{ID: "m1", Content: "User prefers Indonesian."}
		absorbed := &MemoryEntry{ID: "m2", Content: "User prefers Indonesian."}
		survivor.MergeFrom(absorbed)
		if survivor.Content != "User prefers Indonesian." {
			t.Fatalf("Content = %q, want unchanged", survivor.Content)
		}
	})

	t.Run("nil absorbed is a no-op", func(t *testing.T) {
		survivor := &MemoryEntry{ID: "m1", Content: "hello", Tags: []string{"a"}}
		survivor.MergeFrom(nil)
		if survivor.Content != "hello" {
			t.Fatalf("Content = %q, want hello", survivor.Content)
		}
		if len(survivor.Tags) != 1 || survivor.Tags[0] != "a" {
			t.Fatalf("Tags = %v, want [a]", survivor.Tags)
		}
	})
}
