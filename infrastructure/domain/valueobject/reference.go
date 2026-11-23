package valueobject

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

// Reference is one selection the owner made in a message's text: the
// label spans bytes [start, end) of the text and stands for id — a path
// for a file, folder, or skill, a server name, a command name, or a
// conversation or project id.
type Reference struct {
	kind  enum.ReferenceKind
	id    string
	label string
	start int
	end   int
}

func NewReference(kind, id, label string, start, end int) (Reference, error) {
	k, ok := enum.ParseReferenceKind(kind)
	if !ok {
		return Reference{}, fmt.Errorf("reference kind %q is unknown", kind)
	}
	if strings.TrimSpace(id) == "" || label == "" {
		return Reference{}, errors.New("reference needs an id and a label")
	}
	if start < 0 || end <= start {
		return Reference{}, errors.New("reference span is invalid")
	}
	return Reference{kind: k, id: id, label: label, start: start, end: end}, nil
}

func (r Reference) Kind() enum.ReferenceKind { return r.kind }
func (r Reference) ID() string               { return r.id }
func (r Reference) Label() string            { return r.label }
func (r Reference) Start() int               { return r.start }
func (r Reference) End() int                 { return r.end }

// Shifted returns the selection moved by a byte offset — for folding
// several inputs into one joined text, where each input's spans must
// move by the prefix length ahead of it.
func (r Reference) Shifted(offset int) Reference {
	r.start += offset
	r.end += offset
	return r
}

// CheckReferences reports whether refs fit text: in order, not
// overlapping, each label exactly the text it spans, on rune boundaries.
func CheckReferences(text string, refs []Reference) error {
	previous := 0
	for _, r := range refs {
		if r.start < previous || r.end > len(text) || !utf8.ValidString(text[:r.start]) || !utf8.ValidString(text[r.start:r.end]) {
			return errors.New("reference span does not fit the text")
		}
		if text[r.start:r.end] != r.label {
			return fmt.Errorf("reference %q does not match the text it spans", r.label)
		}
		previous = r.end
	}
	return nil
}

// ReferenceNote tells the runtime what each selection in a message means.
// "" when there is nothing to say — a command is the text itself.
func ReferenceNote(refs []Reference) string {
	var lines []string
	for _, r := range refs {
		switch r.kind {
		case enum.ReferenceFile:
			lines = append(lines, fmt.Sprintf("%s refers to the file %s.", r.label, r.id))
		case enum.ReferenceFolder:
			lines = append(lines, fmt.Sprintf("%s refers to the folder %s.", r.label, r.id))
		case enum.ReferenceSkill:
			lines = append(lines, fmt.Sprintf("%s selects the skill at %s; read it and follow its instructions for this request.", r.label, r.id))
		case enum.ReferenceMCP:
			lines = append(lines, fmt.Sprintf("%s selects the MCP server %s; use its tools where they fit this request.", r.label, r.id))
		case enum.ReferenceConversation:
			lines = append(lines, fmt.Sprintf("%s refers to the NusaShell conversation %s; the conversation tools can read it or message it.", r.label, r.id))
		case enum.ReferenceProject:
			lines = append(lines, fmt.Sprintf("%s refers to the NusaShell project %s; the conversation tools can list its conversations.", r.label, r.id))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "Selected references:\n" + strings.Join(lines, "\n")
}
