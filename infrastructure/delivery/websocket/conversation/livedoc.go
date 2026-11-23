package wsconversation

import "encoding/json"

// liveDocMaxBytes bounds the text a turn's snapshot document holds. A
// gapped turn.watch gets the document inside one resync_required frame,
// so the bound also keeps that frame under maxFrameBytes with envelope
// headroom; content beyond it drops from the oldest edge and is counted
// so the tab can say recovery was partial.
const liveDocMaxBytes = 384 << 10

// livePart mirrors one client transcript segment — the same shape
// turn.delta, turn.tool_*, turn.steer, and the card frames project —
// so a reattached tab rebuilds its live view without the journal. A
// part is pointer-shared between `parts` order and its index maps;
// updates land through the doc's helpers so the byte accounting holds.
type livePart struct {
	Kind        string              `json:"kind"`
	Text        string              `json:"text,omitempty"`
	CallID      string              `json:"callId,omitempty"`
	Name        string              `json:"name,omitempty"`
	Input       string              `json:"input,omitempty"`
	Output      string              `json:"output,omitempty"`
	Status      string              `json:"status,omitempty"`
	InputID     string              `json:"inputId,omitempty"`
	Final       bool                `json:"final,omitempty"`
	Entries     json.RawMessage     `json:"entries,omitempty"`
	Request     any                 `json:"request,omitempty"`
	OptionID    string              `json:"optionId,omitempty"`
	Action      string              `json:"action,omitempty"`
	Attachments []attachmentPayload `json:"attachments,omitempty"`
}

// liveDoc is a turn's compact live record: ordered parts plus the
// indexes that fan-in events need (tool chunks by callId, card
// resolutions by request id). It accumulates under h.mu beside the
// journal; where the journal drops whole frames, the doc merges their
// content, so the same bound covers far more history.
type liveDoc struct {
	parts        []*livePart
	tools        map[string]*livePart
	approvals    map[string]*livePart
	elicitations map[string]*livePart
	// plan is the turn's single plan part — each update replaces it.
	plan *livePart
	// phase is the running turn's last reported phase — a status wait or
	// a reasoning stretch — so a snapshot names what the turn is doing.
	phase   *phasePayload
	bytes   int
	dropped int
}

func newLiveDoc() *liveDoc {
	return &liveDoc{
		tools:        map[string]*livePart{},
		approvals:    map[string]*livePart{},
		elicitations: map[string]*livePart{},
	}
}

// clearPhase applies the client's generic rule: any content frame
// clears a phase that is not "reasoning" — reasoning stretches end only
// on text, a tool call, or a steer, which assign nil directly.
func (d *liveDoc) clearPhase() {
	if d.phase != nil && d.phase.Phase != "reasoning" {
		d.phase = nil
	}
}

// steer folds one user input into the tail steer part while chunks of
// the same input are still landing — the same fold the client applies —
// then seals it, since the event carries the whole text.
func (d *liveDoc) steer(inputID, text string, atts []attachmentPayload) {
	if p := d.last(); p != nil && p.Kind == "steer" && !p.Final && p.InputID == inputID {
		p.Text += text
		d.bytes += len(text)
		p.Final = true
	} else {
		d.push(&livePart{Kind: "steer", InputID: inputID, Text: text, Attachments: atts, Final: true})
	}
	d.trim()
}

// last returns the current tail part.
func (d *liveDoc) last() *livePart {
	if len(d.parts) == 0 {
		return nil
	}
	return d.parts[len(d.parts)-1]
}

// partBytes estimates a part's serialized size — every field the
// snapshot marshals, not only the trimmable text — so card payloads and
// plan entries hold memory inside the same bound as text does.
func partBytes(p *livePart) int {
	n := len(p.Text) + len(p.Input) + len(p.Output) + len(p.Entries) +
		len(p.CallID) + len(p.Name) + len(p.InputID) + len(p.OptionID) + len(p.Action)
	switch r := p.Request.(type) {
	case approvalPayload:
		n += len(r.ID) + len(r.CallID) + len(r.Title) + len(r.Input) + len(r.Action)
		for _, o := range r.Options {
			n += len(o.ID) + len(o.Name) + len(o.Choice)
		}
	case elicitationPayload:
		n += len(r.ID) + len(r.Mode) + len(r.Message) + len(r.Schema) + len(r.URL)
	}
	for _, a := range p.Attachments {
		n += len(a.MediaType) + len(a.Modality) + len(a.URI) + len(a.Name)
	}
	return n
}

// push appends one part and accounts its full serialized content.
func (d *liveDoc) push(p *livePart) {
	d.parts = append(d.parts, p)
	d.bytes += partBytes(p)
	d.trim()
}

// text appends provider text to the tail part of the same kind, or
// opens one — the same fold the client applies to streamed chunks.
func (d *liveDoc) text(kind, delta string) {
	if delta == "" {
		return
	}
	if p := d.last(); p != nil && p.Kind == kind {
		p.Text += delta
	} else {
		d.parts = append(d.parts, &livePart{Kind: kind, Text: delta})
	}
	d.bytes += len(delta)
	d.trim()
}

// toolOutput appends one tool's output chunk and flips the row to
// streaming — a settled row stays settled, as the client keeps it.
func (d *liveDoc) toolOutput(callID, delta string) {
	p := d.tools[callID]
	if p == nil || delta == "" {
		return
	}
	p.Output += delta
	if p.Status != "success" && p.Status != "failed" {
		p.Status = "streaming"
	}
	d.bytes += len(delta)
	d.trim()
}

// toolInput extends a call's recorded input — the argument string is
// streamed in chunks, so it accumulates like output does.
func (d *liveDoc) toolInput(callID, delta string) {
	p := d.tools[callID]
	if p == nil || delta == "" {
		return
	}
	p.Input += delta
	d.bytes += len(delta)
	d.trim()
}

// trim drops content from the oldest edge until the doc is under its
// bound — the newest live content is what a reattach needs most, and
// the dropped count tells the client recovery was partial. Text fields
// shave first so a part keeps its structure; a part that holds only
// card payload goes whole.
func (d *liveDoc) trim() {
	for d.bytes > liveDocMaxBytes && len(d.parts) > 0 {
		p := d.parts[0]
		for _, f := range []*string{&p.Text, &p.Input, &p.Output} {
			if *f == "" {
				continue
			}
			n := len(*f)
			if over := d.bytes - liveDocMaxBytes; over < n {
				n = over
			}
			// Drop whole runes — a mid-sequence cut shows mojibake.
			for n < len(*f) && !isRuneStart((*f)[n]) {
				n++
			}
			*f = (*f)[n:]
			d.bytes -= n
			d.dropped += n
			if d.bytes <= liveDocMaxBytes {
				return
			}
		}
		if p.Text == "" && p.Input == "" && p.Output == "" {
			d.drop(p)
		}
	}
}

// drop removes the oldest part entirely — the fallthrough once its
// trimmable fields are gone — clearing the index slots that fan-in
// events look up, so a late resolution lands nowhere.
func (d *liveDoc) drop(p *livePart) {
	n := partBytes(p)
	d.bytes -= n
	d.dropped += n
	delete(d.tools, p.CallID)
	if r, ok := p.Request.(approvalPayload); ok {
		delete(d.approvals, r.ID)
	}
	if r, ok := p.Request.(elicitationPayload); ok {
		delete(d.elicitations, r.ID)
	}
	if d.plan == p {
		d.plan = nil
	}
	d.parts = d.parts[1:]
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
