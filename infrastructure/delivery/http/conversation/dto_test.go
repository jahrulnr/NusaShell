package conversation

import (
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
