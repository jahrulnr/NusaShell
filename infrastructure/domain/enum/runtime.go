package enum

// Runtime identifies who runs a conversation's turns. The model runtime is
// this harness's own turn loop against a provider; the agent runtime hands
// each prompt to an external agent process that owns its own loop, tools,
// and context.
type Runtime string

const (
	RuntimeModel Runtime = "model"
	RuntimeAgent Runtime = "agent"
)

func (r Runtime) String() string { return string(r) }
