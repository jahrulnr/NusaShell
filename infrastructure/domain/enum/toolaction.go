package enum

// ToolAction classifies what a tool call does to the world, independent of
// which tool or runtime issued it. Approval policy decides on this class.
type ToolAction string

const (
	ToolActionRead       ToolAction = "read"
	ToolActionEdit       ToolAction = "edit"
	ToolActionDelete     ToolAction = "delete"
	ToolActionMove       ToolAction = "move"
	ToolActionSearch     ToolAction = "search"
	ToolActionExecute    ToolAction = "execute"
	ToolActionThink      ToolAction = "think"
	ToolActionFetch      ToolAction = "fetch"
	ToolActionSwitchMode ToolAction = "switch_mode"
	ToolActionOther      ToolAction = "other"
)

func (a ToolAction) String() string { return string(a) }

// ParseToolAction maps a stored or reported class onto the closed set. An
// unknown class is ToolActionOther, never a free-form value: policy must
// treat what it cannot classify as the least trusted class.
func ParseToolAction(v string) ToolAction {
	switch a := ToolAction(v); a {
	case ToolActionRead, ToolActionEdit, ToolActionDelete, ToolActionMove, ToolActionSearch,
		ToolActionExecute, ToolActionThink, ToolActionFetch, ToolActionSwitchMode:
		return a
	}
	return ToolActionOther
}

// SideEffectFree reports whether the action only observes: reading,
// searching, fetching, or thinking. Everything else may change state.
func (a ToolAction) SideEffectFree() bool {
	switch a {
	case ToolActionRead, ToolActionSearch, ToolActionFetch, ToolActionThink:
		return true
	}
	return false
}
