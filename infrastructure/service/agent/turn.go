package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	"github.com/jahrulnr/nusashell/infrastructure/service/agent/acp"
	"github.com/jahrulnr/nusashell/infrastructure/service/wire"
)

// maxTurnMedia bounds the files one reply carries, so an agent cannot fill
// the room's storage by streaming pictures.
const maxTurnMedia = 8

// turnSink collects one prompt's session updates into an assistant message
// and forwards them as turn events. Updates arrive on the process's
// notification goroutine and permission requests on request goroutines,
// so every method takes mu; onEvent is only called under it.
type turnSink struct {
	ctx     context.Context
	onEvent event.TurnEventHandler
	approve dservice.Approver
	elicit  dservice.Elicitor

	mu    sync.Mutex
	parts []entity.MessagePart
	calls map[string]*callRecord
	order []string          // call ids in first-seen order
	terms map[string]string // terminal id → the tool call showing it
	done  bool
	// gated holds the agent's updates in held while a steer is in
	// flight, so the owner's input lands before the agent's answer to it.
	gated bool
	held  []acp.SessionUpdate
	// asking counts the permission questions waiting for the owner;
	// yielding marks a turn asked to stop at its next safe point.
	asking   int
	yielding bool
	// folders are the session's folders: a file the agent links is shown
	// only from inside them. save keeps the bytes of a picture or sound the
	// agent sent inline; nil refuses them. media counts the files so far.
	folders []string
	// workspace is the turn's working directory — steered attachments
	// resolve against it the same way prompt attachments do.
	workspace string
	save      func(ctx context.Context, name, mediaType string, data []byte) (valueobject.Attachment, error)
	media     int
}

// callRecord is the latest known state of one agent tool call.
type callRecord struct {
	id         string
	title      string
	kind       string
	input      string
	output     string
	status     string
	at         time.Time
	emitted    bool   // the tool-call event went out
	resultSent bool   // the terminal tool_result event went out
	sent       string // output already streamed
}

func newTurnSink(ctx context.Context, onEvent event.TurnEventHandler, approve dservice.Approver) *turnSink {
	return &turnSink{ctx: ctx, onEvent: onEvent, approve: approve, calls: map[string]*callRecord{}, terms: map[string]string{}}
}

func (t *turnSink) emit(ev event.TurnEvent) {
	if t.onEvent != nil {
		t.onEvent(ev)
	}
}

// appendText adds streamed text or reasoning, merging into the previous
// part of the same kind so the stored order matches what streamed.
func (t *turnSink) appendText(kind, text string) {
	if text == "" {
		return
	}
	if n := len(t.parts); n > 0 && t.parts[n-1].Kind == kind {
		t.parts[n-1].Text += text
	} else {
		t.parts = append(t.parts, entity.MessagePart{Kind: kind, Text: text})
	}
	if kind == entity.MessagePartReasoning {
		t.emit(event.NewReasoningEvent(text))
	} else {
		t.emit(event.NewDeltaEvent(text))
	}
}

func (t *turnSink) update(u acp.SessionUpdate) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.done {
		return
	}
	if t.gated {
		t.held = append(t.held, u)
		return
	}
	t.apply(u)
}

// gate starts holding updates for a steer; false when the turn is over
// or another steer is in flight.
func (t *turnSink) gate() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.done || t.gated {
		return false
	}
	t.gated = true
	return true
}

// release ends a steer's gate. An accepted steer is recorded where it was
// sent and announced, after delivered settles the caller's receipt; then
// the held updates apply. A turn that ended meanwhile records nothing, and
// release reports the steer as not taken.
func (t *turnSink) release(input valueobject.UserInput, recordID string, accepted bool, delivered func()) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gated = false
	taken := accepted && !t.done
	if taken {
		if delivered != nil {
			delivered()
		}
		t.parts = append(t.parts, entity.MessagePart{
			Kind:             entity.MessagePartSteer,
			Text:             input.Text,
			SteerAttachments: input.Attachments,
			SteerContext:     strings.TrimSpace(valueobject.ReferenceNote(input.References) + "\n\n" + valueobject.AnnotationNote(input.Annotations)),
		})
		if recordID != "" {
			t.emit(event.NewRecordedSteerEvent(input.Text, recordID, input.Attachments...))
		} else {
			t.emit(event.NewSteerEvent(input.Text, input.Attachments...))
		}
	}
	held := t.held
	t.held = nil
	for _, u := range held {
		t.apply(u)
	}
	return taken
}

// apply folds one session update into the turn. Callers hold t.mu.
func (t *turnSink) apply(u acp.SessionUpdate) {
	switch u.SessionUpdate {
	case "agent_message_chunk":
		var block acp.ContentBlock
		if json.Unmarshal(u.Content, &block) == nil && block.Type != "" && block.Type != "text" {
			t.addMedia(block)
			return
		}
		t.appendText(entity.MessagePartText, acp.DecodeTextContent(u.Content))
	case "agent_thought_chunk":
		t.appendText(entity.MessagePartReasoning, acp.DecodeTextContent(u.Content))
	case "session_info_update":
		// The agent's own name for the session; the room adopts it when
		// nobody else has named the room.
		if title := strings.TrimSpace(u.Title); title != "" {
			t.emit(event.NewTitleEvent(title))
		}
	case "notice":
		if text := strings.TrimSpace(u.Description); text != "" {
			t.emit(event.NewNoticeEvent(enum.ParseNoticeSeverity(u.Severity), strings.TrimSpace(u.Title), text))
		}
	case "compaction_update":
		t.emit(event.NewNoticeEvent(enum.NoticeInfo, "Compacting context", "The agent is compacting its context window."))
	case "compaction_summary_chunk", "user_message_chunk":
		// The summary the agent writes of its own context is its working
		// material, and the owner's message is already in the room.

	case "plan":
		entries := strings.TrimSpace(string(u.Entries))
		if entries == "" || entries == "null" || !json.Valid(u.Entries) {
			return
		}
		// A plan update replaces the plan: one plan part per turn, kept
		// where it first appeared.
		if i := slices.IndexFunc(t.parts, func(p entity.MessagePart) bool { return p.Kind == entity.MessagePartPlan }); i >= 0 {
			t.parts[i].Text = entries
		} else {
			t.parts = append(t.parts, entity.MessagePart{Kind: entity.MessagePartPlan, Text: entries})
		}
		t.emit(event.NewPlanEvent(entries))
	case "usage_update":
		// The agent's own account of its context window: tokens held, the
		// window's size, and the session's cost so far. A report the domain
		// finds unusable (no window size) says nothing and is dropped.
		var amount float64
		var currency string
		if u.Cost != nil {
			amount, currency = u.Cost.Amount, u.Cost.Currency
		}
		if report, err := valueobject.NewAgentContext(int(u.Used), int(u.Size), amount, currency); err == nil {
			t.emit(event.NewContextEvent(report))
		}
	case "tool_call", "tool_call_update":
		if u.ToolCallID == "" {
			return
		}
		c := t.call(u.ToolCallID)
		if u.Title != "" {
			c.title = u.Title
		} else if u.Name != "" && c.title == "" {
			c.title = u.Name
		}
		if u.Kind != "" {
			c.kind = u.Kind
		}
		if raw := strings.TrimSpace(string(u.RawInput)); raw != "" && raw != "null" && raw != "{}" {
			c.input = raw
		}
		if u.Status != "" {
			c.status = u.Status
		}
		if out := toolOutput(u.Content, u.RawOutput); out != "" {
			c.output = out
		}
		// A terminal block shows a terminal the agent runs through us:
		// its output streams as the call's output.
		var blocks []struct {
			Type       string `json:"type"`
			TerminalID string `json:"terminalId"`
		}
		if json.Unmarshal(u.Content, &blocks) == nil {
			for _, b := range blocks {
				if b.Type == "terminal" && b.TerminalID != "" {
					t.terms[b.TerminalID] = c.id
				}
			}
		}
		t.progress(c)
	}
}

// addMedia puts a picture, sound, or file the agent sent into the reply, at
// the point it arrived. Bytes the agent sent inline are kept through save; a
// link counts only when it names a file inside the session's folders, which
// the owner's files API can then show. Anything else is not shown: the agent
// can still name a file in its text. Callers hold t.mu.
func (t *turnSink) addMedia(b acp.ContentBlock) {
	if t.media >= maxTurnMedia {
		return
	}
	var a valueobject.Attachment
	var err error
	switch b.Type {
	case "image", "audio":
		if b.Data != "" {
			a, err = t.keep(b.Type, b.Name, b.MIMEType, b.Data)
		} else {
			a, err = t.link(b.URI, b.Name, b.MIMEType)
		}
	case "resource_link":
		a, err = t.link(b.URI, b.Name, b.MIMEType)
	case "resource":
		switch {
		case b.Resource == nil:
			return
		case b.Resource.Blob != "":
			a, err = t.keep("file", b.Name, b.Resource.MIMEType, b.Resource.Blob)
		default:
			a, err = t.link(b.Resource.URI, b.Name, b.Resource.MIMEType)
		}
	default:
		return
	}
	if err != nil {
		return
	}
	t.media++
	t.parts = append(t.parts, entity.NewMediaPart(a))
	t.emit(event.NewMediaEvent(a))
}

// keep stores bytes the agent sent as base64 and returns the attachment
// that points at them.
func (t *turnSink) keep(kind, name, mediaType, data string) (valueobject.Attachment, error) {
	if t.save == nil {
		return valueobject.Attachment{}, fmt.Errorf("no place to keep media")
	}
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	probe, err := valueobject.NewAttachment("data:", mediaType)
	if err != nil {
		return valueobject.Attachment{}, err
	}
	if len(data) > base64.StdEncoding.EncodedLen(int(wire.MaxAttachmentBytes(probe.Modality()))) {
		return valueobject.Attachment{}, fmt.Errorf("media too large")
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(raw) == 0 {
		return valueobject.Attachment{}, fmt.Errorf("media does not decode")
	}
	if mediaType == "application/octet-stream" {
		// The agent sent bytes without naming what they are. Read them the way
		// every other caller does — bytes first, the file's own extension as
		// the tie-breaker — so a voice note the raw sniffer calls video (or
		// plain data) is still an audio attachment here.
		mediaType = dservice.SniffMediaType(raw, name)
	}
	if name = strings.TrimSpace(name); name == "" {
		ext := ""
		if exts, _ := mime.ExtensionsByType(mediaType); len(exts) > 0 {
			ext = exts[0]
		}
		name = fmt.Sprintf("%s-%d%s", kind, t.media+1, ext)
	}
	return t.save(t.ctx, name, mediaType, raw)
}

// link returns the attachment for a file the agent names, provided it is a
// file inside one of the session's folders.
func (t *turnSink) link(uri, name, mediaType string) (valueobject.Attachment, error) {
	u, err := url.Parse(strings.TrimSpace(uri))
	if err != nil || u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") {
		return valueobject.Attachment{}, fmt.Errorf("not a local file")
	}
	path := filepath.Clean(u.Path)
	inside := slices.ContainsFunc(t.folders, func(root string) bool {
		rel, err := filepath.Rel(root, path)
		return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	})
	if !inside {
		return valueobject.Attachment{}, fmt.Errorf("outside the session's folders")
	}
	if mediaType == "" {
		if mediaType = mime.TypeByExtension(filepath.Ext(path)); mediaType == "" {
			mediaType = "application/octet-stream"
		}
	}
	a, err := valueobject.NewAttachment("file://"+path, mediaType)
	if err != nil {
		return valueobject.Attachment{}, err
	}
	if name = strings.TrimSpace(name); name == "" {
		name = filepath.Base(path)
	}
	a.SetName(name)
	return a, nil
}

// beginYield marks the turn as asked to stop at its next safe point; false
// when it already was, or is over.
func (t *turnSink) beginYield() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.done || t.yielding {
		return false
	}
	t.yielding = true
	return true
}

// settled reports whether the turn is at a safe point to stop: no tool call
// the agent reported is still open, and no question waits for the owner.
func (t *turnSink) settled() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.done || t.asking > 0 {
		return false
	}
	for _, c := range t.calls {
		if c.status != "completed" && c.status != "failed" {
			return false
		}
	}
	return true
}

// notify tells the owner something about the turn while it runs.
func (t *turnSink) notify(severity enum.NoticeSeverity, title, text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.done {
		t.emit(event.NewNoticeEvent(severity, title, text))
	}
}

// call returns the record for id, creating it and its message part on
// first sight so the call keeps its position among the text parts.
func (t *turnSink) call(id string) *callRecord {
	if c := t.calls[id]; c != nil {
		return c
	}
	c := &callRecord{id: id, at: time.Now()}
	t.calls[id] = c
	t.order = append(t.order, id)
	t.parts = append(t.parts, entity.MessagePart{Kind: entity.MessagePartToolCall, ToolCallID: id})
	return c
}

// progress emits the tool-call event once the call has started running
// (agents first announce a call, then fill in its input), then streams
// output as it grows.
func (t *turnSink) progress(c *callRecord) {
	if !c.emitted && c.status != "" && c.status != "pending" {
		t.emitCall(c)
	}
	if c.emitted && c.output != c.sent {
		chunk := c.output
		if strings.HasPrefix(c.output, c.sent) {
			chunk = c.output[len(c.sent):]
		} else if c.sent != "" {
			chunk = "\n" + c.output
		}
		c.sent = c.output
		if tc, err := c.entity(); err == nil {
			t.emit(event.NewToolOutputEvent(tc, chunk))
		}
	}
	// The agent's terminal update is the explicit success/fail signal —
	// output wording is not a reliable classifier for every tool.
	if !c.resultSent && (c.status == "completed" || c.status == "failed") {
		if tc, err := c.entity(); err == nil {
			c.resultSent = true
			failed := c.status == "failed"
			tc.SetFailed(failed)
			t.emit(event.NewToolResultEvent(tc, failed))
		}
	}
}

func (t *turnSink) emitCall(c *callRecord) {
	tc, err := c.entity()
	if err != nil {
		return
	}
	c.emitted = true
	t.emit(event.NewToolCallEvent(tc))
}

// entity renders the record as a delegated tool call.
func (c *callRecord) entity() (entity.ToolCall, error) {
	name := c.title
	if name == "" {
		name = c.kind
	}
	if name == "" {
		name = "tool"
	}
	input := c.input
	if input == "" {
		input = "{}"
	}
	tc, err := entity.NewToolCall(c.id, name, input, enum.TooltypeDelegated)
	if err != nil {
		return entity.ToolCall{}, err
	}
	tc.SetCallAt(c.at)
	return tc, nil
}

// permission asks the approver about one of this turn's tool calls and
// renders the answer for the agent.
func (t *turnSink) permission(ctx context.Context, req acp.PermissionRequest) acp.PermissionResponse {
	tool := acp.ParsePermissionToolCall(req.ToolCall)
	var options []valueobject.ApprovalOption
	for _, o := range req.Options {
		choice, ok := enum.ParseApprovalChoice(o.Kind)
		if !ok {
			continue
		}
		options = append(options, valueobject.ApprovalOption{ID: o.OptionID, Name: o.Name, Choice: choice})
	}

	t.mu.Lock()
	if t.done || t.approve == nil {
		t.mu.Unlock()
		return cancelledPermission()
	}
	title, kind, input := tool.Title, tool.Kind, strings.TrimSpace(string(tool.RawInput))
	if tool.ToolCallID != "" {
		c := t.call(tool.ToolCallID)
		if title != "" {
			c.title = title
		}
		if kind != "" {
			c.kind = kind
		}
		if input != "" && input != "null" && input != "{}" {
			c.input = input
		}
		// The owner sees the call's card before the question about it.
		if !c.emitted {
			t.emitCall(c)
		}
		title, kind, input = c.title, c.kind, c.input
	}
	t.mu.Unlock()

	id, err := valueobject.NewID("apr", "")
	if err != nil {
		return cancelledPermission()
	}
	request, err := valueobject.NewApprovalRequest(id.String(), tool.ToolCallID, title, input, enum.ParseToolAction(kind), options)
	if err != nil {
		return cancelledPermission()
	}
	// The request's ctx ends with the process; the turn's ends with the
	// owner's cancel. Either one withdraws the question.
	askCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(t.ctx, cancel)
	defer stop()
	t.mu.Lock()
	t.asking++
	t.mu.Unlock()
	optionID, err := t.approve(askCtx, request)
	t.mu.Lock()
	t.asking--
	t.mu.Unlock()
	if _, ok := request.Option(optionID); err != nil || !ok {
		optionID = ""
	}
	t.recordApproval(request, optionID)
	if optionID == "" {
		return cancelledPermission()
	}
	return acp.PermissionResponse{Outcome: acp.PermissionOutcome{Outcome: "selected", OptionID: optionID}}
}

// elicitation asks the owner for the input the agent requests, while the
// turn lasts. Without an elicitor every request is declined.
func (t *turnSink) elicitation(ctx context.Context, params json.RawMessage) (valueobject.ElicitationAnswer, error) {
	var req struct {
		Mode    string          `json:"mode"`
		Message string          `json:"message"`
		Schema  json.RawMessage `json:"requestedSchema"`
		URL     string          `json:"url"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return valueobject.ElicitationAnswer{}, fmt.Errorf("invalid elicitation request")
	}
	if req.Mode == "" {
		req.Mode = "form"
	}
	id, err := valueobject.NewID("eli", "")
	if err != nil {
		return valueobject.ElicitationAnswer{}, err
	}
	e, err := valueobject.NewElicitation(id.String(), req.Mode, req.Message, req.Schema, req.URL)
	if err != nil {
		return valueobject.ElicitationAnswer{}, err
	}
	t.mu.Lock()
	done := t.done
	t.mu.Unlock()
	if done || t.elicit == nil {
		// No answer surface: the request declines at once, recorded
		// unattended so it never reads as an owner's refusal.
		answer := valueobject.ElicitationAnswer{Action: "decline", ResolvedBy: "unattended"}
		t.recordElicitation(e, answer)
		return answer, nil
	}
	askCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(t.ctx, cancel)
	defer stop()
	answer, err := t.elicit(askCtx, e)
	if err != nil {
		// The request died with its context — cancelled for the agent,
		// recorded unanswered.
		if answer.ResolvedBy == "" {
			answer.ResolvedBy = "unanswered"
		}
		answer.Action, answer.Content = "cancel", nil
	} else if answer.ResolvedBy == "" {
		answer.ResolvedBy = "owner"
	}
	t.recordElicitation(e, answer)
	return answer, nil
}

// recordElicitation keeps the request and how it resolved — the wire
// action plus its source — as a turn part; a finished turn records none.
func (t *turnSink) recordElicitation(e valueobject.Elicitation, a valueobject.ElicitationAnswer) {
	raw, _ := json.Marshal(map[string]string{"id": e.ID(), "mode": e.Mode(), "message": e.Message(), "url": e.URL(), "action": a.Action, "resolvedBy": a.ResolvedBy})
	t.mu.Lock()
	if !t.done {
		t.parts = append(t.parts, entity.MessagePart{Kind: entity.MessagePartElicitation, Text: string(raw)})
	}
	t.mu.Unlock()
}

// finish seals the turn into one assistant message. A turn that produced
// nothing and failed yields no message.
func (t *turnSink) finish(res acp.PromptResult, callErr error, cancelled bool) (*entity.Message, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Updates held for a steer still in flight belong to this turn.
	for _, u := range t.held {
		t.apply(u)
	}
	t.held, t.gated = nil, false
	t.done = true

	reason := finishReason(res.StopReason)
	if callErr != nil || cancelled {
		reason = enum.FinishReasonInterrupted
	}
	if len(t.parts) == 0 && callErr != nil {
		return nil, nil
	}
	var text strings.Builder
	for _, p := range t.parts {
		if p.Kind == entity.MessagePartText {
			text.WriteString(p.Text)
		}
	}
	msg, err := entity.NewMessage(enum.RoleAssistant, text.String())
	if err != nil {
		return nil, err
	}
	for _, id := range t.order {
		c := t.calls[id]
		tc, err := c.entity()
		if err != nil {
			continue
		}
		if !c.emitted {
			t.emit(event.NewToolCallEvent(tc))
		}
		if err := msg.AddToolCall(tc); err != nil {
			continue
		}
		out := c.output
		switch {
		case c.status == "failed" && out == "":
			out = "failed"
		case c.status != "completed" && c.status != "failed" && reason == enum.FinishReasonInterrupted:
			out = strings.TrimSpace(out + "\ninterrupted")
		}
		failed := c.status != "completed"
		_ = msg.SetToolOutput(id, out)
		_ = msg.SetToolFailed(id, failed)
		// A call still open when the prompt ends never reached a terminal
		// status; it did not succeed, so the live row must say so.
		if !c.resultSent {
			c.resultSent = true
			tc.SetFailed(failed)
			t.emit(event.NewToolResultEvent(tc, failed))
		}
	}
	msg.SetParts(t.parts)
	msg.SetFinishReason(reason)
	t.emit(event.NewFinishedEvent(reason, usage(res.Usage)))
	return &msg, nil
}

// finishReason maps an ACP stop reason onto the normalized set.
func finishReason(stop string) enum.FinishReason {
	switch stop {
	case "max_tokens", "max_turn_requests":
		return enum.FinishReasonLength
	case "refusal":
		return enum.FinishReasonContentFilter
	case "cancelled":
		return enum.FinishReasonInterrupted
	}
	return enum.FinishReasonStop
}

// usage reads the optional token accounting a prompt result may carry.
// Agents report an inclusive input total with a cached subset.
func usage(raw json.RawMessage) valueobject.Usage {
	var u struct {
		InputTokens       int `json:"inputTokens"`
		OutputTokens      int `json:"outputTokens"`
		CachedReadTokens  int `json:"cachedReadTokens"`
		CachedWriteTokens int `json:"cachedWriteTokens"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &u) != nil {
		return valueobject.Usage{}
	}
	v, err := valueobject.NewUsageFromTotal(u.InputTokens, u.CachedReadTokens, u.CachedWriteTokens, u.OutputTokens)
	if err != nil {
		return valueobject.Usage{}
	}
	return v
}

// toolOutput renders a tool call's content blocks as display text: text
// blocks verbatim, diffs as a header plus the new text. rawOutput is the
// fallback when the agent sent no content.
func toolOutput(content, rawOutput json.RawMessage) string {
	var blocks []struct {
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
		Path    string          `json:"path"`
		OldText *string         `json:"oldText"`
		NewText string          `json:"newText"`
	}
	var out []string
	if len(content) > 0 && json.Unmarshal(content, &blocks) == nil {
		for _, b := range blocks {
			switch b.Type {
			case "content":
				if text := acp.DecodeTextContent(b.Content); text != "" {
					out = append(out, text)
				}
			case "diff":
				verb := "edit"
				if b.OldText == nil {
					verb = "create"
				}
				out = append(out, verb+" "+b.Path+" ("+strconv.Itoa(strings.Count(b.NewText, "\n")+1)+" lines)")
			}
		}
	}
	if len(out) > 0 {
		return strings.Join(out, "\n")
	}
	raw := strings.TrimSpace(string(rawOutput))
	if raw == "" || raw == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(rawOutput, &s) == nil {
		return s
	}
	return raw
}

// terminal streams a terminal's output into the tool call showing it.
func (t *turnSink) terminal(u TerminalUpdate) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.done {
		return
	}
	id, ok := t.terms[u.ID]
	if !ok {
		return
	}
	c := t.calls[id]
	if c == nil {
		return
	}
	c.output = u.Output
	t.progress(c)
}

// recordApproval keeps the question and its answer ("" withdrawn) in the
// message, so the transcript shows the decision after a reload.
func (t *turnSink) recordApproval(r valueobject.ApprovalRequest, optionID string) {
	type option struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Choice string `json:"choice"`
	}
	opts := []option{}
	for _, o := range r.Options() {
		opts = append(opts, option{ID: o.ID, Name: o.Name, Choice: o.Choice.String()})
	}
	input := r.Input()
	if len(input) > 2000 {
		input = strings.ToValidUTF8(input[:2000], "") + "…"
	}
	raw, err := json.Marshal(map[string]any{"id": r.ID(), "callId": r.CallID(), "title": r.Title(), "action": r.Action().String(),
		"input": input, "options": opts, "optionId": optionID})
	if err != nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.done {
		t.parts = append(t.parts, entity.MessagePart{Kind: entity.MessagePartApproval, Text: string(raw)})
	}
}
