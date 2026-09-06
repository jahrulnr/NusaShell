package conversation

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	"nusashell/contracts"
	"nusashell/pkg/rpcdispatch"
)

// Effective returns the conversation workspace when set, and defaultWorkspace
// otherwise. Every consumer that reads a conversation workspace must go
// through this helper so tools, hydration, and advertisement agree on one
// active path — never "." or an empty string unless the default is empty.
func Effective(workspace, defaultWorkspace string) string {
	workspace = strings.TrimSpace(workspace)
	if workspace != "" {
		return workspace
	}
	return defaultWorkspace
}

// HandleWorkspaceListDirs serves the in-app workspace folder browser. An
// empty path means the host home directory; the adapter resolves it and the
// handler returns the resolved path so the frontend breadcrumb stays in sync
// with what the server actually listed.
func (s *Service) HandleWorkspaceListDirs(req contracts.WorkspaceListDirsRequest) (any, *contracts.RPCError) {
	if s.browser == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "workspace folder browser is unavailable"}
	}
	path := strings.TrimSpace(req.Path)
	if path != "" && !filepath.IsAbs(path) {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "workspace path must be absolute"}
	}
	listing, err := s.browser.ListDirs(context.Background(), path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "directory does not exist"}
		}
		if errors.Is(err, fs.ErrPermission) {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "permission denied reading directory"}
		}
		return nil, rpcdispatch.Internal(err)
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
