package enum

// ApprovalPolicy decides which tool calls an agent may run without asking
// the owner first.
type ApprovalPolicy string

const (
	// ApprovalPolicyAsk asks the owner for every permission request.
	ApprovalPolicyAsk ApprovalPolicy = "ask"
	// ApprovalPolicyAutoSafe allows side-effect-free actions and asks for
	// the rest.
	ApprovalPolicyAutoSafe ApprovalPolicy = "auto_safe"
	// ApprovalPolicyAutoAll allows every permission request.
	ApprovalPolicyAutoAll ApprovalPolicy = "auto_all"
)

func (p ApprovalPolicy) String() string { return string(p) }

func ParseApprovalPolicy(v string) (ApprovalPolicy, bool) {
	switch p := ApprovalPolicy(v); p {
	case ApprovalPolicyAsk, ApprovalPolicyAutoSafe, ApprovalPolicyAutoAll:
		return p, true
	}
	return "", false
}

// AutoApproves reports whether the policy grants action without asking.
// An unknown policy asks: approval fails closed.
func (p ApprovalPolicy) AutoApproves(action ToolAction) bool {
	switch p {
	case ApprovalPolicyAutoAll:
		return true
	case ApprovalPolicyAutoSafe:
		return action.SideEffectFree()
	}
	return false
}
