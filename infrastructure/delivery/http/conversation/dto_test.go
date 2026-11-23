package conversation

import (
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

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
