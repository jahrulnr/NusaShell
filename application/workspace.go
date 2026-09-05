package application

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	"nusashell/contracts"
)

// effectiveWorkspace returns the conversation workspace when set, and the
// process default (host home dir, wired in Deps.DefaultWorkspace) otherwise.
// Every consumer that reads a conversation workspace must go through this
// helper so tools, hydration, and advertisement agree on one active path —
// never "." or an empty string.
func (a *App) effectiveWorkspace(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if workspace != "" {
		return workspace
	}
	return a.defaultWorkspace
}

// handleWorkspaceListDirs serves the in-app workspace folder browser. An
// empty path means the host home directory; the adapter resolves it and the
// handler returns the resolved path so the frontend breadcrumb stays in sync
// with what the server actually listed.
func (a *App) handleWorkspaceListDirs(req contracts.WorkspaceListDirsRequest) (any, *contracts.RPCError) {
	if a.DirectoryBrowser == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "workspace folder browser is unavailable"}
	}
	path := strings.TrimSpace(req.Path)
	if path != "" && !filepath.IsAbs(path) {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "workspace path must be absolute"}
	}
	listing, err := a.DirectoryBrowser.ListDirs(context.Background(), path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "directory does not exist"}
		}
		if errors.Is(err, fs.ErrPermission) {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "permission denied reading directory"}
		}
		return nil, rpcInternal(err)
	}
	if listing.Entries == nil {
		listing.Entries = []contracts.WorkspaceDirEntry{}
	}
	return contracts.WorkspaceListDirsResult{
		Path:      listing.Path,
		Parent:    listing.Parent,
		Entries:   listing.Entries,
		Truncated: listing.Truncated,
	}, nil
}
