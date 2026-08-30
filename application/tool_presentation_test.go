package application

import (
	"testing"

	"nusashell/domain"
)

func TestBuildToolPresentationExtractsCompactBuiltInResult(t *testing.T) {
	raw := "---\ncount: 2\ntotal: 109K\n---\n-rw-r--r-- 4K Aug 30 15:36 file-a\n-rw-r--r-- 8K Aug 30 15:38 file with spaces-b"
	got := buildToolPresentation("file_list", `{"path":"/workspace"}`, domain.ToolOK, raw)
	if got == nil {
		t.Fatal("presentation is nil")
	}
	if got.Variant != "file-list" || got.Action != "Files listed" {
		t.Fatalf("presentation header = %+v", got)
	}
	if got.Request != "file_list({\n  \"path\": \"/workspace\"\n})" {
		t.Errorf("request = %q", got.Request)
	}
	if got.Result.Format != "list" || got.Result.Summary != "2 entries · 109K" {
		t.Fatalf("result summary = %+v", got.Result)
	}
	if got.Result.Meta["count"] != 2 {
		t.Errorf("result meta = %+v", got.Result.Meta)
	}
	if got.Result.Text != "" || len(got.Result.Items) != 2 {
		t.Errorf("result items/text = %+v / %q", got.Result.Items, got.Result.Text)
	}
	if got.Result.Items[1]["name"] != "file with spaces-b" {
		t.Errorf("file-list item = %+v", got.Result.Items[1])
	}
}

func TestBuildToolPresentationTurnsBuiltInJSONLIntoItems(t *testing.T) {
	raw := "---\ncount: 2\n---\n{\"id\":\"m1\",\"content\":\"one\"}\n{\"id\":\"m2\",\"content\":\"two\"}"
	got := buildToolPresentation("memory", `{"op":"search","query":"one"}`, domain.ToolOK, raw)
	if got.Variant != "collection" || got.Result.Format != "list" {
		t.Fatalf("presentation = %+v", got)
	}
	if len(got.Result.Items) != 2 || got.Result.Items[0]["id"] != "m1" {
		t.Fatalf("items = %+v", got.Result.Items)
	}
	if got.Result.Text != "" {
		t.Errorf("JSONL body should move to items, got text %q", got.Result.Text)
	}
}

func TestBuildToolPresentationKeepsGenericToolOutputAsText(t *testing.T) {
	raw := "---\nexit_code: 0\n---\nhello\nworld"
	got := buildToolPresentation("exec", `{"command":"printf hello"}`, domain.ToolOK, raw)
	if got.Variant != "terminal" || got.Result.Format != "terminal" {
		t.Fatalf("presentation = %+v", got)
	}
	if got.Result.Text != raw {
		t.Fatalf("generic output changed: got %q, want %q", got.Result.Text, raw)
	}
}

func TestBuildToolPresentationShowsDirectMCPArgumentsInRequest(t *testing.T) {
	got := buildToolPresentation("mcp_call", `{"ref":"files:read","arguments_json":{"path":"/tmp/a b.txt"}}`, domain.ToolOK, "result")
	want := "mcp_call(files:read) {\n  \"path\": \"/tmp/a b.txt\"\n}"
	if got.Request != want {
		t.Fatalf("mcp request = %q, want %q", got.Request, want)
	}
	if got.Result.Text != "result" {
		t.Fatalf("mcp output = %q", got.Result.Text)
	}
}

func TestBuildToolPresentationParsesPredictableSearchRows(t *testing.T) {
	raw := "---\nline_matches: 2\nvia: go\n---\napplication/main.go:12:match\napplication/main.go-11-context"
	got := buildToolPresentation("grep", `{"pattern":"match","path":"application"}`, domain.ToolOK, raw)
	if got.Variant != "search-results" || got.Result.Format != "list" {
		t.Fatalf("presentation = %+v", got)
	}
	if len(got.Result.Items) != 2 {
		t.Fatalf("grep items = %+v", got.Result.Items)
	}
	if got.Result.Items[0]["line"] != 12 || got.Result.Items[0]["match"] != true {
		t.Errorf("match row = %+v", got.Result.Items[0])
	}
	if got.Result.Items[1]["line"] != 11 || got.Result.Items[1]["match"] != nil {
		t.Errorf("context row = %+v", got.Result.Items[1])
	}
}

func TestBuildToolPresentationParsesYAMLCollectionAndKeepsProviderRawPath(t *testing.T) {
	raw := "---\ncount: 1\n---\n- id: wf_1\n  name: Nightly\n  enabled: true"
	got := buildToolPresentation("automation_list", "{}", domain.ToolOK, raw)
	if got.Variant != "collection" || got.Result.Format != "list" || len(got.Result.Items) != 1 {
		t.Fatalf("automation presentation = %+v", got)
	}
	if got.Result.Items[0]["id"] != "wf_1" {
		t.Errorf("automation item = %+v", got.Result.Items[0])
	}
	if got.Result.Text != "" {
		t.Errorf("collection body should move to items, got %q", got.Result.Text)
	}

	terminal := buildToolPresentation("mcp_call", `{"ref":"plugin:tool","arguments_json":"{\"path\":\"/tmp\"}"}`, domain.ToolOK, raw)
	if terminal.Variant != "terminal" || terminal.Result.Text != raw {
		t.Fatalf("mcp call must stay raw terminal: %+v", terminal)
	}
}

func TestToolResultPresentationStatusReadsStructuredFailure(t *testing.T) {
	status := toolResultPresentationStatus("---\nstatus: failed\n---\nprovider rejected request")
	if status != domain.ToolFailed {
		t.Fatalf("status = %q, want fail", status)
	}
}

func TestBuildToolPresentationUsesFrontmatterFilesForFindFile(t *testing.T) {
	raw := "---\ncount: 2\nfiles:\n  - /workspace/a.go\n  - /workspace/b.go\n---"
	got := buildToolPresentation("find_file", `{"pattern":"**/*.go","path":"/workspace"}`, domain.ToolOK, raw)
	if got.Result.Format != "list" || got.Result.Summary != "2 files" {
		t.Fatalf("presentation = %+v", got)
	}
	if len(got.Result.Items) != 2 || got.Result.Items[0]["path"] != "/workspace/a.go" {
		t.Fatalf("frontmatter files = %+v", got.Result.Items)
	}
	if got.Result.Text != "" {
		t.Errorf("frontmatter list should not retain body text: %q", got.Result.Text)
	}
}

func TestBuildToolPresentationSummarizesStatusMetadata(t *testing.T) {
	got := buildToolPresentation("file_write", `{"path":"/workspace/a.txt"}`, domain.ToolOK, "---\nbytes: 12\nwritten: true\nsha256: abcdef0123456789\n---")
	if got.Variant != "status" || got.Result.Format != "status" || got.Result.Summary != "Written" {
		t.Fatalf("status presentation = %+v", got)
	}
	if got.Result.Meta["written"] != true || got.Result.Meta["bytes"] != 12 {
		t.Fatalf("status metadata = %+v", got.Result.Meta)
	}
}

func TestBuildToolPresentationUsesOperationSummaries(t *testing.T) {
	read := buildToolPresentation("file_read", `{"path":"/workspace/a.go"}`, domain.ToolOK, "---\nbytes: 128\ntruncated: true\n---\npackage main")
	if read.Result.Summary != "Read 128 bytes · more available" {
		t.Fatalf("file read summary = %q", read.Result.Summary)
	}

	grep := buildToolPresentation("grep", `{"pattern":"x","path":"."}`, domain.ToolOK, "---\nfiles: 2\ntotal_line_matches: 5\n---\na.go:2\nb.go:3")
	if grep.Result.Summary != "5 matches · 2 files" {
		t.Fatalf("grep summary = %q", grep.Result.Summary)
	}

	validate := buildToolPresentation("automation_validate", `{}`, domain.ToolOK, "---\nsyntax: OK\ncapabilities: OK\nproviders: BLOCKED\n---")
	if validate.Result.Summary != "Blocked" {
		t.Fatalf("validation summary = %q", validate.Result.Summary)
	}
}
