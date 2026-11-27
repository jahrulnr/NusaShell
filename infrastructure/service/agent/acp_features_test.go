package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	repoagent "github.com/jahrulnr/nusashell/infrastructure/repository/agent"
	repoupload "github.com/jahrulnr/nusashell/infrastructure/repository/upload"
	repoworkspace "github.com/jahrulnr/nusashell/infrastructure/repository/workspace"
	"github.com/jahrulnr/nusashell/infrastructure/service/agent/acp"
)

func timeout(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// traceTo sets the fake agent's environment and returns the file it traces to.
func traceTo(t *testing.T, a *aggregate.Agent, env map[string]string) string {
	t.Helper()
	trace := filepath.Join(t.TempDir(), "trace")
	env["FAKE_ACP_TRACE"] = trace
	if err := a.SetEnv(env); err != nil {
		t.Fatal(err)
	}
	return trace
}

func readTrace(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(raw)
}

// next adds another user turn to the room.
func next(t *testing.T, in *dservice.AgentPrompt, text string) {
	t.Helper()
	user, err := entity.NewMessage(enum.RoleUser, text)
	if err != nil {
		t.Fatal(err)
	}
	in.Conversation.Append(user)
	in.Input = user
}

// A project's other folders reach an agent that says it takes them, even when
// it says so with an empty object, and every agent is told about them.
func TestProjectFoldersReachTheAgent(t *testing.T) {
	rt, in := setup(t, "hello")
	trace := traceTo(t, in.Agent, map[string]string{
		"FAKE_ACP_ADDITIONAL_DIRS": "1", "FAKE_ACP_SYSTEM_PROMPT": "1", "FAKE_ACP_TRACE_SESSION_PROMPT": "1",
	})
	other, third := t.TempDir(), t.TempDir()
	in.ExtraFolders = []string{other, third}
	if err := rt.Prompt(timeout(t), in, nil, nil); err != nil {
		t.Fatal(err)
	}
	got := readTrace(t, trace)
	if !strings.Contains(got, `"additionalDirectories":[`) || !strings.Contains(got, other) || !strings.Contains(got, third) {
		t.Fatalf("session/new did not carry the folders: %s", got)
	}
	// An agent that takes a system prompt finds the folders in it.
	if !strings.Contains(got, "several folders") {
		t.Fatalf("the instructions do not name the folders: %s", got)
	}
}

func TestAgentWithoutAdditionalDirectoriesIsToldInstead(t *testing.T) {
	rt, in := setup(t, "hello")
	trace := traceTo(t, in.Agent, map[string]string{"FAKE_ACP_TRACE_SESSION_PROMPT": "1"})
	other := t.TempDir()
	in.ExtraFolders = []string{other}
	if err := rt.Prompt(timeout(t), in, nil, nil); err != nil {
		t.Fatal(err)
	}
	got := readTrace(t, trace)
	if strings.Contains(got, "additionalDirectories") {
		t.Fatalf("folders were sent to an agent that does not take them: %s", got)
	}
	// With no system prompt to carry them, the first input does.
	if line := lineWith(got, "prompt_start"); !strings.Contains(line, "several folders") || !strings.Contains(line, other) {
		t.Fatalf("the first input does not name the folders: %s", line)
	}
}

func lineWith(trace, prefix string) string {
	for l := range strings.SplitSeq(trace, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}

// An agent that cannot take a system prompt hears the instructions once,
// ahead of its first ordinary input; a slash command carries nothing, so the
// agent recognizes it, and the instructions wait for the input after it.
func TestInstructionsWaitOutASlashCommand(t *testing.T) {
	rt, in := setup(t, "/compact")
	trace := traceTo(t, in.Agent, map[string]string{})
	ctx := timeout(t)
	for _, text := range []string{"", "write a poem", "another poem"} {
		if text != "" {
			next(t, &in, text)
		}
		if err := rt.Prompt(ctx, in, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	var starts []string
	for l := range strings.SplitSeq(readTrace(t, trace), "\n") {
		if strings.HasPrefix(l, "prompt_start") {
			starts = append(starts, l)
		}
	}
	if len(starts) != 3 {
		t.Fatalf("prompts = %v", starts)
	}
	const marker = "Standing instructions"
	if strings.Contains(starts[0], marker) || !strings.HasSuffix(starts[0], "/compact") {
		t.Fatalf("a slash command carried more than itself: %s", starts[0])
	}
	if !strings.Contains(starts[1], marker) || !strings.Contains(starts[1], "write a poem") {
		t.Fatalf("the first ordinary input did not carry the instructions: %s", starts[1])
	}
	if strings.Contains(starts[2], marker) {
		t.Fatalf("the instructions were sent twice: %s", starts[2])
	}
}

// What the agent could not take is owed to it again: a prompt that failed
// before reaching it leaves the preamble where it was.
func TestPreambleSurvivesAFailedPrompt(t *testing.T) {
	s := quietSession()
	s.preamble = preamble{instructions: true}
	in := dservice.AgentPrompt{Conversation: mustRoom(t)}
	next(t, &in, "hello")
	_, taken := new(Runtime).promptBlocks(context.Background(), in, s)
	if !taken.instructions || s.preamble.instructions {
		t.Fatalf("the first ordinary input should have taken the preamble: %+v / %+v", taken, s.preamble)
	}
}

func mustRoom(t *testing.T) *aggregate.Conversation {
	t.Helper()
	conv, err := aggregate.NewConversation("")
	if err != nil {
		t.Fatal(err)
	}
	return conv
}

// Attachments go the way the agent says it can take them: a picture inline
// for an agent that takes images, a text file embedded for one that takes
// embedded context, and a link for the rest — a sound the agent does not take
// included.
func TestAttachmentsGoByCapability(t *testing.T) {
	dir := t.TempDir()
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")
	files := map[string][]byte{"pixel.png": png, "notes.txt": []byte("remember the milk"), "voice.mp3": []byte("ID3")}
	var attachments []valueobject.Attachment
	for _, name := range []string{"pixel.png", "notes.txt", "voice.mp3"} {
		if err := os.WriteFile(filepath.Join(dir, name), files[name], 0o600); err != nil {
			t.Fatal(err)
		}
		mt := map[string]string{"pixel.png": "image/png", "notes.txt": "text/plain", "voice.mp3": "audio/mpeg"}[name]
		a, err := valueobject.NewAttachment("file://"+filepath.Join(dir, name), mt)
		if err != nil {
			t.Fatal(err)
		}
		a.SetName(name)
		attachments = append(attachments, a)
	}
	rt, in := setup(t, "look at these")
	rt.opts.Workspace = repoworkspace.NewLocal()
	trace := traceTo(t, in.Agent, map[string]string{})
	in.Workspace = dir
	for _, a := range attachments {
		in.Input.AddAttachment(a)
	}
	if err := rt.Prompt(timeout(t), in, nil, nil); err != nil {
		t.Fatal(err)
	}
	// The fake takes images and embedded context, not audio.
	got := lineWith(readTrace(t, trace), "prompt_blocks")
	if !strings.Contains(got, "text,image,resource,resource_link") {
		t.Fatalf("blocks = %q", got)
	}
}

// An agent's picture is kept and shown with the reply, in its place.
func TestAgentMediaIsKeptWithTheReply(t *testing.T) {
	rt, in := setup(t, "send-media-test")
	uploads, err := repoupload.New(filepath.Join(t.TempDir(), "uploads"))
	if err != nil {
		t.Fatal(err)
	}
	rt.opts.Uploads = uploads
	rec := &recorder{}
	if err := rt.Prompt(timeout(t), in, rec.handle, nil); err != nil {
		t.Fatal(err)
	}
	media := in.Conversation.Last().Media()
	if len(media) != 1 || media[0].Name() != "pixel.png" || media[0].MediaType() != "image/png" || media[0].SHA256() == "" {
		t.Fatalf("media = %+v", media)
	}
	if raw, err := os.ReadFile(strings.TrimPrefix(media[0].URI(), "file://")); err != nil || len(raw) == 0 {
		t.Fatalf("the picture was not kept: %v", err)
	}
	// It sits between the agent's words, where it arrived.
	var kinds []string
	for _, p := range in.Conversation.Last().Parts() {
		kinds = append(kinds, p.Kind)
	}
	if strings.Join(kinds, ",") != "text,media" {
		t.Fatalf("parts = %v", kinds)
	}
	if got := rec.kinds(); got[enum.TurnEventMedia] != 1 {
		t.Fatalf("events = %v", got)
	}
}

func TestAgentMediaNeedsAPlaceToBeKept(t *testing.T) {
	rt, in := setup(t, "send-media-test")
	if err := rt.Prompt(timeout(t), in, nil, nil); err != nil {
		t.Fatal(err)
	}
	if n := len(in.Conversation.Last().Media()); n != 0 {
		t.Fatalf("a picture was shown with nowhere to keep it: %d", n)
	}
}

// A file the agent links is shown only from inside the session's folders.
func TestAgentFileLinksStayInsideTheFolders(t *testing.T) {
	dir := t.TempDir()
	sink := newTurnSink(context.Background(), nil, nil)
	sink.folders = []string{dir}
	inside := "file://" + filepath.Join(dir, "out", "chart.png")
	for _, uri := range []string{inside, "file:///etc/passwd", "https://example.com/x.png", "file://" + dir} {
		sink.addMedia(contentBlock("resource_link", uri))
	}
	if len(sink.parts) != 1 {
		t.Fatalf("parts = %+v", sink.parts)
	}
	a, ok := sink.parts[0].Media()
	if !ok || a.URI() != inside || a.MediaType() != "image/png" || a.Name() != "chart.png" {
		t.Fatalf("media = %+v", a)
	}
}

// A linked file nobody stated a type for is read the way every other caller
// reads one: the bytes decide and the name is only the tie-breaker. An
// extension that says nothing — or that lies — still shows a picture as a
// picture instead of plain bytes.
func TestAgentLinkedFileIsSniffedWhenItsTypeIsUnstated(t *testing.T) {
	dir := t.TempDir()
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	silent := filepath.Join(dir, "chart.bin")
	lying := filepath.Join(dir, "chart.txt")
	for _, path := range []string{silent, lying} {
		if err := os.WriteFile(path, png, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sink := newTurnSink(context.Background(), nil, nil)
	sink.folders = []string{dir}
	sink.addMedia(contentBlock("resource_link", "file://"+silent))
	sink.addMedia(contentBlock("resource_link", "file://"+lying))
	if len(sink.parts) != 2 {
		t.Fatalf("parts = %+v", sink.parts)
	}
	for _, part := range sink.parts {
		a, ok := part.Media()
		if !ok || a.MediaType() != "image/png" {
			t.Fatalf("media = %+v, want image/png from the bytes", a)
		}
	}
}

func TestAgentMediaIsBounded(t *testing.T) {
	dir := t.TempDir()
	sink := newTurnSink(context.Background(), nil, nil)
	sink.folders = []string{dir}
	for i := range 20 {
		sink.addMedia(contentBlock("resource_link", "file://"+filepath.Join(dir, "f"+string(rune('a'+i))+".png")))
	}
	if len(sink.parts) != maxTurnMedia {
		t.Fatalf("media parts = %d, want %d", len(sink.parts), maxTurnMedia)
	}
}

// What the agent says about the session itself reaches the owner as notices
// and a suggested title; the summary it writes of its own context does not.
func TestAgentSessionInformation(t *testing.T) {
	rt, in := setup(t, "info-demo")
	rec := &recorder{}
	if err := rt.Prompt(timeout(t), in, rec.handle, nil); err != nil {
		t.Fatal(err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var titles, notices []string
	for _, ev := range rec.events {
		switch ev.Kind() {
		case enum.TurnEventTitle:
			titles = append(titles, ev.Delta())
		case enum.TurnEventNotice:
			notices = append(notices, ev.NoticeSeverity().String()+"|"+ev.NoticeTitle()+"|"+ev.Delta())
		}
	}
	if len(titles) != 1 || titles[0] != "Fake agent's own title" {
		t.Fatalf("titles = %v", titles)
	}
	want := []string{"warning|Heads up|The fake agent has something to say.", "info|Compacting context|The agent is compacting its context window."}
	if strings.Join(notices, "\n") != strings.Join(want, "\n") {
		t.Fatalf("notices = %v", notices)
	}
	if strings.Contains(in.Conversation.Last().Content(), "SECRET-SUMMARY") {
		t.Fatal("the agent's own context summary showed up in the reply")
	}
}

// An agent that hands two threads one session id would mix their updates and
// approvals together; the second thread is refused.
func TestSessionIDReuseIsRefused(t *testing.T) {
	rt, in := setup(t, "hello")
	if err := in.Agent.SetEnv(map[string]string{"FAKE_ACP_REUSE_SESSION": "1"}); err != nil {
		t.Fatal(err)
	}
	ctx := timeout(t)
	if err := rt.Prompt(ctx, in, nil, nil); err != nil {
		t.Fatal(err)
	}
	other := dservice.AgentPrompt{Agent: in.Agent, Conversation: mustRoom(t), Workspace: in.Workspace}
	if err := other.Conversation.BindAgent(in.Agent.ID()); err != nil {
		t.Fatal(err)
	}
	next(t, &other, "hello from another thread")
	err := rt.Prompt(ctx, other, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "another thread already uses") {
		t.Fatalf("err = %v", err)
	}
}

// A turn asked to yield stops once the tools the agent reported have
// finished, and ends in the ordinary way.
func TestYieldWaitsForToolsThenStopsTheTurn(t *testing.T) {
	rt, in := setup(t, "after-tools-two-tools")
	trace := traceTo(t, in.Agent, map[string]string{})
	ctx := timeout(t)
	convID := in.Conversation.ID()
	done := make(chan error, 1)
	go func() { done <- rt.Prompt(ctx, in, nil, nil) }()
	time.Sleep(150 * time.Millisecond)
	if !rt.Yield(ctx, convID) {
		t.Fatal("no running turn to yield")
	}
	if err := <-done; err != nil {
		t.Fatalf("a yielded turn must end like any other, got %v", err)
	}
	last := in.Conversation.Last()
	if last.FinishReason() != enum.FinishReasonInterrupted {
		t.Fatalf("finish reason = %s", last.FinishReason())
	}
	for _, tc := range last.ToolCalls() {
		if tc.Failed() {
			t.Fatalf("tool %s was cut short: the yield should wait for it", tc.Name())
		}
	}
	if got := readTrace(t, trace); !strings.Contains(got, "tool_done tool-a") || !strings.Contains(got, "tool_done tool-b") {
		t.Fatalf("the yield did not wait for the tools: %s", got)
	}
	// Nothing runs now: there is nothing to yield.
	if rt.Yield(ctx, convID) {
		t.Fatal("yielded a turn that was over")
	}
}

func TestYieldWaitsOutAPermissionQuestion(t *testing.T) {
	rt, in := setup(t, "after-tools-permission")
	ctx := timeout(t)
	asked := make(chan struct{})
	release := make(chan struct{})
	approve := func(_ context.Context, req valueobject.ApprovalRequest) (string, error) {
		close(asked)
		<-release
		opt, _ := req.Option("allow-once")
		return opt.ID, nil
	}
	done := make(chan error, 1)
	go func() { done <- rt.Prompt(ctx, in, nil, approve) }()
	<-asked
	yielded := make(chan struct{})
	go func() { rt.Yield(ctx, in.Conversation.ID()); close(yielded) }()
	select {
	case <-yielded:
		t.Fatal("the turn was stopped with a question still waiting for the owner")
	case <-time.After(400 * time.Millisecond):
	}
	close(release)
	<-yielded
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// A process that ran nothing for a while is stopped, and the room's next
// prompt starts it again and restores the session; one kept resident stays.
func TestIdleProcessesAreReaped(t *testing.T) {
	rt, in := setup(t, "write a poem")
	ctx := timeout(t)
	if err := rt.Prompt(ctx, in, nil, nil); err != nil {
		t.Fatal(err)
	}
	session := in.Conversation.AgentSession()
	rt.ReapIdle(time.Hour)
	if rt.procs[in.Agent.ID()] == nil {
		t.Fatal("a process still in use was stopped")
	}
	rt.ReapIdle(0)
	if rt.procs[in.Agent.ID()] != nil || rt.sessions[in.Conversation.ID()] != nil {
		t.Fatal("the idle process was not stopped")
	}
	next(t, &in, "another poem")
	if err := rt.Prompt(ctx, in, nil, nil); err != nil {
		t.Fatalf("the next prompt did not start the agent again: %v", err)
	}
	if rt.procs[in.Agent.ID()] == nil || in.Conversation.AgentSession() != session {
		t.Fatalf("the session was not restored: %q → %q", session, in.Conversation.AgentSession())
	}

	in.Agent.SetAutoStart(true)
	rt.Reload(in.Agent.ID())
	if err := rt.Warm(ctx, in.Agent); err != nil {
		t.Fatal(err)
	}
	rt.ReapIdle(0)
	if rt.procs[in.Agent.ID()] == nil {
		t.Fatal("an agent kept resident was reaped")
	}
}

// Commands the agent already reported are conversation state, not process
// state: an idle-stop must not make them vanish.
func TestCommandsSurviveIdleStop(t *testing.T) {
	store := repoagent.New(t.TempDir(), nil)
	rt, in := setupCatalog(t, "hello", store)
	ctx := timeout(t)
	if err := rt.Prompt(ctx, in, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := rt.Commands(ctx, in.Conversation.ID(), in.Agent.ID()); len(got) != 1 || got[0].Name != "compact" {
		t.Fatalf("live commands = %+v, want the reported compact", got)
	}
	stored, err := store.CommandCatalog(ctx, in.Conversation.ID())
	if err != nil || len(stored.Commands) != 1 || stored.Commands[0].Name != "compact" || stored.AgentID != in.Agent.ID() {
		t.Fatalf("stored catalog = %+v, %v — want this agent's compact", stored, err)
	}
	rt.ReapIdle(0)
	if rt.sessions[in.Conversation.ID()] != nil || rt.procs[in.Agent.ID()] != nil {
		t.Fatal("the idle process is still running; the catalog would not be read from the store")
	}
	if got := rt.Commands(ctx, in.Conversation.ID(), in.Agent.ID()); len(got) != 1 || got[0].Name != "compact" {
		t.Fatalf("commands after idle-stop = %+v, want the stored compact", got)
	}
}

// An agent that never reports commands leaves no rows behind, before or after
// its process goes away — absence is the honest answer, not a placeholder.
func TestCommandsStayAbsentWhenTheAgentNeverReportsAny(t *testing.T) {
	store := repoagent.New(t.TempDir(), nil)
	rt, in := setupCatalog(t, "hello", store)
	if err := in.Agent.SetEnv(map[string]string{"FAKE_ACP_COMMANDS": "none"}); err != nil {
		t.Fatal(err)
	}
	ctx := timeout(t)
	if err := rt.Prompt(ctx, in, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := rt.Commands(ctx, in.Conversation.ID(), in.Agent.ID()); len(got) != 0 {
		t.Fatalf("commands = %+v, want none", got)
	}
	stored, err := store.CommandCatalog(ctx, in.Conversation.ID())
	if err != nil || stored.AgentID != "" || len(stored.Commands) != 0 {
		t.Fatalf("a room that never reported wrote a catalog: %+v, %v", stored, err)
	}
	rt.ReapIdle(0)
	if got := rt.Commands(ctx, in.Conversation.ID(), in.Agent.ID()); len(got) != 0 {
		t.Fatalf("commands after idle-stop = %+v, want none", got)
	}
}

// A later live session replaces the stored list as one report. Lists are
// never merged: a command absent from the newer report disappears even when
// it was present in the older one.
func TestCommandsAreReplacedByTheNextLiveSession(t *testing.T) {
	store := repoagent.New(t.TempDir(), nil)
	rt, in := setupCatalog(t, "hello", store)
	ctx := timeout(t)
	if err := rt.Prompt(ctx, in, nil, nil); err != nil {
		t.Fatal(err)
	}
	in.Conversation.ForgetAgentSession()
	if err := in.Agent.SetEnv(map[string]string{"FAKE_ACP_COMMANDS": "review,explain"}); err != nil {
		t.Fatal(err)
	}
	next(t, &in, "again")
	if err := rt.Prompt(ctx, in, nil, nil); err != nil {
		t.Fatal(err)
	}
	rt.ReapIdle(0)
	got := rt.Commands(ctx, in.Conversation.ID(), in.Agent.ID())
	names := make([]string, 0, len(got))
	for _, cmd := range got {
		names = append(names, cmd.Name)
	}
	if strings.Join(names, ",") != "review,explain" {
		t.Fatalf("stored commands = %v, want exactly [review explain] — no merge with compact", names)
	}
	stored, err := store.CommandCatalog(ctx, in.Conversation.ID())
	if err != nil {
		t.Fatal(err)
	}
	storedNames := make([]string, 0, len(stored.Commands))
	for _, cmd := range stored.Commands {
		storedNames = append(storedNames, cmd.Name)
	}
	if strings.Join(storedNames, ",") != "review,explain" {
		t.Fatalf("the file still holds %v, want the newer report to have replaced compact", storedNames)
	}
}

// The stored list belongs to the conversation's agent, not to the room key
// alone: after the room moves to another agent, the previous agent's commands
// cannot appear for the new one.
func TestStoredCommandsDoNotCrossAnAgentSwitch(t *testing.T) {
	store := repoagent.New(t.TempDir(), nil)
	ctx := timeout(t)
	cat := drepo.AgentCommandCatalog{AgentID: "old-agent", Commands: []dservice.AgentCommand{{Name: "compact"}}, ReportedAt: time.Now()}
	if err := store.SaveCommandCatalog(ctx, "c_room", cat); err != nil {
		t.Fatal(err)
	}
	rt := New(Options{ClientName: "nusashell-test", ClientVersion: "0", Catalog: store})
	t.Cleanup(func() { _ = rt.Shutdown(context.Background()) })
	if got := rt.Commands(ctx, "c_room", "new-agent"); len(got) != 0 {
		t.Fatalf("the previous agent's commands crossed the switch: %+v", got)
	}
	if got := rt.Commands(ctx, "c_room", "old-agent"); len(got) != 1 || got[0].Name != "compact" {
		t.Fatalf("the owning agent lost its stored commands: %+v", got)
	}
}

func TestDiscardDeletesTheAgentsSession(t *testing.T) {
	rt, in := setup(t, "hello")
	trace := traceTo(t, in.Agent, map[string]string{})
	ctx := timeout(t)
	if err := rt.Prompt(ctx, in, nil, nil); err != nil {
		t.Fatal(err)
	}
	rt.Discard(ctx, in.Agent, in.Conversation.ID(), in.Conversation.AgentSession())
	if !strings.Contains(readTrace(t, trace), "session_delete") {
		t.Fatalf("the agent was not asked to delete the session: %s", readTrace(t, trace))
	}
	if rt.sessions[in.Conversation.ID()] != nil {
		t.Fatal("the live session was kept")
	}

	// A session the runtime no longer holds is deleted all the same, which
	// starts the agent when it is not running.
	rt.Reload(in.Agent.ID())
	rt.Discard(ctx, in.Agent, "c_other", "stored-session")
	if n := strings.Count(readTrace(t, trace), "session_delete"); n != 2 {
		t.Fatalf("deletes = %d", n)
	}
}

// A server started by a service manager has no nvm on its PATH; an agent
// that is a Node script still has to find node.
func TestLaunchEnvReachesNode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("HOME-based nvm layout")
	}
	home := t.TempDir()
	bin := filepath.Join(home, ".nvm", "versions", "node", "v22.1.0", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())

	a, _ := aggregate.NewAgent("claude", "Claude", "claude-agent-acp", nil, enum.ApprovalPolicyAsk)
	got := launchEnv(&a)["PATH"]
	if !strings.HasPrefix(got, bin+string(os.PathListSeparator)) {
		t.Fatalf("PATH = %q, want it to start with %q", got, bin)
	}

	// An agent that sets its own PATH keeps it.
	_ = a.SetEnv(map[string]string{"PATH": "/custom"})
	if got := launchEnv(&a)["PATH"]; got != "/custom" {
		t.Fatalf("PATH = %q", got)
	}
}

func contentBlock(kind, uri string) acp.ContentBlock { return acp.ContentBlock{Type: kind, URI: uri} }

// An edit leaves the agent's old session behind. Removing it must not touch
// the session the room opened since: the room keeps using that one.
func TestDiscardOfAnOldSessionLeavesTheNewOne(t *testing.T) {
	rt, in := setup(t, "hello")
	trace := traceTo(t, in.Agent, map[string]string{})
	ctx := timeout(t)
	if err := rt.Prompt(ctx, in, nil, nil); err != nil {
		t.Fatal(err)
	}
	old := in.Conversation.AgentSession()

	in.Conversation.ForgetAgentSession() // what an edit does to the record
	next(t, &in, "hello again")
	if err := rt.Prompt(ctx, in, nil, nil); err != nil {
		t.Fatal(err)
	}
	fresh := in.Conversation.AgentSession()
	if fresh == "" || fresh == old {
		t.Fatalf("the rewound room should hold a new session, got %q (was %q)", fresh, old)
	}

	rt.Discard(ctx, in.Agent, in.Conversation.ID(), old)
	got := readTrace(t, trace)
	if strings.Count(got, "session_delete") != 1 {
		t.Fatalf("the old session was not deleted exactly once: %s", got)
	}
	if live := rt.sessions[in.Conversation.ID()]; live == nil || live.acpID != fresh {
		t.Fatalf("the room's live session was dropped with the old one: %+v", live)
	}

	// The room carries on in the session it has: nothing is opened again.
	opened := strings.Count(got, "session_new")
	next(t, &in, "and a third")
	if err := rt.Prompt(ctx, in, nil, nil); err != nil {
		t.Fatal(err)
	}
	if again := strings.Count(readTrace(t, trace), "session_new"); again != opened {
		t.Fatalf("a session was opened again after the discard: %d → %d", opened, again)
	}
}

// The recap lists what was said, not what the earlier session did, and files
// it changed stay changed; the agent is told so rather than left to trust it.
func TestRecapSaysWhatItLeavesOut(t *testing.T) {
	conv := mustRoom(t)
	first, _ := entity.NewMessage(enum.RoleUser, "refactor the cache")
	reply, _ := entity.NewMessage(enum.RoleAssistant, "done, I moved it to cache.go")
	input, _ := entity.NewMessage(enum.RoleUser, "now the tests")
	conv.Append(first)
	conv.Append(reply)
	conv.Append(input)
	got := transcriptRecap(conv.Messages(), input.ID())
	for _, want := range []string{"User: refactor the cache", "Assistant: done, I moved it to cache.go", "changed on disk may still be there"} {
		if !strings.Contains(got, want) {
			t.Fatalf("recap lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "now the tests") {
		t.Fatalf("the new input belongs after the recap, not in it:\n%s", got)
	}
}

// An edit of the first message leaves nothing to recap, yet the agent that
// did the dropped work is gone: the standing instructions say that files it
// changed may still be there, whatever the recap holds.
func TestInstructionsWarnThatADroppedTurnsFilesStay(t *testing.T) {
	rt, in := setup(t, "hello")
	trace := traceTo(t, in.Agent, map[string]string{})
	if err := rt.Prompt(timeout(t), in, nil, nil); err != nil {
		t.Fatal(err)
	}
	if line := lineWith(readTrace(t, trace), "prompt_start"); !strings.Contains(line, "may still be there") {
		t.Fatalf("the first input does not carry the warning: %s", line)
	}
}

// Command updates obey the same wire-order discipline as options: a stale
// replay cannot roll back — or durably overwrite — a newer report.
func TestStaleCommandUpdateCannotClobberTheStoredReport(t *testing.T) {
	store := repoagent.New(t.TempDir(), nil)
	rt := New(Options{ClientName: "nusashell-test", ClientVersion: "0", Catalog: store})
	t.Cleanup(func() { _ = rt.Shutdown(context.Background()) })
	p := &proc{agentID: "fake", sessions: map[string]*session{}, early: map[string][]earlyUpdate{}}
	s := &session{convID: "c_room", acpID: "sess", proc: p}
	p.sessions["sess"] = s
	rt.sessions["c_room"] = s
	h := &handler{runtime: rt, proc: p}
	mk := func(names ...string) string {
		list := make([]map[string]string, 0, len(names))
		for _, name := range names {
			list = append(list, map[string]string{"name": name})
		}
		raw, _ := json.Marshal(map[string]any{"sessionUpdate": "available_commands_update", "availableCommands": list})
		return string(raw)
	}
	h.OnNotification("session/update", "sess", json.RawMessage(mk("new")), 2)
	h.OnNotification("session/update", "sess", json.RawMessage(mk("old")), 1)
	ctx := timeout(t)
	got := rt.Commands(ctx, "c_room", "fake")
	if len(got) != 1 || got[0].Name != "new" {
		t.Fatalf("live commands = %+v, want the newer report", got)
	}
	stored, err := store.CommandCatalog(ctx, "c_room")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Commands) != 1 || stored.Commands[0].Name != "new" {
		t.Fatalf("stored commands = %+v, want the newer report", stored.Commands)
	}
}
