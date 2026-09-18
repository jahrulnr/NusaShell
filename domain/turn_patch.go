package domain

// TurnPatchStorage persists the net unified diff of one agent turn's committed
// file_* mutations, so the patch history of a conversation survives restarts
// and can be reviewed (or re-applied) from the data directory. Implementations
// write one patch per turn and treat an empty diff as "nothing to record".
type TurnPatchStorage interface {
	// Save writes the patch for one turn of a conversation, replacing any
	// previous patch for the same run. Unsafe IDs must be rejected.
	Save(conversationID, runID, patch string) error
}
