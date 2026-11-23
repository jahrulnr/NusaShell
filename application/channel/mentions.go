package appchannel

import (
	"context"
	"regexp"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// Mentions cap the suggestions a chat surface shows: rooms first, then a
// few projects even when many rooms match.
const (
	mentionRooms = 15
	mentionTotal = 20
)

// mentionTokenRe finds "@handle" tokens: room labels and project handles.
var mentionTokenRe = regexp.MustCompile(`@[A-Za-z0-9][A-Za-z0-9_-]*`)

// Mentions are the rooms and projects a chat can name, matching query the
// way the web composer does: rooms by label prefix or title, projects by
// name or handle prefix.
func (a *App) Mentions(ctx context.Context, query string) []dservice.ChannelMention {
	q := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(query), "@"))
	var out []dservice.ChannelMention
	if rooms, err := a.rooms.List(ctx); err == nil {
		for _, r := range rooms {
			if len(out) >= mentionRooms {
				break
			}
			if r.Label == "" {
				continue
			}
			if q != "" && !strings.HasPrefix(r.Label, q) && !strings.Contains(strings.ToLower(r.Title), q) {
				continue
			}
			out = append(out, dservice.ChannelMention{Kind: enum.ReferenceConversation.String(), ID: r.ID, Token: "@" + r.Label, Title: r.Title})
		}
	}
	if projects, err := a.projects.List(ctx); err == nil {
		for _, p := range projects {
			if len(out) >= mentionTotal {
				break
			}
			h := p.Handle()
			if h == "" || (q != "" && !strings.HasPrefix(h, q) && !strings.Contains(strings.ToLower(p.Name()), q)) {
				continue
			}
			out = append(out, dservice.ChannelMention{Kind: enum.ReferenceProject.String(), ID: p.ID(), Token: "@" + h, Title: p.Name()})
		}
	}
	return out
}

// inlineReferences resolves the @tokens of a message written through a
// chat's mention picker into references on body, where text starts at
// offset shift. A room label wins over a project handle; a token naming
// neither stays plain text, so the rest of the message can still mention
// people.
func (a *App) inlineReferences(ctx context.Context, text, body string, shift int) []valueobject.Reference {
	locs := mentionTokenRe.FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return nil
	}
	rooms, _ := a.rooms.List(ctx)
	projects, _ := a.projects.List(ctx)
	var refs []valueobject.Reference
	for _, loc := range locs {
		handle := strings.ToLower(text[loc[0]+1 : loc[1]])
		kind, id := "", ""
		for _, r := range rooms {
			if r.Label != "" && r.Label == handle {
				kind, id = enum.ReferenceConversation.String(), r.ID
				break
			}
		}
		if id == "" {
			for _, p := range projects {
				if p.Handle() == handle {
					kind, id = enum.ReferenceProject.String(), p.ID()
					break
				}
			}
		}
		if id == "" {
			continue
		}
		start, end := loc[0]+shift, loc[1]+shift
		if end > len(body) || body[start:end] != text[loc[0]:loc[1]] {
			continue
		}
		if ref, err := valueobject.NewReference(kind, id, text[loc[0]:loc[1]], start, end); err == nil {
			refs = append(refs, ref)
		}
	}
	return refs
}
