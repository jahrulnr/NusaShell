package application

import (
	"nusashell/contracts"
)

func (a *App) handleDocsList() (any, *contracts.RPCError) {
	metas := a.Docs.List()
	out := make([]contracts.DocDTO, 0, len(metas))
	for _, m := range metas {
		out = append(out, contracts.DocDTO{ID: m.ID, Title: m.Title, Path: m.Path})
	}
	return contracts.DocsListResult{Docs: out}, nil
}

func (a *App) handleDocsSearch(req contracts.DocsSearchRequest) (any, *contracts.RPCError) {
	limit := req.Limit
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	hits := a.Docs.Search(req.Query, limit)
	out := make([]contracts.DocHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, contracts.DocHit{
			DocDTO:  contracts.DocDTO{ID: h.ID, Title: h.Title, Path: h.Path},
			Snippet: h.Snippet,
		})
	}
	return contracts.DocsSearchResult{Results: out}, nil
}

func (a *App) handleDocsRead(req contracts.DocReadRequest) (any, *contracts.RPCError) {
	doc, err := a.Docs.Read(req.ID)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	return contracts.DocReadResult{Doc: contracts.DocFull{
		DocDTO:  contracts.DocDTO{ID: doc.ID, Title: doc.Title, Path: doc.Path},
		Content: doc.Content,
	}}, nil
}
