package entity

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// Message is a single provider-agnostic conversation entry. Tool calls and
// their results are kept on the message that produced them. The id is
// permanent — edit/regenerate features address messages by it, never by
// position (compaction shifts indexes).
type Message struct {
	id           string
	role         enum.Role
	content      string
	userType     enum.LLMUserType
	relay        valueobject.Relay // who relayed this prompt; empty id = not relayed
	finishReason enum.FinishReason
	parts        []MessagePart
	toolCalls    []ToolCall
	attachments  []valueobject.Attachment
	references   []valueobject.Reference
	annotations  []valueobject.Annotation
	// recording is the browser recording the owner attached — the ref
	// holds id/status/summary, replayed to a model as a context note.
	recording       *valueobject.RecordingRef
	optionalContent map[string]string
	createdAt       time.Time
	// sentFiles and receipt record the reply's trip to a chat: the files
	// that went with it and the furthest receipt. They are for the owner
	// to see, never replayed to a model.
	sentFiles []valueobject.Attachment
	receipt   valueobject.Receipt
	// inputID is the client request that submitted this message, when the
	// transport issued one — a steer input's id. It lets a reloaded history
	// line be matched against the live steering bubble the journal replays,
	// so one submission renders once. Empty for every message a steer did
	// not write.
	inputID string
}

// MessagePart records the provider-independent order of assistant text,
// reasoning, and tool calls. Tool-call parts refer to the call stored on
// the same message.
type MessagePart struct {
	Kind       string
	Text       string
	ToolCallID string
	// SteerAttachments are the uploads an in-flight steer carried —
	// recorded on the steer part because the folded input lands inside
	// the agent's message, not on its own user record.
	SteerAttachments []valueobject.Attachment
	// SteerContext is the steered input's rendered context — reference
	// and annotation notes — replayed to a model with the steer text but
	// never shown as message text.
	SteerContext string
}

const (
	MessagePartText      = "text"
	MessagePartReasoning = "reasoning"
	MessagePartToolCall  = "tool_call"
	// MessagePartPlan is an agent's plan: Text holds its JSON entries.
	MessagePartPlan = "plan"
	// MessagePartApproval is a permission question and its answer: Text
	// holds JSON {id, callId, title, action, options, optionId}.
	MessagePartApproval = "approval"
	// MessagePartElicitation is an agent's request for input and the
	// owner's action: Text holds JSON {id, mode, message, url, action}.
	// What the owner entered is never kept.
	MessagePartElicitation = "elicitation"
	// MessagePartSteer is input the owner sent into an agent's running
	// turn, at the point the agent took it: Text is the input.
	MessagePartSteer = "steer"
	// MessagePartMedia is a picture, sound, or file an agent put in its
	// reply, at the point it did: Text holds JSON {uri, mediaType, name}.
	// Like the rest of an agent's work it is for the owner to see, not a
	// model to replay.
	MessagePartMedia = "media"
)

// NewMediaPart records a file an agent put in its reply.
func NewMediaPart(a valueobject.Attachment) MessagePart {
	raw, _ := json.Marshal(mediaPartRecord{URI: a.URI(), MediaType: a.MediaType(), Name: a.Name(), SHA256: a.SHA256()})
	return MessagePart{Kind: MessagePartMedia, Text: string(raw)}
}

type mediaPartRecord struct {
	URI       string `json:"uri"`
	MediaType string `json:"mediaType"`
	Name      string `json:"name,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

// Media reads the file a media part records; ok is false for any other
// part or a record that no longer reads.
func (p MessagePart) Media() (valueobject.Attachment, bool) {
	if p.Kind != MessagePartMedia {
		return valueobject.Attachment{}, false
	}
	var r mediaPartRecord
	if json.Unmarshal([]byte(p.Text), &r) != nil {
		return valueobject.Attachment{}, false
	}
	a, err := valueobject.NewAttachment(r.URI, r.MediaType)
	if err != nil {
		return valueobject.Attachment{}, false
	}
	a.SetName(r.Name)
	a.SetSHA256(r.SHA256)
	return a, true
}

// Media lists the files an agent put in the reply, in order.
func (m Message) Media() []valueobject.Attachment {
	var out []valueobject.Attachment
	for _, p := range m.parts {
		if a, ok := p.Media(); ok {
			out = append(out, a)
		}
	}
	return out
}

func NewMessage(role enum.Role, content string) (Message, error) {
	id, _ := valueobject.NewID("m", "") // minted ids are valid by construction
	msg := Message{id: id.String(), role: role, content: content, createdAt: time.Now()}
	return msg, msg.validate()
}

// NewUserMessage builds a user message from one unified input — text
// plus the selections, uploads, and page marks it carries — so send,
// queued follow-up, and in-flight steer folds attach them identically.
func NewUserMessage(in valueobject.UserInput) (Message, error) {
	m, err := NewMessage(enum.RoleUser, in.Text)
	if err != nil {
		return Message{}, err
	}
	if err := m.SetReferences(in.References); err != nil {
		return Message{}, err
	}
	for _, a := range in.Attachments {
		m.AddAttachment(a)
	}
	m.SetAnnotations(in.Annotations)
	m.SetRecording(in.Recording)
	// Only a steer carries a submitting request id; the message keeps it so a
	// reloaded history line can be matched against the journal's live bubble.
	m.SetInputID(in.InputID)
	return m, nil
}

func (m Message) validate() error {
	if _, ok := enum.ParseRole(m.role.String()); !ok {
		return fmt.Errorf("role %q is invalid", m.role)
	}
	return nil
}

func (m Message) ID() string      { return m.id }
func (m Message) Role() enum.Role { return m.role }
func (m Message) Content() string { return m.content }

// Editable reports whether the message may be rewritten — real user
// input only. Synthetic user-role artifacts (compaction checkpoints
// carry userType=system) are not the user's words; rewriting one would
// forge user intent.
func (m Message) Editable() bool {
	return EditableMessage(m.role, m.userType)
}

// EditableMessage is the same rule on the storage tuple — wire records
// (repository rewrite) hold role/userType before hydration. A system payload
// and another thread's relayed prompt are not the owner's words in this room,
// so neither is rewritable here.
func EditableMessage(role enum.Role, userType enum.LLMUserType) bool {
	if role != enum.RoleUser {
		return false
	}
	if userType.SystemPayload() || userType == enum.LLMUserTypeRelay {
		return false
	}
	return true
}

// SetContent replaces the message text — the edit primitive. Only the
// aggregate decides which messages may be rewritten (user-role only).
// Selections spanned the old text, so they go with it.
func (m *Message) SetContent(v string) {
	m.content = v
	m.references = nil
}

// SetReferences records the selections the owner made in the text.
func (m *Message) SetReferences(refs []valueobject.Reference) error {
	if err := valueobject.CheckReferences(m.content, refs); err != nil {
		return err
	}
	m.references = refs
	return nil
}

func (m Message) References() []valueobject.Reference { return m.references }

// SetAnnotations records the marks the owner made on the live page for
// this message, in the order their screenshots are attached.
func (m *Message) SetAnnotations(anns []valueobject.Annotation) { m.annotations = anns }

func (m Message) Annotations() []valueobject.Annotation { return m.annotations }

// SetRecording records the browser recording the owner attached to this
// message; a ref the message replays as context.
func (m *Message) SetRecording(ref *valueobject.RecordingRef) { m.recording = ref }

func (m Message) Recording() *valueobject.RecordingRef { return m.recording }

func (m Message) UserType() enum.LLMUserType         { return m.userType }
func (m Message) Relay() valueobject.Relay           { return m.relay }
func (m Message) FinishReason() enum.FinishReason    { return m.finishReason }
func (m Message) Parts() []MessagePart               { return m.parts }
func (m Message) ToolCalls() []ToolCall              { return m.toolCalls }
func (m Message) OptionalContent() map[string]string { return m.optionalContent }

// clone returns a copy whose optionalContent map is detached from the
// receiver — sanitizers edit provider replay artifacts without mutating
// the stored history they were handed.
func (m Message) clone() Message {
	out := m
	if m.optionalContent != nil {
		out.optionalContent = make(map[string]string, len(m.optionalContent))
		maps.Copy(out.optionalContent, m.optionalContent)
	}
	return out
}

// WithToolOutputBudget returns a copy whose tool calls each show the model
// at most n bytes of output (a smaller budget a call already has stays).
// The stored output is untouched; the receiver is unchanged.
func (m Message) WithToolOutputBudget(n int) Message {
	if len(m.toolCalls) == 0 {
		return m
	}
	out := m.clone()
	out.toolCalls = slices.Clone(m.toolCalls)
	for i := range out.toolCalls {
		if out.toolCalls[i].outputBudget > n {
			out.toolCalls[i].SetOutputBudget(n)
		}
	}
	return out
}

// WithOptionalContent returns a copy carrying one provider replay artifact.
// The receiver is unchanged.
func (m Message) WithOptionalContent(key, value string) Message {
	out := m.clone()
	out.SetOptionalContent(key, value)
	return out
}

// WithoutOptionalContent returns a copy with the named provider replay
// artifacts removed. The receiver is unchanged.
func (m Message) WithoutOptionalContent(keys ...string) Message {
	out := m.clone()
	for _, key := range keys {
		delete(out.optionalContent, key)
	}
	return out
}

// SetOptionalContent stores a provider-specific replay artifact under an
// adapter-namespaced key (e.g. "gemini.thought_signature",
// "deepseek.reasoning_content"). Values are plain strings — adapters owning a
// key parse back non-string values; payloads needing structure get a
// dedicated field instead. The domain never interprets them.
func (m *Message) SetOptionalContent(key, value string) {
	if m.optionalContent == nil {
		m.optionalContent = map[string]string{}
	}
	m.optionalContent[key] = value
}

// SetUserType records the provenance of a user-role message: real user input
// vs a system payload such as compaction output.
func (m *Message) SetUserType(t enum.LLMUserType) {
	m.userType = t
}

// SetRelay records who relayed this prompt. The body stays exactly what the
// sender wrote: the head another model reads is rendered from this record when a
// request is built, never stored as text.
func (m *Message) SetRelay(r valueobject.Relay) {
	m.relay = r
}

// SetFinishReason records the normalized reason the model ended this turn.
func (m *Message) SetFinishReason(r enum.FinishReason) {
	m.finishReason = r
}

// SetParts records the ordered assistant response content.
func (m *Message) SetParts(parts []MessagePart) {
	m.parts = parts
}

func (m Message) CreatedAt() time.Time { return m.createdAt }

// SetCreatedAt restores the persisted timestamp. Repository hydration only.
func (m *Message) SetCreatedAt(t time.Time) { m.createdAt = t }

// SetID restores the persisted id. Repository hydration only — ids are
// generated at construction, never reassigned.
func (m *Message) SetID(id string) { m.id = id }

// SetInputID records the id of the client request that submitted this
// message — a steer's requestId. Only a steer sets one: an ordinary send or
// an edit submits through the client itself, so its message carries none. The
// id travels no further than the stored record and the owner-facing history
// projection; a model replay never sees it.
func (m *Message) SetInputID(id string) { m.inputID = id }

// InputID is the submitting request's id, "" when no steer wrote this message.
func (m Message) InputID() string { return m.inputID }

// AddToolCall records a call on the message. The provider-issued id is
// the only correlation key between a call and its output, so it must be
// unique within the message: a duplicate makes SetToolOutput target the
// first entry twice and leaves the second call permanently unresolved.
func (m *Message) AddToolCall(t ToolCall) error {
	for _, existing := range m.toolCalls {
		if existing.ID() == t.ID() {
			return fmt.Errorf("duplicate tool call id %q", t.ID())
		}
	}
	m.toolCalls = append(m.toolCalls, t)
	return nil
}

// AddAttachment attaches a non-text content reference (image, audio,
// video, file) to the message.
func (m *Message) AddAttachment(a valueobject.Attachment) {
	m.attachments = append(m.attachments, a)
}

func (m Message) Attachments() []valueobject.Attachment { return m.attachments }

// SentFiles are the files this reply took to a chat.
func (m Message) SentFiles() []valueobject.Attachment { return m.sentFiles }

// Receipt is the furthest receipt a chat reported for this reply.
func (m Message) Receipt() valueobject.Receipt { return m.receipt }

// RecordDelivery adds files that went to a chat with this reply (a file
// already recorded by digest or URI is not repeated) and raises the
// receipt; a receipt never goes back from read to delivered.
func (m *Message) RecordDelivery(files []valueobject.Attachment, receipt valueobject.Receipt) {
	for _, f := range files {
		dup := false
		for _, have := range m.sentFiles {
			if have.URI() == f.URI() || (f.SHA256() != "" && have.SHA256() == f.SHA256() && have.Name() == f.Name()) {
				dup = true
				break
			}
		}
		if !dup {
			m.sentFiles = append(m.sentFiles, f)
		}
	}
	if receipt.After(m.receipt) {
		m.receipt = receipt
	}
}

// SetToolOutput records the result of a previously added tool call, matching
// it by the provider-issued call id.
func (m *Message) SetToolOutput(id, output string) error {
	for i := range m.toolCalls {
		if m.toolCalls[i].ID() == id {
			m.toolCalls[i].SetOutput(output)
			return nil
		}
	}
	return errors.New("tool call not found: " + id)
}

// SetToolFailed records the terminal verdict of a tool call, matching it
// by the provider-issued call id.
func (m *Message) SetToolFailed(id string, failed bool) error {
	for i := range m.toolCalls {
		if m.toolCalls[i].ID() == id {
			m.toolCalls[i].SetFailed(failed)
			return nil
		}
	}
	return errors.New("tool call not found: " + id)
}

// SetToolOutputAttachments records media references produced by a tool
// call, matching it by the provider-issued call id. References carry no
// envelope — runtime and hydration use this same setter.
func (m *Message) SetToolOutputAttachments(id string, atts []valueobject.Attachment) error {
	for i := range m.toolCalls {
		if m.toolCalls[i].ID() == id {
			m.toolCalls[i].SetOutputAttachments(atts)
			return nil
		}
	}
	return errors.New("tool call not found: " + id)
}

// SetStoredToolOutput restores a persisted raw tool output and latency.
// Repository hydration and patch application only.
func (m *Message) SetStoredToolOutput(id, output, latency string) error {
	for i := range m.toolCalls {
		if m.toolCalls[i].ID() == id {
			m.toolCalls[i].SetStoredOutput(output, latency)
			return nil
		}
	}
	return errors.New("tool call not found: " + id)
}

// delegatedReplayOutput bounds each delegated call's output when it is
// folded into text for a model that did not run it.
const delegatedReplayOutput = 2000

// AsModelReplay returns the message as a model runtime should replay it:
// tool calls an external agent ran are not this model's calls, so they
// are folded into the reply text as a record of what happened instead of
// being replayed as calls the model would have to have issued. Input the
// owner steered into the agent's turn is folded the same way, and the
// owner's selections and live-page marks are spelled out after their
// text. A message with none of these is returned unchanged.
func (m Message) AsModelReplay() Message {
	delegated := map[string]ToolCall{}
	for _, tc := range m.toolCalls {
		if tc.ttype == enum.TooltypeDelegated {
			delegated[tc.id] = tc
		}
	}
	steered := slices.ContainsFunc(m.parts, func(p MessagePart) bool { return p.Kind == MessagePartSteer })
	selected := strings.TrimSpace(valueobject.ReferenceNote(m.references) + "\n\n" + valueobject.AnnotationNote(m.annotations) + "\n\n" + valueobject.RecordingNote(m.recording))
	if len(delegated) == 0 && !steered && selected == "" {
		return m
	}
	render := func(tc ToolCall) string {
		out := tc.output
		if len(out) > delegatedReplayOutput {
			out = out[:delegatedReplayOutput] + "…"
		}
		text := "[agent tool: " + tc.name + "]"
		if tc.input != "" && tc.input != "{}" {
			text += "\ninput: " + tc.input
		}
		if out != "" {
			text += "\noutput: " + out
		}
		return text
	}
	out := m.clone()
	out.toolCalls = nil
	for _, tc := range m.toolCalls {
		if _, ok := delegated[tc.id]; !ok {
			out.toolCalls = append(out.toolCalls, tc)
		}
	}
	var notes []string
	if len(m.parts) > 0 {
		out.parts = make([]MessagePart, 0, len(m.parts))
		for _, p := range m.parts {
			if tc, ok := delegated[p.ToolCallID]; ok && p.Kind == MessagePartToolCall {
				notes = append(notes, render(tc))
				out.parts = append(out.parts, MessagePart{Kind: MessagePartText, Text: "\n" + render(tc) + "\n"})
				continue
			}
			if p.Kind == MessagePartSteer {
				note := "[the user, mid-turn]: " + p.Text
				if p.SteerContext != "" {
					note += "\n\n" + p.SteerContext
				}
				notes = append(notes, note)
				out.parts = append(out.parts, MessagePart{Kind: MessagePartText, Text: "\n" + note + "\n"})
				continue
			}
			out.parts = append(out.parts, p)
		}
	} else {
		for _, tc := range m.toolCalls {
			if _, ok := delegated[tc.id]; ok {
				notes = append(notes, render(tc))
			}
		}
	}
	prefix := strings.Join(notes, "\n\n")
	switch {
	case prefix == "":
	case out.content == "":
		out.content = prefix
	default:
		out.content = prefix + "\n\n" + out.content
	}
	if selected != "" {
		out.content = strings.TrimSpace(out.content + "\n\n" + selected)
	}
	return out
}
