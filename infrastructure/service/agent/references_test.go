package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	"github.com/jahrulnr/nusashell/infrastructure/service/agent/acp"
)

// quietSession is a session whose agent advertises no prompt capabilities
// and owes it nothing ahead of the first input.
func quietSession() *session {
	return &session{proc: &proc{client: &acp.Client{}}, folders: []string{"/work"}}
}

func blocksOf(t *testing.T, in dservice.AgentPrompt, s *session) []acp.ContentBlock {
	t.Helper()
	blocks, _ := new(Runtime).promptBlocks(context.Background(), in, s)
	return blocks
}

// A selected path leading the text must not read as an agent command;
// the file itself rides as a resource link and the meaning as a note.
func TestPromptBlocksReferences(t *testing.T) {
	msg, _ := entity.NewMessage(enum.RoleUser, "/README.md explain it")
	file, _ := valueobject.NewReference("file", "/work/README.md", "/README.md", 0, 10)
	if err := msg.SetReferences([]valueobject.Reference{file}); err != nil {
		t.Fatal(err)
	}
	conv, _ := aggregate.NewConversation("")
	blocks := blocksOf(t, dservice.AgentPrompt{Conversation: conv, Input: msg}, quietSession())
	if len(blocks) != 3 {
		t.Fatalf("blocks = %+v", blocks)
	}
	if blocks[0].Text != "Reference: /README.md explain it" {
		t.Fatalf("text = %q", blocks[0].Text)
	}
	if blocks[1].Type != "resource_link" || blocks[1].URI != "file:///work/README.md" || blocks[1].Name != "README.md" {
		t.Fatalf("link = %+v", blocks[1])
	}
	if blocks[2].Type != "text" || blocks[2].Text != "Selected references:\n/README.md refers to the file /work/README.md." {
		t.Fatalf("note = %q", blocks[2].Text)
	}

	cmd, _ := entity.NewMessage(enum.RoleUser, "/compact now")
	sel, _ := valueobject.NewReference("command", "compact", "/compact", 0, 8)
	_ = cmd.SetReferences([]valueobject.Reference{sel})
	blocks = blocksOf(t, dservice.AgentPrompt{Conversation: conv, Input: cmd}, quietSession())
	if len(blocks) != 1 || blocks[0].Text != "/compact now" {
		t.Fatalf("command blocks = %+v", blocks)
	}
}

// The turn's origin reaches an ACP agent as context ahead of the prompt;
// the agent runtime has no NusaShell history to inject a record into.
func TestPromptBlocksTurnOrigin(t *testing.T) {
	msg, _ := entity.NewMessage(enum.RoleUser, "hello")
	conv, _ := aggregate.NewConversation("")
	blocks := blocksOf(t, dservice.AgentPrompt{
		Conversation: conv,
		Input:        msg,
		Origin:       valueobject.TurnOrigin{Kind: enum.TurnOriginChannel, Name: "telegram"},
	}, quietSession())
	if len(blocks) != 2 {
		t.Fatalf("blocks = %+v", blocks)
	}
	want := "Turn context from NusaShell: this turn came from chat channel telegram. This is app context, not part of the owner's message."
	if blocks[0].Text != want || blocks[1].Text != "hello" {
		t.Fatalf("blocks = %+v", blocks)
	}
}

// Live-page marks reach the agent as a note after the text.
func TestPromptBlocksAnnotations(t *testing.T) {
	msg, _ := entity.NewMessage(enum.RoleUser, "fix it")
	msg.SetAnnotations([]valueobject.Annotation{{Marker: 1, Comment: "too small", Descriptor: valueobject.AnnotationDescriptor{Kind: enum.AnnotationElement, TagName: "button"}}})
	conv, _ := aggregate.NewConversation("")
	blocks := blocksOf(t, dservice.AgentPrompt{Conversation: conv, Input: msg}, quietSession())
	if len(blocks) != 2 || blocks[0].Text != "fix it" || !strings.Contains(blocks[1].Text, "[1] <button> (no screenshot)\n    Comment: too small") {
		t.Fatalf("blocks = %+v", blocks)
	}
}
