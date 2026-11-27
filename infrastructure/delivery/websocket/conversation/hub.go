package wsconversation

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Turn admission, socket, and journal bounds — deployment constants,
// not contract. The hub admits at most maxActiveTurns running turns and
// maxSockets live sockets; a turn's journal is the bounded frame ring
// `turn.watch` replays, retained briefly after terminal.
const (
	maxActiveTurns     = 32
	maxRetainedTurns   = 32 // terminal journals kept alongside actives
	maxSockets         = 64
	journalMaxFrames   = 512
	journalMaxBytes    = 4 << 20 // 4 MiB
	journalMaxAge      = 2 * time.Minute
	outboundChunkBytes = 32 << 10 // max bytes of text per turn.delta frame
	maxFrameBytes      = 1 << 20  // hard cap on any single outbound frame
	// replayBurst bounds the journaled frames one turn.watch answer may
	// enqueue at once. Half a socket's outbound queue leaves the other
	// half to the live traffic racing the replay — a deeper replay
	// answers with the bounded resync snapshot instead of flooding the
	// queue and getting the watcher's own socket killed.
	replayBurst = outboundQueueCap / 2
	// maxSteerInputs bounds a send turn's lifetime web steering
	// submissions — the receipt snapshot stays a small fixed-size
	// document, and rejected submissions never truncate.
	maxSteerInputs = 32
	// maxControlRequestIDBytes bounds requestIds on turn.steer and
	// turn.steer_recall — the steer command's requestId becomes the
	// input's inputId and rides every snapshot entry.
	maxControlRequestIDBytes = 128
	// shutdownTurnGrace is how long the hub waits for a transport turn's
	// terminal frame before it hands the socket layer over. Persistence is
	// the barrier's turn phase, not this wait: the drain gets its own phase
	// with its own budget, so the grace only covers landing frames already
	// written.
	shutdownTurnGrace  = 2 * time.Second
	shutdownDrainGrace = 3 * time.Second
)

// Sender is the conversation send/edit/resume use case — the same
// application path the CLI drives. Send's receipt carries the only fact
// the terminal frame reports: whether the user line actually committed.
// Edit and Resume terminals never claim persistence.
type Sender interface {
	Send(ctx context.Context, convID, providerID, modelID string,
		message string, attachments []valueobject.Attachment,
		opts appjob.SendOptions,
		onEvent event.TurnEventHandler) (appconversation.SendResult, error)
	Edit(ctx context.Context, convID, providerID, modelID, msgID, content string,
		opts appjob.SendOptions,
		onEvent event.TurnEventHandler) error
	Resume(ctx context.Context, convID, providerID, modelID string,
		opts appjob.SendOptions,
		onEvent event.TurnEventHandler) error
}

// Uploads resolves the files a send names to their attachments.
// *appconversation.ConversationApp satisfies it.
type Uploads interface {
	Uploaded(ctx context.Context, ids []string) ([]valueobject.Attachment, error)
	Annotated(ctx context.Context, convID string, in []appconversation.AnnotationInput) ([]valueobject.Annotation, []valueobject.Attachment, error)
	Recorded(ctx context.Context, convID, id string) (*valueobject.RecordingRef, error)
}

// PlanSink binds one room's update_plan calls to the running turn's
// event stream — the same per-room registry role SessionStore plays for
// exec sessions. *toolplan.Dispatcher satisfies it.
type PlanSink interface {
	Bind(convID string, sink event.TurnEventHandler) (unbind func())
}

// journaledFrame is one sequenced frame retained for turn.watch replay.
type journaledFrame struct {
	seq int
	raw []byte
}

// turnKind distinguishes the command that opened a turn — terminal
// framing differs (edit/resume never claim userMessagePersisted).
type turnKind int

const (
	turnSend turnKind = iota
	turnEdit
	turnResume
)

// String is the wire word naming the opening command — "send", "edit",
// or "resume" — shared by the room-activity notice and ActiveTurn.
func (k turnKind) String() string {
	switch k {
	case turnEdit:
		return "edit"
	case turnResume:
		return "resume"
	default:
		return "send"
	}
}

// turn is one admitted command: its bounded frame journal, the live
// subscriber set, and the job's own server-scoped context — disconnected
// sockets never cancel it.
type turn struct {
	id   string
	kind turnKind
	// origin says where the turn came from; kind says what opened it. A turn a
	// room job ran carries the job's origin with an empty requestID.
	origin         valueobject.TurnOrigin
	conversationID string
	// admitted orders non-terminal turns for one room. The hub is the
	// only owner of "a transport turn is running"; it is not conversation
	// state.
	admitted   int64
	requestID  string
	turnCtx    context.Context
	turnCancel context.CancelCauseFunc
	// job arguments — convID already on the turn.
	providerID, modelID, msgID, content string
	attachments                         []valueobject.Attachment
	opts                                appjob.SendOptions

	seq          int
	frames       []journaledFrame
	journalStart int // seq of frames[0]; == seq when the journal is empty
	bytes        int
	terminal     bool
	closedAt     time.Time
	subscribed   map[*conn]struct{}

	// reasoning stretches coalesce to one phase frame each; tool output
	// is live content and streams uncoalesced.
	inReasoning bool
	// startedAt stamps the admission wall-clock; it rides turn.opened
	// and the resync snapshot so a reattached tab's timer never
	// restarts. live is the bounded shadow of what streamed — the
	// snapshot a gapped turn.watch replays where journal frames were
	// already cut.
	startedAt time.Time
	live      *liveDoc
	// overflowed marks a turn whose own stream frames exceeded the
	// outbound bound — its context is canceled, further events are
	// suppressed, and runTurn emits the sanitized 413 terminal carrying
	// the job's real persistence receipt.
	overflowed bool

	// steering is the send turn's exact-turn mailbox — bound through
	// opts into the job, so submissions land on this turn even while it
	// is still queued on the room worker. steerRecords is the bounded
	// submission list (order = submission), steerByInput the requestId
	// index for dedup and recall. Only turnSend carries these.
	steering     *appjob.SteeringInput
	steerRecords []*steerRecord
	steerByInput map[string]*steerRecord
	// outcome records the emitted terminal verdict so a late
	// resync_required can replay the stored outcome instead of a bare
	// frame.
	outcome *turnOutcome
	// observe and done belong to turns opened through Run: the caller's
	// event sink, and where the job's result goes once the turn ends.
	observe event.TurnEventHandler
	done    chan runResult
	// jobOwned marks a turn the hub only journals: execution belongs to the
	// room's worker, so turnCtx has no consumer and the steering mailbox was
	// never bound here. Cancel and steer for it are forwarded to the worker
	// through jobControl*, never to turnCancel.
	jobOwned bool
}

// Hub owns the process-scoped conversation protocol: every admitted
// turn, its bounded journal, and every live socket. Turns live under
// the hub's own context — a dropped socket changes the subscriber set,
// never the send. Shutdown is the only owner of bulk turn cancellation.
type Hub struct {
	ctx    context.Context
	cancel context.CancelFunc
	sender Sender
	// uploads resolves a send's files; nil rejects sends that name any.
	uploads Uploads
	// planEvents is where a room's update_plan calls find the running
	// turn's event stream; nil drops those events (the tool call and its
	// output still record normally).
	planEvents PlanSink

	mu       sync.Mutex
	turns    map[string]*turn
	admitSeq int64
	sockets  map[*conn]struct{}
	// journalRefusals counts the jobs that ran unjournaled per room, so the
	// warning is one line per room per exhaustion episode, not a storm.
	journalRefusals map[string]int
	wg              sync.WaitGroup
	shutting        bool
	turnGrace       time.Duration // bounded wait for turn terminals at shutdown
	drainGrace      time.Duration // bounded wait for socket writers to flush
	// queueDepth reads how many messages wait in a room, so a queued notice
	// names the count at emit time rather than a number kept beside the queue.
	queueDepth func(convID string) int
	// jobControlCancel/jobControlSteer/jobControlRecall reach the room
	// worker that owns a journal-only turn's execution — the hub only
	// journaled that turn, so canceling its bookkeeping context or writing
	// to a steering mailbox it never had would touch nothing. The direction
	// is one-way, hub→app, set once at wiring: the app never calls back into
	// the hub through these, and a future need for the reverse direction
	// means the design is wrong, not that the set is incomplete. Nil (the
	// CLI's composition or a test) leaves journal-only turns uncancelable
	// and unsteerable through the hub — the same blind run they had before,
	// never a crash. Steer carries the full input, including the command's
	// request id, and returns the mailbox receipt the journal shares with
	// the job. Recall withdraws that same id.
	jobControlCancel func(convID string) bool
	jobControlSteer  func(convID string, in valueobject.UserInput) (*appjob.SteeringReceipt, bool)
	jobControlRecall func(convID, inputID string) bool
	// jobControlMu serializes a journal-only steer or recall with its
	// forward. The forward runs without h.mu — the worker it reaches can
	// re-enter the hub through the turn's event stream.
	jobControlMu sync.Mutex
	// finished run after every turn ends (see OnFinished); events see
	// every turn event (see OnEvent).
	finished []func(convID, kind string, origin valueobject.TurnOrigin, external bool, err error)
	events   []func(convID string, ev event.TurnEvent)
}

// NewHub binds a sender under the given server-lifecycle context —
// callers pass one that survives a signal so Shutdown, not the signal,
// owns terminal emission. A turn's own ctx derives from the hub's.
func NewHub(ctx context.Context, sender Sender) *Hub {
	hctx, cancel := context.WithCancel(ctx)
	return &Hub{
		ctx:             hctx,
		cancel:          cancel,
		sender:          sender,
		turns:           make(map[string]*turn),
		sockets:         make(map[*conn]struct{}),
		journalRefusals: make(map[string]int),
		turnGrace:       shutdownTurnGrace,
		drainGrace:      shutdownDrainGrace,
	}
}

// UseUploads sets how a send's uploads become attachments; without it a
// send naming uploads is rejected.
func (h *Hub) UseUploads(u Uploads) { h.uploads = u }

// UseJobControl wires the one-way bridge to the room workers that own
// journal-only turns: cancel answers true only when a live worker took the
// interruption, and steer answers true only when the running job accepted
// the input. Set once at composition time — see the field comment for the
// direction rule.
func (h *Hub) UseJobControl(cancel func(convID string) bool, steer func(convID string, in valueobject.UserInput) (*appjob.SteeringReceipt, bool), recall func(convID, inputID string) bool) {
	h.mu.Lock()
	h.jobControlCancel = cancel
	h.jobControlSteer = steer
	h.jobControlRecall = recall
	h.mu.Unlock()
}

// steerableLocked reports whether this live turn accepts turn.steer.
// A socket-admitted send has its own mailbox. A journal-only send does
// only when the worker forward is wired. Anything else — edit, resume,
// a finished turn — is a declared no. Callers hold h.mu.
func (h *Hub) steerableLocked(t *turn) bool {
	if t == nil || t.terminal || t.kind != turnSend {
		return false
	}
	if t.jobOwned {
		return h.jobControlSteer != nil
	}
	return t.steering != nil
}

func (h *Hub) openedBody(t *turn) openedPayload {
	return openedPayload{StartedAt: t.startedAt.UnixMilli(), Steerable: h.steerableLocked(t)}
}

// UsePlanEvents sets the registry update_plan resolves the room's live
// turn through; without it plan events drop while the call still records.
func (h *Hub) UsePlanEvents(p PlanSink) { h.planEvents = p }

// UseQueueDepth binds how many messages wait in a room — the worker's own
// queue depth — so the queued notice can name it.
func (h *Hub) UseQueueDepth(fn func(convID string) int) {
	h.mu.Lock()
	h.queueDepth = fn
	h.mu.Unlock()
}

// QueueChanged announces a room's waiting count. The frame is unsequenced and
// belongs to no turn, so it never enters a journal: it is a nudge, and a tab
// that missed it — a fresh load, a reconnect, a socket dropped for being slow —
// reads the same number off the room's projection instead (`waiting` on the
// conversation detail and list), never from a replay of old counts.
func (h *Hub) QueueChanged(convID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.shutting {
		return
	}
	waiting := 0
	if h.queueDepth != nil {
		waiting = h.queueDepth(convID)
	}
	h.broadcastLocked(queuedFrame(convID, waiting))
}

// SocketFull reports whether the socket bound is reached — the upgrade
// edge checks it before upgrading so a full hub answers 503 problem+json
// instead of a half-opened socket. Attach rechecks under the lock.
func (h *Hub) SocketFull() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.shutting || len(h.sockets) >= maxSockets
}

// Attach registers an already-upgraded socket and drives it until
// disconnect. Called by the upgrade boundary on the gin handler
// goroutine.
func (h *Hub) Attach(ws *websocket.Conn) {
	c := newConn(h, ws)
	h.mu.Lock()
	if h.shutting || len(h.sockets) >= maxSockets {
		h.mu.Unlock()
		_ = ws.SetWriteDeadline(time.Now().Add(writeWait))
		_ = ws.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseServiceRestart, "server shutting down or full"))
		_ = ws.Close()
		return
	}
	h.sockets[c] = struct{}{}
	h.mu.Unlock()
	c.serve()
}

// handleCommand validates one inbound frame end-to-end and answers it
// or turns it into work. Validation failure answers an `error` frame on
// the same socket — the socket stays open.
func (h *Hub) handleCommand(c *conn, raw []byte) {
	cmd, ok := decodeCommand(raw)
	if !ok {
		// requestId echoes when it could be parsed — a strict-decode
		// failure still tries the lenient salvage for correlation.
		var salvage struct {
			RequestID string `json:"requestId"`
		}
		_ = json.Unmarshal(raw, &salvage)
		c.push(errorFrame(salvage.RequestID, "invalid_argument", "request is not a valid JSON command"))
		return
	}
	// requestId is mandatory on every command — it correlates the
	// reply/terminal frame for the issuing tab. A blank id isn't an id
	// at all — the reply doesn't echo it.
	if strings.TrimSpace(cmd.RequestID) == "" {
		c.push(errorFrame("", "invalid_argument", "requestId is required"))
		return
	}
	switch cmd.Type {
	case "turn.send":
		h.turnSend(c, cmd)
	case "turn.edit":
		h.turnEdit(c, cmd)
	case "turn.resume":
		h.turnResume(c, cmd)
	case "turn.cancel":
		h.turnCancelCommand(c, cmd)
	case "turn.watch":
		h.turnWatch(c, cmd)
	case "turn.steer":
		h.turnSteer(c, cmd)
	case "turn.steer_recall":
		h.turnSteerRecall(c, cmd)
	default:
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "unknown command type "+cmd.Type))
	}
}

// commandConvID validates a mandatory envelope conversationId — cancel,
// watch, edit, and resume all name an existing room, so absent or
// invalid is a syntactic rejection.
func commandConvID(c *conn, cmd command) (string, bool) {
	if cmd.ConversationID == nil {
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "conversationId is required"))
		return "", false
	}
	convID := strings.TrimSpace(*cmd.ConversationID)
	if valueobject.ValidID(convID) != nil {
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "conversationId is not a valid id"))
		return "", false
	}
	return convID, true
}

// turnSend validates the send body and admits a bounded turn.
func (h *Hub) turnSend(c *conn, cmd command) {
	// turn.send accepts no turn-scoping envelope fields.
	if cmd.TurnID != "" || cmd.AfterSeq != nil {
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "turn.send accepts no turnId/afterSeq"))
		return
	}
	var payload sendPayload
	if cmd.Payload == nil || !decodeStrict(cmd.Payload, &payload) {
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "turn.send payload is invalid"))
		return
	}
	convID := ""
	if cmd.ConversationID != nil {
		convID = strings.TrimSpace(*cmd.ConversationID)
		if valueobject.ValidID(convID) != nil {
			c.push(errorFrame(cmd.RequestID, "invalid_argument", "conversationId is not a valid id"))
			return
		}
	}
	opts, ok := turnSelection(c, cmd, payload.Effort, payload.Upstream, payload.Workspace)
	if !ok {
		return
	}
	if payload.Environment != "" {
		environment, ok := enum.ParseEnvironment(strings.TrimSpace(payload.Environment))
		if !ok {
			c.push(errorFrame(cmd.RequestID, "invalid_argument", "turn.send payload.environment is not a known environment"))
			return
		}
		opts.Environment = environment
	}
	opts.Agent = strings.TrimSpace(payload.AgentID)
	opts.AgentOptions = payload.AgentOptions
	opts.Project = strings.TrimSpace(payload.ProjectID)
	in, ok := h.resolveMessageInput(c, cmd, convID, payload.messageInputPayload)
	if !ok {
		return
	}
	opts.References = in.References
	opts.Annotations = in.Annotations
	opts.Recording = in.Recording
	// Every admitted send owns an exact-turn mailbox: turn.steer
	// submissions land on this turn whether the job is running or still
	// queued on the room worker — never on whatever ran before.
	opts.Steering = appjob.NewSteeringInput()
	h.admitAndRun(c, cmd.RequestID, convID, turn{
		kind: turnSend, providerID: payload.ProviderID, modelID: payload.ModelID, content: in.Text, attachments: in.Attachments, opts: opts,
		steering: opts.Steering, steerByInput: map[string]*steerRecord{},
	})
}

// resolveMessageInput decodes the user-input body shared by turn.send
// and turn.steer into a domain UserInput: reference spans are validated
// against the text, uploads resolve to attachments, and annotation
// picks resolve to annotations plus screenshot attachments. Errors are
// reported on the issuing socket and ok=false; upload/annotation reads
// can do I/O, so callers must invoke this before taking h.mu.
func (h *Hub) resolveMessageInput(c *conn, cmd command, convID string, p messageInputPayload) (valueobject.UserInput, bool) {
	in := valueobject.UserInput{Text: p.Content}
	for _, r := range p.References {
		ref, err := valueobject.NewReference(r.Kind, r.ID, r.Label, r.Start, r.End)
		if err != nil {
			c.push(errorFrame(cmd.RequestID, "invalid_argument", cmd.Type+" payload.references: "+err.Error()))
			return in, false
		}
		in.References = append(in.References, ref)
	}
	if err := valueobject.CheckReferences(in.Text, in.References); err != nil {
		c.push(errorFrame(cmd.RequestID, "invalid_argument", cmd.Type+" payload.references: "+err.Error()))
		return in, false
	}
	if len(p.Uploads) > 0 {
		if h.uploads == nil {
			c.push(errorFrame(cmd.RequestID, "invalid_argument", "this server takes no uploads"))
			return in, false
		}
		attachments, err := h.uploads.Uploaded(h.ctx, p.Uploads)
		if err != nil {
			c.push(errorFrame(cmd.RequestID, "invalid_argument", cmd.Type+" payload.uploads: "+apperror.As(err).Error()))
			return in, false
		}
		in.Attachments = append(in.Attachments, attachments...)
	}
	if len(p.Annotations) > 0 {
		if h.uploads == nil {
			c.push(errorFrame(cmd.RequestID, "invalid_argument", "this server takes no annotations"))
			return in, false
		}
		inputs := make([]appconversation.AnnotationInput, 0, len(p.Annotations))
		for _, a := range p.Annotations {
			inputs = append(inputs, appconversation.AnnotationInput{PickID: a.PickID, Comment: a.Comment, Screenshot: a.Screenshot})
		}
		anns, shots, err := h.uploads.Annotated(h.ctx, convID, inputs)
		if err != nil {
			c.push(errorFrame(cmd.RequestID, "invalid_argument", cmd.Type+" payload.annotations: "+apperror.As(err).Error()))
			return in, false
		}
		in.Annotations = anns
		in.Attachments = append(in.Attachments, shots...)
	}
	if p.RecordingID != "" {
		if h.uploads == nil {
			c.push(errorFrame(cmd.RequestID, "invalid_argument", "this server takes no recordings"))
			return in, false
		}
		rec, err := h.uploads.Recorded(h.ctx, convID, p.RecordingID)
		if err != nil {
			c.push(errorFrame(cmd.RequestID, "invalid_argument", cmd.Type+" payload.recordingId: "+apperror.As(err).Error()))
			return in, false
		}
		in.Recording = rec
	}
	return in, true
}

// turnEdit validates the edit body and admits a bounded turn. The
// conversation must be named — an edit never mints a room.
func (h *Hub) turnEdit(c *conn, cmd command) {
	if cmd.TurnID != "" || cmd.AfterSeq != nil {
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "turn.edit accepts no turnId/afterSeq"))
		return
	}
	convID, ok := commandConvID(c, cmd)
	if !ok {
		return
	}
	var payload editPayload
	if cmd.Payload == nil || !decodeStrict(cmd.Payload, &payload) {
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "turn.edit payload is invalid"))
		return
	}
	if valueobject.ValidID(payload.MessageID) != nil {
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "turn.edit requires a valid payload.messageId"))
		return
	}
	opts, ok := turnSelection(c, cmd, payload.Effort, payload.Upstream, payload.Workspace)
	if !ok {
		return
	}
	h.admitAndRun(c, cmd.RequestID, convID, turn{
		kind: turnEdit, providerID: payload.ProviderID, modelID: payload.ModelID,
		msgID: payload.MessageID, content: payload.Content, opts: opts,
	})
}

// turnResume validates the resume body and admits a bounded turn. The
// conversation must be named — a resume never mints a room, and it
// never rewrites history.
func (h *Hub) turnResume(c *conn, cmd command) {
	if cmd.TurnID != "" || cmd.AfterSeq != nil {
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "turn.resume accepts no turnId/afterSeq"))
		return
	}
	convID, ok := commandConvID(c, cmd)
	if !ok {
		return
	}
	var payload resumePayload
	if cmd.Payload == nil || !decodeStrict(cmd.Payload, &payload) {
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "turn.resume payload is invalid"))
		return
	}
	opts, ok := turnSelection(c, cmd, payload.Effort, payload.Upstream, payload.Workspace)
	if !ok {
		return
	}
	h.admitAndRun(c, cmd.RequestID, convID, turn{
		kind: turnResume, providerID: payload.ProviderID, modelID: payload.ModelID, opts: opts,
	})
}

// turnSelection parses the optional per-turn overrides turn.send,
// turn.edit, and turn.resume share — an effort enum word, an aggregator
// upstream pin, and a workspace path — pushing an invalid_argument reply
// and reporting ok=false on a bad effort word. "" keeps the room's
// stored binding; the selected environment validates the workspace in
// the use case, never here — a host filesystem check would reject
// remote workspaces.
func turnSelection(c *conn, cmd command, effort, upstream, workspace string) (appjob.SendOptions, bool) {
	// Every turn admitted by a socket is a web turn; channel, automation,
	// and cross-thread runs enter through Hub.Run with their own origin.
	opts := appjob.SendOptions{Origin: valueobject.TurnOrigin{Kind: enum.TurnOriginWeb}}
	if effort != "" {
		e, ok := enum.ParseEffort(effort)
		if !ok {
			c.push(errorFrame(cmd.RequestID, "invalid_argument", cmd.Type+" payload.effort is not a known effort"))
			return opts, false
		}
		opts.Effort = e
	}
	opts.Upstream = strings.TrimSpace(upstream)
	if ws := strings.TrimSpace(workspace); ws != "" {
		opts.Workspace = ws
	}
	return opts, true
}

// admitAndRun claims a turn slot under the hub lock — admission
// rejection, journal creation, seq-0 `opened`, and the worker's
// accounting all happen before the job starts.
func (h *Hub) admitAndRun(c *conn, requestID, convID string, job turn) {
	h.mu.Lock()
	h.evictJournalsLocked(time.Now())
	active := 0
	for _, t := range h.turns {
		if !t.terminal {
			active++
		}
	}
	if h.shutting {
		h.mu.Unlock()
		c.push(errorFrame(requestID, "unavailable", "server is shutting down"))
		return
	}
	if active >= maxActiveTurns {
		h.mu.Unlock()
		c.push(errorFrame(requestID, "queue_full", "too many active turns"))
		return
	}
	if convID == "" {
		// Fresh conversation minted server-side at admit time.
		conv, err := aggregate.NewConversation("")
		if err != nil {
			h.mu.Unlock()
			c.push(errorFrame(requestID, "internal", "could not mint conversation id"))
			return
		}
		convID = conv.ID()
	}
	tid, err := valueobject.NewID("t", "")
	if err != nil {
		h.mu.Unlock()
		c.push(errorFrame(requestID, "internal", "could not mint turn id"))
		return
	}
	t := &job
	t.id = tid.String()
	t.conversationID = convID
	t.requestID = requestID
	h.admitSeq++
	t.admitted = h.admitSeq
	t.subscribed = map[*conn]struct{}{c: {}}
	// A turn a socket admitted came from the web: the notice says so, and its
	// requestId is what lets that tab claim the frames.
	t.origin = valueobject.TurnOrigin{Kind: enum.TurnOriginWeb}
	t.turnCtx, t.turnCancel = context.WithCancelCause(h.ctx)
	t.startedAt = time.Now()
	t.live = newLiveDoc()
	h.turns[t.id] = t
	// Accounted under the lock — Shutdown's Wait can't observe the
	// worker before its Add. `opened` is journaled while still locked
	// so seq 0 provably precedes every stream frame.
	h.wg.Add(1)
	h.emitLocked(t, "turn.opened", h.openedBody(t), true)
	// The room-activity notice enqueues on every socket under the same
	// lock — it provably follows seq 0 on each wire.
	h.broadcastLocked(activityFrame(t, "opened"))
	h.mu.Unlock()

	go h.runTurn(t)
}

// runTurn is the turn worker: the job runs on the hub-scoped ctx,
// stream events project onto journal+subscribers, and exactly one
// terminal frame lands — success, classified failure, or shutdown.
func (h *Hub) runTurn(t *turn) {
	defer h.wg.Done()
	h.mu.Lock()
	listeners := h.events
	h.mu.Unlock()
	onEvent := func(ev event.TurnEvent) {
		h.onEvent(t, ev)
		t.observeEvent(ev)
		for _, fn := range listeners {
			fn(t.conversationID, ev)
		}
	}
	// update_plan emits on the same goroutine the send runs on, so its
	// events land journaled between the call and result frames.
	if h.planEvents != nil {
		defer h.planEvents.Bind(t.conversationID, onEvent)()
	}
	var (
		persisted bool
		err       error
		receipt   appconversation.SendResult
	)
	switch t.kind {
	case turnSend:
		receipt, err = h.sender.Send(t.turnCtx, t.conversationID, t.providerID, t.modelID, t.content, t.attachments, t.opts, onEvent)
		persisted = receipt.UserMessagePersisted
	case turnEdit:
		err = h.sender.Edit(t.turnCtx, t.conversationID, t.providerID, t.modelID, t.msgID, t.content, t.opts, onEvent)
	case turnResume:
		err = h.sender.Resume(t.turnCtx, t.conversationID, t.providerID, t.modelID, t.opts, onEvent)
	}

	// The hub's own accounting happens before the seal: the caller that admitted
	// this turn is waiting on its result. The finished hooks fire inside the
	// seal — the one point both paths close through — never here, or an
	// admitted turn would answer two calls.
	h.mu.Lock()
	if t.done != nil {
		t.done <- runResult{receipt: receipt, err: err}
	}
	h.mu.Unlock()
	h.sealTurn(t, persisted, err)
}

// sealTurn writes one turn's terminal frame and closes its journal. Both paths go
// through it — a turn the hub admitted from a socket and a turn a room job ran —
// so the terminal has exactly one shape and no watcher can tell them apart.
//
// It takes the hub lock itself and never depends on the turn's context: a
// shutting-down server still lands the terminal of a job that is finishing its
// persistence, or the room would look stuck after the restart.
func (h *Hub) sealTurn(t *turn, persisted bool, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	defer h.evictJournalsLocked(time.Now())
	if t.terminal {
		return // already terminated
	}
	var typ string
	var p terminalPayload
	switch {
	case t.overflowed:
		// A stream frame exceeded the outbound bound — the turn dies
		// with a sanitized 413 and the job's real receipt, never a
		// fabricated persistence claim.
		typ = "turn.failed"
		p = terminalPayload{
			Status:         http.StatusRequestEntityTooLarge,
			Code:           "input_too_large",
			Message:        "stream frame exceeded the outbound bound",
			ResyncRequired: true,
		}
	case err == nil && t.kind == turnSend && !persisted:
		// A send that reports success without a committed user line is
		// an engine bug — never let it look like a persisted success.
		typ = "turn.failed"
		p = terminalPayload{
			Status:               http.StatusInternalServerError,
			Code:                 "internal",
			Message:              "send completed without a persistence receipt",
			UserMessagePersisted: &persisted,
			ResyncRequired:       true,
		}
	case err == nil:
		typ = "turn.completed"
		p = terminalPayload{ResyncRequired: true}
	case h.shutting && shutdownCasualty(err):
		// Graceful shutdown canceled the turn — classify 503 so the tab
		// knows the turn died with the server. A genuine failure that
		// happens to land while the hub is stopping keeps its own code.
		typ = "turn.failed"
		p = terminalPayload{
			Status:         http.StatusServiceUnavailable,
			Code:           "unavailable",
			Message:        "server shutting down",
			ResyncRequired: true,
		}
	default:
		status, code, message := classifyError(err)
		typ = "turn.failed"
		p = terminalPayload{
			Status:         status,
			Code:           code,
			Message:        clipTerminalMessage(message),
			ResyncRequired: true,
		}
	}
	if t.kind == turnSend {
		p.UserMessagePersisted = &persisted
	}
	// The bounded receipt snapshot rides every terminal — accepted was
	// never consumed, so the client reconciles each submitted input by
	// state rather than guessing.
	p.Steering = t.steeringSnapshotLocked()
	// The stored outcome must marshal too — a late resync_required
	// replays it verbatim, so a terminal that exceeded the frame cap
	// would answer the watch with silence as well.
	t.outcome = &turnOutcome{typ: strings.TrimPrefix(typ, "turn."), payload: p}
	h.emitLocked(t, typ, p, true)
	// The terminal mirrors into the room-level notice before the turn
	// seals — every socket learns the room went idle.
	h.broadcastLocked(activityFrame(t, strings.TrimPrefix(typ, "turn.")))
	h.finishLocked(t)
	// The finished hooks fire here and nowhere else: both paths — a turn the
	// hub admitted and a turn a room job ran — seal through this one point,
	// and the terminal guard above makes a duplicate seal a no-op, so every
	// turn answers exactly one call with the error it actually ended with.
	for _, fn := range h.finished {
		go fn(t.conversationID, t.kind.String(), t.origin, t.done != nil, err)
	}
}

// turnCancelCommand acks-and-cancels one exact active turn: the ack goes
// out BEFORE the context is canceled so "accepted" is already on the
// wire if cancellation wins the race. An unknown, finished, or
// foreign-conversation turnId is a not_found error, never an ack.
func (h *Hub) turnCancelCommand(c *conn, cmd command) {
	if cmd.AfterSeq != nil || cmd.Payload != nil {
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "turn.cancel accepts no afterSeq/payload"))
		return
	}
	convID, ok := commandConvID(c, cmd)
	if !ok {
		return
	}
	if valueobject.ValidID(cmd.TurnID) != nil {
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "turn.cancel requires a valid turnId"))
		return
	}
	h.mu.Lock()
	t, found := h.turns[cmd.TurnID]
	if !found || t.terminal || t.conversationID != convID {
		h.mu.Unlock()
		c.push(errorFrame(cmd.RequestID, "not_found", "no live turn with that id"))
		return
	}
	if t.jobOwned && h.jobControlCancel != nil {
		// The hub only journals this turn: its turnCtx has no consumer, so
		// turnCancel would cancel bookkeeping and call it a stop. The worker
		// owns the execution — forward outside the lock, because the callback
		// reaches into the application and the job's exit re-enters the hub
		// through the journal's seal.
		cancelJob := h.jobControlCancel
		h.mu.Unlock()
		if !cancelJob(convID) {
			c.push(errorFrame(cmd.RequestID, "not_found", "the turn's job already ended"))
			return
		}
		c.push(cancelAckFrame(cmd.RequestID, convID, t.id))
		return
	}
	c.push(cancelAckFrame(cmd.RequestID, convID, t.id))
	// The owner's Stop: a tool this kills says "interrupted by user".
	t.turnCancel(dservice.ErrInterruptedByUser)
	h.mu.Unlock()
}

// turnWatch attaches a socket to a turn's journal: under one lock it
// replays journaled frames with seq > afterSeq and subscribes for live
// frames, so replay and live never gap or interleave. Unsatisfiable
// watches answer turn.resync_required — missing deltas are never
// fabricated; an active turn with a truncated journal still attaches
// for live frames after the resync notice so the tab sees the terminal.
func (h *Hub) turnWatch(c *conn, cmd command) {
	if cmd.Payload != nil {
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "turn.watch accepts no payload"))
		return
	}
	convID, ok := commandConvID(c, cmd)
	if !ok {
		return
	}
	if valueobject.ValidID(cmd.TurnID) != nil {
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "turn.watch requires a valid turnId"))
		return
	}
	if cmd.AfterSeq == nil || *cmd.AfterSeq < -1 {
		c.push(errorFrame(cmd.RequestID, "invalid_argument", "turn.watch requires afterSeq >= -1"))
		return
	}
	after := *cmd.AfterSeq
	h.mu.Lock()
	// Lookup applies the retention bound too — an expired journal is
	// evicted here, not only at the next admission.
	h.evictJournalsLocked(time.Now())
	t, found := h.turns[cmd.TurnID]
	// Unknown, evicted, or foreign-conversation turnId: nothing honest
	// to replay — an unknown outcome the client must reconcile over
	// HTTP, never an implicit resend.
	if !found || t.conversationID != convID {
		h.mu.Unlock()
		c.push(resyncFrame(cmd.RequestID, convID, cmd.TurnID, resyncPayload{Turn: "unknown"}))
		return
	}
	// A client reports the last seq it received, so a caught-up one asks for
	// t.seq-1 and replays nothing below. A claim from beyond the journal —
	// afterSeq >= t.seq, a seq this turn never minted — is not evidence the
	// client is current: it gets the bounded snapshot. A finished turn stops
	// there (the verdict and receipt are the whole answer), while a live one
	// still attaches, or the tab would sit without the rest of the stream,
	// its terminal included.
	if after > t.seq-1 {
		c.push(resyncFrame(cmd.RequestID, convID, t.id, h.resyncForLocked(t)))
		if t.terminal {
			h.mu.Unlock()
			return // a finished turn has no live frames to attach for
		}
		t.subscribed[c] = struct{}{}
		h.mu.Unlock()
		return
	}
	// Journal truncation check: the earliest retained seq must cover
	// the requested window start (afterSeq -1 asks for everything).
	gap := t.journalStart > after+1
	pending := 0
	if !gap {
		for _, jf := range t.frames {
			if jf.seq > after {
				pending++
			}
		}
	}
	// A gap — or a replay deeper than one socket's burst budget — gets
	// the bounded snapshot instead: flooding the outbound queue just
	// kills the socket the watcher attached on.
	if gap || pending > replayBurst {
		if gap {
			slog.Info("turn.watch hit a journal gap",
				"conversation", convID, "turn", t.id,
				"afterSeq", after, "journalStart", t.journalStart,
				"nextSeq", t.seq, "terminal", t.terminal)
		} else {
			slog.Info("turn.watch replay exceeds the burst bound",
				"conversation", convID, "turn", t.id,
				"afterSeq", after, "replayFrames", pending,
				"nextSeq", t.seq, "terminal", t.terminal)
		}
		c.push(resyncFrame(cmd.RequestID, convID, t.id, h.resyncForLocked(t)))
		if t.terminal {
			h.mu.Unlock()
			return // finished turns with gaps get no attach
		}
		// Active: still attach for live frames — the tab sees the
		// terminal while re-reading history over HTTP.
		t.subscribed[c] = struct{}{}
		h.mu.Unlock()
		return
	}
	for _, jf := range t.frames {
		if jf.seq > after {
			c.enqueue(jf.raw)
		}
	}
	if !t.terminal {
		t.subscribed[c] = struct{}{}
	}
	h.mu.Unlock()
}

// emitLocked mints one sequenced frame into the journal and delivers it
// to every subscriber. withRequest attaches requestId — only opened and
// terminal frames carry it (plus the unsequenced command replies).
// Returns false when the frame exceeds the outbound cap — the seq is
// NOT consumed on failure, so the emitted stream stays contiguous.
// Callers hold h.mu.
func (h *Hub) emitLocked(t *turn, typ string, payload any, withRequest bool) bool {
	seq := t.seq
	f := serverFrame{
		Type:           typ,
		ConversationID: t.conversationID,
		TurnID:         t.id,
		Seq:            &seq,
		Payload:        payload,
	}
	if withRequest {
		f.RequestID = t.requestID
	}
	raw, ok := marshalFrame(f)
	if !ok {
		return false
	}
	t.seq++
	// Bounded journal: drop the oldest frames past the frame/byte cap.
	// maxFrameBytes < journalMaxBytes is the safety invariant — one
	// admitted frame can never evict the journal empty (t.frames[0]
	// would panic). The len guard keeps it true even if the caps ever
	// drifted to break it.
	t.frames = append(t.frames, journaledFrame{seq: seq, raw: raw})
	t.bytes += len(raw)
	for len(t.frames) > 1 && (len(t.frames) > journalMaxFrames || t.bytes > journalMaxBytes) {
		t.bytes -= len(t.frames[0].raw)
		t.frames[0].raw = nil
		t.frames = t.frames[1:]
		t.journalStart = t.frames[0].seq
	}
	for c := range t.subscribed {
		c.enqueue(raw)
	}
	return true
}

// clipTerminalMessage bounds the terminal's public message so the frame
// can never breach the outbound cap — the promise is exactly one
// terminal; a provider error body must not silence it (or its replayed
// resync_required).
func clipTerminalMessage(s string) string {
	// The invariant is « maxFrameBytes after marshaling, with headroom
	// for the frame envelope and the steering snapshot — the delta-chunk
	// size happens to satisfy it and keeps the bound in the same unit
	// the transport already thinks in.
	const maxMessageRunes = outboundChunkBytes
	if r := []rune(s); len(r) > maxMessageRunes {
		return string(r[:maxMessageRunes]) + "…"
	}
	return s
}

// RoomChanged tells every socket that a room changed outside a turn (a
// reply's delivery to a chat, a generated title), so a tab showing it
// reloads.
func (h *Hub) RoomChanged(conversationID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.broadcastLocked(serverFrame{Type: "conversation.updated", ConversationID: conversationID})
}

// broadcastLocked delivers one unsequenced, unjournaled room-level
// frame to every socket — the conversation.activity notice. A marshal
// failure drops the notice, never the turn. Callers hold h.mu.
func (h *Hub) broadcastLocked(f serverFrame) {
	raw, ok := marshalFrame(f)
	if !ok {
		return
	}
	for c := range h.sockets {
		c.enqueue(raw)
	}
}

// finishLocked seals a turn after its terminal frame: subscribers
// detach, the context releases, and the journal enters retention.
// Callers hold h.mu.
func (h *Hub) finishLocked(t *turn) {
	t.terminal = true
	t.closedAt = time.Now()
	t.subscribed = nil
	t.turnCancel(nil)
}

// ActiveTurn is the oldest non-terminal transport turn for a room, the
// one a client that loaded the room should watch. A finished journal is
// not a turn in progress. kind is "send", "edit", or "resume".
func (h *Hub) ActiveTurn(conversationID string) (turnID, kind string, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var best *turn
	for _, t := range h.turns {
		if t.terminal || t.conversationID != conversationID {
			continue
		}
		if best == nil || t.admitted < best.admitted {
			best = t
		}
	}
	if best == nil {
		return "", "", false
	}
	return best.id, best.kind.String(), true
}

// dropConn removes a socket from every turn's subscriber set — turns
// run on, their journals intact for watchers.
func (h *Hub) dropConn(c *conn) {
	h.mu.Lock()
	for _, t := range h.turns {
		delete(t.subscribed, c)
	}
	delete(h.sockets, c)
	h.mu.Unlock()
}

// evictJournalsLocked applies the retention bound: terminal turns older
// than journalMaxAge — or in excess of maxRetainedTurns — release their
// journals. Callers hold h.mu.
func (h *Hub) evictJournalsLocked(now time.Time) {
	terminal := 0
	for _, t := range h.turns {
		if t.terminal && now.Sub(t.closedAt) > journalMaxAge {
			delete(h.turns, t.id)
		} else if t.terminal {
			terminal++
		}
	}
	// Over-retained: release oldest terminal journals until the bound
	// holds. Bounded memory beats long retention for a local UI.
	for terminal > maxRetainedTurns {
		var oldest *turn
		for _, t := range h.turns {
			if t.terminal && (oldest == nil || t.closedAt.Before(oldest.closedAt)) {
				oldest = t
			}
		}
		if oldest == nil {
			return
		}
		delete(h.turns, oldest.id)
		terminal--
	}
}

// shutdownCasualty reports whether err is the cancellation Shutdown
// applied. A provider failure returned while the hub is stopping is not
// one of those.
func shutdownCasualty(err error) bool {
	if err == nil {
		return false
	}
	status, _, _ := classifyError(err)
	return status == http.StatusRequestTimeout
}

// Shutdown is the whole hub teardown on the hub's own graces — the
// convenience an early exit and a test use. The barrier calls the two halves
// separately so persistence, the drain and the listener keep their own
// budgets.
func (h *Hub) Shutdown() {
	h.StopTurns()
	ctx, cancel := context.WithTimeout(context.Background(), h.drainGrace)
	defer cancel()
	h.DrainSockets(ctx)
}

// StopTurns closes admission and cancels every live turn with the restart
// cause, then waits — bounded by the turn grace — for the terminal frames
// their workers write. It is the barrier's turn phase and finishes before the
// socket layer moves; if the grace lapses the turns close without a fabricated
// terminal, because persistence already happened in the phase that mattered.
func (h *Hub) StopTurns() {
	h.mu.Lock()
	if !h.shutting {
		h.shutting = true
		for _, t := range h.turns {
			if !t.terminal {
				// A restart cuts these turns: a tool they were running
				// reports "nusashell restarted", never the owner's words.
				t.turnCancel(dservice.ErrServerRestarted)
			}
		}
	}
	h.mu.Unlock()

	done := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(h.turnGrace)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		// Turns still running outlive the socket layer; the context
		// cancellation in the drain unblocks them for process exit.
	}
}

// DrainSockets lands the terminal frames already queued, closes every socket
// and cancels the hub context — all inside the caller's budget, which the
// barrier sets as its socket phase. A client that missed a frame reconnects
// and resyncs, so a slow drain is cut rather than waited for.
func (h *Hub) DrainSockets(ctx context.Context) {
	h.mu.Lock()
	sockets := make([]*conn, 0, len(h.sockets))
	for c := range h.sockets {
		c.shutdown()
		sockets = append(sockets, c)
	}
	h.mu.Unlock()
	for _, c := range sockets {
		select {
		case <-c.flushed:
		case <-ctx.Done():
		}
	}
	h.cancel()
	h.mu.Lock()
	for c := range h.sockets {
		c.kill()
	}
	h.mu.Unlock()
}
