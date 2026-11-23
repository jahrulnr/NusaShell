package valueobject

import (
	"errors"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

// MemoryScope is how far a memory reaches: everywhere, one project's
// threads, or one thread. Its text form is "global", "project:<id>", or
// "room:<conversation id>".
type MemoryScope struct {
	kind enum.MemoryScopeKind
	id   string
}

func GlobalMemoryScope() MemoryScope { return MemoryScope{kind: enum.MemoryScopeGlobal} }

func ProjectMemoryScope(projectID string) (MemoryScope, error) {
	if err := ValidID(projectID); err != nil {
		return MemoryScope{}, err
	}
	return MemoryScope{kind: enum.MemoryScopeProject, id: projectID}, nil
}

func RoomMemoryScope(conversationID string) (MemoryScope, error) {
	if err := ValidID(conversationID); err != nil {
		return MemoryScope{}, err
	}
	return MemoryScope{kind: enum.MemoryScopeRoom, id: conversationID}, nil
}

// ParseMemoryScope reads the text form.
func ParseMemoryScope(v string) (MemoryScope, error) {
	if v == string(enum.MemoryScopeGlobal) {
		return GlobalMemoryScope(), nil
	}
	kind, id, ok := strings.Cut(v, ":")
	if !ok {
		return MemoryScope{}, errors.New(`scope must be "global", "project:<id>", or "room:<id>"`)
	}
	switch enum.MemoryScopeKind(kind) {
	case enum.MemoryScopeProject:
		return ProjectMemoryScope(id)
	case enum.MemoryScopeRoom:
		return RoomMemoryScope(id)
	}
	return MemoryScope{}, errors.New(`scope must be "global", "project:<id>", or "room:<id>"`)
}

func (s MemoryScope) Kind() enum.MemoryScopeKind { return s.kind }
func (s MemoryScope) ID() string                 { return s.id }
func (s MemoryScope) IsZero() bool               { return s.kind == "" }

func (s MemoryScope) String() string {
	if s.kind == enum.MemoryScopeGlobal || s.kind == "" {
		return string(enum.MemoryScopeGlobal)
	}
	return string(s.kind) + ":" + s.id
}

// MemoryScopes returns the scopes a thread sees, nearest first: its own,
// its project's (when it has one), and global.
func MemoryScopes(conversationID, projectID string) []MemoryScope {
	out := make([]MemoryScope, 0, 3)
	if room, err := RoomMemoryScope(conversationID); err == nil {
		out = append(out, room)
	}
	if projectID != "" {
		if p, err := ProjectMemoryScope(projectID); err == nil {
			out = append(out, p)
		}
	}
	return append(out, GlobalMemoryScope())
}
