package valueobject

import (
	"strconv"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

// TurnOrigin says where one turn came from. It is per-turn context, not
// conversation state: the API runtime records it as an injection call and
// the agent runtime sends it ahead of the prompt. Name carries the channel
// word (telegram, whatsapp) or the automation's name.
type TurnOrigin struct {
	Kind enum.TurnOriginKind `json:"kind"`
	Name string              `json:"name,omitempty"`
}

// Normalized trims the name and drops detail a kind cannot use. It
// reports false for the zero value and unknown kinds, which have no
// record to write.
func (o TurnOrigin) Normalized() (TurnOrigin, bool) {
	if _, ok := enum.ParseTurnOriginKind(o.Kind.String()); !ok {
		return TurnOrigin{}, false
	}
	o.Name = strings.TrimSpace(o.Name)
	switch o.Kind {
	case enum.TurnOriginWeb, enum.TurnOriginConversation, enum.TurnOriginCompletion:
		o.Name = ""
	case enum.TurnOriginChannel, enum.TurnOriginAutomation:
	}
	return o, true
}

// Description names the origin for the model in one line.
func (o TurnOrigin) Description() string {
	o, ok := o.Normalized()
	if !ok {
		return ""
	}
	switch o.Kind {
	case enum.TurnOriginWeb:
		return "web"
	case enum.TurnOriginChannel:
		if o.Name == "" {
			return "a chat channel"
		}
		return "chat channel " + o.Name
	case enum.TurnOriginAutomation:
		if o.Name == "" {
			return "an automation run"
		}
		return "automation " + strconv.Quote(o.Name)
	case enum.TurnOriginConversation:
		return "another NusaShell thread"
	case enum.TurnOriginCompletion:
		return "a background completion"
	}
	return ""
}
