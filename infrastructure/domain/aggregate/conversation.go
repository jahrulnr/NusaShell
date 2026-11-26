package aggregate

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// Edit targets — the application layer maps them to status codes.
var (
	ErrMessageNotFound = errors.New("message not found")
	ErrNotUserMessage  = errors.New("only user messages are editable")
)

// Conversation is the aggregate root for a provider-agnostic LLM
// conversation. It holds dialogue state only; routing concerns such as
// provider style and model are decided per request by the service layer, so
// a single history can be translated to any wire and survive model fallback.
// Slice and map getters share internal storage by design — this is an
// internal domain, not a defensive boundary.
type Conversation struct {
	id           string
	providerID   string      // bound provider/account id for resume — strict DDD: aggregate roots reference by id, resolved via Provider repository
	modelID      string      // bound model id on that providerID — Send may still override per request
	effort       enum.Effort // reasoning effort the owner picked for the room; unset = the model's configured effort
	upstream     string      // aggregator backend the owner pinned for the room; "" = the provider decides
	systemPrompt string
	title        string // optional display title for conversation lists
	workspace    string // absolute directory the room's tools work in; empty = the delivery's default
	environment  enum.Environment
	agentID      string                       // bound external agent; empty = the model runtime
	agentSession string                       // the agent's own session handle, opaque to the domain
	agentOptions valueobject.ConfigSelections // the owner's picks of the agent's session options (mode, …), in apply order
	agentContext valueobject.AgentContext     // what the agent last reported about its session's context; belongs to agentSession
	agentTitle   string                       // the title an agent last gave the room; a title that differs is the owner's
	projectID    string                       // owning project; empty = none
	pinned       bool                         // kept at the top of the owner's list
	memoryOff    bool                         // the owner keeps memory out of this room
	label        string                       // short @handle chats use to name the room; "" = none
	routing      *valueobject.Routing         // nil = the server's default routing
	messages     []entity.Message
	tools        []valueobject.ToolSchema
	usage        valueobject.Usage
	// symbolMapRefusedAt is the root whose marker probe refused this room's
	// symbol-map record; "" = no verdict. It is keyed by root so a workspace
	// rebind re-evaluates by comparison, and it is a cache of a deterministic
	// probe, never a claim about the tree.
	symbolMapRefusedAt string
}

// DefaultEnvironment is the execution binding every room starts with —
// local until SetEnvironment rebinds it.
const DefaultEnvironment = enum.EnvironmentLocal

// NewConversation creates the aggregate. An empty id mints a fresh c_<hex>
// — the caller reads it back via ID().
func NewConversation(id string) (*Conversation, error) {
	vo, err := valueobject.NewID("c", id)
	if err != nil {
		return nil, err
	}
	return &Conversation{id: vo.String(), environment: DefaultEnvironment}, nil
}

// Fork returns the aggregate for a copy of this room: a fresh id, the same
// runtime bindings, workspace, environment, project, memory setting, and
// agent session options, and the same title. A room still on its default
// title keeps it, so the copy auto-titles on its first send; any other title
// gains the "(fork)" marker.
//
// The copy drops what belongs to one room rather than to its content — the
// @label, reply routing, pin, the agent's own session handle and context
// report, the title an agent gave the room, approval rules, the saved view,
// the symbol-map gate verdict, and accumulated usage — and starts with no
// history: the repository copies the stored messages in.
func (c Conversation) Fork() (*Conversation, error) {
	fork, err := NewConversation("")
	if err != nil {
		return nil, err
	}
	fork.title = c.title
	if c.title != "" && !c.TitleIsDefault() {
		fork.title = forkTitle(c.title)
	}
	if c.agentID != "" {
		// BindAgent owns the agent state: it clears the provider route, the
		// session handle, and the owner's option picks, so the picks are
		// copied after it.
		if err := fork.BindAgent(c.agentID); err != nil {
			return nil, err
		}
		fork.agentOptions = slices.Clone(c.agentOptions)
	} else {
		fork.SetProviderID(c.providerID)
		fork.SetModelID(c.modelID)
	}
	fork.effort = c.effort
	fork.upstream = c.upstream
	fork.workspace = c.workspace
	if err := fork.SetEnvironment(c.environment); err != nil {
		return nil, err
	}
	if err := fork.SetProject(c.projectID); err != nil {
		return nil, err
	}
	fork.memoryOff = c.memoryOff
	return fork, nil
}

// forkTitle marks a copy's title, keeping the marker inside the display
// bound every title respects.
func forkTitle(source string) string {
	const marker = " (fork)"
	title := titleFrom(source)
	room := maxTitleRunes - len([]rune(marker)) - 1
	if r := []rune(title); len(r) > room {
		title = strings.TrimSpace(string(r[:room])) + "…"
	}
	return title + marker
}

// Append adds a message to the conversation history.
func (c *Conversation) Append(m entity.Message) {
	c.messages = append(c.messages, m)
}

// Compact replaces messages[:dropBefore] with a summary message — the live
// history becomes [summary] + retained. retained is the caller-supplied
// surviving tail, in order; adapters may have stripped replay artifacts
// that no longer validate against the rewritten prefix. The caller must
// pick a boundary that does not split a tool-call/result pair, which would
// leave orphaned results on the wire. dropBefore is clamped to the history
// bounds.
func (c *Conversation) Compact(summary entity.Message, dropBefore int, retained []entity.Message) {
	if dropBefore < 0 {
		dropBefore = 0
	}
	if dropBefore > len(c.messages) {
		dropBefore = len(c.messages)
	}
	// dropBefore is the boundary. A retained tail that is not exactly the
	// live messages from that index (sanitizers may rewrite optional
	// content, not which messages survive) would drop history with no
	// error. Derive the tail from the aggregate instead.
	live := c.messages[dropBefore:]
	if !sameMessageIDs(live, retained) {
		retained = live
	}
	c.messages = append([]entity.Message{summary}, retained...)
	// A fold is a technical reset: the new tail is a new conversation in
	// everything but id, so the symbol-map verdict must not outlive it.
	c.symbolMapRefusedAt = ""
}

func sameMessageIDs(live, retained []entity.Message) bool {
	if len(live) != len(retained) {
		return false
	}
	for i := range live {
		if live[i].ID() != retained[i].ID() {
			return false
		}
	}
	return true
}

// CanManualCompact reports whether a manual checkpoint can fold history.
// The live tail has to be longer than the messages that stay afterwards.
// A tail that is only a previous checkpoint plus the kept messages is
// still longer than keepTail, so another explicit fold is allowed.
func (c *Conversation) CanManualCompact(keepTail int) bool {
	if c.Runtime() == enum.RuntimeAgent {
		return false
	}
	if keepTail < 0 {
		keepTail = 0
	}
	return len(c.messages) > keepTail
}

// RewriteFrom replaces a user message's content and drops every message
// after it — the edit-and-regenerate primitive: the live history ends at
// the edited message, so the caller resends the turn from that point.
// Only messages the aggregate currently holds are candidates; ids outside
// it report ErrMessageNotFound (sealed archives are the repository's
// concern — see RewriteFrom on the Conversation port).
//
// In an agent room the rewrite also drops the agent's session: the agent
// still remembers the dropped turns, so the next turn opens a fresh
// session that gets this record's recap instead. A rewrite the repository
// does on a sealed segment follows the same rule through ForgetAgentSession.
func (c *Conversation) RewriteFrom(msgID, content string) error {
	i, err := c.editTarget(msgID)
	if err != nil {
		return err
	}
	c.messages[i].SetContent(content)
	dropped := c.messages[i+1:]
	c.messages = c.messages[:i+1]
	// Dead region still aliases the backing array — zero it or every
	// edited-away message stays reachable for the room's lifetime.
	clear(dropped)
	c.ForgetAgentSession()
	return nil
}

// editTarget finds the message a rewrite replaces: the user line with that id,
// when the aggregate holds it and it is the owner's own words. RewriteFrom and
// EditCapture both decide through it, so they cannot disagree.
func (c *Conversation) editTarget(msgID string) (int, error) {
	for i := range c.messages {
		if c.messages[i].ID() != msgID {
			continue
		}
		if !c.messages[i].Editable() {
			// Synthetic user-role messages (compaction checkpoints) are
			// not the user's words — editing one would rewrite a system
			// artifact as if the user had said it.
			return 0, ErrNotUserMessage
		}
		return i, nil
	}
	return 0, ErrMessageNotFound
}

// EditCapture reads what RewriteFrom would replace and drop: the target's
// current text and a copy of every message after it. It changes nothing, and ok
// is false wherever RewriteFrom would refuse. The copy is a shallow clone, which
// is enough: RewriteFrom clears its own backing array, so the returned slice is
// the only remaining reference to the dropped messages.
func (c *Conversation) EditCapture(msgID string) (previous string, tail []entity.Message, ok bool) {
	i, err := c.editTarget(msgID)
	if err != nil {
		return "", nil, false
	}
	return c.messages[i].Content(), slices.Clone(c.messages[i+1:]), true
}

// ClearHistory empties the room's dialogue while keeping its identity: the
// messages go, and the agent's session handle and context report go with them
// so the next turn opens a fresh agent-side session. Everything else — title,
// provider or agent binding and its options, workspace, environment, project,
// label, routing, pin, memory setting, and accumulated usage — stays. The
// repository's Clear persists exactly this post-state.
func (c *Conversation) ClearHistory() {
	clear(c.messages)
	c.messages = nil
	c.symbolMapRefusedAt = ""
	c.forgetSession()
}

// ForgetAgentSession drops the agent's session handle after this record
// stopped matching what the agent remembers (a history rewrite); the next
// agent turn opens a fresh session with a recap.
func (c *Conversation) ForgetAgentSession() { c.forgetSession() }

// forgetSession drops the agent's session handle and with it the context
// report, which described that session and means nothing for the next one.
func (c *Conversation) forgetSession() {
	c.agentSession = ""
	c.agentContext = valueobject.AgentContext{}
}

// Last returns the most recent message, or nil when the history is empty.
// The pointer aliases the live history — it detaches silently once Append
// reallocates the backing array, so callers must re-fetch after appending.
func (c *Conversation) Last() *entity.Message {
	if len(c.messages) == 0 {
		return nil
	}
	return &c.messages[len(c.messages)-1]
}

// maxTitleRunes bounds a display title.
const maxTitleRunes = 80

// titleFrom collapses a display-title candidate to a label: its first
// line with the whitespace folded, bounded — a title names a thread, so
// it never carries a message body or the header lines one starts with.
func titleFrom(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	line = strings.Join(strings.Fields(line), " ")
	if r := []rune(line); len(r) > maxTitleRunes {
		line = strings.TrimSpace(string(r[:maxTitleRunes-1])) + "…"
	}
	return line
}

// SetTitle records the display title shown in conversation lists — set by
// the application (user rename or auto-generated from the first turn).
// The candidate normalizes to one bounded line.
func (c *Conversation) SetTitle(title string) {
	c.title = titleFrom(title)
}

// TitleIsDefault reports whether the title is still the one a room starts
// with: the first message's head, normalized the way SetTitle stores it.
// A room titled any other way was named by someone — the owner, a title
// model, or an agent.
func (c Conversation) TitleIsDefault() bool {
	for _, m := range c.messages {
		if m.Role() == enum.RoleUser && !m.UserType().SystemPayload() {
			return c.title == titleFrom(m.Content())
		}
	}
	return false
}

// AdoptAgentTitle takes the title the bound agent suggests, and reports
// whether the room's title changed. An agent names a room that has not been
// named: its title is still the default, or is the one the agent gave
// before. A title anyone else set stays.
func (c *Conversation) AdoptAgentTitle(title string) bool {
	// An agent's suggestion folds to one line rather than cutting at
	// its first — it means a title, not a message — under the same bound.
	title = strings.Join(strings.Fields(title), " ")
	if r := []rune(title); len(r) > maxTitleRunes {
		title = strings.TrimSpace(string(r[:maxTitleRunes-1])) + "…"
	}
	if c.agentID == "" || title == "" || title == c.title {
		return false
	}
	if c.title != "" && !c.TitleIsDefault() && c.title != c.agentTitle {
		return false
	}
	c.title, c.agentTitle = title, title
	return true
}

// AgentTitle is the title an agent last gave the room, "" when none did.
func (c Conversation) AgentTitle() string { return c.agentTitle }

// RestoreAgentTitle records the title an agent gave the room, as stored.
// Hydration only.
func (c *Conversation) RestoreAgentTitle(title string) { c.agentTitle = title }

// SetSystemPrompt records the conversation-level instruction. Adapters map
// it to the provider-specific channel (instructions, system param, or a
// system/developer message).
func (c *Conversation) SetSystemPrompt(prompt string) {
	c.systemPrompt = prompt
}

// SetTools records this conversation's registered tool catalog — including
// harness-side `ToolPermissionSystem` entries like the workspace record.
// Adapters emit only the `CallableByAgent` subset as the provider's tool
// definition array.
func (c *Conversation) SetTools(tools []valueobject.ToolSchema) {
	c.tools = tools
}

// SetProviderID binds the conversation to a provider (provider/account selector) for
// resume. The Provider itself is resolved via its own repository — aggregate
// roots reference each other by id only.
func (c *Conversation) SetProviderID(providerID string) {
	c.providerID = providerID
}

// SetModelID binds the conversation to a model id on its provider for resume.
// Send may still override per request.
func (c *Conversation) SetModelID(modelID string) {
	c.modelID = modelID
}

// SetEffort stores the reasoning effort the owner picked for the room, so a
// turn started anywhere — another browser, a chat, an automation — runs with
// it. Default and the empty effort both mean "the model's configured effort"
// and are stored as empty.
func (c *Conversation) SetEffort(e enum.Effort) {
	if e.IsUnset() {
		e = ""
	}
	c.effort = e
}

// SetUpstream stores the aggregator backend the owner pinned for the room;
// empty lets the provider decide.
func (c *Conversation) SetUpstream(name string) {
	c.upstream = name
}

// SetWorkspace binds the conversation to the path its tools work in within
// the selected environment — file tools confine to it, exec runs there.
// Like the provider binding it persists with the room; empty defers to the
// selected driver's default workspace.
func (c *Conversation) SetWorkspace(dir string) {
	c.workspace = dir
}

// SymbolMapRefusedAt names the root whose marker probe refused this room's
// symbol-map record, "" when no verdict is remembered. The gate reads it to
// skip a root it already refused; the room's own transitions — Compact and
// ClearHistory — drop it, and a different root re-evaluates by comparison,
// which is how a rebind re-opens the question without a write.
func (c *Conversation) SymbolMapRefusedAt() string {
	return c.symbolMapRefusedAt
}

// RefuseSymbolMap remembers that root carries no project marker, so the room
// pays one probe per episode rather than one per provider step.
func (c *Conversation) RefuseSymbolMap(root string) {
	c.symbolMapRefusedAt = root
}

// AllowSymbolMap drops a remembered refusal: the root now proves a project
// (or the room folded, cleared, or moved, which voids the verdict anyway).
func (c *Conversation) AllowSymbolMap() {
	c.symbolMapRefusedAt = ""
}

// SetEnvironment binds this room to one execution environment. A registered
// driver is checked by the application before work starts; the domain only
// validates the known identity.
func (c *Conversation) SetEnvironment(environment enum.Environment) error {
	if _, ok := enum.ParseEnvironment(environment.String()); !ok {
		return fmt.Errorf("unknown environment %q", environment)
	}
	c.environment = environment
	return nil
}

// BindAgent hands this room's turns to an external agent. The room may
// switch runtime at any point: the agent's session handle belongs to the
// agent that issued it, so a change of agent drops it and the next turn
// opens a fresh agent-side session. The provider binding no longer applies;
// modelID is reused for the agent's own model choice.
func (c *Conversation) BindAgent(agentID string) error {
	if err := valueobject.ValidID(agentID); err != nil {
		return fmt.Errorf("agent id: %w", err)
	}
	if agentID == c.agentID {
		return nil
	}
	c.agentID = agentID
	c.forgetSession()
	c.agentOptions = nil
	// The provider binding goes with what only a provider call can honour:
	// its model, and the two picks that ride it — effort and upstream. An
	// agent picks its own model and reasoning, so a room that kept them
	// would show a value nothing applies and hand it back on a later unbind.
	c.providerID, c.modelID = "", ""
	c.effort, c.upstream = "", ""
	return nil
}

// UnbindAgent returns the room to the model runtime. The agent's session
// handle is dropped: turns that run elsewhere are turns that agent never
// saw, so resuming its old session later would hide them from it.
func (c *Conversation) UnbindAgent() {
	if c.agentID == "" {
		return
	}
	c.agentID = ""
	c.forgetSession()
	c.agentOptions = nil
	c.providerID, c.modelID = "", ""
}

// SetAgentOption records the owner's pick for one of the bound agent's
// session options; an empty value returns it to the agent's default. The
// runtime applies picks before each turn. Option ids and values are the
// agent's own.
func (c *Conversation) SetAgentOption(id, value string) error {
	if c.agentID == "" {
		return fmt.Errorf("agent options need a room bound to an agent")
	}
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 128 || len(value) > 256 {
		return fmt.Errorf("invalid agent option")
	}
	if value == "" {
		c.agentOptions = slices.DeleteFunc(c.agentOptions, func(s valueobject.ConfigSelection) bool { return s.ID == id })
		return nil
	}
	// A pick for an id already stored keeps its place in the apply order;
	// a new one appends — agent defaults first, room picks after.
	if i := slices.IndexFunc(c.agentOptions, func(s valueobject.ConfigSelection) bool { return s.ID == id }); i >= 0 {
		c.agentOptions[i].Value = value
	} else {
		c.agentOptions = append(c.agentOptions, valueobject.ConfigSelection{ID: id, Value: value})
	}
	return nil
}

// AgentOptions are the owner's picks of the agent's session options, in
// apply order.
func (c Conversation) AgentOptions() valueobject.ConfigSelections {
	return slices.Clone(c.agentOptions)
}

// SetProject moves the room into a project, or out of any with "". While in
// a project the room works in the project's folders rather than its own
// workspace binding.
func (c *Conversation) SetProject(projectID string) error {
	if projectID != "" {
		if err := valueobject.ValidID(projectID); err != nil {
			return fmt.Errorf("project id: %w", err)
		}
	}
	c.projectID = projectID
	return nil
}

// SetLabel sets the room's handle. The label must already be normalized and
// valid; uniqueness across rooms is checked by whoever assigns it. "" clears.
func (c *Conversation) SetLabel(label string) error {
	if label != "" && !valueobject.ValidLabel(label) {
		return fmt.Errorf("invalid label %q: use 1–24 of a–z, 0–9 and inner hyphens", label)
	}
	c.label = label
	return nil
}

// SetRouting overrides where replies go; nil returns to the default.
func (c *Conversation) SetRouting(r *valueobject.Routing) {
	if r == nil {
		c.routing = nil
		return
	}
	v := *r
	c.routing = &v
}

// RoutingOr returns the room's routing, or def when it has no override.
func (c Conversation) RoutingOr(def valueobject.Routing) valueobject.Routing {
	if c.routing != nil {
		return *c.routing
	}
	return def
}

// SetPinned keeps the room at the top of the owner's list.
func (c *Conversation) SetPinned(pinned bool) { c.pinned = pinned }

// SetMemoryOff keeps memory out of the room: it reads no memory, saves
// none, and its history is not read for suggestions.
func (c *Conversation) SetMemoryOff(off bool) { c.memoryOff = off }

// SetAgentSession records the bound agent's session handle so a later turn
// continues the same agent-side context. Hydration restores it as stored. A
// different handle is a different session, so the context report goes.
func (c *Conversation) SetAgentSession(session string) {
	if session != c.agentSession {
		c.agentContext = valueobject.AgentContext{}
	}
	c.agentSession = session
}

// ReportAgentContext keeps the latest account the bound agent gave of its
// session's context. It replaces the previous report; a room with no agent
// has no such session, so the report is ignored.
func (c *Conversation) ReportAgentContext(a valueobject.AgentContext) {
	if c.agentID != "" {
		c.agentContext = a
	}
}

// AgentContext is the agent's latest report about its session's context;
// the zero value until it reports one.
func (c Conversation) AgentContext() valueobject.AgentContext { return c.agentContext }

// ModelReplay is the history as a model runtime sends it: every message
// with the tool calls an external agent ran folded into text. The live
// history is untouched.
func (c Conversation) ModelReplay() []entity.Message {
	out := make([]entity.Message, len(c.messages))
	for i, m := range c.messages {
		out[i] = m.AsModelReplay()
	}
	return out
}

// Runtime reports who runs this room's turns.
func (c Conversation) Runtime() enum.Runtime {
	if c.agentID != "" {
		return enum.RuntimeAgent
	}
	return enum.RuntimeModel
}

// AddUsage accumulates the token accounting reported for one request —
// providers report per call, the conversation tracks the running total.
func (c *Conversation) AddUsage(u valueobject.Usage) {
	c.usage = c.usage.Add(u)
}

// EstimatedTokens approximates the conversation's context size in tokens
// using a chars/4 heuristic over everything adapters replay onto the wire:
// system prompt, agent-offered tool schemas, message content,
// optionalContent values (signatures, reasoning content, thinking payloads),
// and tool call input/output.
// Attachments — message-level and tool-produced alike — are billed by
// providers via media tokenization (image tiles, audio/video duration),
// not characters — each contributes a flat attachmentTokenEstimate
// regardless of URI form. The figure is sized for an image as the wire sends
// it, at most 2048 px on the long side: a 16:9 frame at that size costs about
// 3000 tokens (width × height / 750, the rule providers publish), so a lower
// figure left a thread that had loaded a few dozen frames looking far
// smaller than it was.
// Per-model tokenizers differ so no exact count exists — use this for
// budgeting/compaction decisions only; Usage() holds the provider-reported
// truth for billing.
const attachmentTokenEstimate = 3000

func (c *Conversation) EstimatedTokens() int {
	chars := len(c.systemPrompt)
	for _, t := range c.tools {
		if !t.CallableByAgent() {
			continue
		}
		chars += len(t.Name()) + len(t.Description()) + len(t.Parameters())
	}
	flat := 0
	for _, m := range c.messages {
		chars += len(m.Content())
		flat += len(m.Attachments()) * attachmentTokenEstimate
		for _, v := range m.OptionalContent() {
			chars += len(v)
		}
		for _, tc := range m.ToolCalls() {
			chars += len(tc.Input()) + tc.OutputWithUntrustedTagLen()
			flat += len(tc.OutputAttachments()) * attachmentTokenEstimate
		}
	}
	return chars/4 + flat
}

// NeedCompaction reports whether the estimated context size has reached
// budget — the caller derives it via model.CompactionBudgetTokens so
// headroom stays reserved for the reply and a model switch is reflected
// immediately. Budget <= 0 fails open: compaction is a budgeting concern,
// never a hard gate.
func (c *Conversation) NeedCompaction(budget int) bool {
	return budget > 0 && c.EstimatedTokens() >= budget
}

// MediaCount reports how many live attachments carry the modality —
// user-side and tool-produced alike. Every replayed request re-sends
// them, so the count is what a per-request media limit checks against.
// It counts what the history holds, not what the wire will emit —
// adapters may degrade some references at encode time, so the real
// request can carry fewer; the gate errs on the conservative side.
func (c *Conversation) MediaCount(mod enum.Modality) int {
	n := 0
	for _, m := range c.messages {
		for _, a := range m.Attachments() {
			if a.Modality() == mod {
				n++
			}
		}
		for _, tc := range m.ToolCalls() {
			for _, a := range tc.OutputAttachments() {
				if a.Modality() == mod {
					n++
				}
			}
		}
	}
	return n
}

func (c Conversation) ID() string                    { return c.id }
func (c Conversation) ProviderID() string            { return c.providerID }
func (c Conversation) ModelID() string               { return c.modelID }
func (c Conversation) Effort() enum.Effort           { return c.effort }
func (c Conversation) Upstream() string              { return c.upstream }
func (c Conversation) Workspace() string             { return c.workspace }
func (c Conversation) Environment() enum.Environment { return c.environment }
func (c Conversation) AgentID() string               { return c.agentID }
func (c Conversation) AgentSession() string          { return c.agentSession }
func (c Conversation) ProjectID() string             { return c.projectID }
func (c Conversation) Pinned() bool                  { return c.pinned }
func (c Conversation) MemoryOff() bool               { return c.memoryOff }
func (c Conversation) Label() string                 { return c.label }
func (c Conversation) Routing() *valueobject.Routing {
	if c.routing == nil {
		return nil
	}
	v := *c.routing
	return &v
}
func (c Conversation) SystemPrompt() string            { return c.systemPrompt }
func (c Conversation) Title() string                   { return c.title }
func (c Conversation) Messages() []entity.Message      { return c.messages }
func (c Conversation) Tools() []valueobject.ToolSchema { return c.tools }
func (c Conversation) Usage() valueobject.Usage        { return c.usage }
