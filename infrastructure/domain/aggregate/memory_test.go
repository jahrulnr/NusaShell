package aggregate

import (
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

var memNow = time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)

func newMem(t *testing.T, typ enum.MemoryType) *Memory {
	t.Helper()
	m, err := NewMemory("", valueobject.GlobalMemoryScope(), typ, "a-fact", "One line", "Body", enum.MemorySourceAgent, "c_1", memNow)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestNewMemoryValidates(t *testing.T) {
	m := newMem(t, enum.MemoryTypeUser)
	if !strings.HasPrefix(m.ID(), "mem_") || m.Status() != enum.MemoryStatusActive || !m.VerifiedAt().Equal(memNow) {
		t.Fatalf("%s %s %v", m.ID(), m.Status(), m.VerifiedAt())
	}
	bad := []struct {
		name, desc, body string
		typ              enum.MemoryType
		scope            valueobject.MemoryScope
	}{
		{"Not Kebab", "d", "b", enum.MemoryTypeUser, valueobject.GlobalMemoryScope()},
		{strings.Repeat("a", MemoryNameMax+1), "d", "b", enum.MemoryTypeUser, valueobject.GlobalMemoryScope()},
		{"ok", "two\nlines", "b", enum.MemoryTypeUser, valueobject.GlobalMemoryScope()},
		{"ok", strings.Repeat("d", MemoryDescriptionMax+1), "b", enum.MemoryTypeUser, valueobject.GlobalMemoryScope()},
		{"ok", "d", " ", enum.MemoryTypeUser, valueobject.GlobalMemoryScope()},
		{"ok", "d", strings.Repeat("b", MemoryBodyMax+1), enum.MemoryTypeUser, valueobject.GlobalMemoryScope()},
		{"ok", "d", "b", "opinion", valueobject.GlobalMemoryScope()},
		{"ok", "d", "b", enum.MemoryTypeUser, valueobject.MemoryScope{}},
	}
	for _, c := range bad {
		if _, err := NewMemory("", c.scope, c.typ, c.name, c.desc, c.body, enum.MemorySourceAgent, "", memNow); err == nil {
			t.Errorf("accepted %+v", c)
		}
	}
}

func TestMemoryFreshnessByType(t *testing.T) {
	cases := []struct {
		typ  enum.MemoryType
		age  time.Duration
		want enum.MemoryFreshness
	}{
		{enum.MemoryTypeProject, 14 * 24 * time.Hour, enum.MemoryFresh},
		{enum.MemoryTypeProject, 16 * 24 * time.Hour, enum.MemoryAging},
		{enum.MemoryTypeProject, 31 * 24 * time.Hour, enum.MemoryStale},
		{enum.MemoryTypeReference, 60 * 24 * time.Hour, enum.MemoryAging},
		{enum.MemoryTypeUser, 100 * 24 * time.Hour, enum.MemoryAging},
		{enum.MemoryTypeUser, 181 * 24 * time.Hour, enum.MemoryStale},
		{enum.MemoryTypeFeedback, 5 * 365 * 24 * time.Hour, enum.MemoryFresh},
	}
	for _, c := range cases {
		if got := newMem(t, c.typ).Freshness(memNow.Add(c.age)); got != c.want {
			t.Errorf("%s after %v = %s, want %s", c.typ, c.age, got, c.want)
		}
	}
}

func TestMemoryLifecycle(t *testing.T) {
	m := newMem(t, enum.MemoryTypeFeedback)
	if err := m.MarkStale("  ", memNow); err == nil {
		t.Fatal("stale without a reason")
	}
	if err := m.MarkStale("owner  said\notherwise", memNow); err != nil || m.Reason() != "owner said otherwise" || m.Freshness(memNow) != enum.MemoryStale {
		t.Fatalf("%v %q %s", err, m.Reason(), m.Freshness(memNow))
	}
	later := memNow.Add(time.Hour)
	if err := m.Revise("New line", "New body", enum.MemorySourceOwner, "", later); err != nil || m.Status() != enum.MemoryStatusActive || m.Reason() != "" || !m.VerifiedAt().Equal(later) {
		t.Fatalf("revise keeps the stale mark: %v %s %q", err, m.Status(), m.Reason())
	}
	if err := m.Restore(later); err == nil {
		t.Fatal("restored a live memory")
	}
	if err := m.Archive("wrong", later); err != nil || m.Live() {
		t.Fatalf("archive: %v live=%v", err, m.Live())
	}
	if err := m.MarkStale("x", later); err == nil {
		t.Fatal("marked a forgotten memory stale")
	}
	if m.Purgeable(later.Add(MemoryArchiveRetention)) || !m.Purgeable(later.Add(MemoryArchiveRetention+time.Second)) {
		t.Fatal("purge window")
	}
	if err := m.Restore(later.Add(time.Minute)); err != nil || !m.Live() || !m.VerifiedAt().Equal(later) {
		t.Fatalf("restore: %v live=%v verified=%v", err, m.Live(), m.VerifiedAt())
	}
	room, _ := valueobject.RoomMemoryScope("c_1")
	if m.VisibleFrom(valueobject.MemoryScopes("c_2", "")) != true || !m.VisibleFrom([]valueobject.MemoryScope{valueobject.GlobalMemoryScope()}) {
		t.Fatal("global not visible")
	}
	r, _ := NewMemory("", room, enum.MemoryTypeProject, "n", "d", "b", enum.MemorySourceAgent, "c_1", memNow)
	if r.VisibleFrom(valueobject.MemoryScopes("c_2", "p")) || !r.VisibleFrom(valueobject.MemoryScopes("c_1", "")) {
		t.Fatal("room visibility")
	}
}
