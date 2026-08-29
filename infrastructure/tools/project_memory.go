package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"nusashell/application"
	"nusashell/domain"
)

func (t *Toolbox) executeProjectMemory(ctx context.Context, op string, argsJSON []byte) (string, error) {
	ws := strings.TrimSpace(application.WorkspaceFromContext(ctx))
	if ws == "" {
		return "", fmt.Errorf("memory_project requires an active workspace")
	}
	if t.ProjectMemory == nil {
		return "", fmt.Errorf("project memory store not configured")
	}
	var args struct {
		Topic   string `json:"topic"`
		Kind    string `json:"kind"`
		Related string `json:"related"`
		ID      string `json:"id"`
		Archive bool   `json:"archive"`
		Full    bool   `json:"full"`
		Limit   int    `json:"limit"`
		Content string `json:"content"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	switch op {
	case "query":
		q := domain.ProjectMemoryQuery{
			Topic: args.Topic, Kind: args.Kind, Related: args.Related, ID: args.ID,
			Archive: args.Archive, Full: args.Full, Limit: args.Limit,
		}
		if !domain.HasProjectMemorySelector(q) {
			return "", fmt.Errorf("query requires at least one selector: topic, kind, related, or id")
		}
		hits, err := t.ProjectMemory.Query(ws, q)
		if err != nil {
			return "", err
		}
		items := make([]any, 0, len(hits))
		for _, h := range hits {
			row := map[string]any{"id": h.ID, "kind": h.Kind, "file": h.File, "scope": h.Scope}
			if q.Full {
				row["body"] = h.Body
			}
			items = append(items, row)
		}
		return yamlJSONL(map[string]any{"count": len(hits)}, items), nil
	case "list":
		files, err := t.ProjectMemory.List(ws)
		if err != nil {
			return "", err
		}
		items := make([]any, 0, len(files))
		for _, f := range files {
			items = append(items, map[string]any{"file": f})
		}
		return yamlJSONL(map[string]any{"count": len(files)}, items), nil
	case "read":
		body, err := t.ProjectMemory.Read(ws, args.Kind, args.ID)
		if err != nil {
			return "", err
		}
		return yamlMD(map[string]any{"kind": args.Kind, "id": args.ID}, body), nil
	case "admit":
		res, err := t.ProjectMemory.Admit(ws, args.Kind, args.ID, args.Content)
		if err != nil {
			return "", err
		}
		out := map[string]any{"status": "admitted", "id": res.ID, "kind": res.Kind}
		if res.PatternNote != "" {
			out["pattern_note"] = res.PatternNote
		}
		return yamlBlock(out), nil
	case "skip":
		reason := strings.TrimSpace(args.Reason)
		if reason == "" {
			return "", fmt.Errorf("reason is required for op=skip")
		}
		return yamlBlock(map[string]any{"status": "skipped", "reason": reason}), nil
	case "archive":
		if strings.TrimSpace(args.ID) == "" {
			return "", fmt.Errorf("id is required")
		}
		if err := t.ProjectMemory.Archive(ws, args.ID); err != nil {
			return "", err
		}
		return yamlBlock(map[string]any{"status": "archived", "id": args.ID}), nil
	case "lint":
		problems, err := t.ProjectMemory.Lint(ws)
		if err != nil {
			return "", err
		}
		items := make([]any, 0, len(problems))
		for _, p := range problems {
			items = append(items, map[string]any{"file": p.File, "message": p.Message})
		}
		status := "clean"
		if len(problems) > 0 {
			status = "issues"
		}
		return yamlJSONL(map[string]any{"status": status, "count": len(problems)}, items), nil
	default:
		return "", fmt.Errorf("unknown memory_project op %q", op)
	}
}
