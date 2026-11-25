package repoconversation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// B-010: a patch line (a tool output, a delivery receipt) names its target
// message by id; repeating an envelope there wrote an empty message into every
// patch. Only a message line carries one.
func TestPatchLinesCarryNoEmptyEnvelope(t *testing.T) {
	dir := t.TempDir()
	repo := NewConversationRepository(dir)
	ctx := t.Context()
	conv, err := aggregate.NewConversation("c1")
	if err != nil {
		t.Fatal(err)
	}
	user, err := entity.NewMessage(enum.RoleUser, "go")
	if err != nil {
		t.Fatal(err)
	}
	conv.Append(user)
	if err := repo.Save(ctx, conv); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AppendMessage(ctx, "c1", user); err != nil {
		t.Fatal(err)
	}
	assistant, err := entity.NewMessage(enum.RoleAssistant, "")
	if err != nil {
		t.Fatal(err)
	}
	call, err := entity.NewToolCall("call_1", "exec", `{}`, enum.TooltypeOrigin)
	if err != nil {
		t.Fatal(err)
	}
	if err := assistant.AddToolCall(call); err != nil {
		t.Fatal(err)
	}
	conv.Append(assistant)
	if _, err := repo.AppendMessage(ctx, "c1", assistant); err != nil {
		t.Fatal(err)
	}
	call.SetStoredOutput("done", "")
	if err := repo.UpdateToolCallOutput(ctx, "c1", call); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordDelivery(ctx, "c1", user.ID(), nil, valueobject.ReceiptDelivered); err != nil {
		t.Fatal(err)
	}

	lines, err := readLines(filepath.Join(dir, "c1", activeFile))
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, line := range lines {
		var rec map[string]json.RawMessage
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatal(err)
		}
		var kind string
		if err := json.Unmarshal(rec["type"], &kind); err != nil {
			t.Fatal(err)
		}
		kinds[kind]++
		_, hasEnvelope := rec["data"]
		if kind == lineMessage {
			if !hasEnvelope {
				t.Fatalf("message line lost its envelope: %s", line)
			}
			continue
		}
		if hasEnvelope {
			t.Fatalf("%s line carries an envelope: %s", kind, line)
		}
	}
	for _, kind := range []string{lineMessage, lineToolOutput, lineDelivery} {
		if kinds[kind] == 0 {
			t.Fatalf("fixture wrote no %s line: %v", kind, kinds)
		}
	}
}

// A file written before the fix keeps its patch lines' empty envelopes: the
// readers never look at a patch line's data, so the history loads unchanged
// (no migration, no shim).
func TestOldPatchLinesWithAnEmptyEnvelopeStillLoad(t *testing.T) {
	dir := t.TempDir()
	repo := NewConversationRepository(dir)
	ctx := t.Context()
	conv, err := aggregate.NewConversation("c1")
	if err != nil {
		t.Fatal(err)
	}
	user, err := entity.NewMessage(enum.RoleUser, "look")
	if err != nil {
		t.Fatal(err)
	}
	conv.Append(user)
	if err := repo.Save(ctx, conv); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AppendMessage(ctx, "c1", user); err != nil {
		t.Fatal(err)
	}
	assistant, err := entity.NewMessage(enum.RoleAssistant, "")
	if err != nil {
		t.Fatal(err)
	}
	call, err := entity.NewToolCall("call_1", "read_media", `{"path":"shot.png"}`, enum.TooltypeOrigin)
	if err != nil {
		t.Fatal(err)
	}
	if err := assistant.AddToolCall(call); err != nil {
		t.Fatal(err)
	}
	conv.Append(assistant)
	if _, err := repo.AppendMessage(ctx, "c1", assistant); err != nil {
		t.Fatal(err)
	}

	// The exact shape B-010 wrote: every patch line repeats an empty message.
	empty := `{"id":"","role":"","content":"","createdAt":"0001-01-01T00:00:00Z"}`
	old := []string{
		`{"type":"tool_output","data":` + empty + `,"callId":"call_1","output":"A terminal shows the failing test."}`,
		`{"type":"delivery","data":` + empty + `,"messageId":"` + assistant.ID() + `","receipt":"delivered"}`,
	}
	f, err := os.OpenFile(filepath.Join(dir, "c1", activeFile), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range old {
		if _, err := f.WriteString(line + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	loaded, err := repo.Load(ctx, "c1")
	if err != nil {
		t.Fatalf("an old file with empty patch envelopes must still load: %v", err)
	}
	msgs := loaded.Messages()
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want the user line and the call", len(msgs))
	}
	got := msgs[1].ToolCalls()[0]
	if !got.HasOutput() || got.Output() != "A terminal shows the failing test." {
		t.Fatalf("patched output = %q, want the stored description", got.Output())
	}
	if msgs[1].Receipt() != valueobject.ReceiptDelivered {
		t.Fatalf("delivery = %q, want the stored receipt", msgs[1].Receipt())
	}
}
