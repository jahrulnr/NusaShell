package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	workspacerepo "github.com/jahrulnr/nusashell/infrastructure/repository/workspace"
	"github.com/jahrulnr/nusashell/infrastructure/service/agent/acp"
)

// quietSession is a session whose agent advertises no prompt capabilities
// and owes it nothing ahead of the first input.
func quietSession() *session {
	return &session{proc: &proc{client: &acp.Client{}}, folders: []string{"/work"}}
}

func blocksOf(t *testing.T, in dservice.AgentPrompt, s *session) []acp.ContentBlock {
	t.Helper()
	// A real workspace store, the way the composition wires it: a link's pin is
	// verified through the same door that serves the file.
	rt := New(Options{ClientName: "nusashell-test", ClientVersion: "0", Workspace: workspacerepo.NewLocal()})
	t.Cleanup(func() { _ = rt.Shutdown(context.Background()) })
	blocks, _ := rt.promptBlocks(context.Background(), in, s)
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

// A reference handed to an agent is read by the agent, so a pin that no longer
// matches must stop the link: the file is refused with a note, never offered as
// a path the agent would read as a different picture.
func TestAttachmentLinkVerifiesThePinFirst(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(path, []byte("original bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	pinned, err := valueobject.NewAttachment("file://"+path, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	pinned.SetName("shot.png")
	sum := sha256.Sum256([]byte("original bytes"))
	pinned.SetSHA256(hex.EncodeToString(sum[:]))

	msg, _ := entity.NewMessage(enum.RoleUser, "look at this")
	msg.AddAttachment(pinned)
	conv, _ := aggregate.NewConversation("")
	// A pinned file outside the room's workspace still crosses: the pin is what
	// vouches for it, and it is read through the host door.
	room := t.TempDir()
	prompt := dservice.AgentPrompt{Conversation: conv, Input: msg, Workspace: room}
	blocks := blocksOf(t, prompt, quietSession())
	// The prompt text comes first; the attachment follows as its link.
	if len(blocks) != 2 || blocks[1].Type != "resource_link" || blocks[1].URI != "file://"+path {
		t.Fatalf("intact pinned file blocks = %+v, want the link", blocks)
	}

	// Rewritten since the pin: no link, a note naming the file and the reason.
	if err := os.WriteFile(path, []byte("different bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	blocks = blocksOf(t, prompt, quietSession())
	for _, b := range blocks {
		if b.Type == "resource_link" {
			t.Fatalf("a changed file was handed over as a link: %+v", b)
		}
	}
	note := blocks[len(blocks)-1]
	if note.Type != "text" || !strings.Contains(note.Text, `"shot.png"`) || !strings.Contains(note.Text, "content changed since it was sent") {
		t.Fatalf("note = %+v, want the omission named", blocks)
	}

	// Gone: also refused, with its own reason.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	blocks = blocksOf(t, prompt, quietSession())
	for _, b := range blocks {
		if b.Type == "resource_link" {
			t.Fatalf("a vanished file was handed over as a link: %+v", b)
		}
	}
	if note := blocks[len(blocks)-1]; !strings.Contains(note.Text, "unreadable now") {
		t.Fatalf("vanished file blocks = %+v, want its own reason", blocks)
	}
}

// The link the agent receives obeys the serving rule: a symlink inside the
// workspace that points outside is refused here too, even when the pin matches
// the target's bytes — that hash proves nothing about what would have been
// served.
func TestAttachmentLinkRefusesASymlinkOutOfTheWorkspace(t *testing.T) {
	room := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.png")
	if err := os.WriteFile(outside, []byte("outside bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(room, "shot.png")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	escaped, err := valueobject.NewAttachment("file://"+link, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	escaped.SetName("shot.png")
	sum := sha256.Sum256([]byte("outside bytes"))
	escaped.SetSHA256(hex.EncodeToString(sum[:]))

	msg, _ := entity.NewMessage(enum.RoleUser, "look at this")
	msg.AddAttachment(escaped)
	conv, _ := aggregate.NewConversation("")
	blocks := blocksOf(t, dservice.AgentPrompt{Conversation: conv, Input: msg, Workspace: room}, quietSession())
	for _, b := range blocks {
		if b.Type == "resource_link" {
			t.Fatalf("the agent was handed a link the server refuses to serve: %+v", b)
		}
	}
	note := blocks[len(blocks)-1]
	if note.Type != "text" || !strings.Contains(note.Text, "omitted") {
		t.Fatalf("blocks = %+v, want the attachment omitted with a note", blocks)
	}
}

// Two references must keep passing through untouched: a remote URI the provider
// fetches itself, and a local file with no pin (nothing claims a hash to
// verify). The pin check must not turn either into an omission — and the
// unpinned path here does not exist, so a link proves nothing was read.
func TestAttachmentLinkLeavesUnpinnedAndRemoteReferencesAlone(t *testing.T) {
	remote, err := valueobject.NewAttachment("https://example.com/pic.png", "image/png")
	if err != nil {
		t.Fatal(err)
	}
	remote.SetName("pic.png")
	unpinned, err := valueobject.NewAttachment("file://"+filepath.Join(t.TempDir(), "never-there.png"), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	unpinned.SetName("never-there.png")

	msg, _ := entity.NewMessage(enum.RoleUser, "see these")
	msg.AddAttachment(remote)
	msg.AddAttachment(unpinned)
	conv, _ := aggregate.NewConversation("")
	blocks := blocksOf(t, dservice.AgentPrompt{Conversation: conv, Input: msg}, quietSession())

	var links []string
	for _, b := range blocks {
		if b.Type == "resource_link" {
			links = append(links, b.URI)
			continue
		}
		if strings.Contains(b.Text, "omitted") {
			t.Fatalf("a reference was omitted that had nothing to verify: %+v", b)
		}
	}
	if len(links) != 2 || links[0] != remote.URI() || links[1] != unpinned.URI() {
		t.Fatalf("links = %v, want the remote URI and the unpinned path unchanged", links)
	}
}
