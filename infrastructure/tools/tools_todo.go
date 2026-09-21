package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nusashell/application"
	"nusashell/domain"
)

func (t *Toolbox) todoToolEntries() []toolEntry {
	return []toolEntry{
		{
			info:    application.ToolInfo{Name: "todo", Description: "Manage the conversation task checklist. Modes: `new` (default, full-replace the list; empty items clears it; every item needs content), `add` (append new items; content required; existing ids are rejected), `replace` (update status/content of existing items only; omit content to keep the stored description; unknown ids are rejected), `delete` (remove items by id; status/content ignored). Prefer add/replace/delete after the list exists (saves tokens). The user can delete items from the UI — treat deleted items as gone and do not re-add them. The optional `brief` argument is a living markdown plan — required sections `## Objective` (user intent) and `## Done when` (acceptance criteria), plus optional `## Findings` and `## Approach` that grow as the task progresses — that survives compaction and is re-injected via hydration (the current checkpoint is reused until compaction, not re-injected each turn); update it as findings emerge and never drift from the Objective. The brief is mirrored to a plan file under the data directory — the result returns `plan_path` (absolute); `file_read` it to re-read the brief and hand it to subagents that need the plan. Set `clear_brief: true` to delete the brief and its plan file (items are untouched unless you also clear them); an empty `brief` alone never clears.", InputSchema: obj("object", props("items", arrObj("Todo items (max 50). new: full list. add: items to append. replace: items to update. delete: ids to remove.", props("id", str("Stable item id (unique within the list)"), "content", str("Short task description (max 500 chars). Required for new/add. Optional for replace (empty keeps stored text). Ignored for delete."), "status", strEnum("Item status; prefer exactly one in_progress at a time. Required for replace. Defaults to pending for new/add. Ignored for delete.", "pending", "in_progress", "completed")), "id"), "mode", strEnum("new (default, full list), add (append), replace (update existing), delete (remove by id)", "new", "add", "replace", "delete"), "brief", str("Living planning document. Required sections: `## Objective` (user intent in their words), `## Done when` (acceptance criteria); optional, grows over time: `## Findings` (paths, line numbers), `## Approach` (key steps). Max ~10000 tokens."), "clear_brief", obj("boolean", nil)), "items")},
			handler: t.execTodo,
		},
		{
			info:    application.ToolInfo{Name: "ask_question", Description: "Pause and ask the user a structured clarifying question before continuing. Use only for genuine decisions the user must make — not things you can figure out yourself. A plain-text reply does not pause auto-continue; only this tool does. Set multi_select=true when more than one option could fit (preferences, scope, priorities); the user can also add free text (when allow_free_text=true) or answer purely with text. The turn blocks until the user answers or cancels.", InputSchema: obj("object", props("question", str("The question to show the user"), "options", arrObj("Selectable choices (1-8). Mark one default when possible.", props("id", str("Stable option id"), "label", str("Short option label"), "description", str("Optional one-line explanation"), "default", obj("boolean", nil), "icon", str("Optional emoji or short icon glyph"), "image", str("Optional image URL or compact data URI")), "id", "label"), "allow_free_text", obj("boolean", nil), "multi_select", obj("boolean", nil)), "question", "options")},
			handler: t.execAskQuestion,
		},
		{
			info:    application.ToolInfo{Name: "wait_until", Description: "Explain or create a durable wait_until step. Waiting never keeps a runner occupied.", InputSchema: obj("object", props("at", str("RFC3339 time")), "at")},
			handler: t.executeWaitUntil,
		},
		{
			info:    application.ToolInfo{Name: "sleep", Description: "Pause for the given number of seconds (max 300). Use for retry backoff or to wait between polls of an async automation(op=run). Does not consume a provider round — the turn resumes after the pause.", InputSchema: obj("object", props("seconds", intSchema("Seconds to sleep (1-300)")), "seconds")},
			handler: t.execSleep,
		},
	}
}

func (t *Toolbox) execSleep(ctx context.Context, argsJSON []byte) (string, error) {
	var args struct {
		Seconds int `json:"seconds"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if args.Seconds < 1 {
		return "", fmt.Errorf("seconds must be at least 1")
	}
	if args.Seconds > 300 {
		args.Seconds = 300
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(time.Duration(args.Seconds) * time.Second):
	}
	return yamlBlock(map[string]any{"status": "slept"}), nil
}

func (t *Toolbox) executeWaitUntil(_ context.Context, argsJSON []byte) (string, error) {
	var args struct {
		At string `json:"at"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(args.At) == "" {
		return "", fmt.Errorf("at is required")
	}
	if _, err := time.Parse(time.RFC3339, args.At); err != nil {
		return "", fmt.Errorf("at must be RFC3339")
	}
	return yamlBlock(map[string]string{
		"hint": "Use a workflow step wait_until: <RFC3339>. The run enters waiting and resumes after restart.",
		"at":   args.At,
	}), nil
}

// execTodo replaces the conversation todo checklist (full-replace, Claude
// TodoWrite style). Empty items clears the list. The optional `brief` argument
// is a living planning document that stays visible in tool history while the
// hydration checkpoint is reused, then is included in the fresh checkpoint
// after compaction. Requires a conversation id in the context (set via
// WithConversationID by the turn runner). An empty `goal` is ignored; a
// non-empty `goal` fills `brief` when brief is omitted.
const (
	todoMaxItems        = 50
	todoMaxContentChars = 500
	todoMaxBriefChars   = 40000 // ~10k tokens (4 chars/token average)
)

func (t *Toolbox) execTodo(ctx context.Context, argsJSON []byte) (string, error) {
	if t.Todos == nil {
		return "", depMissing("todo tracking is not available")
	}
	conversationID := application.ConversationIDFromContext(ctx)
	if conversationID == "" {
		return "", fmt.Errorf("todo tool requires a conversation context")
	}
	var args struct {
		Items      json.RawMessage `json:"items"`
		Mode       string          `json:"mode"`
		Brief      string          `json:"brief"`
		Goal       string          `json:"goal"` // mapped to brief when brief is empty
		ClearBrief bool            `json:"clear_brief"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	// itemsPresent distinguishes an omitted `items` key (leave the list
	// alone) from an explicit `items: []` (clear the list). This matters for
	// clear_brief: `{"clear_brief": true}` must remove only the brief, never
	// the checklist.
	itemsPresent := args.Items != nil
	var rawItems []struct {
		ID      string `json:"id"`
		Content string `json:"content"`
		Status  string `json:"status"`
	}
	if itemsPresent {
		if err := json.Unmarshal(args.Items, &rawItems); err != nil {
			return "", fmt.Errorf("invalid items: %w", err)
		}
	}
	if len(rawItems) > todoMaxItems {
		return "", fmt.Errorf("items must have at most %d entries", todoMaxItems)
	}
	brief := strings.TrimSpace(args.Brief)
	if brief == "" && args.Goal != "" {
		brief = strings.TrimSpace(args.Goal)
	}
	if args.ClearBrief && brief != "" {
		return "", fmt.Errorf("clear_brief and brief are mutually exclusive")
	}
	if len(brief) > todoMaxBriefChars {
		return "", fmt.Errorf("brief exceeds %d chars (~10k tokens)", todoMaxBriefChars)
	}
	if err := domain.ValidateBrief(brief); err != nil {
		return "", err
	}
	mode := strings.TrimSpace(args.Mode)
	if mode == "" {
		mode = "new"
	}
	switch mode {
	case "patch":
		return "", fmt.Errorf("unknown todo mode %q; use new (full list), add (append), replace (update existing), or delete", mode)
	case "new", "add", "replace", "delete":
	default:
		return "", fmt.Errorf("unknown todo mode %q; use new, add, replace, or delete", mode)
	}
	if (mode == "add" || mode == "replace" || mode == "delete") && len(rawItems) == 0 {
		return "", fmt.Errorf("%s requires at least one item", mode)
	}
	existing := t.Todos.Get(conversationID)
	existingByID := make(map[string]domain.TodoItem, len(existing))
	for _, item := range existing {
		existingByID[item.ID] = item
	}
	items := make([]domain.TodoItem, 0, len(rawItems))
	seenIDs := make(map[string]bool, len(rawItems))
	for _, raw := range rawItems {
		id := strings.TrimSpace(raw.ID)
		content := strings.TrimSpace(raw.Content)
		status := domain.TodoStatus(raw.Status)
		if id == "" {
			return "", fmt.Errorf("each item requires a non-empty id")
		}
		if seenIDs[id] {
			return "", fmt.Errorf("duplicate item id: %s", id)
		}
		seenIDs[id] = true
		stored, exists := existingByID[id]
		switch mode {
		case "new":
			if content == "" {
				return "", fmt.Errorf("item %q requires non-empty content", id)
			}
			if status == "" {
				status = domain.TodoPending
			}
		case "add":
			if exists {
				return "", fmt.Errorf("item %q already exists; use replace to update it", id)
			}
			if content == "" {
				return "", fmt.Errorf("item %q requires non-empty content", id)
			}
			if status == "" {
				status = domain.TodoPending
			}
		case "replace":
			if !exists {
				return "", fmt.Errorf("unknown item id %q; use add to create it", id)
			}
			if content == "" {
				content = stored.Content
			}
			if status == "" {
				return "", fmt.Errorf("item %q requires status", id)
			}
		case "delete":
			if !exists {
				return "", fmt.Errorf("unknown item id %q", id)
			}
		}
		if mode != "delete" {
			if content != "" && len(content) > todoMaxContentChars {
				return "", fmt.Errorf("item content exceeds %d chars", todoMaxContentChars)
			}
			if !domain.IsValidTodoStatus(status) {
				return "", fmt.Errorf("item status must be pending, in_progress, or completed")
			}
		}
		items = append(items, domain.TodoItem{ID: id, Content: content, Status: status})
	}
	switch mode {
	case "new":
		if itemsPresent {
			t.Todos.Set(conversationID, items)
		}
	case "add", "replace":
		t.Todos.Patch(conversationID, items)
	case "delete":
		remaining := make([]domain.TodoItem, 0, len(existing)-len(items))
		for _, item := range existing {
			if !seenIDs[item.ID] {
				remaining = append(remaining, item)
			}
		}
		t.Todos.Set(conversationID, remaining)
	}
	if args.ClearBrief {
		// Explicit clear: remove the brief and its mirrored plan file.
		// Items are untouched (clear them separately with mode=new and
		// items: []). An empty `brief` arg alone never clears — it
		// means "don't change the brief" so status replaces are safe.
		if err := t.Todos.ClearBrief(conversationID); err != nil {
			return "", fmt.Errorf("clear brief: %w", err)
		}
	} else if brief != "" {
		t.Todos.SetBrief(conversationID, brief)
	}
	// Return a compact acknowledgment only: summary counts. The full item
	// list and brief are NOT echoed back — the agent just sent them, and
	// the UI receives the complete list via the agent.todo.updated event.
	// Echoing wastes tokens (the brief alone can be ~10k tokens). The
	// plan_path points at the mirrored plan file (always current) so the
	// agent or an ACP subagent can file_read it later.
	summary := domain.SummarizeTodos(t.Todos.Get(conversationID))
	meta := map[string]any{
		"ok":           true,
		"conversation": conversationID,
		"mode":         mode,
		"total":        summary.Total,
		"pending":      summary.Pending,
		"in_progress":  summary.InProgress,
		"completed":    summary.Completed,
	}
	if planPath := t.Todos.PlanPath(conversationID); planPath != "" {
		meta["plan_path"] = planPath
	}
	return yamlJSONL(meta, nil), nil
}

// execAskQuestion pauses the turn and asks the user a structured clarifying
// question. The tool blocks until the UI answers via agent.ask.answer RPC or
// the turn is cancelled. Requires a run id and conversation id in the context.
func (t *Toolbox) execAskQuestion(ctx context.Context, argsJSON []byte) (string, error) {
	if t.AskQuestions == nil {
		return "", depMissing("ask_question is not available in this runtime")
	}
	runID := application.RunIDFromContext(ctx)
	if runID == "" {
		return "", fmt.Errorf("ask_question requires a running turn context")
	}
	conversationID := application.ConversationIDFromContext(ctx)
	var args struct {
		Question      string                     `json:"question"`
		Options       []domain.AskQuestionOption `json:"options"`
		AllowFreeText *bool                      `json:"allow_free_text"`
		MultiSelect   *bool                      `json:"multi_select"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	allowFreeText := true
	if args.AllowFreeText != nil {
		allowFreeText = *args.AllowFreeText
	}
	multiSelect := false
	if args.MultiSelect != nil {
		multiSelect = *args.MultiSelect
	}
	req, err := domain.ValidateAskQuestionRequest(args.Question, args.Options, allowFreeText, multiSelect)
	if err != nil {
		return "", err
	}
	// Generate a tool call ID if not available from context. The tool
	// execution framework passes the call ID separately; for now we use
	// a composite key from runID + question hash to avoid collisions.
	callID := application.ToolCallIDFromContext(ctx)
	if callID == "" {
		callID = domain.NewID(domain.IDPrefixAsk)
	}
	ch, err := t.AskQuestions.Ask(runID, callID, conversationID, req)
	if err != nil {
		return "", err
	}
	select {
	case result := <-ch:
		if !result.OK {
			return "", fmt.Errorf("%s", result.Answer)
		}
		return yamlBlock(map[string]any{
			"ok":     true,
			"via":    result.Via,
			"answer": result.Answer,
		}), nil
	case <-ctx.Done():
		t.AskQuestions.Cancel(runID, callID, "Agent turn cancelled")
		return "", ctx.Err()
	}
}
