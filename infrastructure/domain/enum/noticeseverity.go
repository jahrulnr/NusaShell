package enum

// NoticeSeverity ranks a notice an agent raises for the owner.
type NoticeSeverity string

const (
	NoticeInfo    NoticeSeverity = "info"
	NoticeWarning NoticeSeverity = "warning"
	NoticeError   NoticeSeverity = "error"
)

func (s NoticeSeverity) String() string { return string(s) }

// ParseNoticeSeverity reads the severity an agent names. Agents spell it
// differently ("warn", "warning"); anything unrecognized is plain
// information, which is what a notice without a level is.
func ParseNoticeSeverity(v string) NoticeSeverity {
	switch v {
	case "warn", "warning":
		return NoticeWarning
	case "error", "err", "fatal":
		return NoticeError
	}
	return NoticeInfo
}
