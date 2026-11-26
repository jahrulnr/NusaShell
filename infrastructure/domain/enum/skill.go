package enum

// SkillKind is what a learned skill teaches: a tool procedure or a method.
type SkillKind string

const (
	SkillKindTool   SkillKind = "tool"
	SkillKindMethod SkillKind = "method"
)

func (k SkillKind) String() string { return string(k) }

func ParseSkillKind(v string) (SkillKind, bool) {
	switch k := SkillKind(v); k {
	case SkillKindTool, SkillKindMethod:
		return k, true
	}
	return "", false
}

// SkillEvidenceKind is what a learned skill rests on: the owner's words, a
// rewrite of an owner message, two owner turns, or a tool call that worked.
type SkillEvidenceKind string

const (
	SkillEvidenceQuote   SkillEvidenceKind = "quote"
	SkillEvidenceRewrite SkillEvidenceKind = "rewrite"
	SkillEvidenceRepeat  SkillEvidenceKind = "repeat"
	SkillEvidenceCall    SkillEvidenceKind = "call"
)

func (k SkillEvidenceKind) String() string { return string(k) }

func ParseSkillEvidenceKind(v string) (SkillEvidenceKind, bool) {
	switch k := SkillEvidenceKind(v); k {
	case SkillEvidenceQuote, SkillEvidenceRewrite, SkillEvidenceRepeat, SkillEvidenceCall:
		return k, true
	}
	return "", false
}

// SkillProposalOp is what a skill proposal would do to the library.
type SkillProposalOp string

const (
	SkillProposalCreate SkillProposalOp = "create"
	SkillProposalPatch  SkillProposalOp = "patch"
)

func (o SkillProposalOp) String() string { return string(o) }

func ParseSkillProposalOp(v string) (SkillProposalOp, bool) {
	switch o := SkillProposalOp(v); o {
	case SkillProposalCreate, SkillProposalPatch:
		return o, true
	}
	return "", false
}

// SkillProposalStatus is where a skill proposal stands with the owner.
type SkillProposalStatus string

const (
	SkillProposalPending   SkillProposalStatus = "pending"
	SkillProposalAccepted  SkillProposalStatus = "accepted"
	SkillProposalDismissed SkillProposalStatus = "dismissed"
)

func (s SkillProposalStatus) String() string { return string(s) }

func ParseSkillProposalStatus(v string) (SkillProposalStatus, bool) {
	switch s := SkillProposalStatus(v); s {
	case SkillProposalPending, SkillProposalAccepted, SkillProposalDismissed:
		return s, true
	}
	return "", false
}

// SkillLevel is who a learned skill answers to: the loop, or the owner.
type SkillLevel string

const (
	SkillLevelUser  SkillLevel = "user"
	SkillLevelAgent SkillLevel = "agent"
)

func (l SkillLevel) String() string { return string(l) }

func ParseSkillLevel(v string) (SkillLevel, bool) {
	switch l := SkillLevel(v); l {
	case SkillLevelUser, SkillLevelAgent:
		return l, true
	}
	return "", false
}

// SkillRoot names where a skill lives. Learned skills are written only to the
// nusashell root; the other roots are read-only sources.
type SkillRoot string

const (
	SkillRootBuiltin   SkillRoot = "builtin"
	SkillRootNusashell SkillRoot = "nusashell"
	SkillRootGlobal    SkillRoot = "global"
	SkillRootWorkspace SkillRoot = "workspace"
)

func (r SkillRoot) String() string { return string(r) }

func ParseSkillRoot(v string) (SkillRoot, bool) {
	switch r := SkillRoot(v); r {
	case SkillRootBuiltin, SkillRootNusashell, SkillRootGlobal, SkillRootWorkspace:
		return r, true
	}
	return "", false
}
