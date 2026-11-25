package valueobject

import (
	"strings"
	"unicode/utf8"
)

// Relay is the provenance of a prompt one thread sent another: who asked, and
// the name the sender held when the prompt was written. It rides the message as
// its own record — never as text inside the body — so the body stays exactly
// what the sender wrote and a reader draws the header from data.
//
// Every field is a plain string, empty when unknown: nothing here is a pointer,
// so no consumer guards a null. The snapshot is taken from the live source when
// the message is written; rendering reads this record alone, so no request path
// reads another room.
type Relay struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Title string `json:"title"`
}

// relayNameRunes bounds the sender's name inside the head — the display bound a
// title already obeys (the aggregate's title normalization).
const relayNameRunes = 80

// relayMarkers are the head's own bracket markers. A title carrying one was
// itself relayed; it must not travel another hop.
var relayMarkers = []string{"[From thread", "[From another thread", "[Your answer here"}

// NewRelay takes the sender's identity at write time and normalizes it: a title
// that was itself a relayed head sheds those blocks, every newline folds into
// one line, and the name is bounded. Normalizing here (not at render) keeps the
// renderer a plain formatter with nothing to look up.
func NewRelay(id, label, title string) Relay {
	return Relay{ID: strings.TrimSpace(id), Label: strings.TrimSpace(label), Title: cleanRelayTitle(title)}
}

// cleanRelayTitle folds a candidate name to one bounded line with no relay
// markers of its own.
func cleanRelayTitle(title string) string {
	for _, marker := range relayMarkers {
		for {
			i := strings.Index(title, marker)
			if i < 0 {
				break
			}
			end := strings.Index(title[i:], "]")
			if end < 0 {
				title = title[:i]
				break
			}
			title = title[:i] + title[i+end+1:]
		}
	}
	name := strings.Join(strings.Fields(title), " ")
	if utf8.RuneCountInString(name) > relayNameRunes {
		name = strings.TrimSpace(string([]rune(name)[:relayNameRunes-1])) + "…"
	}
	return name
}

// Display names the sender the way a person reads it: the label when the room
// had one, then its title, then the id.
func (r Relay) Display() string {
	name := strings.TrimSpace(r.Title)
	switch {
	case r.Label != "" && name != "":
		return "@" + r.Label + " " + name
	case r.Label != "":
		return "@" + r.Label
	case name != "":
		return name
	}
	return r.ID
}

// Head is the text a model reads above a relayed prompt: who asked, and how to
// answer. It is the one place that text is assembled — the encoders and the
// agent prompt builder call it — and it is never stored: a message keeps the
// provenance record and the head is rendered whenever a request is built.
//
// A relay whose sender can no longer be named still renders, saying only that
// another thread asked.
func (r Relay) Head() string {
	if r.ID == "" {
		return "[From another thread]\n\n"
	}
	return "[From thread " + r.Display() + " (" + r.ID + ")]\n" +
		"[Your answer here does not reach that thread. If it needs a reply, send one to " + r.ID + " with the conversation MCP tool send.]\n\n"
}
