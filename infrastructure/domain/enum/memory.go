package enum

// MemoryType says what a memory is for — it decides where a memory goes
// by default and how fast it ages (see aggregate.Memory.Freshness).
type MemoryType string

const (
	// MemoryTypeUser is who the owner is: role, expertise, preferences.
	MemoryTypeUser MemoryType = "user"
	// MemoryTypeFeedback is how to work: corrections and confirmed
	// approaches, with why and how to apply them.
	MemoryTypeFeedback MemoryType = "feedback"
	// MemoryTypeProject is ongoing work: decisions, constraints, and goals
	// the workspace does not record.
	MemoryTypeProject MemoryType = "project"
	// MemoryTypeReference is where something lives outside the workspace.
	MemoryTypeReference MemoryType = "reference"
)

func (t MemoryType) String() string { return string(t) }

func ParseMemoryType(v string) (MemoryType, bool) {
	switch t := MemoryType(v); t {
	case MemoryTypeUser, MemoryTypeFeedback, MemoryTypeProject, MemoryTypeReference:
		return t, true
	}
	return "", false
}

// MemoryTypes lists the types in the order an index presents them: how to
// work and who for first, then the work, then pointers.
func MemoryTypes() []MemoryType {
	return []MemoryType{MemoryTypeFeedback, MemoryTypeUser, MemoryTypeProject, MemoryTypeReference}
}

// MemoryStatus is a memory's standing.
type MemoryStatus string

const (
	MemoryStatusActive MemoryStatus = "active"
	// MemoryStatusStale was found doubtful or contradicted; it stays
	// visible with its reason until verified, rewritten, or forgotten.
	MemoryStatusStale MemoryStatus = "stale"
	// MemoryStatusArchived was forgotten; it is out of every index and
	// search, restorable until it is purged.
	MemoryStatusArchived MemoryStatus = "archived"
)

func (s MemoryStatus) String() string { return string(s) }

func ParseMemoryStatus(v string) (MemoryStatus, bool) {
	switch s := MemoryStatus(v); s {
	case MemoryStatusActive, MemoryStatusStale, MemoryStatusArchived:
		return s, true
	}
	return "", false
}

// MemorySource says who wrote a memory.
type MemorySource string

const (
	// MemorySourceAgent is a model or agent, during a thread.
	MemorySourceAgent MemorySource = "agent"
	// MemorySourceOwner is the owner, in Settings.
	MemorySourceOwner MemorySource = "owner"
	// MemorySourceConsolidation is a suggestion read from a thread after
	// its compaction, accepted by the owner.
	MemorySourceConsolidation MemorySource = "consolidation"
)

func (s MemorySource) String() string { return string(s) }

func ParseMemorySource(v string) (MemorySource, bool) {
	switch s := MemorySource(v); s {
	case MemorySourceAgent, MemorySourceOwner, MemorySourceConsolidation:
		return s, true
	}
	return "", false
}

// MemoryScopeKind is how far a memory reaches.
type MemoryScopeKind string

const (
	// MemoryScopeGlobal follows the owner into every thread.
	MemoryScopeGlobal MemoryScopeKind = "global"
	// MemoryScopeProject reaches the threads of one project.
	MemoryScopeProject MemoryScopeKind = "project"
	// MemoryScopeRoom reaches one thread.
	MemoryScopeRoom MemoryScopeKind = "room"
)

func (k MemoryScopeKind) String() string { return string(k) }

// MemoryFreshness is how far a memory can be trusted without checking.
type MemoryFreshness string

const (
	MemoryFresh MemoryFreshness = "fresh"
	// MemoryAging is past half its type's horizon: still usable, worth
	// confirming when it matters.
	MemoryAging MemoryFreshness = "aging"
	// MemoryStale must be checked against the present before relying on it.
	MemoryStale MemoryFreshness = "stale"
)

func (f MemoryFreshness) String() string { return string(f) }

// MemoryProposalOp is what a memory suggestion would do.
type MemoryProposalOp string

const (
	// MemoryProposalAdd writes a new memory.
	MemoryProposalAdd MemoryProposalOp = "add"
	// MemoryProposalUpdate rewrites an existing memory.
	MemoryProposalUpdate MemoryProposalOp = "update"
	// MemoryProposalStale marks an existing memory stale.
	MemoryProposalStale MemoryProposalOp = "stale"
)

func (o MemoryProposalOp) String() string { return string(o) }

func ParseMemoryProposalOp(v string) (MemoryProposalOp, bool) {
	switch o := MemoryProposalOp(v); o {
	case MemoryProposalAdd, MemoryProposalUpdate, MemoryProposalStale:
		return o, true
	}
	return "", false
}

// MemoryProposalStatus is where a suggestion stands with the owner.
type MemoryProposalStatus string

const (
	MemoryProposalPending   MemoryProposalStatus = "pending"
	MemoryProposalAccepted  MemoryProposalStatus = "accepted"
	MemoryProposalDismissed MemoryProposalStatus = "dismissed"
)

func (s MemoryProposalStatus) String() string { return string(s) }

func ParseMemoryProposalStatus(v string) (MemoryProposalStatus, bool) {
	switch s := MemoryProposalStatus(v); s {
	case MemoryProposalPending, MemoryProposalAccepted, MemoryProposalDismissed:
		return s, true
	}
	return "", false
}
