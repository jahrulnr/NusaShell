package valueobject

import (
	"errors"
	"slices"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

// ApprovalOption is one answer an approval request offers. ID is the
// runtime's own token for the answer; Choice is its normalized meaning.
type ApprovalOption struct {
	ID     string
	Name   string
	Choice enum.ApprovalChoice
}

// ApprovalRequest is a permission question a running agent raised about
// one of its tool calls.
type ApprovalRequest struct {
	id      string
	callID  string
	title   string
	input   string
	action  enum.ToolAction
	options []ApprovalOption
}

// NewApprovalRequest validates the request: it needs an id and at least
// one option, and every option needs an id and a known choice.
func NewApprovalRequest(id, callID, title, input string, action enum.ToolAction, options []ApprovalOption) (ApprovalRequest, error) {
	if id == "" {
		return ApprovalRequest{}, errors.New("approval request id is required")
	}
	if len(options) == 0 {
		return ApprovalRequest{}, errors.New("approval request offers no options")
	}
	for _, o := range options {
		if o.ID == "" {
			return ApprovalRequest{}, errors.New("approval option id is required")
		}
		if _, ok := enum.ParseApprovalChoice(o.Choice.String()); !ok {
			return ApprovalRequest{}, errors.New("approval option has an unknown choice")
		}
	}
	return ApprovalRequest{
		id: id, callID: callID, title: title, input: input,
		action: enum.ParseToolAction(action.String()), options: slices.Clone(options),
	}, nil
}

func (r ApprovalRequest) ID() string                { return r.id }
func (r ApprovalRequest) CallID() string            { return r.callID }
func (r ApprovalRequest) Title() string             { return r.title }
func (r ApprovalRequest) Input() string             { return r.input }
func (r ApprovalRequest) Action() enum.ToolAction   { return r.action }
func (r ApprovalRequest) Options() []ApprovalOption { return slices.Clone(r.options) }

// Option finds an offered option by id.
func (r ApprovalRequest) Option(id string) (ApprovalOption, bool) {
	for _, o := range r.options {
		if o.ID == id {
			return o, true
		}
	}
	return ApprovalOption{}, false
}

// AutoAnswer is the option a policy picks without asking: the first
// offered one-time allow, then any allow. ok is false when the policy
// must ask, or when the request offers no allow at all.
func (r ApprovalRequest) AutoAnswer(policy enum.ApprovalPolicy) (ApprovalOption, bool) {
	if !policy.AutoApproves(r.action) {
		return ApprovalOption{}, false
	}
	return r.AllowAnswer()
}

// AllowAnswer is the option that allows the request — once when offered,
// else always. ok is false when the request offers no allowing answer.
func (r ApprovalRequest) AllowAnswer() (ApprovalOption, bool) {
	for _, want := range []enum.ApprovalChoice{enum.ApprovalAllowOnce, enum.ApprovalAllowAlways} {
		for _, o := range r.options {
			if o.Choice == want {
				return o, true
			}
		}
	}
	return ApprovalOption{}, false
}

// RejectAnswer is the option that declines the request once, used when the
// owner cannot answer (the turn ended or was cancelled). ok is false when
// the request offers no rejection.
func (r ApprovalRequest) RejectAnswer() (ApprovalOption, bool) {
	for _, want := range []enum.ApprovalChoice{enum.ApprovalRejectOnce, enum.ApprovalRejectAlways} {
		for _, o := range r.options {
			if o.Choice == want {
				return o, true
			}
		}
	}
	return ApprovalOption{}, false
}
