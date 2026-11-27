package service

import (
	"context"
	"slices"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// Elicitor gets the owner's answer to an agent's request for input. It
// blocks until the owner answers or ctx ends.
type Elicitor func(ctx context.Context, e valueobject.Elicitation) (valueobject.ElicitationAnswer, error)

// Approver answers a permission request an agent raised mid-turn. It
// returns the chosen option id. It blocks until the owner decides or ctx
// ends; on ctx end it returns ctx's error and the runtime declines.
type Approver func(ctx context.Context, req valueobject.ApprovalRequest) (optionID string, err error)

// AgentPrompt is one user turn handed to an external agent.
type AgentPrompt struct {
	// Agent is the launch configuration, environment resolved.
	Agent *aggregate.Agent
	// Conversation is the live room. Prompt appends exactly one assistant
	// message to it and records the agent's session handle on it.
	Conversation *aggregate.Conversation
	// Workspace is the directory the agent works in.
	Workspace string
	// ExtraFolders are further directories the agent may work in (a
	// project's non-primary folders). Agents that cannot take extra
	// directories work in Workspace only.
	ExtraFolders []string
	// Input is the user message just appended to the room.
	Input entity.Message
	// Origin says where this turn started — the web UI, a chat channel,
	// an automation, or another thread. The runtime sends it as turn
	// context ahead of the prompt; the zero value adds nothing.
	Origin valueobject.TurnOrigin
	// Model is the agent model to run the turn on; "" keeps the agent's
	// current choice.
	Model string
	// MCPServers are the tool servers the agent gets in this room's
	// session.
	MCPServers []MCPServer
	// Options are the owner's picks of the agent's session options,
	// applied in list order before the turn: agent defaults first, room
	// picks after, the model pick ahead of what it materializes.
	Options valueobject.ConfigSelections
	// Elicit asks the owner for input the agent requests; nil declines
	// every request.
	Elicit Elicitor

	// OnStarted runs once, after the current prompt is sent to the agent.
	// It must not block. Nil skips the notification.
	OnStarted func()
}

// AgentOption is one of an agent's session options (a mode, a model
// parameter, …) other than the model itself.
type AgentOption struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Category    string         `json:"category,omitempty"`
	Type        string         `json:"type"` // select or boolean
	Current     string         `json:"current"`
	Choices     []OptionChoice `json:"choices,omitempty"`
	// Deferred marks an entry that is not a real offered option: a stored
	// pick whose id the session does not offer. It carries the picked value
	// apart from Current — deferred is a stored intent, never the active
	// choice — and the entry disappears when the option materializes.
	Deferred string `json:"deferred,omitempty"`
}

// Offers reports whether value is one this option accepts: a listed choice for
// a select, "true" or "false" for a boolean, and any non-empty value for free
// text. It is the single answer to "may this value be recorded for this
// option" — the picker's validation, the live session's refusal, and the turn
// that replays a stored pick all refuse the same set.
func (o AgentOption) Offers(value string) bool {
	switch o.Type {
	case "select":
		return slices.ContainsFunc(o.Choices, func(c OptionChoice) bool { return c.Value == value })
	case "boolean":
		return value == "true" || value == "false"
	}
	return value != ""
}

// AgentCommand is a slash command an agent session offers. It runs by
// sending "/name input" as the prompt.
type AgentCommand struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Hint        string `json:"hint,omitempty"`
}

// OptionChoice is one value of a select option.
type OptionChoice struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// MCPServer is an MCP server an agent gets for a session: a command it
// launches on stdio, or a URL it reaches over http or sse. An agent that
// cannot speak a server's transport goes without that server.
type MCPServer struct {
	Name      string
	Transport enum.MCPTransport // "" is stdio
	Command   string
	Args      []string
	Env       map[string]string
	URL       string
	Headers   map[string]string
}

// AgentModel is one model an agent offers.
type AgentModel struct {
	ID   string
	Name string
	// Current marks the model a new session starts on.
	Current bool
}

// OptionsState says what an Options answer means for a picker right now.
type OptionsState string

const (
	// OptionsReady is the authoritative offered list — empty included. An
	// empty ready list means the agent offers no session options, and the
	// picker may say so. It never comes from a live session: a session
	// reporting nothing is warming up or shrinking transiently, which is
	// OptionsEmpty instead.
	OptionsReady OptionsState = "ready"
	// OptionsEmpty is a reachable agent whose offered surface is empty at
	// this moment — a live session that has not offered anything yet or
	// just retracted its options. It is transient: the picker should say
	// "nothing offered right now" and offer a refresh, never the permanent
	// "this agent has no options".
	OptionsEmpty OptionsState = "empty"
	// OptionsUnavailable means the agent could not be asked; err carries
	// the cause.
	OptionsUnavailable OptionsState = "unavailable"
)

// OptionsSource says where an offered option list came from. Only
// OptionsSourceSession is the room's live session; catalog and default answers
// are fallback evidence for an idle room, never the options in force now.
type OptionsSource string

const (
	OptionsSourceSession      OptionsSource = "session"
	OptionsSourceCatalog      OptionsSource = "catalog"
	OptionsSourceAgentDefault OptionsSource = "agent-default"
	OptionsSourceNone         OptionsSource = ""
)

// AgentRuntime runs turns on external agents — the agent-side counterpart
// of TextModelService. One Prompt is one agent turn: the agent runs its own
// tool loop, streams progress through onEvent, asks approve for permission
// decisions, and the runtime appends the resulting assistant message to
// the conversation — partial and stamped interrupted when ctx ends first.
//
// onEvent and approve may be called from the runtime's own goroutines;
// callers that need ordering serialize inside them.
type AgentRuntime interface {
	Prompt(ctx context.Context, p AgentPrompt, onEvent event.TurnEventHandler, approve Approver) error
	// Models lists the models the agent offers — empty when the agent
	// does not let clients choose.
	Models(ctx context.Context, agent *aggregate.Agent) ([]AgentModel, error)
	// Options lists the agent's session options for a room: its live
	// session's when one runs, else the best fallback evidence available. State
	// says what the answer means and source says where it came from.
	Options(ctx context.Context, agent *aggregate.Agent, conversationID string) ([]AgentOption, OptionsState, OptionsSource, error)
	// SetOption applies one option to the room's live session, if any.
	SetOption(ctx context.Context, conversationID, id, value string) error
	// Commands lists the slash commands the room's live session offers;
	// empty before its first turn.
	Commands(conversationID string) []AgentCommand
	// Steer delivers input into the room's running turn when the agent
	// takes input mid-turn, and reports whether it did. delivered runs
	// just before the input is recorded in the turn and announced, so the
	// caller can settle its receipt first. False leaves the input with the
	// caller, which sends it after the turn.
	Steer(ctx context.Context, conversationID string, input valueobject.UserInput, recordID string, delivered func()) bool
	// Status reports the agent's process: whether it runs, its recent
	// stderr, and how it can be signed in to. It never starts the agent.
	Status(agentID string) AgentStatus
	// Authenticate signs the agent in with one of the methods it offers,
	// starting it when needed. A terminal method cannot run here: it is
	// interactive, so the owner runs SignInCommand in a terminal.
	Authenticate(ctx context.Context, agent *aggregate.Agent, methodID string) error
	// SignInCommand is the command line of one of the agent's terminal
	// sign-in methods, with the environment the agent itself launches
	// with, starting the agent when needed to learn its methods.
	SignInCommand(ctx context.Context, agent *aggregate.Agent, methodID string) (ShellCommand, error)
	// Logout signs the running agent out, when it supports that.
	Logout(ctx context.Context, agent *aggregate.Agent) error
	// Yield asks the room's running turn to stop at its next safe point —
	// once every tool call the agent has reported has finished — so the
	// turn ends in the ordinary way and the caller can start the next
	// prompt with the input the agent could not take mid-turn. It waits
	// for a safe point or the context to end, and reports whether a
	// running turn was marked to yield. Prompt owns the completion barrier.
	Yield(ctx context.Context, conversationID string) bool
	// Release ends the agent-side session bound to a conversation — on
	// delete, or when the agent's configuration changed.
	Release(ctx context.Context, conversationID string)
	// Discard removes one agent-side session a room no longer uses — the
	// one a deleted room had, or the one an edit or a change of runtime
	// left behind — when the agent can delete or close sessions. It starts
	// the agent's process when it must. The room's live session is dropped
	// only when it is that session: a room that already moved on to a new
	// one keeps it.
	Discard(ctx context.Context, agent *aggregate.Agent, conversationID, sessionID string)
	// Warm starts the agent's process ahead of its first prompt.
	Warm(ctx context.Context, agent *aggregate.Agent) error
	// ReapIdle stops the processes of agents that ran nothing for maxIdle,
	// except those kept resident (Agent.AutoStart). A room's next prompt
	// starts the process again and restores its session.
	ReapIdle(maxIdle time.Duration)
	// Reload stops the processes of one agent so the next prompt launches
	// it with its current configuration.
	Reload(agentID string)
	// Shutdown stops every agent process.
	Shutdown(ctx context.Context) error
}

// AgentState is where an agent process stands.
type AgentState string

const (
	AgentStopped AgentState = "stopped"
	AgentRunning AgentState = "running"
	AgentFailed  AgentState = "error"
)

// AgentAuthMethod is one way an agent can be signed in. A "terminal"
// method is interactive: Command is the command line the owner runs.
type AgentAuthMethod struct {
	ID          string
	Name        string
	Description string
	Type        string
	Command     []string
}

// AgentStatus is an agent process as the owner sees it. Logs are the
// latest stderr lines, kept across restarts; AuthMethods come from the
// last launch that got as far as initialize.
type AgentStatus struct {
	State       AgentState
	PID         int
	Uptime      time.Duration
	LastError   string
	Logs        []string
	AuthMethods []AgentAuthMethod
	CanLogout   bool
	AuthNeeded  bool
}

// AgentCandidate is a known agent CLI found on this host, ready to be
// registered as an Agent.
type AgentCandidate struct {
	ID          string
	Name        string
	Description string
	Command     string
	Args        []string
}

// AgentDetector finds known agent CLIs installed on the host.
type AgentDetector interface {
	Detect() []AgentCandidate
}
