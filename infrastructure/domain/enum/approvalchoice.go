package enum

// ApprovalChoice is the meaning of one answer an approval request offers.
type ApprovalChoice string

const (
	ApprovalAllowOnce    ApprovalChoice = "allow_once"
	ApprovalAllowAlways  ApprovalChoice = "allow_always"
	ApprovalRejectOnce   ApprovalChoice = "reject_once"
	ApprovalRejectAlways ApprovalChoice = "reject_always"
)

func (c ApprovalChoice) String() string { return string(c) }

func ParseApprovalChoice(v string) (ApprovalChoice, bool) {
	switch c := ApprovalChoice(v); c {
	case ApprovalAllowOnce, ApprovalAllowAlways, ApprovalRejectOnce, ApprovalRejectAlways:
		return c, true
	}
	return "", false
}

// Allows reports whether the choice grants the request.
func (c ApprovalChoice) Allows() bool {
	return c == ApprovalAllowOnce || c == ApprovalAllowAlways
}
