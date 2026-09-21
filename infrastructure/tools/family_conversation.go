package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"nusashell/application"
)

// executeConversationFamily runs the conversation root's ops. It is reached
// only through executeFamily, which resolves and validates the op via
// application.DispatchOp before delegating here.
func (t *Toolbox) executeConversationFamily(ctx context.Context, op string, argsJSON []byte) (string, error) {
	switch op {
	case "list":
		if t.Conversations == nil {
			return "", depMissing("conversation service not available")
		}
		var args struct {
			Limit  int `json:"limit"`
			Offset int `json:"offset"`
		}
		_ = json.Unmarshal(argsJSON, &args)
		limit := args.Limit
		if limit <= 0 {
			limit = 20
		}
		currID := application.ConversationIDFromContext(ctx)
		count, items, err := t.Conversations.List(currID, limit, args.Offset)
		if err != nil {
			return "", err
		}
		rawItems := make([]any, len(items))
		for i, item := range items {
			rawItems[i] = item
		}
		return yamlJSONL(map[string]any{"count": count, "offset": args.Offset, "limit": limit}, rawItems), nil

	case "search":
		if t.Conversations == nil {
			return "", depMissing("conversation service not available")
		}
		var args struct {
			Query  string `json:"query"`
			ID     string `json:"id"`
			Limit  int    `json:"limit"`
			Offset int    `json:"offset"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if strings.TrimSpace(args.Query) == "" {
			return "", fmt.Errorf("query is required")
		}
		limit := args.Limit
		if limit <= 0 {
			limit = 20
		}
		currID := application.ConversationIDFromContext(ctx)
		scopeID := strings.TrimSpace(args.ID)
		if scopeID != "" {
			count, items, err := t.Conversations.SearchMessages(scopeID, args.Query, limit, args.Offset)
			if err != nil {
				return "", err
			}
			rawItems := make([]any, len(items))
			for i, item := range items {
				rawItems[i] = item
			}
			return yamlJSONL(map[string]any{
				"count":  count,
				"offset": args.Offset,
				"limit":  limit,
				"query":  args.Query,
				"id":     scopeID,
				"scope":  "messages",
			}, rawItems), nil
		}
		count, items, err := t.Conversations.Search(currID, args.Query, limit, args.Offset)
		if err != nil {
			return "", err
		}
		rawItems := make([]any, len(items))
		for i, item := range items {
			rawItems[i] = item
		}
		return yamlJSONL(map[string]any{
			"count":  count,
			"offset": args.Offset,
			"limit":  limit,
			"query":  args.Query,
			"scope":  "rooms",
		}, rawItems), nil

	case "info":
		if t.Conversations == nil {
			return "", depMissing("conversation service not available")
		}
		var args struct {
			ID    string `json:"id"`
			Chunk *int   `json:"chunk"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if strings.TrimSpace(args.ID) == "" {
			return "", fmt.Errorf("id is required")
		}
		info, err := t.Conversations.Info(args.ID, args.Chunk)
		if err != nil {
			return "", err
		}
		return yamlBlock(info), nil

	case "read":
		if t.Conversations == nil {
			return "", depMissing("conversation service not available")
		}
		var args struct {
			ID    string `json:"id"`
			Chunk *int   `json:"chunk"`
			Start *int   `json:"start"`
			End   *int   `json:"end"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if strings.TrimSpace(args.ID) == "" {
			return "", fmt.Errorf("id is required")
		}
		result, err := t.Conversations.Read(args.ID, args.Chunk, args.Start, args.End)
		if err != nil {
			return "", err
		}
		meta := map[string]any{
			"id":         result.ID,
			"start":      result.Start,
			"end":        result.End,
			"turn_count": result.TurnCount,
			"count":      len(result.Messages),
		}
		if result.ChunkIndex != nil {
			meta["chunk"] = *result.ChunkIndex
		}
		rawItems := make([]any, len(result.Messages))
		for i, item := range result.Messages {
			rawItems[i] = item
		}
		return capJSONL("conversation_read", meta, rawItems), nil

	case "send":
		if t.Conversations == nil {
			return "", depMissing("conversation service not available")
		}
		var args struct {
			ID      string `json:"id"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if strings.TrimSpace(args.ID) == "" {
			return "", fmt.Errorf("target conversation id is required")
		}
		if strings.TrimSpace(args.Content) == "" {
			return "", fmt.Errorf("message content is required")
		}
		currID := application.ConversationIDFromContext(ctx)
		if err := t.Conversations.Send(currID, args.ID, args.Content); err != nil {
			return "", err
		}
		return fmt.Sprintf("Message delivered to conversation `%s`", args.ID), nil

	default:
		return "", fmt.Errorf("unknown %s op %q", "conversation_"+op, op)
	}
}
