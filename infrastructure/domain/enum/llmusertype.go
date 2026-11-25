package enum

// LLMUserType marks the provenance of a user-role message: origin is real
// end-user input, system is a system-generated payload carried by a user
// message (e.g. compaction output), relay is a prompt another thread sent into
// this room — user content the owner did not type here, so the room never
// offers to edit it.
type LLMUserType string

const (
	// origin for real user message
	LLMUserTypeOrigin LLMUserType = "origin"
	// system for system message (example: compaction need placed at user message)
	LLMUserTypeSystem LLMUserType = "system"
	// relay for a prompt delivered by another thread through the conversation
	// tool: it reads as owner input to the model, but the owner cannot rewrite
	// another room's words from this room.
	LLMUserTypeRelay LLMUserType = "relay"
)

func (t LLMUserType) String() string {
	return string(t)
}

func ParseLLMUserType(v string) (LLMUserType, bool) {
	switch t := LLMUserType(v); t {
	case LLMUserTypeOrigin, LLMUserTypeSystem, LLMUserTypeRelay:
		return t, true
	}
	return "", false
}
