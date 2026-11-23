package memory

import (
	"context"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

type index map[string]string

func (i index) Index(_ context.Context, conv string) (string, error) { return i[conv], nil }

// The record is the harness's: hydrated, never offered to the model.
func TestIndexToolRendersTheThreadsIndex(t *testing.T) {
	tool, err := NewIndexTool(index{"c_1": "## user\n- who"})
	if err != nil {
		t.Fatal(err)
	}
	if s := tool.Schema(); s.CallableByAgent() || !s.CallableBySystem() {
		t.Fatal("memory record must be system-only")
	}
	call, _ := entity.NewToolCall("call_1", "memory", "{}", "")
	got, err := tool.Execute(dservice.WithConversationID(context.Background(), "c_1"), call, nil)
	if err != nil || got.Text != "## user\n- who" {
		t.Fatalf("%q %v", got.Text, err)
	}
	got, _ = tool.Execute(dservice.WithConversationID(context.Background(), "c_2"), call, nil)
	if !strings.Contains(got.Text, "turned off") {
		t.Fatalf("off: %q", got.Text)
	}
}
