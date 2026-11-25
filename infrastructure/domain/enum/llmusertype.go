package enum

// LLMUserType marks the provenance of a user-role message: origin is real
// end-user input, system is a system-generated payload carried by a user
// message (e.g. the instruction a fold is written with), compaction is the
// checkpoint that fold leaves behind (a system payload too, but one the UI
// draws as the boundary between sealed history and the live tail), relay is a
// prompt another thread sent into this room — user content the owner did not
// type here, so the room never offers to edit it.
type LLMUserType string

const (
	// origin for real user message
	LLMUserTypeOrigin LLMUserType = "origin"
	// system for system message (example: compaction need placed at user message)
	LLMUserTypeSystem LLMUserType = "system"
	// compaction marks the checkpoint a fold writes: the head of the live tail,
	// holding the handoff for everything sealed before it.
	LLMUserTypeCompaction LLMUserType = "compaction"
	// relay for a prompt delivered by another thread through the conversation
	// tool: it reads as owner input to the model, but the owner cannot rewrite
	// another room's words from this room.
	LLMUserTypeRelay LLMUserType = "relay"
)

func (t LLMUserType) String() string {
	return string(t)
}

// SystemPayload reports whether the type is a payload the server wrote into a
// user-role message rather than words the owner typed. Every such message is
// handled alike — counted as no owner turn, never editable — and the types
// differ only on the wire, where a checkpoint's boundary is drawn and a plain
// note's is not.
func (t LLMUserType) SystemPayload() bool {
	return t == LLMUserTypeSystem || t == LLMUserTypeCompaction
}

func ParseLLMUserType(v string) (LLMUserType, bool) {
	switch t := LLMUserType(v); t {
	case LLMUserTypeOrigin, LLMUserTypeSystem, LLMUserTypeCompaction, LLMUserTypeRelay:
		return t, true
	}
	return "", false
}
