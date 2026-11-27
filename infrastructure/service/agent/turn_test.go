package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	"github.com/jahrulnr/nusashell/infrastructure/service/agent/acp"
)

// usageUpdate decodes a usage_update the way the process delivers it.
func usageUpdate(t *testing.T, body string) acp.SessionUpdate {
	t.Helper()
	var u acp.SessionUpdate
	if err := json.Unmarshal([]byte(body), &u); err != nil {
		t.Fatal(err)
	}
	return u
}

func contextReports(events []event.TurnEvent) []valueobject.AgentContext {
	var out []valueobject.AgentContext
	for _, ev := range events {
		if ev.Kind() == enum.TurnEventContext {
			out = append(out, ev.AgentContext())
		}
	}
	return out
}

// keptMedia is one payload the sink handed the store.
type keptMedia struct {
	name      string
	mediaType string
	raw       []byte
}

// mediaSink builds a sink whose saved media land in the returned slice, and
// feeds it one content block the way the agent's turn would.
func mediaSink(t *testing.T) (*turnSink, *[]keptMedia) {
	t.Helper()
	kept := []keptMedia{}
	sink := newTurnSink(context.Background(), func(event.TurnEvent) {}, nil)
	sink.save = func(_ context.Context, name, mediaType string, raw []byte) (valueobject.Attachment, error) {
		kept = append(kept, keptMedia{name: name, mediaType: mediaType, raw: raw})
		return valueobject.NewAttachment("file:///tmp/kept/"+name, mediaType)
	}
	return sink, &kept
}

func (s *turnSink) addMediaLocked(b acp.ContentBlock) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addMedia(b)
}

// blob wraps payload bytes the way the agent sends them.
func blob(raw []byte) string { return base64.StdEncoding.EncodeToString(raw) }

// An agent that sends a voice note without a media type is answered with the
// file's own name: the bytes alone read as video, and only the extension says
// it is audio.
func TestKeptMediaSniffsWithTheFilesName(t *testing.T) {
	sink, kept := mediaSink(t)
	sink.addMediaLocked(acp.ContentBlock{Type: "resource", Name: "voice.m4a",
		Resource: &acp.EmbeddedResource{MIMEType: "application/octet-stream",
			Blob: blob([]byte("\x00\x00\x00\x18ftypM4A \x00\x00\x00\x00"))}})
	if len(*kept) != 1 || (*kept)[0].mediaType != "audio/mp4" {
		t.Fatalf("kept = %+v, want audio/mp4", *kept)
	}
}

// A payload with no name at all is still named by its bytes — the sniffer's
// extension tie-break simply has nothing to add.
func TestKeptMediaWithoutANameFallsBackToItsBytes(t *testing.T) {
	sink, kept := mediaSink(t)
	sink.addMediaLocked(acp.ContentBlock{Type: "resource",
		Resource: &acp.EmbeddedResource{MIMEType: "application/octet-stream",
			Blob: blob([]byte("OggS\x00\x02\x00\x00\x00\x00\x00\x00\x00\x00"))}})
	if len(*kept) != 1 || (*kept)[0].mediaType != "audio/ogg" {
		t.Fatalf("kept = %+v, want audio/ogg", *kept)
	}
	if (*kept)[0].name == "" {
		t.Fatal("the store still needs a name for a payload that arrived without one")
	}
}

// A media type the agent states is its own answer and is kept as given: these
// are Ogg bytes, but the agent says wav, and the sink must not overrule it.
func TestKeptMediaKeepsTheDeclaredType(t *testing.T) {
	sink, kept := mediaSink(t)
	sink.addMediaLocked(acp.ContentBlock{Type: "audio", Name: "voice.wav", MIMEType: "audio/wav",
		Data: blob([]byte("OggS\x00\x02\x00\x00\x00\x00\x00\x00\x00\x00"))})
	if len(*kept) != 1 || (*kept)[0].mediaType != "audio/wav" {
		t.Fatalf("kept = %+v, want the declared audio/wav", *kept)
	}
}

// One sniffer names payloads in this package: no caller sniffs content types
// itself, where the extension tie-break would be lost.
func TestNoRawContentTypeSniffingInThePackage(t *testing.T) {
	needle := "Detect" + "ContentType" // split so this file never matches itself
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), needle) {
			t.Fatalf("%s sniffs content types itself; call dservice.SniffMediaType instead", name)
		}
	}
}

func TestUsageUpdateReportsTheAgentsContext(t *testing.T) {
	var got []event.TurnEvent
	sink := newTurnSink(context.Background(), func(ev event.TurnEvent) { got = append(got, ev) }, nil)
	sink.update(usageUpdate(t, `{"sessionUpdate":"usage_update","used":53000,"size":200000,"cost":{"amount":0.0421,"currency":"usd"}}`))
	sink.update(usageUpdate(t, `{"sessionUpdate":"usage_update","used":61000,"size":200000}`))

	reports := contextReports(got)
	if len(reports) != 2 {
		t.Fatalf("context events = %d, want one per update", len(reports))
	}
	if r := reports[0]; r.Used() != 53000 || r.Size() != 200000 {
		t.Fatalf("first report = %d/%d", r.Used(), r.Size())
	}
	if amount, currency, ok := reports[0].Cost(); !ok || amount != 0.0421 || currency != "USD" {
		t.Fatalf("cost = %v %q %v", amount, currency, ok)
	}
	if _, _, ok := reports[1].Cost(); ok {
		t.Fatal("a report without a cost must carry none")
	}
}

func TestUsageUpdateWithoutAWindowSaysNothing(t *testing.T) {
	var got []event.TurnEvent
	sink := newTurnSink(context.Background(), func(ev event.TurnEvent) { got = append(got, ev) }, nil)
	sink.update(usageUpdate(t, `{"sessionUpdate":"usage_update","used":500}`))
	sink.update(usageUpdate(t, `{"sessionUpdate":"usage_update","used":-1,"size":100}`))
	if len(got) != 0 {
		t.Fatalf("unusable reports produced %d events", len(got))
	}
}

func TestUsageUpdateWaitsOutASteer(t *testing.T) {
	var got []event.TurnEvent
	sink := newTurnSink(context.Background(), func(ev event.TurnEvent) { got = append(got, ev) }, nil)
	if !sink.gate() {
		t.Fatal("gate refused")
	}
	sink.update(usageUpdate(t, `{"sessionUpdate":"usage_update","used":10,"size":100}`))
	if len(got) != 0 {
		t.Fatal("an update went out while a steer was in flight")
	}
	sink.release(valueobject.UserInput{Text: "more"}, "", true, nil)
	// The owner's input lands first, then what the agent said meanwhile.
	if len(got) != 2 || got[0].Kind() != enum.TurnEventSteer || got[1].Kind() != enum.TurnEventContext {
		t.Fatalf("events after release = %v", got)
	}
}

// Devin's member tag is translated exactly once: the raw cognition.ai meta
// stays inside the adapter, while the record carries only the neutral
// acp.subagent.parent fact.
func TestToolCallMemberMetaBecomesAScopedRecordKey(t *testing.T) {
	sink := newTurnSink(context.Background(), func(event.TurnEvent) {}, nil)
	sink.update(usageUpdate(t, `{"sessionUpdate":"tool_call","toolCallId":"member-1","title":"Read file","kind":"read","rawInput":{"file_path":"/tmp/x"},"_meta":{"cognition.ai/subagent_context":{"parentAgentId":"run-1"},"cognition.ai/timestamp":"2026-11-27T00:00:00Z"}}`))
	sink.mu.Lock()
	c := sink.calls["member-1"]
	sink.mu.Unlock()
	if c == nil {
		t.Fatal("the member call was not recorded")
	}
	tc, err := c.entity()
	if err != nil {
		t.Fatal(err)
	}
	if got := tc.OptionalContent()["acp.subagent.parent"]; got != "run-1" {
		t.Fatalf("acp.subagent.parent = %q, want run-1", got)
	}
	if _, leaked := tc.OptionalContent()["cognition.ai/subagent_context"]; leaked {
		t.Fatalf("raw cognition meta leaked into the record: %+v", tc.OptionalContent())
	}
}

// A bare update is not a record. It neither mints one nor repeats its
// diagnostic on every pulse.
func TestBareUnknownToolCallUpdateIsDroppedOnce(t *testing.T) {
	var events []event.TurnEvent
	var logs bytes.Buffer
	sink := newTurnSink(context.Background(), func(ev event.TurnEvent) { events = append(events, ev) }, nil)
	sink.log = slog.New(slog.NewTextHandler(&logs, nil))
	pulse := `{"sessionUpdate":"tool_call_update","toolCallId":"unknown-bare-1","status":"in_progress"}`
	sink.update(usageUpdate(t, pulse))
	sink.update(usageUpdate(t, pulse))

	sink.mu.Lock()
	_, minted := sink.calls["unknown-bare-1"]
	parts := len(sink.parts)
	sink.mu.Unlock()
	if minted || parts != 0 || len(events) != 0 {
		t.Fatalf("bare update minted=%v parts=%d events=%d", minted, parts, len(events))
	}
	if n := strings.Count(logs.String(), "agent tool update dropped"); n != 1 {
		t.Fatalf("drop logs = %d, want one per id; logs:\n%s", n, logs.String())
	}
}

// A tool_call_update may introduce a record only when it carries record truth
// itself — here a title — rather than only a progress pulse.
func TestSelfDescribingUnknownToolCallUpdateMintsOneRecord(t *testing.T) {
	var events []event.TurnEvent
	sink := newTurnSink(context.Background(), func(ev event.TurnEvent) { events = append(events, ev) }, nil)
	sink.update(usageUpdate(t, `{"sessionUpdate":"tool_call_update","toolCallId":"late-1","title":"Read file","kind":"read","status":"in_progress"}`))

	sink.mu.Lock()
	c := sink.calls["late-1"]
	sink.mu.Unlock()
	if c == nil || c.title != "Read file" || c.kind != "read" {
		t.Fatalf("self-describing update record = %+v", c)
	}
	if len(events) != 1 || events[0].Kind() != enum.TurnEventToolCall || events[0].ToolCall().Name() != "Read file" {
		t.Fatalf("events = %v", events)
	}
}

// Lifecycle ids are routing identities, not tool calls. Started correlates to
// the exact unbound run call, completion annotates only through that written
// id, and neither lifecycle id ever mints a record.
func TestSubagentLifecycleAnnotatesTheRunCallWithoutMintingRecords(t *testing.T) {
	sink := newTurnSink(context.Background(), func(event.TurnEvent) {}, nil)
	for _, call := range []struct{ id, title, task string }{
		{"run-a", "Alpha", "first task"},
		{"run-b", "Beta", "second task"},
	} {
		input, _ := json.Marshal(map[string]string{"title": call.title, "task": call.task})
		meta, _ := json.Marshal(map[string]string{"cognition.ai/inferenceToolName": "run_subagent"})
		sink.update(acp.SessionUpdate{SessionUpdate: "tool_call", ToolCallID: call.id, Title: "Ran explore subagent " + call.title, RawInput: input, Meta: meta})
	}

	sink.update(usageUpdate(t, `{"sessionUpdate":"tool_call_update","toolCallId":"agent-beta","status":"in_progress","_meta":{"cognition.ai/subagent_started":{"agentId":"agent-beta","title":"Beta","task":"second task","profile":"subagent_explore","depth":2,"isBackground":false,"model":"devin"}}}`))
	sink.update(usageUpdate(t, `{"sessionUpdate":"tool_call_update","toolCallId":"agent-beta","status":"completed","_meta":{"cognition.ai/subagent_completed":{"agentId":"agent-beta","success":true,"summary":"done","depth":2}}}`))

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if _, minted := sink.calls["agent-beta"]; minted {
		t.Fatal("the lifecycle id minted a tool record")
	}
	run := sink.calls["run-b"]
	if run == nil {
		t.Fatal("run-b was not recorded")
	}
	want := map[string]string{
		"acp.subagent.id":         "agent-beta",
		"acp.subagent.profile":    "subagent_explore",
		"acp.subagent.depth":      "2",
		"acp.subagent.background": "false",
		"acp.subagent.model":      "devin",
		"acp.subagent.success":    "true",
		"acp.subagent.summary":    "done",
	}
	for key, value := range want {
		if got := run.keys[key]; got != value {
			t.Fatalf("%s = %q, want %q; keys=%+v", key, got, value, run.keys)
		}
	}
	if other := sink.calls["run-a"]; other.keys["acp.subagent.id"] != "" {
		t.Fatalf("the exact match was bypassed: run-a keys=%+v", other.keys)
	}
	for key := range run.keys {
		if strings.HasPrefix(key, "cognition.ai/") {
			t.Fatalf("raw cognition meta leaked into the record: %+v", run.keys)
		}
	}
}

// A lifecycle pulse with no run call is dropped once and never becomes a card.
func TestUnboundSubagentLifecycleIsDroppedOnce(t *testing.T) {
	var events []event.TurnEvent
	var logs bytes.Buffer
	sink := newTurnSink(context.Background(), func(ev event.TurnEvent) { events = append(events, ev) }, nil)
	sink.log = slog.New(slog.NewTextHandler(&logs, nil))
	pulse := `{"sessionUpdate":"tool_call_update","toolCallId":"agent-orphan","status":"in_progress","_meta":{"cognition.ai/subagent_started":{"agentId":"agent-orphan","title":"Orphan","task":"missing"}}}`
	sink.update(usageUpdate(t, pulse))
	sink.update(usageUpdate(t, pulse))

	sink.mu.Lock()
	_, minted := sink.calls["agent-orphan"]
	sink.mu.Unlock()
	if minted || len(events) != 0 {
		t.Fatalf("unbound lifecycle minted=%v events=%d", minted, len(events))
	}
	if n := strings.Count(logs.String(), "agent tool update dropped"); n != 1 {
		t.Fatalf("drop logs = %d, want one per id; logs:\n%s", n, logs.String())
	}
}

// A member never receives a completion, so finishing must preserve it as
// incomplete activity — not manufacture an output or a failed verdict. An
// ordinary unfinished tool remains failed, which is the control.
func TestFinishDoesNotForceAMemberFailed(t *testing.T) {
	var events []event.TurnEvent
	sink := newTurnSink(context.Background(), func(ev event.TurnEvent) { events = append(events, ev) }, nil)
	sink.update(usageUpdate(t, `{"sessionUpdate":"tool_call","toolCallId":"member-1","title":"Read file","kind":"read","status":"in_progress","_meta":{"cognition.ai/subagent_context":{"parentAgentId":"agent-1"}}}`))
	sink.update(usageUpdate(t, `{"sessionUpdate":"tool_call_update","toolCallId":"member-1","status":"completed","rawOutput":"member output"}`))
	sink.update(usageUpdate(t, `{"sessionUpdate":"tool_call","toolCallId":"ordinary-1","title":"Write file","kind":"write","status":"in_progress"}`))

	msg, err := sink.finish(acp.PromptResult{StopReason: "end_turn"}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string]entity.ToolCall{}
	for _, tc := range msg.ToolCalls() {
		calls[tc.ID()] = tc
	}
	member := calls["member-1"]
	if member.ID() == "" {
		t.Fatal("the member call is missing from the finished message")
	}
	if member.Failed() || member.HasOutput() || member.Output() != "" {
		t.Fatalf("member outcome manufactured: failed=%v hasOutput=%v output=%q", member.Failed(), member.HasOutput(), member.Output())
	}
	ordinary := calls["ordinary-1"]
	if !ordinary.Failed() {
		t.Fatal("control failed: an ordinary unfinished tool must remain failed")
	}
	var memberResults, memberOutputs int
	for _, ev := range events {
		if ev.ToolCall().ID() != "member-1" {
			continue
		}
		switch ev.Kind() {
		case enum.TurnEventToolResult:
			memberResults++
		case enum.TurnEventToolOutput:
			memberOutputs++
		}
	}
	if memberResults != 0 || memberOutputs != 0 {
		t.Fatalf("member outcome events: results=%d outputs=%d, want none", memberResults, memberOutputs)
	}
}

// A lifecycle fact after the opening frame is another turn.tool_call for
// the same parent id. It does not mint the agentId, does not replace the
// report, and does not write success when the completion said false.
func TestSubagentLifecycleReplacesKeysWithoutASecondRow(t *testing.T) {
	var events []event.TurnEvent
	sink := newTurnSink(context.Background(), func(ev event.TurnEvent) { events = append(events, ev) }, nil)
	input, _ := json.Marshal(map[string]string{"title": "Beta", "task": "second task"})
	meta, _ := json.Marshal(map[string]string{"cognition.ai/inferenceToolName": "run_subagent"})
	sink.update(acp.SessionUpdate{
		SessionUpdate: "tool_call", ToolCallID: "run-b", Title: "Ran explore subagent Beta",
		Status: "in_progress", RawInput: input, Meta: meta,
		Content: json.RawMessage(`[{"type":"content","content":{"type":"text","text":"the report"}}]`),
	})
	opened := toolCallEvents(events, "run-b")
	if len(opened) != 1 {
		t.Fatalf("opening frames = %d, want 1", len(opened))
	}
	if _, ok := opened[0].ToolCall().OptionalContent()["acp.subagent.id"]; ok {
		t.Fatal("the opening frame invented an id before the lifecycle")
	}

	sink.update(usageUpdate(t, `{"sessionUpdate":"tool_call_update","toolCallId":"agent-beta","status":"in_progress","_meta":{"cognition.ai/subagent_started":{"agentId":"agent-beta","title":"Beta","task":"second task","profile":"subagent_explore","depth":2,"isBackground":false,"model":"devin"},"cognition.ai/timestamp":"2026-11-28T00:00:00Z"}}`))
	replaced := toolCallEvents(events, "run-b")
	if len(replaced) != 2 {
		t.Fatalf("frames after start = %d, want the opening plus one replacement", len(replaced))
	}
	if replaced[1].ToolCall().ID() != "run-b" {
		t.Fatalf("replacement id = %q, want the parent call", replaced[1].ToolCall().ID())
	}
	keys := replaced[1].ToolCall().OptionalContent()
	for key, value := range map[string]string{
		"acp.subagent.id": "agent-beta", "acp.subagent.profile": "subagent_explore",
		"acp.subagent.model": "devin", "acp.subagent.depth": "2", "acp.subagent.background": "false",
	} {
		if keys[key] != value {
			t.Fatalf("%s = %q, want %q", key, keys[key], value)
		}
	}
	for key := range keys {
		if strings.HasPrefix(key, "cognition.ai/") || !strings.HasPrefix(key, "acp.subagent.") {
			t.Fatalf("key outside the family: %q", key)
		}
	}
	sink.mu.Lock()
	if _, minted := sink.calls["agent-beta"]; minted {
		t.Fatal("the lifecycle id minted a tool record")
	}
	if sink.calls["run-b"].output != "the report" {
		t.Fatalf("report = %q", sink.calls["run-b"].output)
	}
	sink.mu.Unlock()

	sink.update(usageUpdate(t, `{"sessionUpdate":"tool_call_update","toolCallId":"agent-beta","status":"completed","_meta":{"cognition.ai/subagent_completed":{"agentId":"agent-beta","success":false,"summary":"nope"}}}`))
	if n := len(toolCallEvents(events, "run-b")); n != 2 {
		t.Fatalf("frames after success false = %d, want no new frame", n)
	}
	sink.mu.Lock()
	if _, ok := sink.calls["run-b"].keys["acp.subagent.success"]; ok {
		t.Fatalf("success false was written: %+v", sink.calls["run-b"].keys)
	}
	if _, ok := sink.calls["run-b"].keys["acp.subagent.summary"]; ok {
		t.Fatal("a summary rode a success false completion")
	}
	sink.mu.Unlock()

	sink.update(usageUpdate(t, `{"sessionUpdate":"tool_call_update","toolCallId":"agent-beta","status":"completed","_meta":{"cognition.ai/subagent_completed":{"agentId":"agent-beta","success":true,"summary":"done"}}}`))
	done := toolCallEvents(events, "run-b")
	if len(done) != 3 {
		t.Fatalf("frames after success = %d, want a replacement", len(done))
	}
	if done[2].ToolCall().OptionalContent()["acp.subagent.success"] != "true" || done[2].ToolCall().OptionalContent()["acp.subagent.summary"] != "done" {
		t.Fatalf("success keys = %+v", done[2].ToolCall().OptionalContent())
	}
	sink.mu.Lock()
	if sink.calls["run-b"].output != "the report" {
		t.Fatalf("the summary replaced the report: %q", sink.calls["run-b"].output)
	}
	if len(sink.order) != 1 {
		t.Fatalf("rows = %v, want the one parent call", sink.order)
	}
	sink.mu.Unlock()
}

func toolCallEvents(events []event.TurnEvent, id string) []event.TurnEvent {
	var out []event.TurnEvent
	for _, ev := range events {
		if ev.Kind() == enum.TurnEventToolCall && ev.ToolCall().ID() == id {
			out = append(out, ev)
		}
	}
	return out
}

// D-27: every agent activity leaves the clock on working when apply
// returns, and no update ends the clock. The table is the v1 names plus the
// v2 names ACP gives the same activity: agent_message and agent_thought for
// the whole-message upserts beside the chunk names, plan_update for plan. A
// report that is not activity moves nothing.
func TestEveryAgentActivityLeavesTheClockWorking(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		activity bool
	}{
		{"agent_message_chunk v1", `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hi"}}`, true},
		{"agent_thought_chunk v1", `{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"thinking"}}`, true},
		{"tool_call v1", `{"sessionUpdate":"tool_call","toolCallId":"call-1","title":"Read file","kind":"read"}`, true},
		{"tool_call_update v1", `{"sessionUpdate":"tool_call_update","toolCallId":"call-1","title":"Read file","status":"completed"}`, true},
		{"plan v1", `{"sessionUpdate":"plan","entries":[{"content":"Step 1","priority":"high","status":"pending"}]}`, true},
		{"agent_message v2", `{"sessionUpdate":"agent_message","messageId":"m1","content":[{"type":"text","text":"whole"}]}`, true},
		{"agent_thought v2", `{"sessionUpdate":"agent_thought","messageId":"m2","content":[{"type":"text","text":"whole"}]}`, true},
		{"plan_update v2", `{"sessionUpdate":"plan_update","plan":{"type":"items","planId":"plan-1","entries":[{"content":"Step 1","priority":"high","status":"pending"}]}}`, true},
		{"usage_update is not activity", `{"sessionUpdate":"usage_update","used":10,"size":100}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var clocks []event.TurnEvent
			sink := newTurnSink(context.Background(), func(ev event.TurnEvent) {
				if ev.Kind() == enum.TurnEventClock {
					clocks = append(clocks, ev)
				}
			}, nil)
			sink.markPrompted(1_700_000_000_000)
			sink.update(usageUpdate(t, tc.body))
			if !tc.activity {
				if len(clocks) != 1 || clocks[0].Clock() != "waiting" {
					t.Fatalf("clocks = %d, want only the waiting frame", len(clocks))
				}
				return
			}
			if len(clocks) < 2 {
				t.Fatalf("clock frames = %d, want the waiting one and a working one", len(clocks))
			}
			last := clocks[len(clocks)-1]
			if last.Clock() != "working" {
				t.Fatalf("last clock = %q, want working", last.Clock())
			}
			if last.PromptedAt() != 1_700_000_000_000 || last.LastUpdateAt() == 0 {
				t.Fatalf("working = prompted %d updated %d", last.PromptedAt(), last.LastUpdateAt())
			}
		})
	}
}

// The true end of the turn writes no clock: the last activity's working frame
// stands and the terminal frame ends the live turn.
func TestTurnEndLeavesTheClockWhereTheLastActivityPutIt(t *testing.T) {
	var clocks []event.TurnEvent
	sink := newTurnSink(context.Background(), func(ev event.TurnEvent) {
		if ev.Kind() == enum.TurnEventClock {
			clocks = append(clocks, ev)
		}
	}, nil)
	sink.markPrompted(1_700_000_000_000)
	sink.update(usageUpdate(t, `{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"thinking"}}`))
	if _, err := sink.finish(acp.PromptResult{StopReason: "end_turn"}, nil, false); err != nil {
		t.Fatal(err)
	}
	if len(clocks) != 2 || clocks[len(clocks)-1].Clock() != "working" {
		t.Fatalf("clocks after the turn ended = %d, last %q; want waiting then working", len(clocks), clocks[len(clocks)-1].Clock())
	}
}
