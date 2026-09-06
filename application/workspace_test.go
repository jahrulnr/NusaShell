package application

import (
	"context"
	"io/fs"
	"testing"

	"nusashell/contracts"
)

func TestEffectiveWorkspaceFallsBackToDefault(t *testing.T) {
	app := &App{defaultWorkspace: "/home/tuan"}
	if got := app.effectiveWorkspace(""); got != "/home/tuan" {
		t.Fatalf("empty workspace default = %q, want /home/tuan", got)
	}
	if got := app.effectiveWorkspace("   "); got != "/home/tuan" {
		t.Fatalf("blank workspace default = %q, want /home/tuan", got)
	}
	if got := app.effectiveWorkspace("/projects/x"); got != "/projects/x" {
		t.Fatalf("explicit workspace = %q, want /projects/x", got)
	}
	if app := (&App{}); app.effectiveWorkspace("") != "" {
		t.Fatal("without a configured default the effective workspace must stay empty")
	}
}

func TestHandleToolContractsAdvertisesMemoryProjectWithDefaultWorkspace(t *testing.T) {
	box := &factoryStubToolbox{tools: []ToolInfo{
		{Name: "memory_project", InputSchema: map[string]any{"type": "object"}},
	}}
	a := &App{Toolbox: box, defaultWorkspace: "/home/tuan"}

	result, rpcErr := a.handleToolContracts(contracts.ToolContractsRequest{Workspace: ""})
	if rpcErr != nil {
		t.Fatalf("handleToolContracts: %v", rpcErr)
	}
	got := result.(contracts.ToolContractsResult)
	if _, ok := findToolContract(got.Tools, "memory_project"); !ok {
		t.Fatal("memory_project must be advertised when the default workspace is active")
	}
}

// fakeDirBrowser is the in-memory DirectoryBrowser used by workspace tests.
// Both methods default to success so notice/validation tests only override
// what they care about.
type fakeDirBrowser struct {
	list   func(ctx context.Context, path string) (DirListing, error)
	ensure func(ctx context.Context, path string) error
}

func (f fakeDirBrowser) ListDirs(ctx context.Context, path string) (DirListing, error) {
	if f.list != nil {
		return f.list(ctx, path)
	}
	return DirListing{Entries: []contracts.WorkspaceDirEntry{}}, nil
}

func (f fakeDirBrowser) EnsureDir(ctx context.Context, path string) error {
	if f.ensure != nil {
		return f.ensure(ctx, path)
	}
	return nil
}

func TestHandleWorkspaceListDirsReturnsResolvedListing(t *testing.T) {
	app := &App{
		Logs: &fakeLogStore{},
		Bus:  NewBus(),
		DirectoryBrowser: fakeDirBrowser{list: func(_ context.Context, path string) (DirListing, error) {
			return DirListing{
				Path:   "/home/tuan/projects",
				Parent: "/home/tuan",
				Entries: []contracts.WorkspaceDirEntry{
					{Name: "alpha", Path: "/home/tuan/projects/alpha"},
					{Name: "beta", Path: "/home/tuan/projects/beta"},
				},
				Truncated: false,
			}, nil
		}},
	}

	resp, rpcErr := app.handleWorkspaceListDirs(contracts.WorkspaceListDirsRequest{Path: ""})
	if rpcErr != nil {
		t.Fatalf("list dirs: %v", rpcErr)
	}
	got, ok := resp.(contracts.WorkspaceListDirsResult)
	if !ok {
		t.Fatalf("resp = %T, want WorkspaceListDirsResult", resp)
	}
	if got.Path != "/home/tuan/projects" || got.Parent != "/home/tuan" {
		t.Fatalf("path/parent = %q/%q", got.Path, got.Parent)
	}
	if len(got.Entries) != 2 || got.Entries[0].Name != "alpha" {
		t.Fatalf("entries = %+v", got.Entries)
	}
	if got.Truncated {
		t.Fatal("truncated must be false")
	}
}

func TestHandleWorkspaceListDirsRejectsRelativePath(t *testing.T) {
	app := &App{Logs: &fakeLogStore{}, Bus: NewBus(), DirectoryBrowser: fakeDirBrowser{}}

	_, rpcErr := app.handleWorkspaceListDirs(contracts.WorkspaceListDirsRequest{Path: "relative/dir"})
	if rpcErr == nil || rpcErr.Code != contracts.CodeValidation {
		t.Fatalf("want VALIDATION_ERROR for relative path, got %+v", rpcErr)
	}
}

func TestHandleWorkspaceListDirsMapsNotExistToValidation(t *testing.T) {
	app := &App{
		Logs: &fakeLogStore{},
		Bus:  NewBus(),
		DirectoryBrowser: fakeDirBrowser{list: func(context.Context, string) (DirListing, error) {
			return DirListing{}, fs.ErrNotExist
		}},
	}

	_, rpcErr := app.handleWorkspaceListDirs(contracts.WorkspaceListDirsRequest{Path: "/gone"})
	if rpcErr == nil || rpcErr.Code != contracts.CodeValidation {
		t.Fatalf("want VALIDATION_ERROR for missing dir, got %+v", rpcErr)
	}
}

func TestHandleWorkspaceListDirsMapsPermissionToValidation(t *testing.T) {
	app := &App{
		Logs: &fakeLogStore{},
		Bus:  NewBus(),
		DirectoryBrowser: fakeDirBrowser{list: func(context.Context, string) (DirListing, error) {
			return DirListing{}, fs.ErrPermission
		}},
	}

	_, rpcErr := app.handleWorkspaceListDirs(contracts.WorkspaceListDirsRequest{Path: "/root"})
	if rpcErr == nil || rpcErr.Code != contracts.CodeValidation {
		t.Fatalf("want VALIDATION_ERROR for unreadable dir, got %+v", rpcErr)
	}
}

func TestHandleWorkspaceListDirsNilBrowserUnavailable(t *testing.T) {
	app := &App{Logs: &fakeLogStore{}, Bus: NewBus()}

	_, rpcErr := app.handleWorkspaceListDirs(contracts.WorkspaceListDirsRequest{Path: "/x"})
	if rpcErr == nil || rpcErr.Code != contracts.CodeValidation {
		t.Fatalf("want VALIDATION_ERROR when browser unavailable, got %+v", rpcErr)
	}
}

func TestHandleWorkspaceListDirsCoercesNilEntriesToEmpty(t *testing.T) {
	path := t.TempDir()
	app := &App{
		Logs: &fakeLogStore{},
		Bus:  NewBus(),
		DirectoryBrowser: fakeDirBrowser{list: func(context.Context, string) (DirListing, error) {
			return DirListing{Path: path}, nil
		}},
	}

	resp, rpcErr := app.handleWorkspaceListDirs(contracts.WorkspaceListDirsRequest{Path: path})
	if rpcErr != nil {
		t.Fatalf("list dirs: %v", rpcErr)
	}
	got := resp.(contracts.WorkspaceListDirsResult)
	if got.Entries == nil {
		t.Fatal("entries must marshal as [] not null")
	}
}
