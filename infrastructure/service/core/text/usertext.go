package coretext

import "github.com/jahrulnr/nusashell/infrastructure/domain/entity"

// UserText is the text a model reads for one user message: a relayed prompt
// carries the head naming who asked, rendered here at request build from the
// message's provenance record. The head is never stored, so the body in history
// stays exactly what the sender wrote.
//
// It answers the message's own content for everything else — and callers use it
// for both the emptiness check and the body, so a relayed message whose body is
// empty is still a message with a head.
func UserText(m entity.Message) string {
	if r := m.Relay(); r.ID != "" {
		return r.Head() + m.Content()
	}
	return m.Content()
}
