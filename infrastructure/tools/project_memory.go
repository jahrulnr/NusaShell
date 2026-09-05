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
		Name    string `json:"name"`
		Create  bool   `json:"create"`
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
		return domain.FormatProjectMemoryHits(hits, q.Full), nil
	case "list":
		files, err := t.ProjectMemory.List(ws)
		if err != nil {
			return "", err
		}
		return domain.FormatProjectMemoryList(files), nil
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
		var kinds []string
		if k := strings.TrimSpace(args.Kind); k != "" {
			kinds = []string{k}
		}
		problems, err := t.ProjectMemory.Lint(ws, kinds...)
		if err != nil {
			return "", err
		}
		report := domain.FormatLintReport(problems)
		if len(problems) > 0 {
			return "", &domain.ProjectMemoryLintError{Problems: problems}
		}
		return report, nil
	case "audit":
		out, err := t.ProjectMemory.Audit(ws)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(out, "\n"), nil
	case "gate":
		out, err := t.ProjectMemory.Gate(ws, args.Reason)
		if err != nil {
			return "", err
		}
		return out, nil
	case "pattern_track":
		kind := strings.TrimSpace(args.Kind)
		if kind == "" {
			return "", fmt.Errorf("kind is required")
		}
		out, err := t.ProjectMemory.TrackPatterns(ws, kind)
		if err != nil {
			return "", err
		}
		return out, nil
	case "path":
		kind := strings.TrimSpace(args.Kind)
		if kind == "" {
			return "", fmt.Errorf("kind is required")
		}
		p, err := t.ProjectMemory.Path(ws, kind, args.Create)
		if err != nil {
			return "", err
		}
		return p + "\n", nil
	case "script_path":
		name := strings.TrimSpace(args.Name)
		if name == "" {
			return "", fmt.Errorf("name is required")
		}
		p, err := t.ProjectMemory.ScriptPath(ws, name, args.Create)
		if err != nil {
			return "", err
		}
		return p + "\n", nil
	default:
		return "", fmt.Errorf("unknown memory_project op %q", op)
	}
}
