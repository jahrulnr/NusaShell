package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"nusashell/domain"
)

// executeMemoryFamily runs the memory root's ops. It is reached only through
// executeFamily, which resolves and validates the op via
// application.DispatchOp before delegating here.
func (t *Toolbox) executeMemoryFamily(ctx context.Context, op string, argsJSON []byte) (string, error) {
	switch op {
	case "search":
		if t.MemoryRecords == nil {
			return "", depMissing("memory record store not configured")
		}
		var args struct {
			Query   string `json:"query"`
			Type    string `json:"type"`
			Status  string `json:"status"`
			Scope   string `json:"scope"`
			Project string `json:"project"`
			Limit   int    `json:"limit"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		limit := args.Limit
		if limit <= 0 {
			limit = 20
		}
		filter := domain.MemorySearchFilter{
			Query:   args.Query,
			Type:    args.Type,
			Status:  args.Status,
			Scope:   args.Scope,
			Project: args.Project,
			Limit:   limit,
		}
		items := make([]any, 0)
		for _, rec := range t.MemoryRecords.List() {
			if !memoryRecordMatches(rec, filter) {
				continue
			}
			items = append(items, formatMemoryRecordJSON(rec))
			if len(items) >= limit {
				break
			}
		}
		return yamlJSONL(map[string]any{"count": len(items)}, items), nil

	case "get":
		if t.MemoryRecords == nil {
			return "", depMissing("memory record store not configured")
		}
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		id := strings.TrimSpace(args.ID)
		if id == "" {
			return "", fmt.Errorf("id is required")
		}
		rec, err := t.MemoryRecords.Get(id)
		if err != nil {
			return "", err
		}
		return yamlBlock(formatMemoryRecordJSON(rec)), nil

	case "list":
		if t.MemoryRecords == nil {
			return yamlBlock(map[string]any{"count": 0, "error": "memory record store not configured"}), nil
		}
		var args struct {
			Type    string `json:"type"`
			Status  string `json:"status"`
			Scope   string `json:"scope"`
			Project string `json:"project"`
			Limit   int    `json:"limit"`
		}
		_ = json.Unmarshal(argsJSON, &args)
		limit := args.Limit
		if limit <= 0 {
			limit = 50
		}
		filter := domain.MemorySearchFilter{
			Type:    args.Type,
			Status:  args.Status,
			Scope:   args.Scope,
			Project: args.Project,
			Limit:   limit,
		}
		items := make([]any, 0)
		for _, rec := range t.MemoryRecords.List() {
			if !memoryRecordMatches(rec, filter) {
				continue
			}
			items = append(items, formatMemoryRecordJSON(rec))
			if len(items) >= limit {
				break
			}
		}
		return yamlJSONL(map[string]any{"count": len(items)}, items), nil

	default:
		return "", fmt.Errorf("unknown %s op %q", "memory_"+op, op)
	}
}

func memoryRecordMatches(m *domain.MemoryRecord, filter domain.MemorySearchFilter) bool {
	return m.Matches(filter)
}

func formatMemoryRecordJSON(m *domain.MemoryRecord) map[string]any {
	out := map[string]any{
		"id":     m.ID,
		"type":   m.Type,
		"body":   m.Body,
		"status": m.Status,
		"scope":  m.Scope.Level,
	}
	if m.Subject != "" {
		out["subject"] = m.Subject
	}
	if m.Scope.Project != "" {
		out["project"] = m.Scope.Project
	}
	return out
}
