package application

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"nusashell/application/service/mediaread"
	"nusashell/application/service/toolpresentation"
	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/domain/turndiff"
)

type toolExecResult struct {
	status          domain.ToolCallStatus
	output          string
	atts            []domain.Attachment
	learningNodeIDs []string
}

func (a *App) executeTurnTools(run *TurnRun, messageID string, toolCalls []domain.ToolCall, caps ModelCapabilities, settings domain.Settings, round int) error {
	if err := run.Ctx.Err(); err != nil {
		if len(toolCalls) > 0 {
			conversation, gerr := a.loadRepo(run.ConversationID)
			if gerr == nil {
				c := conversation.Conversation()
				for _, toolCall := range toolCalls {
					a.Bus.Emit(contracts.EventToolCompleted, contracts.ToolCompletedEvent{
						RunID: run.ID, ConversationID: run.ConversationID, ToolCallID: toolCall.ID,
						Name: toolCall.Name, Args: toolpresentation.ToolArgsRaw(toolCall.Args), Status: string(domain.ToolInterrupted), Output: "interrupted by user",
						Presentation: toolpresentation.BuildToolPresentation(toolCall.Name, toolCall.Args, domain.ToolInterrupted, "interrupted by user"),
					})
					c = a.updateToolResult(c, messageID, toolCall.ID, domain.ToolInterrupted, "interrupted by user", nil)
				}
				_ = conversation.Save()
			}
		}
		return err
	}

	// Phase 1: execute all tool calls concurrently (bounded). Only tool
	// execution and event emission happen here — no conversation-store writes —
	// so each backing store's own lock is sufficient and there are no
	// read-modify-write races on the conversation snapshot. Results are kept in
	// tool-call order for deterministic persistence in phase 2.
	results := make([]toolExecResult, len(toolCalls))
	var wg sync.WaitGroup
	limit := settings.MaxParallelTools
	if limit < 1 {
		limit = defaultMaxParallelTools
	}
	sem := make(chan struct{}, limit)
	for i := range toolCalls {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = a.runOneTool(run, messageID, toolCalls[i], caps, settings, round)
		}(i)
	}
	wg.Wait()

	// Phase 2: persist results in tool-call order and emit todo updates. This
	// runs on the single turn goroutine, so conversation snapshot writes never
	// race with each other.
	repo, err := a.loadRepo(run.ConversationID)
	if err != nil {
		return err
	}
	conversation := repo.Conversation()
	for i := range toolCalls {
		toolCall := toolCalls[i]
		r := results[i]
		conversation = a.updateToolResult(conversation, messageID, toolCall.ID, r.status, r.output, r.atts)
		// When the model updates the todo checklist, emit a dedicated event so
		// the UI can re-render the strip without polling agent.todos.get.
		if toolCall.Name == "todo" && r.status == domain.ToolOK && a.Todos != nil {
			items := a.Todos.Get(run.ConversationID)
			dtos := make([]contracts.TodoItemDTO, 0, len(items))
			for _, item := range items {
				dtos = append(dtos, contracts.TodoItemDTO{ID: item.ID, Content: item.Content, Status: string(item.Status)})
			}
			summary := domain.SummarizeTodos(items)
			a.Bus.Emit(contracts.EventTodoUpdated, contracts.TodoUpdatedEvent{
				ConversationID: run.ConversationID,
				Items:          dtos,
				Summary:        contracts.TodoSummaryDTO{Total: summary.Total, Pending: summary.Pending, InProgress: summary.InProgress, Completed: summary.Completed},
				Brief:          a.Todos.GetBrief(run.ConversationID),
			})
		}
	}
	// A brief change no longer strips the hydration checkpoint. The
	// checkpoint's todo_list brief is frozen until the next compaction epoch;
	// the agent can call todo/todo_list live. Stripping + rebuilding relocated
	// the checkpoint after whatever user was last (the cache-poison dump),
	// invalidating the prompt-cache prefix from the hydration byte onward.
	if saveErr := repo.Save(); saveErr != nil {
		return saveErr
	}
	for i := range results {
		if results[i].status == domain.ToolOK {
			a.recordLearningTurnNodes(run, results[i].learningNodeIDs)
		}
	}
	// Cross-conversation announcements: the skill library is global, so a
	// successful mutating skill call by this conversation's agent is
	// external news to every OTHER active agent. The caller's own room is
	// skipped — the model already knows it made the call (no
	// self-announcement). The memory dispatcher is read-only.
	for i := range toolCalls {
		r := results[i]
		if r.status != domain.ToolOK {
			continue
		}
		switch toolCalls[i].Name {
		case "skill":
			op := OpArg([]byte(toolCalls[i].Args))
			if op == "save" || op == "delete" {
				a.publishAnnouncementToAll(newAnnouncement(
					"skills_changed",
					domain.AnnouncementSkillsChangedArgs(op),
					domain.AnnouncementSkillsChangedMessage(),
				), run.ConversationID)
			}
		}
	}
	if err := run.Ctx.Err(); err != nil {
		return err
	}
	return nil
}

// runOneTool executes a single tool call and returns its result. It emits the
// tool-started and tool-completed events and never writes to the conversation
// store, so it is safe to run concurrently for the tool calls of one round.
func (a *App) runOneTool(run *TurnRun, messageID string, toolCall domain.ToolCall, caps ModelCapabilities, settings domain.Settings, round int) toolExecResult {
	baseCtx := run.Ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	toolCtx, cancelTool := context.WithCancel(baseCtx)
	run.registerToolCancel(toolCall.ID, cancelTool)
	defer func() {
		run.unregisterToolCancel(toolCall.ID)
		cancelTool()
	}()

	a.Bus.Emit(contracts.EventToolStarted, contracts.ToolStartedEvent{
		RunID: run.ID, ConversationID: run.ConversationID, ToolCallID: toolCall.ID, Name: toolCall.Name, Args: toolpresentation.ToolArgsRaw(toolCall.Args),
		Presentation: toolpresentation.BuildToolPresentation(toolCall.Name, toolCall.Args, domain.ToolRunning, ""),
	})
	a.log("info", "tools", "tool call: %s", toolCall.Name)

	// If the turn was already cancelled, do not start the tool — mark it
	// interrupted (mirrors the pre-parallel behavior of skipping remaining
	// tools after cancellation).
	if baseCtx.Err() != nil || toolCtx.Err() != nil {
		res := toolExecResult{status: domain.ToolInterrupted, output: "interrupted by user"}
		a.emitToolCompleted(run, toolCall, res)
		return res
	}

	var output string
	var outputAttachments []domain.Attachment
	var err error
	switch toolCall.Name {
	case "read_media":
		kind, sniffErr := mediaread.SniffMediaKind([]byte(toolCall.Args))
		if sniffErr != nil {
			output = "error: " + sniffErr.Error()
			err = sniffErr
		} else {
			switch kind {
			case "image":
				output, outputAttachments, err = a.executeReadImage(run, toolCall, caps, settings)
			case "audio":
				output, outputAttachments, err = a.executeReadAudio(run, toolCall, caps, settings)
			case "video":
				output, outputAttachments, err = a.executeReadVideo(run, toolCall, caps, settings)
			case "document":
				output, outputAttachments, err = a.executeReadDocument(run, toolCall, caps, settings)
			default:
				output = "error: unrecognized media type"
				err = fmt.Errorf("unrecognized media type")
			}
		}
	case "generate_media", "generate_image", "generate_speech", "generate_video":
		output, outputAttachments, err = a.executeGenerateMedia(run, toolCall, settings)
	default:
		toolCtx = WithConversationID(toolCtx, run.ConversationID)
		toolCtx = WithWorkspace(toolCtx, run.Workspace)
		toolCtx = WithRunID(toolCtx, run.ID)
		toolCtx = WithToolCallID(toolCtx, toolCall.ID)
		toolCtx, deltaCap := turndiff.WithCapture(toolCtx)
		toolPresentation := toolpresentation.BuildToolPresentation(toolCall.Name, toolCall.Args, domain.ToolRunning, "")
		executeTool := func() error {
			if s, ok := a.Toolbox.(interface {
				ExecuteStreamed(ctx context.Context, name string, argsJSON []byte, onChunk func(string)) (string, error)
			}); ok {
				a.publishRoundToolStart(run.ID, messageID, round, toolCall.ID, toolCall.Name, toolCall.Args, toolPresentation)
				var e error
				output, e = s.ExecuteStreamed(toolCtx, toolCall.Name, []byte(toolCall.Args), func(text string) {
					if text != "" {
						a.publishRoundDelta(run.ID, messageID, round, contracts.RoundDeltaTool, toolCall.ID, toolCall.Name, text)
					}
				})
				return e
			}
			var e error
			output, e = a.Toolbox.Execute(toolCtx, toolCall.Name, []byte(toolCall.Args))
			return e
		}
		err = executeTool()
		if d, ok := deltaCap.Take(); ok {
			a.trackTurnDiff(run, d)
		}
	}
	status := domain.ToolOK
	interrupted := baseCtx.Err() != nil || toolCtx.Err() != nil
	if interrupted {
		status = domain.ToolInterrupted
		// Interrupted streaming tools keep the partial output received so
		// far (the executor returns it with the cancellation error), while
		// the explicit marker tells the next model round why the tool ended.
		if err != nil && strings.Contains(err.Error(), "partial output") {
			output = "interrupted by user\n" + err.Error()
		} else if strings.TrimSpace(output) == "" {
			output = "interrupted by user"
		} else {
			output = strings.TrimRight(output, "\n") + "\n\ninterrupted by user"
		}
	} else if err != nil {
		status = domain.ToolFailed
		output = "error: " + truncateToolError(err.Error())
	}
	// Async subagent: the tool returns immediately with "starting" status.
	// Keep the tool call marked as running so the UI shows a spinner; the
	// OnDone callback will update it to ok/fail with the summary when the
	// subagent finishes.
	if status == domain.ToolOK && (toolCall.Name == "subagent" || toolCall.Name == domain.DelegateToolName) && err == nil {
		status = domain.ToolRunning
	}
	res := toolExecResult{status: status, output: output, atts: outputAttachments}
	if status == domain.ToolOK {
		res.learningNodeIDs = learningNodeIDsFromTool(a, toolCall, output)
	}
	a.emitToolCompleted(run, toolCall, res)
	a.emitLearningMutationEvents(toolCall.Name, status)
	return res
}

func (a *App) emitToolCompleted(run *TurnRun, toolCall domain.ToolCall, res toolExecResult) {
	event := contracts.ToolCompletedEvent{
		RunID: run.ID, ConversationID: run.ConversationID, ToolCallID: toolCall.ID,
		Name: toolCall.Name, Args: toolpresentation.ToolArgsRaw(toolCall.Args), Status: string(res.status), Output: res.output,
		Presentation: toolpresentation.BuildToolPresentation(toolCall.Name, toolCall.Args, res.status, res.output, res.atts),
	}
	event.Attachments = toolpresentation.ToolAttachmentDTOs(res.atts)
	a.Bus.Emit(contracts.EventToolCompleted, event)
}

// emitLearningMutationEvents publishes memory.updated and/or skill.updated
// events when a tool mutates the memory or skill stores, so the Learning UI
// can refresh its memory list, search results, and graph in real time without
// polling. Only successful tool calls trigger the events.
func (a *App) emitLearningMutationEvents(toolName string, status domain.ToolCallStatus) {
	if a.Bus == nil || status != domain.ToolOK {
		return
	}
	// Route by dispatcher root; any successful family op refreshes the
	// Learning UI panes in real time.
	switch toolName {
	case "memory":
		a.Bus.Emit(contracts.EventMemoryUpdated, map[string]any{
			"source": "tool",
			"tool":   toolName,
		})
	case "skill":
		a.Bus.Emit(contracts.EventSkillUpdated, map[string]any{
			"source": "tool",
			"tool":   toolName,
		})
	}
}
