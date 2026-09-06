package tools

import (
	"errors"
	"testing"

	"nusashell/contracts"
)

type stubDocs struct {
	metas []DocMeta
	hits  []DocHit
	doc   DocFull
	err   error
}

func (s stubDocs) List() []DocMeta              { return s.metas }
func (s stubDocs) Search(string, int) []DocHit  { return s.hits }
func (s stubDocs) Read(string) (DocFull, error) { return s.doc, s.err }

func TestDocsHandleListMapsDTO(t *testing.T) {
	svc := New(Deps{Docs: stubDocs{metas: []DocMeta{{ID: "tools", Title: "Tools", Path: "tools.md"}}}})
	resp, rpcErr := svc.HandleList()
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	got := resp.(contracts.DocsListResult)
	if len(got.Docs) != 1 || got.Docs[0].ID != "tools" {
		t.Fatalf("docs = %+v", got.Docs)
	}
}

func TestDocsHandleSearchMapsHits(t *testing.T) {
	svc := New(Deps{Docs: stubDocs{hits: []DocHit{{
		DocMeta: DocMeta{ID: "mcp", Title: "MCP", Path: "mcp.md"},
		Snippet: "plugin",
	}}}})
	resp, rpcErr := svc.HandleSearch(contracts.DocsSearchRequest{Query: "plugin"})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	got := resp.(contracts.DocsSearchResult)
	if len(got.Results) != 1 || got.Results[0].Snippet != "plugin" {
		t.Fatalf("results = %+v", got.Results)
	}
}

func TestDocsHandleReadNotFound(t *testing.T) {
	svc := New(Deps{Docs: stubDocs{err: errors.New("missing")}})
	_, rpcErr := svc.HandleRead(contracts.DocReadRequest{ID: "nope"})
	if rpcErr == nil || rpcErr.Code != contracts.CodeNotFound {
		t.Fatalf("rpcErr = %+v", rpcErr)
	}
}
