package application

import (
	"context"
	"encoding/json"

	"nusashell/domain"
)

// ChangeJournal records workspace file changes caused by agent tool calls so
// the agent retains an authoritative, compaction-proof history of what it
// changed. It is the causal layer that Git does not provide: Git answers
// "what is different between commits", the journal answers "which tool call
// in which turn caused this file change, and what was the file before".
//
// The journal is an optional port. When nil, the agent loop skips all
// journaling and behavior is unchanged. Implementations live in
// infrastructure/journal and are wired in the composition root.
type ChangeJournal interface {
	// WrapMutation wraps one mutating tool execution. It captures the
	// before-state, runs exec, captures the after-state, and records the
	// observed changes. The observation strategy depends on req.Class:
	//   - declared: capture the pre-image of each path in req.Paths.
	//   - opaque:   list the workspace under req.WorkspaceRoot before and
	//     after, recording every dirty file.
	//   - unobserved: record the event only; effects are detected lazily
	//     when a watched file is next touched.
	// exec is the actual tool execution. WrapMutation must call it exactly
	// once and return its error unchanged (journaling failures are logged,
	// never surfaced to the model).
	WrapMutation(ctx context.Context, req MutationRequest, exec func() error) error

	// SessionState returns the accumulated workspace change state for a
	// conversation, used to build the post-compaction hydration slot. It
	// reports the absolute paths that were changed/added/deleted (with kind
	// and origin) plus the journal sidecar location, so a fresh context knows
	// which files the session touched without the prior conversation. It does
	// not render file contents or diffs; the journal sidecar remains the
	// authoritative restore source.
	SessionState(ctx context.Context, conversationID, workspaceRoot string) (*WorkspaceState, error)
}

// MutationRequest describes one mutating tool invocation for the journal.
// All paths are absolute. ConversationID/RunID/ToolCallID identify the causal
// chain (session → turn → tool call).
type MutationRequest struct {
	ConversationID string
	RunID          string
	ToolCallID     string
	ToolName       string
	Class          domain.MutationClass

	// WorkspaceRoot is the absolute directory an opaque mutation lists
	// (the conversation workspace, or the exec cwd when provided).
	WorkspaceRoot string

	// Cwd is the exec tool's working directory argument when set
	// (absolute). When non-empty it takes precedence over WorkspaceRoot
	// as the opaque listing root.
	Cwd string

	// Paths are the absolute paths a declared mutation may touch.
	Paths []string

	// Command is the shell command for opaque exec mutations.
	Command string
}

// WorkspaceState is the journal's view of one conversation's workspace
// changes, ready for the hydration slot.
type WorkspaceState struct {
	ConversationID string
	WorkspaceRoot  string

	// Changes is the accumulated file changes for the session. Each entry
	// carries the absolute path, kind (added/modified/deleted), origin, and
	// size/hash metadata — enough for the agent to know what was touched
	// without re-reading file contents.
	Changes []domain.FileChange

	// JournalPath is the absolute path of the conversation's journal sidecar
	// directory (the authoritative restore source). Empty when the sidecar
	// path cannot be resolved.
	JournalPath string
}

// ClassifyMutation maps a tool call to its mutation class and extracts the
// journal-relevant fields (declared paths, exec command). It is a pure
// function with no I/O. Callers fill ConversationID/RunID/ToolCallID and
// WorkspaceRoot on the returned request.
//
// Classification is intentionally conservative: anything not explicitly known
// to mutate is MutationNone, and mcp_call is MutationUnobserved because the
// runtime cannot see which files a plugin tool touches.
func ClassifyMutation(toolName string, argsJSON []byte) MutationRequest {
	req := MutationRequest{ToolName: toolName, Class: domain.MutationNone}

	switch toolName {
	case "exec":
		req.Class = domain.MutationOpaque
		var args struct {
			Command string `json:"command"`
			Cwd     string `json:"cwd"`
		}
		if json.Unmarshal(argsJSON, &args) == nil {
			req.Command = args.Command
			req.Cwd = args.Cwd
		}

	case "mcp_call":
		req.Class = domain.MutationUnobserved

	case "file_write", "file_patch", "file_delete", "file_mkdir":
		req.Class = domain.MutationDeclared
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(argsJSON, &args) == nil && args.Path != "" {
			req.Paths = []string{args.Path}
		}

	case "file_move", "file_copy":
		req.Class = domain.MutationDeclared
		var args struct {
			Source      string `json:"source"`
			Destination string `json:"destination"`
		}
		if json.Unmarshal(argsJSON, &args) == nil {
			if args.Source != "" {
				req.Paths = append(req.Paths, args.Source)
			}
			if args.Destination != "" {
				req.Paths = append(req.Paths, args.Destination)
			}
		}
	}

	return req
}
