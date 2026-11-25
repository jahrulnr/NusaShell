package conversation

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// The DTOs below are the contract's wire projections — every field is
// explicitly chosen; no domain entity is marshaled. Message parts preserve
// the assistant's text, reasoning, and tool-call order for the owner UI.
// Provider replay artifacts, attachment sha256 pins, and error causes stay
// internal. Tool input/output and attachment uri/name cross as inert text.

type conversationDTO struct {
	ID         string `json:"id"`
	Title      string `json:"title,omitempty"`
	ProviderID string `json:"providerId"`
	ModelID    string `json:"modelId"`
	// Effort and Upstream are the reasoning effort and aggregator backend
	// the owner picked for the room; absent = the model's and the
	// provider's own.
	Effort   string `json:"effort,omitempty"`
	Upstream string `json:"upstream,omitempty"`
	// Agent names the external agent running this room; empty means the
	// room runs on the provider route.
	AgentID   string `json:"agentId,omitempty"`
	ProjectID string `json:"projectId,omitempty"`
	Pinned    bool   `json:"pinned,omitempty"`
	MemoryOff bool   `json:"memoryOff,omitempty"`
	Label     string `json:"label,omitempty"`
	// Routing is the room's reply routing override; absent = the default.
	Routing        *valueobject.Routing   `json:"routing,omitempty"`
	Workspace      string                 `json:"workspace,omitempty"`
	Environment    enum.Environment       `json:"environment"`
	Usage          transport.UsagePayload `json:"usage"`
	CreatedAt      time.Time              `json:"createdAt"`
	UpdatedAt      time.Time              `json:"updatedAt"`
	MessageCount   int                    `json:"messageCount"`
	ActiveCount    int                    `json:"activeCount"`
	SealedSegments int                    `json:"sealedSegments"`
	CanCompact     *bool                  `json:"canCompact,omitempty"`
	// ActiveTurnID is the hub's in-progress transport turn for this room.
	// Detail and list items set it while a non-terminal turn exists;
	// writes leave it empty so it stays off the wire. It is not
	// persisted conversation state.
	ActiveTurnID   string `json:"activeTurnId,omitempty"`
	ActiveTurnKind string `json:"activeTurnKind,omitempty"`
	// Waiting is how many jobs queue behind that turn, read from the
	// worker queue itself. Detail and list items set it, so a tab that
	// never saw a `conversation.queued` frame still opens on the real
	// count; writes leave it empty, like the turn above. Not persisted.
	Waiting int `json:"waiting,omitempty"`
	// TurnActive says work is executing in this room now — a turn, a fold, or a
	// fork. It is not ActiveTurnID: that names a turn a client can watch and
	// stop, while a fold has no stream and still keeps the owner waiting. Absent
	// on an idle room, so the wire shape does not change for one.
	TurnActive bool `json:"turnActive,omitempty"`
	// Pending is a pick the room has taken but not applied yet: a turn's
	// runtime and session options are fixed when it starts, so the pick waits
	// for the next one. Absent when nothing waits. Detail and list items set
	// it, so the "applies to the next turn" line survives a reload.
	Pending *pendingDTO `json:"pending,omitempty"`
}

// pendingDTO is one room's waiting pick — the object a 202 answers with and the
// room projection carries, so a tab renders the same thing before and after a
// reload. It is never a claim that the pick is applied.
type pendingDTO struct {
	// One runtime only: an agent pick names the model the agent reports, a
	// provider pick names its catalog entry.
	AgentID    string `json:"agentId,omitempty"`
	ModelID    string `json:"modelId,omitempty"`
	ProviderID string `json:"providerId,omitempty"`
	Effort     string `json:"effort,omitempty"`
	Upstream   string `json:"upstream,omitempty"`
	// Options is an array, sorted by id: a map would reorder between reads and
	// the surface showing it must not flicker.
	Options    []pendingOption `json:"options,omitempty"`
	AcceptedAt time.Time       `json:"acceptedAt"`
}

type pendingOption struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

// toPending projects a parked selection. The selection stores an agent's model
// beside the agent id; on the wire both runtimes use modelId.
func toPending(sel drepo.PendingSelection) *pendingDTO {
	out := &pendingDTO{
		AgentID: sel.AgentID, ProviderID: sel.ProviderID,
		Upstream: deref(sel.Upstream), AcceptedAt: sel.AcceptedAt,
	}
	if sel.Effort != nil {
		out.Effort = sel.Effort.String()
	}
	out.ModelID = sel.ModelID
	if sel.AgentID != "" {
		out.ModelID = sel.AgentModel
	}
	ids := make([]string, 0, len(sel.Options))
	for id := range sel.Options {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		out.Options = append(out.Options, pendingOption{ID: id, Value: sel.Options[id]})
	}
	return out
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func toConversation(i drepo.ConversationInfo) conversationDTO {
	return conversationDTO{
		ID:             i.ID,
		Title:          i.Title,
		ProviderID:     i.ProviderID,
		ModelID:        i.ModelID,
		Effort:         i.Effort,
		Upstream:       i.Upstream,
		AgentID:        i.AgentID,
		ProjectID:      i.ProjectID,
		Pinned:         i.Pinned,
		MemoryOff:      i.MemoryOff,
		Label:          i.Label,
		Routing:        i.Routing,
		Workspace:      i.Workspace,
		Environment:    i.Environment,
		Usage:          transport.MarshalUsage(i.Usage),
		CreatedAt:      i.CreatedAt,
		UpdatedAt:      i.UpdatedAt,
		MessageCount:   i.MessageCount,
		ActiveCount:    i.ActiveCount,
		SealedSegments: i.SealedSegments,
	}
}

// toolCallDTO is the owner-visible call record — id, name, provenance
// (model-originated vs application-injected), lifecycle status, the exact
// input string the model produced, when the call was
// recorded, the plain result text when one exists, and any
// media the tool produced. Tool I/O intentionally crosses to the local
// owner UI; optionalContent and key material never do. Output and
// calledAt are pointers so absent facts omit the key rather than
// inventing a result or the zero time.
type toolCallDTO struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Type        string          `json:"type"`   // "origin" | "injection"
	Status      string          `json:"status"` // "pending" | "completed" | "failed"
	Input       string          `json:"input"`
	CalledAt    *time.Time      `json:"calledAt,omitempty"`
	Output      *string         `json:"output,omitempty"`
	Attachments []attachmentDTO `json:"attachments,omitempty"`
}

func toToolCall(t entity.ToolCall) toolCallDTO {
	d := toolCallDTO{
		ID:     t.ID(),
		Name:   t.Name(),
		Type:   t.Type().String(),
		Status: "completed",
		Input:  t.Input(),
	}
	if at := t.CallAt(); !at.IsZero() {
		d.CalledAt = &at
	}
	if t.HasOutput() {
		plain := t.Output()
		d.Output = &plain
		if t.Failed() {
			d.Status = "failed"
		}
	} else {
		d.Status = "pending"
	}
	for _, a := range t.OutputAttachments() {
		d.Attachments = append(d.Attachments, toAttachment(a))
	}
	return d
}

// attachmentDTO projects the stored reference — uri (a `file://` local
// path or remote URL is owner-visible by design on this loopback UI)
// and the optional display name (stored names can carry path-shaped
// data; the client renders them as inert text). The sha256 content pin
// stays internal — it is an adapter integrity check, not content.
type attachmentDTO struct {
	MediaType string `json:"mediaType"`
	Modality  string `json:"modality"`
	URI       string `json:"uri"`
	Name      string `json:"name,omitempty"`
}

func toAttachment(a valueobject.Attachment) attachmentDTO {
	return attachmentDTO{
		MediaType: a.MediaType(),
		Modality:  a.Modality().String(),
		URI:       a.URI(),
		Name:      a.Name(),
	}
}

type messageDTO struct {
	ID       string           `json:"id"`
	Role     string           `json:"role"`
	Content  string           `json:"content"`
	Parts    []messagePartDTO `json:"parts,omitempty"`
	UserType string           `json:"userType,omitempty"`
	// Relay is a relayed prompt's provenance: who asked, and the name it held
	// then. Present beside userType ("relay") and nowhere else — the head is
	// never stored, so a client renders its header from this record.
	Relay *relayDTO `json:"relay,omitempty"`
	// InputID is the client request that submitted the message — a steer's
	// requestId — so a reloaded history line can be matched against the live
	// steering bubble the turn journal replays. Present on steer-written user
	// messages only.
	InputID     string          `json:"inputId,omitempty"`
	Finish      string          `json:"finishReason,omitempty"`
	ToolCalls   []toolCallDTO   `json:"toolCalls,omitempty"`
	Attachments []attachmentDTO `json:"attachments,omitempty"`
	References  []referenceDTO  `json:"references,omitempty"`
	Annotations []annotationDTO `json:"annotations,omitempty"`
	// Recording is the browser recording a user message attached; only
	// id and status reach the UI — the summary is model context.
	Recording *recordingDTO `json:"recording,omitempty"`
	// SentFiles went to a chat with this reply; Receipt is how far the
	// chat confirmed it ("delivered" or "read").
	SentFiles []attachmentDTO `json:"sentFiles,omitempty"`
	Receipt   string          `json:"receipt,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
}

// referenceDTO is a selection in the message text; start and end are
// byte offsets into content.
type referenceDTO struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Label string `json:"label"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

type messagePartDTO struct {
	Kind       string `json:"kind"`
	Text       string `json:"text,omitempty"`
	ToolCallID string `json:"toolCallId,omitempty"`
	// Attachments are the uploads an in-flight steer carried — only on
	// steer parts.
	Attachments []attachmentDTO `json:"attachments,omitempty"`
}

func toMessage(m entity.Message) messageDTO {
	out := messageDTO{
		ID:        m.ID(),
		Role:      m.Role().String(),
		Content:   m.Content(),
		Finish:    m.FinishReason().String(),
		CreatedAt: m.CreatedAt(),
	}
	// userType is only meaningful on user-role messages — it marks real
	// input ("origin") vs synthetic artifacts like compaction checkpoints.
	if m.Role() == enum.RoleUser {
		out.UserType = m.UserType().String()
		out.InputID = m.InputID()
		if r := m.Relay(); r.ID != "" {
			out.Relay = &relayDTO{ID: r.ID, Label: r.Label, Title: r.Title}
		}
	}
	for _, t := range m.ToolCalls() {
		out.ToolCalls = append(out.ToolCalls, toToolCall(t))
	}
	for _, part := range m.Parts() {
		text := part.Text
		if part.Kind == entity.MessagePartMedia {
			// A file the agent put in its reply is served as any
			// attachment is: without the content pin.
			a, ok := part.Media()
			if !ok {
				continue
			}
			raw, _ := json.Marshal(toAttachment(a))
			text = string(raw)
		}
		partDTO := messagePartDTO{
			Kind:       part.Kind,
			Text:       text,
			ToolCallID: part.ToolCallID,
		}
		for _, a := range part.SteerAttachments {
			partDTO.Attachments = append(partDTO.Attachments, toAttachment(a))
		}
		out.Parts = append(out.Parts, partDTO)
	}
	for _, a := range m.Attachments() {
		out.Attachments = append(out.Attachments, toAttachment(a))
	}
	for _, a := range m.SentFiles() {
		out.SentFiles = append(out.SentFiles, toAttachment(a))
	}
	out.Receipt = string(m.Receipt())
	for _, an := range m.Annotations() {
		out.Annotations = append(out.Annotations, annotationDTO{Marker: an.Marker, Comment: an.Comment, Screenshot: an.Screenshot, Descriptor: an.Descriptor})
	}
	if rec := m.Recording(); rec != nil {
		out.Recording = &recordingDTO{ID: rec.ID, Status: rec.Status}
	}
	for _, r := range m.References() {
		out.References = append(out.References, referenceDTO{Kind: r.Kind().String(), ID: r.ID(), Label: r.Label(), Start: r.Start(), End: r.End()})
	}
	return out
}

// recordingDTO is the browser recording a user message carried.
type recordingDTO struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// annotationDTO is a live-page mark a user message carried.
type annotationDTO struct {
	Marker     int                              `json:"marker"`
	Comment    string                           `json:"comment,omitempty"`
	Screenshot bool                             `json:"screenshot,omitempty"`
	Descriptor valueobject.AnnotationDescriptor `json:"descriptor"`
}

// relayDTO is a relayed prompt's sender. Every field is a string, empty when
// unknown: a title-less room relays with Title "" and the client falls back to
// the id, so no consumer guards a null.
type relayDTO struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Title string `json:"title"`
}
