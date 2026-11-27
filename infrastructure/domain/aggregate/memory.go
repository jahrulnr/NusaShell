package aggregate

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// Memory limits keep one memory one fact: the index shows every
// description, and a body is read whole.
const (
	MemoryNameMax        = 64
	MemoryDescriptionMax = 200
	MemoryBodyMax        = 4000
	MemoryReasonMax      = 300
)

// MemoryArchiveRetention is how long a forgotten memory stays restorable.
const MemoryArchiveRetention = 30 * 24 * time.Hour

// memoryHorizon is how long a memory of each type is trusted after it was
// last verified. Feedback holds until it is contradicted.
var memoryHorizon = map[enum.MemoryType]time.Duration{
	enum.MemoryTypeProject:   30 * 24 * time.Hour,
	enum.MemoryTypeReference: 90 * 24 * time.Hour,
	enum.MemoryTypeUser:      180 * 24 * time.Hour,
}

var memoryName = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Memory is one remembered fact: what the owner or their work is, typed by
// purpose, scoped to where it applies, and dated so its trust can decay.
// A memory is the model's own note from the past — evidence, never an
// instruction — so it carries when it was last confirmed true.
type Memory struct {
	id          string
	scope       valueobject.MemoryScope
	mtype       enum.MemoryType
	name        string
	description string
	body        string
	source      enum.MemorySource // who wrote the current text
	origin      string            // conversation that wrote it; "" when the owner did
	createdAt   time.Time
	updatedAt   time.Time
	verifiedAt  time.Time
	recalledAt  time.Time
	recalls     int
	status      enum.MemoryStatus
	reason      string // why it is stale or was forgotten
	archivedAt  time.Time
}

// NewMemory builds an active memory, verified now. An empty id mints one.
func NewMemory(id string, scope valueobject.MemoryScope, t enum.MemoryType, name, description, body string, source enum.MemorySource, origin string, now time.Time) (*Memory, error) {
	vo, err := valueobject.NewID("mem", id)
	if err != nil {
		return nil, err
	}
	if scope.IsZero() {
		return nil, errors.New("memory scope is required")
	}
	if _, ok := enum.ParseMemoryType(string(t)); !ok {
		return nil, fmt.Errorf("memory type %q is not one of user, feedback, project, reference", t)
	}
	name, err = memoryNameOf(name)
	if err != nil {
		return nil, err
	}
	m := &Memory{id: vo.String(), scope: scope, mtype: t, name: name, createdAt: now, status: enum.MemoryStatusActive}
	if err := m.Revise(description, body, source, origin, now); err != nil {
		return nil, err
	}
	return m, nil
}

// Revise replaces what the memory says, written by source (from the
// thread origin, "" for the owner). Rewriting a memory is confirming it,
// so it is verified and loses a stale mark; an archived memory comes
// back, since saving it again means it is wanted.
func (m *Memory) Revise(description, body string, source enum.MemorySource, origin string, now time.Time) error {
	if _, ok := enum.ParseMemorySource(string(source)); !ok {
		return fmt.Errorf("memory source %q is invalid", source)
	}
	description, body, err := memoryText(description, body)
	if err != nil {
		return err
	}
	m.description, m.body, m.source, m.origin = description, body, source, origin
	m.updatedAt = now
	m.Verify(now)
	return nil
}

// memoryText checks what a memory says: a one-line description and a body,
// each within its bound. A proposal that would write a memory obeys it too.
func memoryText(description, body string) (string, string, error) {
	description = strings.TrimSpace(description)
	body = strings.TrimSpace(body)
	switch {
	case description == "":
		return "", "", errors.New("a memory needs a one-line description")
	case strings.ContainsAny(description, "\r\n"):
		return "", "", errors.New("a memory description must be one line")
	case utf8.RuneCountInString(description) > MemoryDescriptionMax:
		return "", "", fmt.Errorf("a memory description is at most %d characters", MemoryDescriptionMax)
	case body == "":
		return "", "", errors.New("a memory needs a body")
	case utf8.RuneCountInString(body) > MemoryBodyMax:
		return "", "", fmt.Errorf("a memory body is at most %d characters — keep one fact per memory", MemoryBodyMax)
	}
	return description, body, nil
}

// Verify confirms the memory is still true without changing it.
func (m *Memory) Verify(now time.Time) {
	m.verifiedAt = now
	m.status = enum.MemoryStatusActive
	m.reason = ""
	m.archivedAt = time.Time{}
}

// MarkStale records that the memory was found doubtful or contradicted.
// It stays visible, with the reason, until it is verified, rewritten, or
// forgotten. An archived memory cannot be marked.
func (m *Memory) MarkStale(reason string, now time.Time) error {
	if m.status == enum.MemoryStatusArchived {
		return errors.New("a forgotten memory cannot be marked stale; restore it first")
	}
	reason, err := memoryReason(reason)
	if err != nil {
		return err
	}
	m.status, m.reason, m.updatedAt = enum.MemoryStatusStale, reason, now
	return nil
}

// Archive forgets the memory: out of every index and search, restorable
// until Purgeable.
func (m *Memory) Archive(reason string, now time.Time) error {
	reason, err := memoryReason(reason)
	if err != nil {
		return err
	}
	m.status, m.reason, m.archivedAt, m.updatedAt = enum.MemoryStatusArchived, reason, now, now
	return nil
}

// Restore brings a forgotten memory back as it was, unverified: it keeps
// the age it had.
func (m *Memory) Restore(now time.Time) error {
	if m.status != enum.MemoryStatusArchived {
		return errors.New("only a forgotten memory can be restored")
	}
	m.status, m.reason, m.archivedAt, m.updatedAt = enum.MemoryStatusActive, "", time.Time{}, now
	return nil
}

// Recalled records that the memory's body was read.
func (m *Memory) Recalled(now time.Time) {
	m.recalledAt = now
	m.recalls++
}

// Live reports whether the memory takes part in indexes and searches.
func (m *Memory) Live() bool { return m.status != enum.MemoryStatusArchived }

// Freshness is how far the memory can be trusted now: stale when marked
// so or past its type's horizon since last verified, aging past half of
// it. Feedback does not age.
func (m *Memory) Freshness(now time.Time) enum.MemoryFreshness {
	if m.status == enum.MemoryStatusStale {
		return enum.MemoryStale
	}
	horizon, ages := memoryHorizon[m.mtype]
	if !ages {
		return enum.MemoryFresh
	}
	switch age := now.Sub(m.verifiedAt); {
	case age > horizon:
		return enum.MemoryStale
	case age > horizon/2:
		return enum.MemoryAging
	}
	return enum.MemoryFresh
}

// Purgeable reports a forgotten memory past its restore window.
func (m *Memory) Purgeable(now time.Time) bool {
	return m.status == enum.MemoryStatusArchived && now.Sub(m.archivedAt) > MemoryArchiveRetention
}

// VisibleFrom reports whether a thread with the given scopes sees it.
func (m *Memory) VisibleFrom(scopes []valueobject.MemoryScope) bool {
	return slices.Contains(scopes, m.scope)
}

func memoryNameOf(name string) (string, error) {
	name = strings.TrimSpace(name)
	if len(name) > MemoryNameMax || !memoryName.MatchString(name) {
		return "", fmt.Errorf("memory name %q must be a kebab-case slug of at most %d characters", name, MemoryNameMax)
	}
	return name, nil
}

func memoryReason(reason string) (string, error) {
	reason = strings.Join(strings.Fields(reason), " ")
	if reason == "" {
		return "", errors.New("give the reason")
	}
	if utf8.RuneCountInString(reason) > MemoryReasonMax {
		return "", fmt.Errorf("the reason is at most %d characters", MemoryReasonMax)
	}
	return reason, nil
}

func (m *Memory) ID() string                     { return m.id }
func (m *Memory) Scope() valueobject.MemoryScope { return m.scope }
func (m *Memory) Type() enum.MemoryType          { return m.mtype }
func (m *Memory) Name() string                   { return m.name }
func (m *Memory) Description() string            { return m.description }
func (m *Memory) Body() string                   { return m.body }
func (m *Memory) Source() enum.MemorySource      { return m.source }
func (m *Memory) Origin() string                 { return m.origin }
func (m *Memory) CreatedAt() time.Time           { return m.createdAt }
func (m *Memory) UpdatedAt() time.Time           { return m.updatedAt }
func (m *Memory) VerifiedAt() time.Time          { return m.verifiedAt }
func (m *Memory) RecalledAt() time.Time          { return m.recalledAt }
func (m *Memory) Recalls() int                   { return m.recalls }
func (m *Memory) Status() enum.MemoryStatus      { return m.status }
func (m *Memory) Reason() string                 { return m.reason }
func (m *Memory) ArchivedAt() time.Time          { return m.archivedAt }

// SetStored hydrates the persisted lifecycle fields — the repository's
// path back from storage, after NewMemory rebuilt the identity and content.
func (m *Memory) SetStored(createdAt, updatedAt, verifiedAt, recalledAt time.Time, recalls int, status enum.MemoryStatus, reason string, archivedAt time.Time) {
	m.createdAt, m.updatedAt, m.verifiedAt, m.recalledAt = createdAt, updatedAt, verifiedAt, recalledAt
	m.recalls, m.status, m.reason, m.archivedAt = recalls, status, reason, archivedAt
}

// SetType retypes a memory — the owner's correction in Settings.
func (m *Memory) SetType(t enum.MemoryType, now time.Time) error {
	if _, ok := enum.ParseMemoryType(string(t)); !ok {
		return fmt.Errorf("memory type %q is not one of user, feedback, project, reference", t)
	}
	m.mtype, m.updatedAt = t, now
	return nil
}
