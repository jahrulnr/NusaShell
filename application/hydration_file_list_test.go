package application

import (
	"errors"
	"strings"
	"testing"
)

var errFakeListing = errors.New("fake listing failure")

// TestHydrationFileListRealTool pins the workspace-map slot: when a
// workspace is set, the hydration checkpoint includes a REAL file_list call
// against the workspace root, attached verbatim, positioned right after the
// AGENTS.md file_read so the project map leads before memory and catalogs.
func TestHydrationFileListRealTool(t *testing.T) {
	agentsOut := "---\nbytes: 42\n---\n\n# Project rules\nUse Go, keep it simple."
	listingOut := "---\nbytes: 120\ncount: 3\ntotal: 1.2 KB\n---\n" +
		"drwxr-xr-x application\n" +
		"-rw-r--r-- go.mod\n" +
		"-rw-r--r-- README.md\n"
	exec := &stubHydrationExecutor{fn: func(name string, _ []byte) (string, error) {
		switch name {
		case "file_read":
			return agentsOut, nil
		case "file_list":
			return listingOut, nil
		default:
			return "", nil
		}
	}}
	b := NewHydrationBuilder(HydrationSource{
		Executor:       exec,
		RuntimeContext: RuntimeContextSnapshot{Workspace: "/ws/proj"},
	})
	result := b.Build()

	// file_list is the third call: runtime_context, AGENTS.md file_read,
	// then the workspace listing.
	if len(result.Messages[0].ToolCalls) < 3 {
		t.Fatalf("expected at least 3 hydration calls, got %d", len(result.Messages[0].ToolCalls))
	}
	call := result.Messages[0].ToolCalls[2]
	if call.Name != "file_list" {
		t.Fatalf("third hydration call = %s, want file_list", call.Name)
	}
	if call.Args != `{"path":"/ws/proj"}` {
		t.Fatalf("file_list args = %s, want workspace root", call.Args)
	}
	slot := findHydrationTool(result.Messages, "file_list")
	if slot == nil {
		t.Fatal("file_list hydration slot missing")
	}
	// Verbatim real tool output.
	if slot.Content != listingOut {
		t.Fatalf("file_list content not verbatim:\n got %q\nwant %q", slot.Content, listingOut)
	}
}

// TestHydrationFileListHiddenWithoutWorkspace pins the fail-soft rules: no
// executor, no workspace, a failing listing, or an empty directory all hide
// the slot (dynamic hydration — no stubs).
func TestHydrationFileListHiddenWithoutWorkspace(t *testing.T) {
	// No executor → hidden even with a workspace.
	result := NewHydrationBuilder(HydrationSource{
		RuntimeContext: RuntimeContextSnapshot{Workspace: "/ws/proj"},
	}).Build()
	for _, c := range result.Messages[0].ToolCalls {
		if c.Name == "file_list" {
			t.Fatal("file_list must be hidden without an executor")
		}
	}

	// No workspace → hidden even with an executor.
	exec := &stubHydrationExecutor{fn: func(name string, _ []byte) (string, error) {
		if name == "file_list" {
			return "---\nbytes: 3\ncount: 0\n---\n<listing>", nil
		}
		return "", nil
	}}
	result = NewHydrationBuilder(HydrationSource{
		Executor: exec,
	}).Build()
	for _, c := range result.Messages[0].ToolCalls {
		if c.Name == "file_list" {
			t.Fatal("file_list must be hidden without a workspace")
		}
	}

	// Failing listing → hidden.
	failing := &stubHydrationExecutor{fn: func(name string, _ []byte) (string, error) {
		if name == "file_list" {
			return "", errFakeListing
		}
		return "", nil
	}}
	result = NewHydrationBuilder(HydrationSource{
		Executor:       failing,
		RuntimeContext: RuntimeContextSnapshot{Workspace: "/ws/proj"},
	}).Build()
	assertNoFileListSlot(t, result, "failing listing")

	// Empty directory (meta block, no body) → hidden.
	empty := &stubHydrationExecutor{fn: func(name string, _ []byte) (string, error) {
		if name == "file_list" {
			return "---\nbytes: 21\ncount: 0\ntotal: 0 B\n---\n", nil
		}
		return "", nil
	}}
	result = NewHydrationBuilder(HydrationSource{
		Executor:       empty,
		RuntimeContext: RuntimeContextSnapshot{Workspace: "/ws/proj"},
	}).Build()
	assertNoFileListSlot(t, result, "empty listing")

	// Oversized listing (e.g. a home-directory workspace root) → hidden,
	// so the prompt never carries a multi-thousand-entry listing.
	bigListing := "---\nbytes: 10000\ncount: 900\ntotal: 1.2 GB\n---\n" +
		strings.Repeat("-rw-r--r-- some-entry.txt\n", 700)
	if len(bigListing) <= hydrationFileListMaxBytes {
		t.Fatalf("test fixture too small: %d bytes", len(bigListing))
	}
	oversized := &stubHydrationExecutor{fn: func(name string, _ []byte) (string, error) {
		if name == "file_list" {
			return bigListing, nil
		}
		return "", nil
	}}
	result = NewHydrationBuilder(HydrationSource{
		Executor:       oversized,
		RuntimeContext: RuntimeContextSnapshot{Workspace: "/ws/proj"},
	}).Build()
	assertNoFileListSlot(t, result, "oversized listing")
}

func assertNoFileListSlot(t *testing.T, result HydrationResult, why string) {
	t.Helper()
	for _, c := range result.Messages[0].ToolCalls {
		if c.Name == "file_list" {
			t.Fatalf("file_list must be hidden for %s", why)
		}
	}
	if slot := findHydrationTool(result.Messages, "file_list"); slot != nil {
		t.Fatalf("file_list result must be hidden for %s", why)
	}
}
