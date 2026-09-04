package turndiff

// FileChangeKind is the committed apply-patch change shape Codex tracks.
type FileChangeKind int

const (
	// ChangeAdd creates or overwrites a path with Content.
	ChangeAdd FileChangeKind = iota
	// ChangeDelete removes a path whose pre-image is Content.
	ChangeDelete
	// ChangeUpdate replaces OldContent with NewContent, optionally renaming
	// via MovePath.
	ChangeUpdate
)

// FileChange is one committed textual mutation, in application order.
type FileChange struct {
	Path string
	Kind FileChangeKind

	// Content is the new file body for Add and the deleted body for Delete.
	Content string
	// OverwrittenContent is the pre-image when Add replaced an existing file.
	OverwrittenContent *string

	MovePath               *string
	OldContent             string
	OverwrittenMoveContent *string
	NewContent             string
}

// Delta is the set of committed file changes from one tool call.
// Exact=false means the pre/post images could not be captured faithfully
// (binary, directory, unreadable, partial failure) and the turn tracker
// must invalidate.
type Delta struct {
	Exact   bool
	Changes []FileChange
}

// Inexact returns a delta that invalidates the turn tracker.
func Inexact() Delta {
	return Delta{}
}

// AddFile records creating or overwriting path with content.
func AddFile(path, content string, overwritten *string) Delta {
	return Delta{Exact: true, Changes: []FileChange{AddChange(path, content, overwritten)}}
}

// DeleteFile records deleting path whose pre-image is content.
func DeleteFile(path, content string) Delta {
	return Delta{Exact: true, Changes: []FileChange{DeleteChange(path, content)}}
}

// UpdateFile records a content change, optionally renaming to movePath.
func UpdateFile(path, oldContent, newContent string, movePath *string, overwrittenMove *string) Delta {
	return Delta{Exact: true, Changes: []FileChange{UpdateChange(path, oldContent, newContent, movePath, overwrittenMove)}}
}

// ExactChanges wraps an ordered list of committed changes.
func ExactChanges(changes ...FileChange) Delta {
	return Delta{Exact: true, Changes: changes}
}

// AddChange constructs an Add file change.
func AddChange(path, content string, overwritten *string) FileChange {
	return FileChange{Path: path, Kind: ChangeAdd, Content: content, OverwrittenContent: overwritten}
}

// DeleteChange constructs a Delete file change.
func DeleteChange(path, content string) FileChange {
	return FileChange{Path: path, Kind: ChangeDelete, Content: content}
}

// UpdateChange constructs an Update file change.
func UpdateChange(path, oldContent, newContent string, movePath *string, overwrittenMove *string) FileChange {
	return FileChange{
		Path:                   path,
		Kind:                   ChangeUpdate,
		MovePath:               movePath,
		OldContent:             oldContent,
		OverwrittenMoveContent: overwrittenMove,
		NewContent:             newContent,
	}
}

// StringPtr returns a pointer to s for optional delta fields.
func StringPtr(s string) *string {
	return &s
}
