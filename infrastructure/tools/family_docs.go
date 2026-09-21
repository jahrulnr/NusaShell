package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// executeDocsFamily runs the docs root's ops. It is reached only through
// executeFamily, which resolves and validates the op via
// application.DispatchOp before delegating here.
func (t *Toolbox) executeDocsFamily(ctx context.Context, op string, argsJSON []byte) (string, error) {
	switch op {
	case "list":
		var args struct {
			Limit int `json:"limit"`
		}
		_ = json.Unmarshal(argsJSON, &args)
		limit := args.Limit
		if limit <= 0 {
			limit = 50
		}
		metas := t.Docs.List()
		total := len(metas)
		if limit < len(metas) {
			metas = metas[:limit]
		}
		items := make([]any, 0, len(metas))
		for _, m := range metas {
			items = append(items, map[string]any{"id": m.ID, "title": m.Title, "path": m.Path})
		}
		return capJSONL("docs", map[string]any{"count": len(metas), "total": total, "limit": limit}, items), nil

	case "search":
		var args struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		limit := args.Limit
		if limit <= 0 {
			limit = 10
		}
		hits := t.Docs.Search(args.Query, limit)
		items := make([]any, 0, len(hits))
		for _, h := range hits {
			items = append(items, map[string]any{"id": h.ID, "title": h.Title, "path": h.Path, "snippet": h.Snippet})
		}
		return capJSONL("docs", map[string]any{"count": len(hits)}, items), nil

	case "read":
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		doc, err := t.Docs.Read(args.ID)
		if err != nil {
			return "", fmt.Errorf("document %q not found; use docs with op=list or op=search first", args.ID)
		}
		return capToolOutput("docs", map[string]any{"title": doc.Title, "path": doc.Path}, doc.Content), nil

	default:
		return "", fmt.Errorf("unknown %s op %q", "docs_"+op, op)
	}
}
