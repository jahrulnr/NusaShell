package repoconversation

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// metaRecord is meta.json — conversation-level state plus the counters
// Info serves, so inspection never scans message files.
type metaRecord struct {
	ID             string                       `json:"id"`
	Title          string                       `json:"title,omitempty"`
	ProviderID     string                       `json:"providerId,omitempty"`
	ModelID        string                       `json:"modelId,omitempty"`
	Effort         string                       `json:"effort,omitempty"`
	Upstream       string                       `json:"upstream,omitempty"`
	Workspace      string                       `json:"workspace,omitempty"`
	Environment    string                       `json:"environment"`
	AgentID        string                       `json:"agentId,omitempty"`
	ProjectID      string                       `json:"projectId,omitempty"`
	Pinned         bool                         `json:"pinned,omitempty"`
	MemoryOff      bool                         `json:"memoryOff,omitempty"`
	Label          string                       `json:"label,omitempty"`
	Routing        *routingRecord               `json:"routing,omitempty"`
	AgentSession   string                       `json:"agentSession,omitempty"`
	AgentOptions   valueobject.ConfigSelections `json:"agentOptions,omitempty"`
	AgentContext   *agentContextRecord          `json:"agentContext,omitempty"`
	AgentTitle     string                       `json:"agentTitle,omitempty"`
	Tools          []toolSchemaRecord           `json:"tools,omitempty"`
	Usage          usageRecord                  `json:"usage"`
	MessageCount   int                          `json:"messageCount"`
	ActiveCount    int                          `json:"activeCount"`
	SealedSegments int                          `json:"sealedSegments"`
	CreatedAt      time.Time                    `json:"createdAt"`
	UpdatedAt      time.Time                    `json:"updatedAt"`
	// SymbolMapRefusedAt is the root the symbol-map gate refused; absent
	// means "never evaluated", so no migration is needed for older records.
	SymbolMapRefusedAt string `json:"symbolMapRefusedAt,omitempty"`
	// Terminal is the last turn's outcome. Absent on rooms written before
	// the field existed, which is "nothing stored", not a completed turn.
	Terminal string `json:"terminal,omitempty"`
}

type routingRecord struct {
	ReplyToOrigin   bool `json:"replyToOrigin"`
	MirrorFinal     bool `json:"mirrorFinal"`
	NotifyApprovals bool `json:"notifyApprovals"`
}

// metaFor projects the aggregate onto its record. Record-level fields —
// counters and timestamps — come from storage, so the caller sets them: Save
// preserves what is stored, Clear zeroes the counters, and Fork copies the
// source's.
func metaFor(conv *aggregate.Conversation) metaRecord {
	m := metaRecord{
		ID:           conv.ID(),
		Title:        conv.Title(),
		ProviderID:   conv.ProviderID(),
		ModelID:      conv.ModelID(),
		Effort:       conv.Effort().String(),
		Upstream:     conv.Upstream(),
		Workspace:    conv.Workspace(),
		Environment:  conv.Environment().String(),
		AgentID:      conv.AgentID(),
		ProjectID:    conv.ProjectID(),
		Pinned:       conv.Pinned(),
		MemoryOff:    conv.MemoryOff(),
		Label:        conv.Label(),
		AgentSession: conv.AgentSession(),
		AgentOptions: conv.AgentOptions(),
		AgentTitle:   conv.AgentTitle(),
		Usage:        encodeUsage(conv.Usage()),
		// The symbol-map verdict is room state, not history: one root (or
		// "" for none), dropped by the aggregate's own transitions.
		SymbolMapRefusedAt: conv.SymbolMapRefusedAt(),
		Terminal:           conv.Terminal(),
		// SystemPrompt is deliberately absent: the prompt ships embedded in
		// the binary, so it is applied in memory, never persisted.
	}
	if a := conv.AgentContext(); !a.IsZero() {
		amount, currency, _ := a.Cost()
		m.AgentContext = &agentContextRecord{Used: a.Used(), Size: a.Size(), CostAmount: amount, CostCurrency: currency}
	}
	if r := conv.Routing(); r != nil {
		m.Routing = &routingRecord{ReplyToOrigin: r.ReplyToOrigin, MirrorFinal: r.MirrorFinal, NotifyApprovals: r.NotifyApprovals}
	}
	for _, t := range conv.Tools() {
		m.Tools = append(m.Tools, encodeToolSchema(t))
	}
	return m
}

// agentContextRecord is the agent's last report about its session's context.
type agentContextRecord struct {
	Used         int     `json:"used"`
	Size         int     `json:"size"`
	CostAmount   float64 `json:"costAmount,omitempty"`
	CostCurrency string  `json:"costCurrency,omitempty"`
}

type usageRecord struct {
	InputTokens      int `json:"inputTokens"`
	CacheReadTokens  int `json:"cacheReadTokens"`
	CacheWriteTokens int `json:"cacheWriteTokens"`
	OutputTokens     int `json:"outputTokens"`
}

type toolSchemaRecord struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Enabled     bool            `json:"enabled"`
	Permission  string          `json:"permission,omitempty"`
}

// jsonl line envelope: messages and tool-output patches share one stream
// per file so appends stay O(1) and ordering is literal.
type lineRecord struct {
	Type string `json:"type"` // "message" | "tool_output" | "delivery"
	// Data is the message a line carries — only a message line has one.
	// Patches (a tool output, a delivery receipt) point at a stored message
	// by id, so they carry no envelope; writing a zero one would put an
	// empty message into every patch line (B-010). Old files that carry the
	// empty envelope still parse: the readers never look at Data for a patch.
	Data          *messageRecord     `json:"data,omitempty"`
	CallID        string             `json:"callId,omitempty"`
	MessageID     string             `json:"messageId,omitempty"` // a delivery patch's message
	Receipt       string             `json:"receipt,omitempty"`
	Output        string             `json:"output,omitempty"`
	OutputLatency string             `json:"outputLatency,omitempty"`
	Failed        bool               `json:"failed,omitempty"`
	Attachments   []attachmentRecord `json:"attachments,omitempty"`
}

type messageRecord struct {
	ID       string `json:"id"`
	Role     string `json:"role"`
	Content  string `json:"content"`
	UserType string `json:"userType,omitempty"`
	// Relay is a relayed prompt's provenance: who asked. Its own record, never
	// text inside Content — the head is rendered at request time.
	Relay *relayRecord `json:"relay,omitempty"`
	// InputID names the client request that submitted the message — a steer's
	// requestId. Absent on every other message, and on records written before
	// the field existed.
	InputID         string              `json:"inputId,omitempty"`
	FinishReason    string              `json:"finishReason,omitempty"`
	Parts           []messagePartRecord `json:"parts,omitempty"`
	CreatedAt       time.Time           `json:"createdAt"`
	ToolCalls       []toolCallRecord    `json:"toolCalls,omitempty"`
	Attachments     []attachmentRecord  `json:"attachments,omitempty"`
	References      []referenceRecord   `json:"references,omitempty"`
	Annotations     []annotationRecord  `json:"annotations,omitempty"`
	Recording       *recordingRecord    `json:"recording,omitempty"`
	OptionalContent map[string]string   `json:"optionalContent,omitempty"`
	SentFiles       []attachmentRecord  `json:"sentFiles,omitempty"`
	Receipt         string              `json:"receipt,omitempty"`
}

type referenceRecord struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Label string `json:"label"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

type annotationRecord struct {
	Marker     int                              `json:"marker"`
	Comment    string                           `json:"comment,omitempty"`
	Screenshot bool                             `json:"screenshot,omitempty"`
	Descriptor valueobject.AnnotationDescriptor `json:"descriptor"`
}

// recordingRecord is the browser recording a user message attached — the
// ref as it read at send time, summary included.
type recordingRecord struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Summary string `json:"summary,omitempty"`
}

type messagePartRecord struct {
	Kind             string             `json:"kind"`
	Text             string             `json:"text,omitempty"`
	ToolCallID       string             `json:"toolCallId,omitempty"`
	SteerAttachments []attachmentRecord `json:"steerAttachments,omitempty"`
	SteerContext     string             `json:"steerContext,omitempty"`
}

type toolCallRecord struct {
	ID                string             `json:"id"`
	Name              string             `json:"name"`
	Input             string             `json:"input"`
	Output            string             `json:"output,omitempty"`
	OutputSet         bool               `json:"outputSet,omitempty"`
	Failed            bool               `json:"failed,omitempty"`
	OutputLatency     string             `json:"outputLatency,omitempty"`
	OutputBudget      int                `json:"outputBudget,omitempty"`
	OutputAttachments []attachmentRecord `json:"outputAttachments,omitempty"`
	Type              string             `json:"type,omitempty"`
	CallAt            time.Time          `json:"callAt"`
	OptionalContent   map[string]string  `json:"optionalContent,omitempty"`
}

type attachmentRecord struct {
	URI       string `json:"uri"`
	MediaType string `json:"mediaType"`
	Name      string `json:"name,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

// messageEnvelope is the envelope a message line carries — the one line type
// that repeats the message; a patch line names its target by id instead.
func messageEnvelope(m entity.Message) *messageRecord {
	rec := encodeMessage(m)
	return &rec
}

func encodeMessage(m entity.Message) messageRecord {
	r := messageRecord{
		ID:              m.ID(),
		Role:            m.Role().String(),
		Content:         m.Content(),
		UserType:        m.UserType().String(),
		Relay:           relayOf(m.Relay()),
		InputID:         m.InputID(),
		FinishReason:    m.FinishReason().String(),
		CreatedAt:       m.CreatedAt(),
		OptionalContent: m.OptionalContent(),
	}
	for _, part := range m.Parts() {
		r.Parts = append(r.Parts, messagePartRecord{
			Kind:             part.Kind,
			Text:             part.Text,
			ToolCallID:       part.ToolCallID,
			SteerAttachments: encodeAttachments(part.SteerAttachments),
			SteerContext:     part.SteerContext,
		})
	}
	for _, tc := range m.ToolCalls() {
		rec := toolCallRecord{
			ID:              tc.ID(),
			Name:            tc.Name(),
			Input:           tc.Input(),
			Output:          tc.Output(),
			OutputSet:       tc.HasOutput(),
			Failed:          tc.Failed(),
			OutputLatency:   tc.OutputLatency(),
			OutputBudget:    tc.OutputBudget(),
			Type:            tc.Type().String(),
			CallAt:          tc.CallAt(),
			OptionalContent: tc.OptionalContent(),
		}
		for _, a := range tc.OutputAttachments() {
			rec.OutputAttachments = append(rec.OutputAttachments, attachmentRecord{
				URI:       a.URI(),
				MediaType: a.MediaType(),
				Name:      a.Name(),
				SHA256:    a.SHA256(),
			})
		}
		r.ToolCalls = append(r.ToolCalls, rec)
	}
	for _, a := range m.Attachments() {
		r.Attachments = append(r.Attachments, attachmentRecord{
			URI:       a.URI(),
			MediaType: a.MediaType(),
			Name:      a.Name(),
			SHA256:    a.SHA256(),
		})
	}
	for _, ref := range m.References() {
		r.References = append(r.References, referenceRecord{
			Kind: ref.Kind().String(), ID: ref.ID(), Label: ref.Label(), Start: ref.Start(), End: ref.End(),
		})
	}
	for _, an := range m.Annotations() {
		r.Annotations = append(r.Annotations, annotationRecord{Marker: an.Marker, Comment: an.Comment, Screenshot: an.Screenshot, Descriptor: an.Descriptor})
	}
	if rec := m.Recording(); rec != nil {
		r.Recording = &recordingRecord{ID: rec.ID, Status: rec.Status, Summary: rec.Summary}
	}
	r.SentFiles = encodeAttachments(m.SentFiles())
	r.Receipt = string(m.Receipt())
	return r
}

func encodeAttachments(atts []valueobject.Attachment) []attachmentRecord {
	var out []attachmentRecord
	for _, a := range atts {
		out = append(out, attachmentRecord{URI: a.URI(), MediaType: a.MediaType(), Name: a.Name(), SHA256: a.SHA256()})
	}
	return out
}

func decodeAttachments(recs []attachmentRecord) ([]valueobject.Attachment, error) {
	var out []valueobject.Attachment
	for _, a := range recs {
		att, err := valueobject.NewAttachment(a.URI, a.MediaType)
		if err != nil {
			return nil, err
		}
		att.SetName(a.Name)
		att.SetSHA256(a.SHA256)
		out = append(out, att)
	}
	return out, nil
}

// decodeMessage hydrates an entity.Message via the public constructor +
// setters — no reconstitute seam. Stored enum strings are validated strictly:
// unknown values mean corrupt or forward-incompatible data.
func decodeMessage(r messageRecord) (entity.Message, error) {
	role, ok := enum.ParseRole(r.Role)
	if !ok {
		return entity.Message{}, fmt.Errorf("unknown role %q", r.Role)
	}
	if r.ID == "" {
		return entity.Message{}, fmt.Errorf("message record missing id")
	}
	m, err := entity.NewMessage(role, r.Content)
	if err != nil {
		return entity.Message{}, err
	}
	m.SetID(r.ID)
	m.SetInputID(r.InputID)
	if r.UserType != "" {
		ut, ok := enum.ParseLLMUserType(r.UserType)
		if !ok {
			return entity.Message{}, fmt.Errorf("unknown userType %q", r.UserType)
		}
		m.SetUserType(ut)
	}
	if r.Relay != nil {
		m.SetRelay(valueobject.NewRelay(r.Relay.ID, r.Relay.Label, r.Relay.Title))
	}
	if r.FinishReason != "" {
		fr, ok := enum.ParseFinishReason(r.FinishReason)
		if !ok {
			return entity.Message{}, fmt.Errorf("unknown finishReason %q", r.FinishReason)
		}
		m.SetFinishReason(fr)
	}
	m.SetCreatedAt(r.CreatedAt)
	for _, a := range r.Attachments {
		att, err := valueobject.NewAttachment(a.URI, a.MediaType)
		if err != nil {
			return entity.Message{}, fmt.Errorf("attachment: %w", err)
		}
		att.SetName(a.Name)
		att.SetSHA256(a.SHA256)
		m.AddAttachment(att)
	}
	if len(r.SentFiles) > 0 || r.Receipt != "" {
		sent, err := decodeAttachments(r.SentFiles)
		if err != nil {
			return entity.Message{}, fmt.Errorf("sent file: %w", err)
		}
		receipt, err := valueobject.ParseReceipt(r.Receipt)
		if err != nil {
			return entity.Message{}, err
		}
		m.RecordDelivery(sent, receipt)
	}
	if len(r.References) > 0 {
		refs := make([]valueobject.Reference, 0, len(r.References))
		for _, rec := range r.References {
			ref, err := valueobject.NewReference(rec.Kind, rec.ID, rec.Label, rec.Start, rec.End)
			if err != nil {
				return entity.Message{}, fmt.Errorf("reference: %w", err)
			}
			refs = append(refs, ref)
		}
		if err := m.SetReferences(refs); err != nil {
			return entity.Message{}, fmt.Errorf("references: %w", err)
		}
	}
	if len(r.Annotations) > 0 {
		anns := make([]valueobject.Annotation, 0, len(r.Annotations))
		for _, rec := range r.Annotations {
			if _, ok := enum.ParseAnnotationKind(rec.Descriptor.Kind.String()); !ok {
				return entity.Message{}, fmt.Errorf("unknown annotation kind %q", rec.Descriptor.Kind)
			}
			anns = append(anns, valueobject.Annotation{Marker: rec.Marker, Comment: rec.Comment, Screenshot: rec.Screenshot, Descriptor: rec.Descriptor})
		}
		m.SetAnnotations(anns)
	}
	if r.Recording != nil {
		m.SetRecording(&valueobject.RecordingRef{ID: r.Recording.ID, Status: r.Recording.Status, Summary: r.Recording.Summary})
	}
	parts := make([]entity.MessagePart, 0, len(r.Parts))
	for _, part := range r.Parts {
		atts, err := decodeAttachments(part.SteerAttachments)
		if err != nil {
			return entity.Message{}, err
		}
		parts = append(parts, entity.MessagePart{
			Kind:             part.Kind,
			Text:             part.Text,
			ToolCallID:       part.ToolCallID,
			SteerAttachments: atts,
			SteerContext:     part.SteerContext,
		})
	}
	m.SetParts(parts)
	for _, t := range r.ToolCalls {
		tc, err := decodeToolCall(t)
		if err != nil {
			return entity.Message{}, err
		}
		if err := m.AddToolCall(tc); err != nil {
			return entity.Message{}, err
		}
	}
	for k, v := range r.OptionalContent {
		m.SetOptionalContent(k, v)
	}
	return m, nil
}

func decodeToolCall(r toolCallRecord) (entity.ToolCall, error) {
	var tt enum.Tooltype
	if r.Type != "" {
		parsed, ok := enum.ParseTooltype(r.Type)
		if !ok {
			return entity.ToolCall{}, fmt.Errorf("unknown tooltype %q", r.Type)
		}
		tt = parsed
	}
	tc, err := entity.NewToolCall(r.ID, r.Name, r.Input, tt)
	if err != nil {
		return entity.ToolCall{}, fmt.Errorf("toolCall %q: %w", r.ID, err)
	}
	tc.SetCallAt(r.CallAt)
	tc.SetFailed(r.Failed)
	if r.OutputBudget > 0 {
		tc.SetOutputBudget(r.OutputBudget)
	}
	if r.OutputSet {
		tc.SetStoredOutput(r.Output, r.OutputLatency)
	}
	var atts []valueobject.Attachment
	for _, a := range r.OutputAttachments {
		att, err := valueobject.NewAttachment(a.URI, a.MediaType)
		if err != nil {
			return entity.ToolCall{}, fmt.Errorf("toolCall %q attachment: %w", r.ID, err)
		}
		att.SetName(a.Name)
		att.SetSHA256(a.SHA256)
		atts = append(atts, att)
	}
	tc.SetOutputAttachments(atts)
	for k, v := range r.OptionalContent {
		tc.SetOptionalContent(k, v)
	}
	return tc, nil
}

func decodeUsage(r usageRecord) (valueobject.Usage, error) {
	return valueobject.NewUsage(r.InputTokens, r.CacheReadTokens, r.CacheWriteTokens, r.OutputTokens)
}

// mustUsage is decodeUsage for records we wrote ourselves — counters were
// validated at write time.
func mustUsage(r usageRecord) valueobject.Usage {
	u, _ := decodeUsage(r)
	return u
}

func encodeUsage(u valueobject.Usage) usageRecord {
	return usageRecord{
		InputTokens:      u.InputTokens(),
		CacheReadTokens:  u.CacheReadTokens(),
		CacheWriteTokens: u.CacheWriteTokens(),
		OutputTokens:     u.OutputTokens(),
	}
}

func encodeToolSchema(s valueobject.ToolSchema) toolSchemaRecord {
	return toolSchemaRecord{
		Name:        s.Name(),
		Description: s.Description(),
		Parameters:  s.Parameters(),
		Enabled:     s.Enabled(),
		Permission:  s.Permission().String(),
	}
}

func decodeToolSchema(r toolSchemaRecord) (valueobject.ToolSchema, error) {
	var perm enum.ToolPermission
	if r.Permission != "" {
		parsed, ok := enum.ParseToolPermission(r.Permission)
		if !ok {
			return valueobject.ToolSchema{}, fmt.Errorf("unknown permission %q", r.Permission)
		}
		perm = parsed
	}
	return valueobject.NewToolSchema(r.Name, r.Description, r.Parameters, r.Enabled, perm)
}

func decodeRouting(r *routingRecord) *valueobject.Routing {
	if r == nil {
		return nil
	}
	return &valueobject.Routing{ReplyToOrigin: r.ReplyToOrigin, MirrorFinal: r.MirrorFinal, NotifyApprovals: r.NotifyApprovals}
}
