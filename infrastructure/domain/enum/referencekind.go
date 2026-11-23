package enum

// ReferenceKind names what a selected reference in a message points at.
type ReferenceKind string

const (
	ReferenceFile         ReferenceKind = "file"
	ReferenceFolder       ReferenceKind = "folder"
	ReferenceSkill        ReferenceKind = "skill"
	ReferenceMCP          ReferenceKind = "mcp"
	ReferenceCommand      ReferenceKind = "command"
	ReferenceConversation ReferenceKind = "conversation"
	ReferenceProject      ReferenceKind = "project"
)

func (k ReferenceKind) String() string { return string(k) }

func ParseReferenceKind(v string) (ReferenceKind, bool) {
	switch k := ReferenceKind(v); k {
	case ReferenceFile, ReferenceFolder, ReferenceSkill, ReferenceMCP, ReferenceCommand, ReferenceConversation, ReferenceProject:
		return k, true
	}
	return "", false
}
