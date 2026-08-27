package domain

// MutationClass classifies how a tool interacts with the filesystem.
// The journal uses this to decide what observation strategy to apply:
// declared tools capture specific paths, opaque tools require a full
// workspace listing diff, and unobserved tools are recorded as events
// whose effects are detected lazily on the next touch.
type MutationClass string

const (
	// MutationNone means the tool is not expected to modify the filesystem.
	// Examples: file_read, file_list, grep, web_search.
	MutationNone MutationClass = "none"

	// MutationDeclared means the tool knows exactly which paths it may
	// modify. The journal captures the pre-image of each declared path
	// before execution and records the after-state.
	// Examples: file_write, file_patch, file_delete, file_move, file_copy.
	MutationDeclared MutationClass = "declared"

	// MutationOpaque means the tool may modify arbitrary workspace files.
	// The journal takes a workspace listing before and after execution and
	// records every dirty file. Example: exec.
	MutationOpaque MutationClass = "opaque"

	// MutationUnobserved means the tool may modify files but the runtime
	// cannot observe which ones (e.g. MCP plugin tools). The event is
	// recorded; effects are detected when a watched file is next touched
	// and its hash does not match the expected baseline.
	MutationUnobserved MutationClass = "unobserved"
)

// ChangeKind describes what happened to a single filesystem path.
type ChangeKind string

const (
	ChangeAdded    ChangeKind = "added"
	ChangeModified ChangeKind = "modified"
	ChangeDeleted  ChangeKind = "deleted"
)

// ChangeOrigin attributes a change to its cause.
type ChangeOrigin string

const (
	// OriginAgent means the change is attributed to a specific tool event.
	OriginAgent ChangeOrigin = "agent"

	// OriginUnobserved means the change was detected but the causing tool
	// could not observe it (e.g. an mcp_call gap). The suspect event ID
	// is recorded in the change.
	OriginUnobserved ChangeOrigin = "unobserved"

	// OriginExternal means the change was detected outside any tool event
	// (e.g. the user edited a file while the agent was idle).
	OriginExternal ChangeOrigin = "external"
)

// FileChange is one observed filesystem effect recorded by the journal.
type FileChange struct {
	// Path is the absolute filesystem path of the changed file.
	Path string `json:"path"`

	// Kind is added, modified, or deleted.
	Kind ChangeKind `json:"kind"`

	// Origin attributes the change to its cause.
	Origin ChangeOrigin `json:"origin"`

	// BeforeHash is the content hash before the change. Empty means the
	// file did not exist before (added).
	BeforeHash string `json:"beforeHash,omitempty"`

	// AfterHash is the content hash after the change. Empty means the file
	// was deleted.
	AfterHash string `json:"afterHash,omitempty"`

	// BeforeSize is the file size in bytes before the change.
	BeforeSize int64 `json:"beforeSize,omitempty"`

	// AfterSize is the file size in bytes after the change.
	AfterSize int64 `json:"afterSize,omitempty"`

	// EventID is the tool event that caused this change. Empty for
	// external changes.
	EventID string `json:"eventId,omitempty"`
}
