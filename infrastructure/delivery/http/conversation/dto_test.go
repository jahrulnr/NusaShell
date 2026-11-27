package conversation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// The wire tells a fold's checkpoint apart from every other system payload:
// both are user-role notes the owner never typed, but only the checkpoint is a
// boundary, and the UI needs the string to draw it.
func TestCompactionCheckpointCarriesItsOwnUserType(t *testing.T) {
	checkpoint, err := entity.NewMessage(enum.RoleUser, "CHECKPOINT: the handoff")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.SetUserType(enum.LLMUserTypeCompaction)
	if got := toMessage(checkpoint).UserType; got != "compaction" {
		t.Fatalf("checkpoint userType = %q, want compaction", got)
	}
	note, err := entity.NewMessage(enum.RoleUser, "a system note")
	if err != nil {
		t.Fatal(err)
	}
	note.SetUserType(enum.LLMUserTypeSystem)
	if got := toMessage(note).UserType; got != "system" {
		t.Fatalf("system note userType = %q, want system", got)
	}
}

// A file an agent put in its reply is served like any attachment: by
// reference, without the content pin the server keeps to itself.
func TestMediaPartIsServedWithoutItsPin(t *testing.T) {
	a, err := valueobject.NewAttachment("file:///data/uploads/u_1/chart.png", "image/png")
	if err != nil {
		t.Fatal(err)
	}
	a.SetName("chart.png")
	a.SetSHA256("0123456789abcdef")
	m, _ := entity.NewMessage(enum.RoleAssistant, "here it is")
	m.SetParts([]entity.MessagePart{
		{Kind: entity.MessagePartText, Text: "here it is"},
		entity.NewMediaPart(a),
		{Kind: entity.MessagePartMedia, Text: "not a record"},
	})
	dto := toMessage(m)
	if len(dto.Parts) != 2 {
		t.Fatalf("parts = %+v (an unreadable media record must be dropped)", dto.Parts)
	}
	got := dto.Parts[1]
	if got.Kind != "media" || strings.Contains(got.Text, "sha256") || strings.Contains(got.Text, "0123456789abcdef") {
		t.Fatalf("media part leaked its pin: %+v", got)
	}
	want := `{"mediaType":"image/png","modality":"image","uri":"file:///data/uploads/u_1/chart.png","name":"chart.png"}`
	if got.Text != want {
		t.Fatalf("media part = %s\nwant        %s", got.Text, want)
	}
}

// History toolCalls carry the acp.subagent family and nothing else.
// success false is omitted. An ordinary call omits keys.
func TestHistoryToolCallsCarryOnlyTheSubagentFamily(t *testing.T) {
	run, err := entity.NewToolCall("run-b", "Ran explore", `{"title":"Beta"}`, enum.TooltypeDelegated)
	if err != nil {
		t.Fatal(err)
	}
	run.SetOutput("the report")
	run.SetOptionalContent("acp.subagent.id", "agent-beta")
	run.SetOptionalContent("acp.subagent.success", "false")
	run.SetOptionalContent("acp.subagent.summary", "done")
	run.SetOptionalContent("acp.subagent.extra", "nope")
	run.SetOptionalContent("cognition.ai/subagent_started", "nope")
	member, err := entity.NewToolCall("member-1", "Read file", `{}`, enum.TooltypeDelegated)
	if err != nil {
		t.Fatal(err)
	}
	member.SetOptionalContent("acp.subagent.parent", "agent-beta")
	ordinary, err := entity.NewToolCall("ordinary", "exec", `{}`, enum.TooltypeOrigin)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := entity.NewMessage(enum.RoleAssistant, "done")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []entity.ToolCall{run, member, ordinary} {
		if err := msg.AddToolCall(tc); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(toMessage(msg))
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		ToolCalls []map[string]json.RawMessage `json:"toolCalls"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	byID := map[string]map[string]json.RawMessage{}
	for _, row := range body.ToolCalls {
		var id string
		_ = json.Unmarshal(row["id"], &id)
		byID[id] = row
	}
	var keys map[string]string
	if json.Unmarshal(byID["run-b"]["keys"], &keys) != nil {
		t.Fatalf("run keys = %s", byID["run-b"]["keys"])
	}
	if keys["acp.subagent.id"] != "agent-beta" || keys["acp.subagent.summary"] != "done" {
		t.Fatalf("keys = %+v", keys)
	}
	if _, ok := keys["acp.subagent.success"]; ok {
		t.Fatalf("success false crossed: %+v", keys)
	}
	if _, ok := keys["acp.subagent.extra"]; ok {
		t.Fatalf("a key outside the family crossed: %+v", keys)
	}
	if _, ok := keys["cognition.ai/subagent_started"]; ok {
		t.Fatal("cognition.ai crossed")
	}
	var output string
	if json.Unmarshal(byID["run-b"]["output"], &output) != nil || output != "the report" {
		t.Fatalf("output = %s", byID["run-b"]["output"])
	}
	var parent map[string]string
	if json.Unmarshal(byID["member-1"]["keys"], &parent) != nil || parent["acp.subagent.parent"] != "agent-beta" {
		t.Fatalf("member keys = %s", byID["member-1"]["keys"])
	}
	if _, ok := byID["ordinary"]["keys"]; ok {
		t.Fatalf("ordinary call wrote keys: %s", byID["ordinary"]["keys"])
	}
}
