// Package agent implements the AgentRuntime port over the Agent Client
// Protocol: each configured agent runs as one long-lived child process
// speaking JSON-RPC over stdio, and each conversation holds one session
// inside it. The package translates ACP session updates into turn events
// and one assistant message per prompt; it decides nothing about approval
// policy, persistence, or queueing.
package agent

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	"github.com/jahrulnr/nusashell/infrastructure/prompt"
	"github.com/jahrulnr/nusashell/infrastructure/service/agent/acp"
	coretext "github.com/jahrulnr/nusashell/infrastructure/service/core/text"
	"github.com/jahrulnr/nusashell/infrastructure/service/wire"
	"github.com/jahrulnr/nusashell/infrastructure/system"
)

// cancelGrace is how long a cancelled prompt may take to settle after
// session/cancel before the runtime stops waiting for the agent's reply.
const cancelGrace = 15 * time.Second

// recapMessages bounds the transcript recap a fresh session receives when
// the agent could not restore the conversation's earlier session.
const recapMessages = 40

// Options configure the runtime.
type Options struct {
	// ClientName and ClientVersion identify this harness to agents.
	ClientName    string
	ClientTitle   string
	ClientVersion string
	// Log receives process lifecycle diagnostics. Nil uses slog.Default.
	Log *slog.Logger
	// Workspace serves the agents' fs/read_text_file and fs/write_text_file
	// inside each session's folders; nil refuses them.
	Workspace drepo.Workspace
	// BuiltinSkillsDir is where the composition root installed NusaShell's
	// builtin skills. Session instructions name the SKILL.md files there so
	// an ACP agent can read them with its own file tools; "" omits the note.
	BuiltinSkillsDir string
	// Uploads keeps the pictures and sounds an agent sends inline in its
	// reply; nil drops them.
	Uploads drepo.Uploads
	// Catalog persists each agent's last seen offered surface; nil leaves
	// the picker with only the live session and the probe cache.
	Catalog drepo.AgentCatalogStore
}

// Runtime is the ACP-backed AgentRuntime.
type Runtime struct {
	opts Options
	log  *slog.Logger

	mu       sync.Mutex
	procs    map[string]*proc    // by agent id
	sessions map[string]*session // by conversation id
	models   map[string]modelCache
	closed   bool
	terms    *TerminalManager
	// status is each agent's log and last outcome, kept across process
	// restarts so a failed launch can still be inspected.
	status map[string]*agentStatus
}

// agentStatus is what outlives one agent process.
type agentStatus struct {
	logs       *acp.Ring
	lastErr    string
	authNeeded bool
	methods    []acp.AuthMethod
	logout     bool
	launch     []string // command and args, for terminal sign-in
}

// modelCache is the model list and default options probed from one launch
// configuration.
type modelCache struct {
	fingerprint string
	options     []dservice.AgentOption
	models      []dservice.AgentModel
}

// New returns a runtime with no running agents.
func New(opts Options) *Runtime {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	return &Runtime{opts: opts, log: opts.Log, procs: map[string]*proc{}, sessions: map[string]*session{}, models: map[string]modelCache{},
		terms: NewTerminalManager(opts.Log), status: map[string]*agentStatus{}}
}

var _ dservice.AgentRuntime = (*Runtime)(nil)

// proc is one running agent process and the sessions it serves.
type proc struct {
	agentID     string
	fingerprint string
	client      *acp.Client

	// autoStart keeps the process resident: the idle reaper skips it.
	autoStart bool

	mu       sync.Mutex
	sessions map[string]*session // by ACP session id
	dead     bool
	// caps is the agent's initialize-time capabilities, marshaled once —
	// part of the catalog record, unchanged across sessions.
	caps json.RawMessage
	// busy counts the operations in flight on the process, and lastUse is
	// when one last began or ended; together they say when it is idle.
	busy    int
	lastUse time.Time
	// early holds session updates that arrived before their session was
	// registered (agents announce commands while answering session/new).
	early map[string][]earlyUpdate
}

// earlyUpdate keeps the wire position of a pre-registration update so
// replaying it cannot roll back state the session/open response carried.
type earlyUpdate struct {
	raw json.RawMessage
	seq uint64
}

// session is one conversation's context inside an agent process.
type session struct {
	convID string
	acpID  string
	proc   *proc
	// folders is the working directory followed by extra directories the
	// session was opened with; a change reopens the session.
	folders []string
	// tools keys the MCP servers the session was opened with; a change
	// reopens the session.
	tools string

	mu       sync.Mutex
	turn     *turnSink             // the running prompt, nil while idle
	configs  json.RawMessage       // the session's config options as last reported
	modes    *acp.SessionModeState // legacy modes, when the agent reports them
	commands []dservice.AgentCommand
	// configSeq and modesSeq are the wire positions of the newest applied
	// config options and modes. Notifications and responses are processed
	// concurrently, so each writer applies only when it is newer on the
	// wire — an update that overtook the response it preceded on the wire
	// cannot roll the session back.
	configSeq uint64
	modesSeq  uint64
	// catalogModels and catalogCurrent are the session's model block kept
	// for catalog writes: verbatim ids from models.availableModels when the
	// agent reports it, else the config-options' own list.
	catalogModels  []dservice.AgentModel
	catalogCurrent string
	// modelSet records whether a model pick was set on this session since
	// it opened — as opposed to the value it arrived with. It gates the
	// one-time list refresh below: a session resumed onto a model it never
	// picked needs one republish, a pinned room does not need one per turn.
	modelSet bool
	// preamble is what the agent still has to be told before the next
	// ordinary input: the session's instructions, when it cannot take them
	// as a system prompt, and a recap of the thread, when its own session
	// is new. A slash command carries neither, so they wait for the input
	// after it.
	preamble preamble
	// instructions are the session's standing instructions, with the folders
	// the room works in, as a system prompt would carry them.
	instructions string
}

// preamble is what a session's first ordinary input carries ahead of the
// owner's words.
type preamble struct{ instructions, recap bool }

func (s *session) activeTurn() *turnSink {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.turn
}

// fingerprint changes whenever the launch configuration does, so a prompt
// after an edit starts a fresh process instead of reusing a stale one.
func fingerprint(a *aggregate.Agent) string {
	h := sha256.New()
	enc := json.NewEncoder(h)
	env := a.Env()
	names := make([]string, 0, len(env))
	for k := range env {
		names = append(names, k)
	}
	slices.Sort(names)
	pairs := make([]string, 0, len(names))
	for _, k := range names {
		pairs = append(pairs, k+"="+env[k])
	}
	_ = enc.Encode([]any{a.Command(), a.Args(), pairs})
	return hex.EncodeToString(h.Sum(nil))
}

// ensureProc returns a live process for the agent, starting one when none
// runs or the running one was launched from an older configuration.
func (r *Runtime) ensureProc(ctx context.Context, a *aggregate.Agent) (*proc, error) {
	fp := fingerprint(a)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, apperror.New(503, "agent runtime is shut down", nil)
	}
	if p := r.procs[a.ID()]; p != nil {
		if !p.isDead() && p.fingerprint == fp {
			r.mu.Unlock()
			return p, nil
		}
		delete(r.procs, a.ID())
		r.dropSessionsLocked(p)
		go func() { _ = p.client.Close() }()
	}
	r.mu.Unlock()

	p := &proc{agentID: a.ID(), fingerprint: fp, autoStart: a.AutoStart(), lastUse: time.Now(),
		sessions: map[string]*session{}, early: map[string][]earlyUpdate{}}
	caps := acp.ClientCapabilities{}
	// The advertised surface: boolean config options (codex fast-mode
	// gates on it) and form elicitation — the owner-answer contract now
	// exists. Everything else stays off: fs/terminal are dead surface on
	// codex, and url is not promised — its presentation still renders if
	// a request arrives anyway, but no agent has been shown to send one.
	caps.Session.ConfigOptions = &acp.SessionConfigOptionsCapabilities{Boolean: &struct{}{}}
	caps.Elicitation = map[string]any{"form": map[string]any{}}
	client, err := acp.Start(ctx, acp.ClientConfig{
		Command:      a.Command(),
		Args:         a.Args(),
		Env:          launchEnv(a),
		Log:          r.log.With("agent", a.ID()),
		ClientInfo:   acp.Implementation{Name: r.opts.ClientName, Title: r.opts.ClientTitle, Version: r.opts.ClientVersion},
		Capabilities: &caps,
	}, &handler{runtime: r, proc: p})
	if err != nil {
		r.noteFailure(a.ID(), err)
		if acp.IsAuthRequired(err) {
			return nil, errAuthRequired(err)
		}
		return nil, apperror.New(502, "agent failed to start", err)
	}
	p.client = client
	if raw, err := json.Marshal(client.Capabilities().AgentCapabilities); err == nil {
		p.caps = raw
	}
	r.mu.Lock()
	st := r.statusLocked(a.ID())
	st.lastErr, st.authNeeded = "", false
	st.methods, st.logout = client.AuthMethods(), client.Capabilities().SupportsLogout()
	st.launch = append([]string{a.Command()}, a.Args()...)
	r.mu.Unlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		go func() { _ = client.Close() }()
		return nil, apperror.New(503, "agent runtime is shut down", nil)
	}
	if other := r.procs[a.ID()]; other != nil && !other.isDead() && other.fingerprint == fp {
		// A concurrent prompt started the same agent first; keep that one.
		go func() { _ = client.Close() }()
		return other, nil
	}
	r.procs[a.ID()] = p
	return p, nil
}

// launchEnv is the environment an agent starts with: its own, plus a PATH
// that reaches a Node binary when the server's does not. Many adapters are
// "#!/usr/bin/env node" scripts, and a server started by a service manager
// has not been through the shell setup that puts nvm's Node on PATH.
func launchEnv(a *aggregate.Agent) map[string]string {
	env := a.Env()
	if _, set := env["PATH"]; set {
		return env
	}
	dir := system.NodeDir()
	path := os.Getenv("PATH")
	if dir == "" || slices.Contains(filepath.SplitList(path), dir) {
		return env
	}
	if env == nil {
		env = map[string]string{}
	}
	env["PATH"] = dir + string(os.PathListSeparator) + path
	return env
}

// begin marks one operation in flight on the process; the function it
// returns marks it over. A process with operations in flight is never idle.
func (p *proc) begin() (end func()) {
	p.mu.Lock()
	p.busy++
	p.lastUse = time.Now()
	p.mu.Unlock()
	return func() {
		p.mu.Lock()
		p.busy--
		p.lastUse = time.Now()
		p.mu.Unlock()
	}
}

func (p *proc) isDead() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dead
}

// dropSessionsLocked forgets every conversation session of p. Callers hold
// r.mu.
func (r *Runtime) dropSessionsLocked(p *proc) {
	for convID, s := range r.sessions {
		if s.proc == p {
			delete(r.sessions, convID)
		}
	}
}

// opened says how ensureSession got the conversation's session. fresh is a
// new agent-side context that has not seen the conversation's history;
// recovered is a fresh one opened because the agent could not restore the
// session the room had.
type opened struct{ fresh, recovered bool }

// ensureSession returns the conversation's session inside p, restoring the
// stored one when the agent can, else opening a fresh one.
func (r *Runtime) ensureSession(ctx context.Context, p *proc, conv *aggregate.Conversation, cwd string, extra []string, servers []dservice.MCPServer) (s *session, how opened, err error) {
	client := p.client
	// The project's other folders go to every agent that takes them; an
	// agent that does not is told about them in the session's instructions.
	listed := extra
	if !client.SupportsAdditionalDirectories() {
		extra = nil
	}
	folders := append([]string{cwd}, extra...)
	mcp := make([]acp.MCPServerConfig, 0, len(servers))
	names := make([]string, 0, len(servers))
	vars := func(m map[string]string) []acp.EnvVar {
		out := make([]acp.EnvVar, 0, len(m))
		for k, v := range m {
			out = append(out, acp.EnvVar{Name: k, Value: v})
		}
		slices.SortFunc(out, func(a, b acp.EnvVar) int { return strings.Compare(a.Name, b.Name) })
		return out
	}
	for _, m := range servers {
		cfg := acp.MCPServerConfig{Name: m.Name}
		switch m.Transport {
		case enum.MCPHTTP, enum.MCPSSE:
			if (m.Transport == enum.MCPHTTP && !client.SupportsMCPHTTP()) || (m.Transport == enum.MCPSSE && !client.SupportsMCPSSE()) {
				continue
			}
			cfg.Type, cfg.URL, cfg.Headers = m.Transport.String(), m.URL, vars(m.Headers)
		default:
			cfg.Command, cfg.Args, cfg.Env = m.Command, m.Args, vars(m.Env)
		}
		mcp = append(mcp, cfg)
		// The key changes with any part of the launch, so an edited
		// server reopens the session.
		key, _ := json.Marshal(cfg)
		names = append(names, string(key))
	}
	tools := strings.Join(names, "\n")
	replaced := false
	r.mu.Lock()
	if s := r.sessions[conv.ID()]; s != nil {
		if s.proc == p && s.acpID == conv.AgentSession() && slices.Equal(s.folders, folders) && s.tools == tools {
			r.mu.Unlock()
			return s, opened{}, nil
		}
		// The room's folders changed (its project was edited or it moved
		// project), or its tool servers did: restore the same session
		// under the new setup below. A room that forgot its session (a
		// history rewrite) opens a new one.
		replaced = true
		delete(r.sessions, conv.ID())
		s.proc.mu.Lock()
		delete(s.proc.sessions, s.acpID)
		s.proc.mu.Unlock()
	}
	r.mu.Unlock()

	// Agents that take a system prompt get NusaShell's guidance appended
	// to their own once per session, with the folders the room works in;
	// the others get the same text ahead of their first input.
	instructions := prompt.SystemPrompt("agent-session") + workspaceNote(cwd, listed) + builtinSkillsNote(r.opts.BuiltinSkillsDir)
	var system *acp.SystemPrompt
	takesSystemPrompt := client.Capabilities().SupportsSystemPrompt()
	if takesSystemPrompt {
		system = &acp.SystemPrompt{Mode: "append", Content: []acp.ContentBlock{{Type: "text", Text: instructions}}}
	}
	var state acp.SessionState
	stored := conv.AgentSession()
	restored := false
	if stored != "" {
		switch {
		case client.SupportsResume():
			state, err = client.ResumeSession(ctx, acp.ResumeSessionRequest{SessionID: stored, Cwd: cwd, AdditionalDirectories: extra, MCPServers: mcp, SystemPrompt: system})
		case client.SupportsLoad():
			// session/load replays history as session/update notifications.
			// No turn is attached yet, so the replay is dropped: this
			// conversation's record is already the source of truth.
			state, err = client.LoadSession(ctx, acp.LoadSessionRequest{SessionID: stored, Cwd: cwd, AdditionalDirectories: extra, MCPServers: mcp, SystemPrompt: system})
		default:
			err = errors.New("agent cannot restore sessions")
		}
		if err == nil {
			restored = true
			if state.SessionID == "" {
				state.SessionID = stored
			}
		} else if ctx.Err() != nil {
			return nil, opened{}, err
		} else {
			r.log.Info("agent session not restored, opening a new one", "agent", p.agentID, "conversation", conv.ID(), "error", err)
		}
	}
	if !restored {
		state, err = client.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, AdditionalDirectories: extra, MCPServers: mcp, SystemPrompt: system})
		if err != nil && system != nil && acp.IsInvalidParams(err) {
			// An agent that advertises the capability but rejects the
			// field still opens a plain session.
			state, err = client.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, AdditionalDirectories: extra, MCPServers: mcp})
			takesSystemPrompt = false
		}
		if err != nil {
			if acp.IsAuthRequired(err) {
				r.noteFailure(p.agentID, err)
				return nil, opened{}, errAuthRequired(err)
			}
			return nil, opened{}, apperror.New(502, "agent could not open a session", err)
		}
		if state.SessionID == "" {
			return nil, opened{}, apperror.New(502, "agent opened a session without an id", nil)
		}
	}

	// The response is the newest wire position: it stamps the seqs of the
	// state it carries so the pre-response updates replayed from early
	// below lose to it. A field the response did not carry stays at zero,
	// leaving those early updates the only source to apply.
	configSeq, modesSeq := uint64(0), uint64(0)
	if len(state.ConfigOptions) > 0 {
		configSeq = state.WireSeq
	}
	if state.Modes != nil {
		modesSeq = state.WireSeq
	}
	catalogModels, catalogCurrent := sessionCatalogModels(&state)
	s = &session{convID: conv.ID(), acpID: state.SessionID, proc: p, folders: folders, tools: tools, configs: state.ConfigOptions, modes: state.Modes,
		configSeq: configSeq, modesSeq: modesSeq, catalogModels: catalogModels, catalogCurrent: catalogCurrent,
		instructions: instructions, preamble: preamble{recap: !restored, instructions: !takesSystemPrompt && (!restored || replaced)}}
	p.mu.Lock()
	// Notifications and requests are routed by session id, so an agent that
	// hands two threads the same id would mix them up: one thread would see
	// the other's replies and approvals. Refuse the second.
	if other := p.sessions[s.acpID]; other != nil && other.convID != conv.ID() {
		p.mu.Unlock()
		return nil, opened{}, apperror.New(502, "agent gave this thread a session another thread already uses", nil)
	}
	p.sessions[s.acpID] = s
	early := p.early[s.acpID]
	delete(p.early, s.acpID)
	p.mu.Unlock()
	r.mu.Lock()
	r.sessions[conv.ID()] = s
	r.mu.Unlock()
	h := &handler{runtime: r, proc: p}
	for _, e := range early {
		h.OnNotification("session/update", s.acpID, e.raw, e.seq)
	}
	r.writeCatalog(s)
	return s, opened{fresh: !restored, recovered: stored != "" && !restored}, nil
}

// workspaceNote tells the agent which folders the thread works in when it
// works in more than one: the first is where commands start, the rest are
// the project's other folders, which the agent reaches by absolute path.
func workspaceNote(cwd string, extra []string) string {
	if len(extra) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nThis thread works in several folders, not only the current directory. ")
	b.WriteString("The primary folder, where commands start, is " + cwd + ". It also works in:\n")
	for _, dir := range extra {
		b.WriteString("- " + dir + "\n")
	}
	b.WriteString("Read, search, and change files in all of them by absolute path, as the task needs.\n")
	return b.String()
}

// builtinSkillsNote names the installed builtin skills for an agent that
// reads them as ordinary files. The composition root owns the directory; the
// adapter only renders the instruction beside the workspace note.
func builtinSkillsNote(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	dir = filepath.Clean(dir)
	return "\nNusaShell's builtin skills are installed under " + dir + ". When relevant, read " +
		filepath.Join(dir, "nusashell", "SKILL.md") + " or " + filepath.Join(dir, "coding-workflow", "SKILL.md") +
		" as ordinary files with your own file tools.\n"
}

// Prompt runs one agent turn and appends its assistant message.
func (r *Runtime) Prompt(ctx context.Context, in dservice.AgentPrompt, onEvent event.TurnEventHandler, approve dservice.Approver) error {
	if in.Agent == nil || in.Conversation == nil {
		return apperror.NewInternalError("agent prompt is incomplete", nil)
	}
	p, err := r.ensureProc(ctx, in.Agent)
	if err != nil {
		return err
	}
	end := p.begin()
	defer end()
	s, how, err := r.ensureSession(ctx, p, in.Conversation, in.Workspace, in.ExtraFolders, in.MCPServers)
	if err != nil {
		return err
	}
	in.Conversation.SetAgentSession(s.acpID)
	if how.recovered && onEvent != nil {
		onEvent(event.NewNoticeEvent(enum.NoticeInfo, "Session recovered",
			"The agent could not restore this thread's earlier session, so it started a new one and was given a recap of the thread."))
	}
	if err := r.selectModel(ctx, s, in.Model); err != nil {
		return err
	}
	// The stored picks apply in order, and the pick that selects the model
	// goes first: dependent options — codex's reasoning_effort, for one —
	// materialize only once the model pick resolves to a listed model, so
	// any read before that sees a shorter list (B-038).
	s.mu.Lock()
	modelOptID, _, _ := modelOption(s.configs)
	s.mu.Unlock()
	picks := in.Options
	if modelOptID != "" {
		picks = make(valueobject.ConfigSelections, 0, len(in.Options))
		for _, pick := range in.Options {
			if pick.ID == modelOptID {
				picks = append(picks, pick)
			}
		}
		for _, pick := range in.Options {
			if pick.ID != modelOptID {
				picks = append(picks, pick)
			}
		}
	}
	// Applying a pick can materialize the options another pick waits for
	// (the effort a model pick publishes, for one): re-read the offered
	// list after every SetConfig — applyOption already reads the fresh
	// configs — and retry what was deferred, bounded and stopping when a
	// round makes no progress. Picks already at their value recheck too:
	// a later set may have reset them. What stays unoffered is deferred,
	// never counted as applied.
	pending := picks
	for round := 0; round < applyOptionMaxRounds && len(pending) > 0; round++ {
		next := make(valueobject.ConfigSelections, 0, len(pending))
		progressed := false
		for _, pick := range pending {
			outcome, err := r.applyOption(ctx, s, pick.ID, pick.Value)
			switch {
			case err != nil:
				// The room's stored picks are applied before every turn. One
				// the agent's options no longer list is stale — the agent
				// narrowed its choices since — so the turn runs on the
				// session's current value instead of failing on a record
				// nothing here can fix.
				if errors.Is(err, errOptionValueNotOffered) {
					continue
				}
				return err
			case outcome == optionApplied:
				progressed = true
			default:
				// optionDeferred waits for a set to publish it;
				// optionAlreadyCurrent rechecks in case a later set
				// (a resetting parent) moved the value underneath it.
				next = append(next, pick)
			}
		}
		pending = next
		if !progressed {
			break
		}
	}
	if len(pending) > 0 {
		r.log.Warn("agent option picks deferred: the session does not offer them",
			"conversation", in.Conversation.ID(), "picks", pending)
	}

	blocks, taken := r.promptBlocks(ctx, in, s)
	sink := newTurnSink(ctx, onEvent, approve)
	sink.elicit = in.Elicit
	sink.folders = s.folders
	sink.workspace = in.Workspace
	if r.opts.Uploads != nil {
		sink.save = func(ctx context.Context, name, mediaType string, data []byte) (valueobject.Attachment, error) {
			_, a, err := r.opts.Uploads.Save(ctx, name, mediaType, data)
			return a, err
		}
	}
	s.mu.Lock()
	if s.turn != nil {
		s.mu.Unlock()
		return apperror.New(409, "agent session is already running a turn", nil)
	}
	s.turn = sink
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.turn = nil
		s.mu.Unlock()
	}()

	// The prompt call outlives a cancelled ctx: cancellation is a
	// session/cancel notification, after which the agent still answers the
	// prompt with stopReason "cancelled" and the updates it already sent
	// stay ordered before that answer. The grace bound covers an agent
	// that never answers.
	callCtx, stopCall := context.WithCancel(context.WithoutCancel(ctx))
	defer stopCall()
	stopAfter := context.AfterFunc(ctx, func() {
		_ = p.client.Cancel(s.acpID)
		time.AfterFunc(cancelGrace, stopCall)
	})
	defer stopAfter()

	if in.OnStarted != nil {
		callCtx = acp.WithRequestSent(callCtx, in.OnStarted)
	}
	res, err := p.client.Prompt(callCtx, s.acpID, blocks)
	if err != nil && ctx.Err() == nil {
		// The agent never took the input: what it was to be told first is
		// still owed to it.
		s.mu.Lock()
		s.preamble.instructions = s.preamble.instructions || taken.instructions
		s.preamble.recap = s.preamble.recap || taken.recap
		s.mu.Unlock()
	}
	msg, buildErr := sink.finish(res, err, ctx.Err() != nil)
	if buildErr != nil {
		return buildErr
	}
	if msg != nil {
		in.Conversation.Append(*msg)
	}
	switch {
	case ctx.Err() != nil:
		return apperror.New(408, "turn canceled", ctx.Err())
	case err != nil:
		if errors.Is(err, acp.ErrProcessExited) {
			r.forgetProc(p)
			return apperror.New(502, "agent process exited", processError(p, err))
		}
		if acp.IsUnknownSession(err) {
			// The agent lost the session; the next prompt opens a new one.
			r.Release(context.WithoutCancel(ctx), in.Conversation.ID())
			in.Conversation.SetAgentSession("")
		}
		return apperror.New(502, "agent turn failed", err)
	}
	return nil
}

// sessionCatalogModels reads the session's model block for the catalog:
// verbatim availableModels/currentModelId when the agent reports the field,
// else the model option's own list — the only model surface some agents
// (the fake) expose.
func sessionCatalogModels(state *acp.SessionState) ([]dservice.AgentModel, string) {
	if state.Models != nil {
		models := make([]dservice.AgentModel, 0, len(state.Models.AvailableModels))
		for _, m := range state.Models.AvailableModels {
			models = append(models, dservice.AgentModel{ID: m.ModelID, Name: m.Name, Current: m.ModelID == state.Models.CurrentModelID})
		}
		return models, state.Models.CurrentModelID
	}
	_, current, models := modelOption(state.ConfigOptions)
	return models, current
}

// writeCatalog records the offered surface this session last reported —
// what B-033 loses on every restart. A store failure is logged, never
// fatal: the session keeps its in-memory list either way.
func (r *Runtime) writeCatalog(s *session) {
	if r.opts.Catalog == nil {
		return
	}
	s.mu.Lock()
	cat := drepo.AgentCatalog{
		Options:      sessionOptions(s.configs, s.modes),
		Models:       slices.Clone(s.catalogModels),
		CurrentModel: s.catalogCurrent,
		Caps:         s.proc.caps,
		ProbedAt:     time.Now(),
	}
	s.mu.Unlock()
	if err := r.opts.Catalog.SaveCatalog(context.Background(), s.proc.agentID, cat); err != nil {
		r.log.Warn("the agent catalog could not be saved", "agent", s.proc.agentID, "error", err)
	}
}

// selectModel switches the session to model when the agent offers it and
// the session runs another one.
func (r *Runtime) selectModel(ctx context.Context, s *session, model string) error {
	if model == "" {
		return nil
	}
	s.mu.Lock()
	configID, current, models := modelOption(s.configs)
	s.mu.Unlock()
	if configID == "" {
		return nil
	}
	if current == model {
		// The model already matches the pick, so nothing is set — but a
		// session resumed on an older offered list may still miss the
		// dependent options this model materializes. A set to the current
		// value is the only read the protocol gives: the response republishes
		// the list. Once per session — a model set some agents answer by
		// resetting the dependent values is churn a pinned room does not
		// need every turn — and only when the value itself is offered.
		if s.modelSet || !slices.ContainsFunc(models, func(m dservice.AgentModel) bool { return m.ID == model }) {
			return nil
		}
		state, err := s.proc.client.SetConfig(ctx, s.acpID, configID, model)
		if err != nil {
			// A refresh changes nothing: the turn keeps the list it has.
			r.log.Warn("the model refresh was refused", "agent", s.proc.agentID, "session", s.acpID, "error", err)
			return nil
		}
		s.mu.Lock()
		s.modelSet = true
		if len(state.ConfigOptions) > 0 && state.WireSeq > s.configSeq {
			// A response names what the write changed; merging keeps the options it
			// does not mention (a notification still replaces: it is the authoritative
			// offered list).
			s.configs = absorbConfigResponse(s.configs, state.ConfigOptions)
			s.configSeq = state.WireSeq
		}
		s.mu.Unlock()
		r.writeCatalog(s)
		return nil
	}
	if !slices.ContainsFunc(models, func(m dservice.AgentModel) bool { return m.ID == model }) {
		return apperror.New(400, "the agent does not offer this model", fmt.Errorf("model %q", model))
	}
	state, err := s.proc.client.SetConfig(ctx, s.acpID, configID, model)
	if err != nil {
		return apperror.New(502, "agent refused the model change", err)
	}
	s.mu.Lock()
	s.modelSet = true
	if len(state.ConfigOptions) > 0 && state.WireSeq > s.configSeq {
		// A response names what the write changed; merging keeps the options it
		// does not mention (a notification still replaces: it is the authoritative
		// offered list).
		s.configs = absorbConfigResponse(s.configs, state.ConfigOptions)
		s.configSeq = state.WireSeq
	}
	s.mu.Unlock()
	r.writeCatalog(s)
	return nil
}

// Models probes the agent's model choice with a throwaway session. The
// result is cached per launch configuration.
func (r *Runtime) Models(ctx context.Context, a *aggregate.Agent) ([]dservice.AgentModel, error) {
	fp := fingerprint(a)
	r.mu.Lock()
	if c, ok := r.models[a.ID()]; ok && c.fingerprint == fp {
		r.mu.Unlock()
		return slices.Clone(c.models), nil
	}
	r.mu.Unlock()
	p, err := r.ensureProc(ctx, a)
	if err != nil {
		return nil, err
	}
	defer p.begin()()
	state, err := p.client.NewSession(ctx, acp.NewSessionRequest{Cwd: os.TempDir()})
	if err != nil {
		if acp.IsAuthRequired(err) {
			r.noteFailure(p.agentID, err)
			return nil, errAuthRequired(err)
		}
		return nil, apperror.New(502, "agent could not open a session", err)
	}
	discardCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	_ = p.client.DiscardSession(discardCtx, state.SessionID)
	cancel()
	_, _, models := modelOption(state.ConfigOptions)
	if models == nil {
		models = []dservice.AgentModel{}
	}
	r.mu.Lock()
	r.models[a.ID()] = modelCache{fingerprint: fp, models: models, options: sessionOptions(state.ConfigOptions, state.Modes)}
	r.mu.Unlock()
	if r.opts.Catalog != nil {
		catModels, catCurrent := sessionCatalogModels(&state)
		cat := drepo.AgentCatalog{Options: sessionOptions(state.ConfigOptions, state.Modes), Models: catModels,
			CurrentModel: catCurrent, Caps: p.caps, ProbedAt: time.Now()}
		if err := r.opts.Catalog.SaveCatalog(ctx, a.ID(), cat); err != nil {
			r.log.Warn("the agent catalog could not be saved", "agent", a.ID(), "error", err)
		}
	}
	return slices.Clone(models), nil
}

// processError adds the agent's last stderr lines to a process failure.
func processError(p *proc, err error) error {
	lines := p.client.Stderr()
	if len(lines) > 10 {
		lines = lines[len(lines)-10:]
	}
	if text := strings.TrimSpace(strings.Join(lines, "\n")); text != "" {
		return fmt.Errorf("%w: %s", err, text)
	}
	return err
}

// promptBlocks renders the user message for the agent and says what it
// told the agent ahead of it. The first ordinary input of a session carries
// what the agent is still owed — the session's instructions, when it cannot
// take them as a system prompt, and a bounded recap of the thread when its
// own session is new — so it is not answering mid-conversation with no
// context. A slash command carries none of that, so the agent recognizes it
// as a command; the preamble waits for the input after it.
func (r *Runtime) promptBlocks(ctx context.Context, in dservice.AgentPrompt, s *session) (blocks []acp.ContentBlock, taken preamble) {
	// A relayed prompt reaches the agent with its head, rendered here from the
	// message'''s provenance record: the store keeps the sender'''s text alone.
	user := r.userBlocks(ctx, valueobject.UserInput{
		Text:        coretext.UserText(in.Input),
		References:  in.Input.References(),
		Attachments: in.Input.Attachments(),
		Annotations: in.Input.Annotations(),
	}, in.Workspace, s)
	// A selected path at the very start ("/README.md …") would read as an
	// agent command; userBlocks already prefixes it with "Reference:" —
	// only a real command or a command selection suppresses the preamble.
	if len(user) == 0 || !strings.HasPrefix(user[0].Text, "/") {
		s.mu.Lock()
		taken, s.preamble = s.preamble, preamble{}
		s.mu.Unlock()
	}
	if taken.instructions {
		blocks = append(blocks, acp.ContentBlock{Type: "text", Text: "Standing instructions for this session, from the app you are connected through. " +
			"They are not part of the user's message, which follows.\n\n" + s.instructions})
	}
	if taken.recap {
		if recap := transcriptRecap(in.Conversation.Messages(), in.Input.ID()); recap != "" {
			blocks = append(blocks, acp.ContentBlock{Type: "text", Text: recap})
		}
	}
	if desc := in.Origin.Description(); desc != "" {
		blocks = append(blocks, acp.ContentBlock{Type: "text", Text: "Turn context from NusaShell: this turn came from " + desc +
			". This is app context, not part of the owner's message."})
	}
	return append(blocks, user...), taken
}

// userBlocks renders one user input as prompt content — shared by
// Prompt and mid-turn Steer so a steered input carries the same
// references, uploads, and marks as a fresh prompt. An empty text is
// skipped so an attachment-only input sends no empty text block.
func (r *Runtime) userBlocks(ctx context.Context, input valueobject.UserInput, workspace string, s *session) []acp.ContentBlock {
	// A selected path at the very start ("/README.md …") would read as an
	// agent command; only a command selection may lead the text.
	text := input.Text
	if len(input.References) > 0 && input.References[0].Start() == 0 && input.References[0].Kind() != enum.ReferenceCommand && strings.HasPrefix(input.References[0].Label(), "/") {
		text = "Reference: " + text
	}
	var blocks []acp.ContentBlock
	if text != "" {
		blocks = append(blocks, acp.ContentBlock{Type: "text", Text: text})
	}
	if note := valueobject.AnnotationNote(input.Annotations); note != "" {
		blocks = append(blocks, acp.ContentBlock{Type: "text", Text: note})
	}
	blocks = append(blocks, r.attachmentBlocks(ctx, input.Attachments, workspace, s)...)
	for _, ref := range input.References {
		if ref.Kind() == enum.ReferenceFile {
			blocks = append(blocks, acp.ContentBlock{Type: "resource_link", URI: (&url.URL{Scheme: "file", Path: ref.ID()}).String(), Name: filepath.Base(ref.ID())})
		}
	}
	if note := valueobject.ReferenceNote(input.References); note != "" {
		blocks = append(blocks, acp.ContentBlock{Type: "text", Text: note})
	}
	return blocks
}

// maxEmbeddedText bounds a text file sent inside the prompt; a larger one
// goes as a link the agent reads with its own tools.
const maxEmbeddedText = 1 << 20

// attachmentBlocks turns the message's attachments into prompt content the
// agent accepts: a picture inline for an agent that takes images, a sound
// for one that takes audio, a text file embedded for one that takes embedded
// context, and a link to the file for everything else — which an agent can
// open with its own tools. A file that cannot be read goes as a link too.
func (r *Runtime) attachmentBlocks(ctx context.Context, attachments []valueobject.Attachment, workspace string, s *session) []acp.ContentBlock {
	caps := s.proc.client.PromptCapabilities()
	root := wire.MediaRoot{Dir: workspace, Store: r.opts.Workspace}
	var blocks []acp.ContentBlock
	for _, a := range attachments {
		link := acp.ContentBlock{Type: "resource_link", URI: a.URI(), Name: a.Name(), MIMEType: a.MediaType()}
		inline := root.Store != nil && ctx.Err() == nil
		switch {
		case inline && a.Modality() == enum.ModalityImage && caps.Image,
			inline && a.Modality() == enum.ModalityAudio && caps.Audio:
			if data, mediaType, err := wire.ResolveBase64(a, root); err == nil {
				blocks = append(blocks, acp.ContentBlock{Type: a.Modality().String(), MIMEType: mediaType, Data: data, URI: a.URI(), Name: a.Name()})
				continue
			}
		case inline && caps.EmbeddedContext && textual(a):
			if data, _, err := wire.ResolveBase64(a, root); err == nil {
				if raw, err := base64.StdEncoding.DecodeString(data); err == nil && len(raw) <= maxEmbeddedText && utf8.Valid(raw) {
					blocks = append(blocks, acp.ContentBlock{Type: "resource", Resource: &acp.EmbeddedResource{URI: a.URI(), MIMEType: a.MediaType(), Text: string(raw)}})
					continue
				}
			}
		}
		// A reference is read by the agent itself, so the pin has to hold
		// before the link crosses: a file that changed since it was sent is
		// refused with a note, never served as a different picture through the
		// door the inline path does not use.
		if err := wire.VerifyPinnedFile(a, root); err != nil {
			reason := "unreadable now"
			if errors.Is(err, wire.ErrPinChanged) {
				reason = "content changed since it was sent"
			}
			blocks = append(blocks, acp.ContentBlock{Type: "text",
				Text: fmt.Sprintf("(attachment %q omitted: %s)", a.Name(), reason)})
			continue
		}
		blocks = append(blocks, link)
	}
	return blocks
}

// textual reports whether an attachment is text a prompt can embed.
func textual(a valueobject.Attachment) bool {
	if a.Modality() == enum.ModalityText {
		return true
	}
	switch a.MediaType() {
	case "application/json", "application/xml", "application/yaml", "application/x-yaml", "application/toml",
		"application/javascript", "application/x-sh", "application/sql":
		return true
	}
	return false
}

// transcriptRecap summarizes the visible dialogue before the current input.
func transcriptRecap(msgs []entity.Message, inputID string) string {
	var lines []string
	for _, m := range msgs {
		if m.ID() == inputID {
			break
		}
		text := strings.TrimSpace(m.Content())
		if text == "" {
			continue
		}
		switch m.Role() {
		case enum.RoleUser:
			if !m.Editable() {
				continue
			}
			lines = append(lines, "User: "+text)
		case enum.RoleAssistant:
			lines = append(lines, "Assistant: "+text)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	if len(lines) > recapMessages {
		lines = lines[len(lines)-recapMessages:]
	}
	return "This conversation continues from an earlier session you no longer have. " +
		"Earlier messages, oldest first:\n\n" + strings.Join(lines, "\n\n") +
		"\n\nWork the earlier session did with tools is not listed here, and anything it changed on disk may still be there: " +
		"check the files rather than trusting these messages alone." +
		"\n\nContinue from here. The next message is the user's new request."
}

// errAuthRequired is the owner-facing error of an agent that needs a
// sign-in first.
func errAuthRequired(err error) error {
	return apperror.New(401, "agent requires sign-in — open Settings → Agents to sign in", err)
}

// statusLocked returns the agent's kept status, creating it. Callers hold
// r.mu.
func (r *Runtime) statusLocked(agentID string) *agentStatus {
	st := r.status[agentID]
	if st == nil {
		st = &agentStatus{logs: acp.NewRing(400)}
		r.status[agentID] = st
	}
	return st
}

// noteFailure records why the agent could not start or open a session. A
// launch that died before writing anything leaves its error as the log.
func (r *Runtime) noteFailure(agentID string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.statusLocked(agentID)
	st.lastErr = err.Error()
	st.authNeeded = acp.IsAuthRequired(err)
	if len(st.logs.Lines()) == 0 {
		st.logs.Add(st.lastErr)
	}
}

// Status reports the agent's process without starting it.
func (r *Runtime) Status(agentID string) dservice.AgentStatus {
	r.mu.Lock()
	p := r.procs[agentID]
	st := r.statusLocked(agentID)
	out := dservice.AgentStatus{State: dservice.AgentStopped, LastError: st.lastErr, Logs: st.logs.Lines(),
		CanLogout: st.logout, AuthNeeded: st.authNeeded}
	for _, m := range st.methods {
		am := dservice.AgentAuthMethod{ID: m.ID, Name: m.Name, Description: m.Description, Type: m.Type}
		if m.Type == "terminal" {
			am.Command = append(slices.Clone(st.launch), m.Args...)
		}
		out.AuthMethods = append(out.AuthMethods, am)
	}
	r.mu.Unlock()
	switch {
	case p != nil && !p.isDead() && p.client.Alive():
		out.State, out.PID, out.Uptime = dservice.AgentRunning, p.client.PID(), p.client.Uptime()
	case out.LastError != "":
		out.State = dservice.AgentFailed
	}
	return out
}

// SignInCommand is the agent's launch with a terminal method's arguments
// and variables added, the way the ACP terminal-auth extension asks.
func (r *Runtime) SignInCommand(ctx context.Context, a *aggregate.Agent, methodID string) (dservice.ShellCommand, error) {
	p, err := r.ensureProc(ctx, a)
	if err != nil && !acp.IsAuthRequired(err) {
		return dservice.ShellCommand{}, err
	}
	var methods []acp.AuthMethod
	if p != nil {
		methods = p.client.AuthMethods()
	} else {
		r.mu.Lock()
		methods = slices.Clone(r.statusLocked(a.ID()).methods)
		r.mu.Unlock()
	}
	for _, m := range methods {
		if m.ID != methodID {
			continue
		}
		if m.Type != "terminal" {
			return dservice.ShellCommand{}, apperror.New(400, "that sign-in method does not run in a terminal", nil)
		}
		env := launchEnv(a)
		if env == nil {
			env = map[string]string{}
		}
		maps.Copy(env, m.Env)
		argv := append(append([]string{a.Command()}, a.Args()...), m.Args...)
		return dservice.ShellCommand{Argv: argv, Env: env}, nil
	}
	return dservice.ShellCommand{}, apperror.New(400, "the agent does not offer that sign-in method", nil)
}

// Authenticate signs the agent in with methodID, starting it when needed.
func (r *Runtime) Authenticate(ctx context.Context, a *aggregate.Agent, methodID string) error {
	p, err := r.ensureProc(ctx, a)
	if err != nil {
		return err
	}
	defer p.begin()()
	var method *acp.AuthMethod
	for _, m := range p.client.AuthMethods() {
		if m.ID == methodID {
			method = &m
			break
		}
	}
	if method == nil {
		return apperror.New(400, "the agent does not offer that sign-in method", nil)
	}
	if method.Type == "terminal" {
		return apperror.New(400, "this sign-in method runs in a terminal", nil)
	}
	if err := p.client.Authenticate(ctx, methodID); err != nil {
		return apperror.New(502, "agent sign-in failed", err)
	}
	r.mu.Lock()
	st := r.statusLocked(a.ID())
	st.lastErr, st.authNeeded = "", false
	delete(r.models, a.ID())
	r.mu.Unlock()
	return nil
}

// Logout signs the running agent out and stops it, so the next turn
// starts signed out.
func (r *Runtime) Logout(ctx context.Context, a *aggregate.Agent) error {
	p, err := r.ensureProc(ctx, a)
	if err != nil {
		return err
	}
	defer p.begin()()
	if !p.client.Capabilities().SupportsLogout() {
		return apperror.New(400, "the agent does not support signing out", nil)
	}
	if err := p.client.Logout(ctx); err != nil {
		return apperror.New(502, "agent sign-out failed", err)
	}
	r.Reload(a.ID())
	return nil
}

// forgetProc drops a dead process and every session it held.
func (r *Runtime) forgetProc(p *proc) {
	p.mu.Lock()
	p.dead = true
	p.mu.Unlock()
	r.mu.Lock()
	if r.procs[p.agentID] == p {
		delete(r.procs, p.agentID)
	}
	r.dropSessionsLocked(p)
	r.mu.Unlock()
}

// Steer delivers input into the room's running turn: through the
// _session/steering extension, or as a second prompt to an agent that
// takes prompts mid-turn. The agent's updates wait while the steering
// call is in flight, so the input is recorded before the answer to it.
func (r *Runtime) Steer(ctx context.Context, conversationID string, input valueobject.UserInput, delivered func()) bool {
	r.mu.Lock()
	s := r.sessions[conversationID]
	r.mu.Unlock()
	if s == nil {
		return false
	}
	t := s.activeTurn()
	if t == nil {
		return false
	}
	caps := s.proc.client.Capabilities()
	blocks := r.userBlocks(ctx, input, t.workspace, s)
	switch {
	case caps.SupportsSteering():
		if !t.gate() {
			return false
		}
		sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		res, err := s.proc.client.Steer(sctx, s.acpID, blocks)
		cancel()
		if err != nil || !res.Accepted() {
			r.log.Info("agent did not take steering", "conversation", conversationID, "outcome", res.Outcome, "error", err)
		}
		return t.release(input, err == nil && res.Accepted(), delivered)
	case caps.SupportsMidTurn():
		// The agent answers a mid-turn prompt only when the turn ends, with
		// the turn's own stop reason: sending it is the delivery.
		if !t.gate() || !t.release(input, true, delivered) {
			return false
		}
		go func() {
			if _, err := s.proc.client.Prompt(t.ctx, s.acpID, blocks); err != nil && t.ctx.Err() == nil {
				r.log.Warn("mid-turn prompt failed", "conversation", conversationID, "error", err)
			}
		}()
		return true
	}
	return false
}

// yieldNotice is how long an agent may take to answer the stop a yield asks
// of it before the owner is told their message waits for the turn's end.
const yieldNotice = 15 * time.Second

// Yield stops the room's running turn at a safe point: once every tool the
// agent reported has finished, it cancels the prompt, and the agent answers
// with the turn's own stop reason. The turn then ends in the ordinary way,
// and the caller starts the next prompt with the input the agent could not
// take mid-turn. An agent that does not answer within yieldNotice keeps its
// turn, and the input waits for its end.
func (r *Runtime) Yield(ctx context.Context, conversationID string) bool {
	r.mu.Lock()
	s := r.sessions[conversationID]
	r.mu.Unlock()
	if s == nil {
		return false
	}
	t := s.activeTurn()
	if t == nil || !t.beginYield() {
		return false
	}
	for {
		if ctx.Err() != nil || t.ctx.Err() != nil {
			return true
		}
		if t.settled() {
			break
		}
		select {
		case <-ctx.Done():
			return true
		case <-t.ctx.Done():
			return true
		case <-time.After(100 * time.Millisecond):
		}
	}
	if ctx.Err() != nil || t.ctx.Err() != nil {
		return true
	}
	_ = s.proc.client.Cancel(s.acpID)
	time.AfterFunc(yieldNotice, func() {
		if s.activeTurn() == t {
			t.notify(enum.NoticeWarning, "The agent is still working", "It has not stopped yet. Your message is sent when this turn ends.")
		}
	})
	return true
}

// Discard removes one agent-side session a room no longer uses: the session
// of a deleted room, or the one an edit or a change of runtime left behind.
// It deletes the session when the agent can, and closes it otherwise, which
// keeps the agent's own history free of threads that no longer exist. The
// room's live session is dropped only when it is the one named; a room that
// already moved on to a new session keeps it. Discard starts the agent when it
// has to, and an agent that cannot be started leaves its session as it was.
func (r *Runtime) Discard(ctx context.Context, a *aggregate.Agent, conversationID, sessionID string) {
	var p *proc
	r.mu.Lock()
	if s := r.sessions[conversationID]; s != nil && (sessionID == "" || s.acpID == sessionID) {
		delete(r.sessions, conversationID)
		s.proc.mu.Lock()
		delete(s.proc.sessions, s.acpID)
		s.proc.mu.Unlock()
		sessionID, p = s.acpID, s.proc
	}
	r.mu.Unlock()
	if sessionID == "" {
		return
	}
	if p == nil {
		var err error
		if p, err = r.ensureProc(ctx, a); err != nil {
			r.log.Info("could not start the agent to delete a session", "agent", a.ID(), "error", err)
			return
		}
	}
	defer p.begin()()
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := p.client.DiscardSession(dctx, sessionID); err != nil {
		r.log.Info("agent did not delete a session", "agent", a.ID(), "error", err)
	}
}

// Warm starts the agent's process ahead of its first prompt.
func (r *Runtime) Warm(ctx context.Context, a *aggregate.Agent) error {
	p, err := r.ensureProc(ctx, a)
	if err != nil {
		return err
	}
	defer p.begin()()
	return nil
}

// ReapIdle stops the processes that ran nothing for maxIdle. Their rooms
// keep their session handles, so the next prompt starts the process again
// and restores the session. A process kept resident, or with an operation in
// flight, stays.
func (r *Runtime) ReapIdle(maxIdle time.Duration) {
	r.mu.Lock()
	var idle []*proc
	for id, p := range r.procs {
		p.mu.Lock()
		stale := !p.dead && !p.autoStart && p.busy == 0 && time.Since(p.lastUse) >= maxIdle
		p.mu.Unlock()
		if !stale {
			continue
		}
		delete(r.procs, id)
		r.dropSessionsLocked(p)
		idle = append(idle, p)
	}
	r.mu.Unlock()
	for _, p := range idle {
		r.log.Info("stopping idle agent process", "agent", p.agentID)
		_ = p.client.Close()
	}
}

// Release ends the conversation's agent-side session.
func (r *Runtime) Release(ctx context.Context, conversationID string) {
	r.mu.Lock()
	s := r.sessions[conversationID]
	delete(r.sessions, conversationID)
	r.mu.Unlock()
	if s == nil {
		return
	}
	s.proc.mu.Lock()
	delete(s.proc.sessions, s.acpID)
	s.proc.mu.Unlock()
	closeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = s.proc.client.CloseSession(closeCtx, s.acpID)
}

// Reload stops the agent's process; the next prompt starts it again.
func (r *Runtime) Reload(agentID string) {
	r.mu.Lock()
	delete(r.models, agentID)
	p := r.procs[agentID]
	delete(r.procs, agentID)
	if p != nil {
		r.dropSessionsLocked(p)
	}
	r.mu.Unlock()
	if p != nil {
		_ = p.client.Close()
	}
}

// Shutdown stops every agent process.
func (r *Runtime) Shutdown(ctx context.Context) error {
	r.terms.CloseAll()
	r.mu.Lock()
	r.closed = true
	procs := make([]*proc, 0, len(r.procs))
	for _, p := range r.procs {
		procs = append(procs, p)
	}
	r.procs = map[string]*proc{}
	r.sessions = map[string]*session{}
	r.mu.Unlock()
	var wg sync.WaitGroup
	for _, p := range procs {
		wg.Go(func() { _ = p.client.Close() })
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// handler routes one process's notifications and requests to the session
// whose turn is running.
type handler struct {
	runtime *Runtime
	proc    *proc
}

func (h *handler) session(acpID string) *session {
	h.proc.mu.Lock()
	defer h.proc.mu.Unlock()
	return h.proc.sessions[acpID]
}

func (h *handler) OnNotification(method, sessionID string, raw json.RawMessage, seq uint64) {
	if method != "session/update" {
		return
	}
	s := h.session(sessionID)
	if s == nil {
		// Not registered yet: keep a few for when it is.
		h.proc.mu.Lock()
		if list := h.proc.early[sessionID]; len(list) < 20 && len(h.proc.early) < 16 {
			h.proc.early[sessionID] = append(list, earlyUpdate{raw: append(json.RawMessage(nil), raw...), seq: seq})
		}
		h.proc.mu.Unlock()
		return
	}
	var u acp.SessionUpdate
	if err := json.Unmarshal(raw, &u); err != nil {
		return
	}
	switch u.SessionUpdate {
	case "config_option_update":
		// The notification carries the authoritative offered list: replace,
		// never merge. An option the update does not name is gone — keeping it
		// would leave a ghost the next set_config_option fails on.
		if len(u.ConfigOptions) > 0 {
			s.mu.Lock()
			// Newer wire position than every applied write only: an update
			// dispatched before a response may still run after it.
			updated := false
			if seq > s.configSeq {
				s.configs = u.ConfigOptions
				s.configSeq = seq
				updated = true
			}
			s.mu.Unlock()
			if updated {
				h.runtime.writeCatalog(s)
			}
		}
		return
	case "available_commands_update":
		var list []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Input       *struct {
				Hint string `json:"hint"`
			} `json:"input"`
		}
		if json.Unmarshal(u.AvailableCommands, &list) != nil {
			return
		}
		cmds := make([]dservice.AgentCommand, 0, len(list))
		for _, c := range list {
			if c.Name == "" {
				continue
			}
			cmd := dservice.AgentCommand{Name: c.Name, Description: c.Description}
			if c.Input != nil {
				cmd.Hint = c.Input.Hint
			}
			cmds = append(cmds, cmd)
		}
		s.mu.Lock()
		s.commands = cmds
		s.mu.Unlock()
		return
	case "current_mode_update":
		s.mu.Lock()
		if s.modes != nil && seq > s.modesSeq {
			s.modes.CurrentModeID = u.CurrentModeID
			s.modesSeq = seq
		}
		s.mu.Unlock()
		return
	}
	if t := s.activeTurn(); t != nil {
		t.update(u)
	}
}

func (h *handler) OnRequest(ctx context.Context, method string, params json.RawMessage) (any, *acp.RPCError) {
	switch method {
	case "session/request_permission":
	case "fs/read_text_file", "fs/write_text_file":
		return h.file(ctx, method, params)
	case "elicitation/create":
		var req struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(params, &req)
		s := h.session(req.SessionID)
		if s == nil {
			return nil, &acp.RPCError{Code: acp.CodeInvalidParams, Message: "unknown session"}
		}
		t := s.activeTurn()
		if t == nil {
			return valueobject.ElicitationAnswer{Action: "cancel"}, nil
		}
		answer, err := t.elicitation(ctx, params)
		if err != nil {
			return nil, &acp.RPCError{Code: acp.CodeInvalidParams, Message: err.Error()}
		}
		return answer, nil
	case "terminal/create", "terminal/output", "terminal/wait_for_exit", "terminal/kill", "terminal/release":
		var ref struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(params, &ref)
		s := h.session(ref.SessionID)
		if s == nil {
			return nil, &acp.RPCError{Code: acp.CodeInvalidParams, Message: "unknown session"}
		}
		return h.runtime.terms.Handle(ctx, method, params, s.folders, func(u TerminalUpdate) {
			if t := s.activeTurn(); t != nil {
				t.terminal(u)
			}
		})
	default:
		return nil, &acp.RPCError{Code: acp.CodeMethodNotFound, Message: "method not supported by this client: " + method}
	}
	var req acp.PermissionRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &acp.RPCError{Code: acp.CodeInvalidParams, Message: "invalid permission request"}
	}
	s := h.session(req.SessionID)
	if s == nil {
		return cancelledPermission(), nil
	}
	t := s.activeTurn()
	if t == nil {
		return cancelledPermission(), nil
	}
	return t.permission(ctx, req), nil
}

func (h *handler) OnStderr(line string) {
	h.runtime.mu.Lock()
	h.runtime.statusLocked(h.proc.agentID).logs.Add(line)
	h.runtime.mu.Unlock()
}

func (h *handler) OnExit(error) { h.runtime.forgetProc(h.proc) }

func cancelledPermission() acp.PermissionResponse {
	return acp.PermissionResponse{Outcome: acp.PermissionOutcome{Outcome: "cancelled"}}
}

// file serves fs/read_text_file and fs/write_text_file inside the
// session's folders through the workspace store. Writes to secret or
// execution-control files (.env, .git/hooks, …) are refused.
func (h *handler) file(ctx context.Context, method string, params json.RawMessage) (any, *acp.RPCError) {
	var req struct {
		SessionID string `json:"sessionId"`
		Path      string `json:"path"`
		Content   string `json:"content"`
		Line      *int   `json:"line"`
		Limit     *int   `json:"limit"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &acp.RPCError{Code: acp.CodeInvalidParams, Message: "invalid file request"}
	}
	s := h.session(req.SessionID)
	if s == nil || h.runtime.opts.Workspace == nil {
		return nil, &acp.RPCError{Code: acp.CodeInvalidParams, Message: "unknown session"}
	}
	if !filepath.IsAbs(req.Path) {
		return nil, &acp.RPCError{Code: acp.CodeInvalidParams, Message: "path must be absolute"}
	}
	write := method == "fs/write_text_file"
	clean := filepath.Clean(req.Path)
	if write && sensitivePath(clean) {
		return nil, &acp.RPCError{Code: acp.CodeInvalidParams, Message: "writing secret or execution-control files is refused; do it outside the agent"}
	}
	for _, root := range s.folders {
		rel, err := filepath.Rel(root, clean)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			continue
		}
		ws, err := h.runtime.opts.Workspace.Open(ctx, root)
		if err != nil {
			return nil, &acp.RPCError{Code: acp.CodeInternalError, Message: err.Error()}
		}
		defer ws.Close()
		name := filepath.ToSlash(rel)
		if write {
			if dir := filepath.ToSlash(filepath.Dir(rel)); dir != "." {
				if err := ws.MkdirAll(dir, 0o755); err != nil {
					return nil, &acp.RPCError{Code: acp.CodeInternalError, Message: err.Error()}
				}
			}
			if err := ws.WriteFile(name, []byte(req.Content), 0o644); err != nil {
				return nil, &acp.RPCError{Code: acp.CodeInternalError, Message: err.Error()}
			}
			return map[string]any{}, nil
		}
		if info, err := ws.Stat(name); err == nil && info.Size() > maxAgentRead {
			return nil, &acp.RPCError{Code: acp.CodeInvalidParams, Message: "file is too large to read"}
		}
		data, err := ws.ReadFile(name)
		if err != nil {
			return nil, &acp.RPCError{Code: acp.CodeResourceNotFound, Message: err.Error()}
		}
		lines := strings.SplitAfter(string(data), "\n")
		start, end := 0, len(lines)
		if req.Line != nil && *req.Line > 1 {
			start = min(*req.Line-1, len(lines))
		}
		if req.Limit != nil && *req.Limit >= 0 && start+*req.Limit < end {
			end = start + *req.Limit
		}
		return map[string]any{"content": strings.Join(lines[start:end], "")}, nil
	}
	return nil, &acp.RPCError{Code: acp.CodeInvalidParams, Message: "path " + req.Path + " is outside the session workspace"}
}

// maxAgentRead caps one fs/read_text_file.
const maxAgentRead = 16 << 20

// sensitivePath is a file an agent must not write through the client:
// environment secrets and git hooks.
func sensitivePath(p string) bool {
	base := strings.ToLower(filepath.Base(p))
	if base == ".env" || strings.HasPrefix(base, ".env.") || base == "mcp_config.json" {
		return true
	}
	parts := strings.Split(filepath.Clean(p), string(filepath.Separator))
	for i, part := range parts {
		if strings.EqualFold(part, ".git") && i+1 < len(parts) && strings.EqualFold(parts[i+1], "hooks") {
			return true
		}
	}
	return false
}

// Options lists the room's session options: the live session's, else the
// agent's defaults from the probe session.
func (r *Runtime) Options(ctx context.Context, a *aggregate.Agent, conversationID string) ([]dservice.AgentOption, error) {
	r.mu.Lock()
	s := r.sessions[conversationID]
	r.mu.Unlock()
	if s != nil && s.proc.agentID == a.ID() && !s.proc.isDead() {
		s.mu.Lock()
		defer s.mu.Unlock()
		return sessionOptions(s.configs, s.modes), nil
	}
	// Stored before the probe: the catalog holds the list a real session
	// last reported — a throwaway probe session can offer less (codex's
	// default model never surfaces its effort option).
	// Stored before the probe: the catalog holds the list a real session
	// last reported — a throwaway probe session can offer less (codex's
	// default model never surfaces its effort option).
	if r.opts.Catalog != nil {
		if cat, err := r.opts.Catalog.Catalog(ctx, a.ID()); err != nil {
			r.log.Warn("the agent catalog could not be read", "agent", a.ID(), "error", err)
		} else if len(cat.Options) > 0 {
			return slices.Clone(cat.Options), nil
		}
	}
	if _, err := r.Models(ctx, a); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.models[a.ID()].options), nil
}

// SetOption applies an option to the room's live session; without one the
// next turn applies it.
func (r *Runtime) SetOption(ctx context.Context, conversationID, id, value string) error {
	r.mu.Lock()
	s := r.sessions[conversationID]
	r.mu.Unlock()
	if s == nil || s.proc.isDead() {
		return nil
	}
	_, err := r.applyOption(ctx, s, id, value)
	return err
}

// errOptionValueNotOffered marks a pick of an option the session does offer but
// with another set of values. The picker refuses it (nothing may be recorded);
// a turn that inherited such a pick from an older session skips it, because a
// stale record must not fail a turn the owner can no longer fix from there.
var errOptionValueNotOffered = errors.New("option value not offered")

// optionOutcome is what applyOption did with one pick. Step 5 turns the
// deferred kind into stored-pick semantics; the turn already must not count
// an unoffered id as applied.
type optionOutcome int

const (
	// optionApplied: a SetConfig/SetMode ran and the session took it.
	optionApplied optionOutcome = iota
	// optionAlreadyCurrent: the session already held the pick's value.
	optionAlreadyCurrent
	// optionDeferred: no option by this id is offered now — kept for a
	// later round or a later session, never reported as applied.
	optionDeferred
)

// applyOptionMaxRounds bounds the apply-retry fixpoint: three rounds apply a
// chain three deep (model → dependent → dependent-of-dependent), and an
// endlessly-materializing chain stops there instead of holding the turn.
const applyOptionMaxRounds = 3

// applyOption sets one option when the session offers it with another
// value: legacy modes through session/set_mode, config options through
// session/set_config_option. An id the session does not offer is deferred,
// not silently applied — a later SetConfig may materialize it. A value the
// offered option does not list is refused, so no caller can record a pick
// the agent would never take.
func (r *Runtime) applyOption(ctx context.Context, s *session, id, value string) (optionOutcome, error) {
	s.mu.Lock()
	opts := sessionOptions(s.configs, s.modes)
	s.mu.Unlock()
	i := slices.IndexFunc(opts, func(o dservice.AgentOption) bool { return o.ID == id })
	var o dservice.AgentOption
	if i >= 0 {
		if opts[i].Current == value {
			return optionAlreadyCurrent, nil
		}
		o = opts[i]
	} else {
		// The model option is kept out of the picker's list but a stored
		// pick on its id still applies — ahead of the dependent options the
		// model pick materializes. Any other id is an option the session
		// does not offer: deferred, to be retried after a SetConfig that may
		// publish it or kept until it does.
		s.mu.Lock()
		configID, current, models := modelOption(s.configs)
		modelSet := s.modelSet
		s.mu.Unlock()
		if id != configID {
			return optionDeferred, nil
		}
		if current == value && modelSet {
			return optionAlreadyCurrent, nil
		}
		// current == value without a resolved set still goes through: the
		// same once-per-session republish selectModel runs — a resumed
		// session may sit on this model without its dependents offered.
		o = dservice.AgentOption{ID: configID, Type: "select", Current: current,
			Choices: make([]dservice.OptionChoice, 0, len(models))}
		for _, m := range models {
			o.Choices = append(o.Choices, dservice.OptionChoice{Value: m.ID, Name: m.Name})
		}
	}
	// A value the offered option does not list is refused, so no caller can
	// record a pick the agent would never take. Offers is the same rule the
	// picker validates against, so the two can never disagree.
	if !o.Offers(value) {
		message := fmt.Sprintf("option %q does not offer %q", id, value)
		if o.Type == "boolean" {
			message = fmt.Sprintf("option %q takes true or false, not %q", id, value)
		}
		return optionDeferred, apperror.New(400, message, errOptionValueNotOffered)
	}
	// The synthetic mode option is legacy: no config option carries its id.
	s.mu.Lock()
	var ids []struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(s.configs, &ids)
	legacy := id == legacyModeID && s.modes != nil && !slices.ContainsFunc(ids, func(c struct {
		ID string `json:"id"`
	}) bool {
		return c.ID == id
	})
	s.mu.Unlock()
	if legacy {
		seq, err := s.proc.client.SetMode(ctx, s.acpID, value)
		if err != nil {
			return optionDeferred, apperror.New(502, "agent refused the mode change", err)
		}
		s.mu.Lock()
		if seq > s.modesSeq {
			s.modes.CurrentModeID = value
			s.modesSeq = seq
		}
		s.mu.Unlock()
		r.writeCatalog(s)
		return optionApplied, nil
	}
	var v any = value
	if o.Type == "boolean" {
		v = value == "true"
	}
	state, err := s.proc.client.SetConfig(ctx, s.acpID, id, v)
	if err != nil {
		return optionDeferred, apperror.New(502, "agent refused the option change", err)
	}
	s.mu.Lock()
	if i < 0 {
		s.modelSet = true
	}
	if len(state.ConfigOptions) > 0 && state.WireSeq > s.configSeq {
		// A response names what the write changed; merging keeps the options it
		// does not mention instead of narrowing the session's catalog to one
		// entry (the bug that dropped a materialized effort from the list).
		s.configs = absorbConfigResponse(s.configs, state.ConfigOptions)
		s.configSeq = state.WireSeq
	}
	s.mu.Unlock()
	r.writeCatalog(s)
	return optionApplied, nil
}

// Commands lists the slash commands of the room's live session.
func (r *Runtime) Commands(conversationID string) []dservice.AgentCommand {
	r.mu.Lock()
	s := r.sessions[conversationID]
	r.mu.Unlock()
	if s == nil {
		return []dservice.AgentCommand{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.commands)
}
