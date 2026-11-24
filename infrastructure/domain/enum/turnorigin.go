package enum

// TurnOriginKind is the closed set of places a turn can start from: the
// web UI, a linked chat channel, a scheduled automation, or another
// NusaShell thread.
type TurnOriginKind string

const (
	TurnOriginWeb          TurnOriginKind = "web"
	TurnOriginChannel      TurnOriginKind = "channel"
	TurnOriginAutomation   TurnOriginKind = "automation"
	TurnOriginConversation TurnOriginKind = "conversation"
)

func (k TurnOriginKind) String() string { return string(k) }

func ParseTurnOriginKind(v string) (TurnOriginKind, bool) {
	switch k := TurnOriginKind(v); k {
	case TurnOriginWeb, TurnOriginChannel, TurnOriginAutomation, TurnOriginConversation:
		return k, true
	}
	return "", false
}
