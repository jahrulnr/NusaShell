package aggregate

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

func mustConv(t *testing.T) *Conversation {
	t.Helper()
	c, err := NewConversation("conv-1")
	if err != nil {
		t.Fatalf("NewConversation: %v", err)
	}
	return c
}

func TestNewConversation(t *testing.T) {
	if _, err := NewConversation("../bad"); err == nil {
		t.Fatal("expected error for path-element id")
	}
	minted, err := NewConversation("") // empty mints a fresh id
	if err != nil || minted.ID() == "" {
		t.Fatalf("minted id: %q err=%v", minted.ID(), err)
	}
	c := mustConv(t)
	if c.ID() != "conv-1" || c.Last() != nil {
		t.Fatalf("unexpected conversation: %+v", c)
	}
}

func TestConversationEnvironmentBinding(t *testing.T) {
	c := mustConv(t)
	if c.Environment() != enum.EnvironmentLocal {
		t.Fatalf("new room environment = %q, want local", c.Environment())
	}
	if err := c.SetEnvironment(enum.EnvironmentDocker); err != nil {
		t.Fatal(err)
	}
	if c.Environment() != enum.EnvironmentDocker {
		t.Fatalf("environment = %q, want docker", c.Environment())
	}
	if err := c.SetEnvironment(enum.Environment("unregistered")); err == nil {
		t.Fatal("unknown environment must not replace the binding")
	}
	if c.Environment() != enum.EnvironmentDocker {
		t.Fatal("invalid environment changed the binding")
	}
}

func TestConversationAppendLast(t *testing.T) {
	c := mustConv(t)
	m, _ := entity.NewMessage(enum.RoleUser, "hi")
	c.Append(m)
	if c.Last() == nil || c.Last().Content() != "hi" {
		t.Fatal("Last did not return appended message")
	}
}

func TestConversationEstimatedTokens(t *testing.T) {
	c := mustConv(t)
	c.SetSystemPrompt(strings.Repeat("s", 400)) // ~100 tokens

	m, _ := entity.NewMessage(enum.RoleUser, strings.Repeat("x", 400))
	m.SetOptionalContent("gemini.thought_signature", strings.Repeat("s", 40))
	a, _ := valueobject.NewAttachment("https://x/i.png", "image/png")
	m.AddAttachment(a)
	c.Append(m)

	// Tool-produced media bills like a message attachment — a media-heavy
	// history must still trip the compaction budget.
	am, _ := entity.NewMessage(enum.RoleAssistant, "")
	tc, _ := entity.NewToolCall("c1", "read_media", strings.Repeat("i", 40), enum.TooltypeOrigin)
	tc.SetStoredOutput(strings.Repeat("o", 40), "")
	tc.SetOutputAttachments([]valueobject.Attachment{a})
	am.AddToolCall(tc)
	c.Append(am)

	// Provider context includes the untrusted evidence envelope; media
	// contributes a flat estimate independent of text length.
	want := (400+400+40+40+len(tc.OutputWithUntrustedTag()))/4 + 2*attachmentTokenEstimate
	if got := c.EstimatedTokens(); got != want {
		t.Fatalf("EstimatedTokens = %d, want %d", got, want)
	}
}

func TestConversationMediaCount(t *testing.T) {
	c := mustConv(t)
	img, _ := valueobject.NewAttachment("file://shot.png", "image/png")
	audio, _ := valueobject.NewAttachment("file://clip.mp3", "audio/mpeg")

	user, _ := entity.NewMessage(enum.RoleUser, "look")
	user.AddAttachment(img)
	user.AddAttachment(audio)
	c.Append(user)

	am, _ := entity.NewMessage(enum.RoleAssistant, "")
	tc, _ := entity.NewToolCall("c1", "read_media", `{}`, enum.TooltypeOrigin)
	tc.SetStoredOutput("loaded", "")
	tc.SetOutputAttachments([]valueobject.Attachment{img, img})
	am.AddToolCall(tc)
	c.Append(am)

	if got := c.MediaCount(enum.ModalityImage); got != 3 {
		t.Fatalf("image count = %d, want 3", got)
	}
	if got := c.MediaCount(enum.ModalityAudio); got != 1 {
		t.Fatalf("audio count = %d, want 1", got)
	}
	if got := c.MediaCount(enum.ModalityVideo); got != 0 {
		t.Fatalf("video count = %d, want 0", got)
	}
}

func TestConversationNeedCompaction(t *testing.T) {
	c := mustConv(t)
	m, _ := entity.NewMessage(enum.RoleUser, strings.Repeat("x", 400))
	c.Append(m) // 100 tokens

	if c.NeedCompaction(0) {
		t.Fatal("zero budget must fail open")
	}
	if !c.NeedCompaction(100) {
		t.Fatal("estimate at budget must trigger")
	}
	if c.NeedCompaction(101) {
		t.Fatal("estimate below budget must not trigger")
	}
}

func TestConversationCompact(t *testing.T) {
	c := mustConv(t)
	for _, txt := range []string{"m1", "m2", "m3", "m4"} {
		m, _ := entity.NewMessage(enum.RoleUser, txt)
		c.Append(m)
	}
	sum, _ := entity.NewMessage(enum.RoleUser, "SUMMARY")
	sum.SetUserType(enum.LLMUserTypeSystem)
	c.Compact(sum, 2, c.Messages()[2:])

	msgs := c.Messages()
	if len(msgs) != 3 || msgs[0].Content() != "SUMMARY" ||
		msgs[1].Content() != "m3" || msgs[2].Content() != "m4" {
		t.Fatalf("compact produced %+v", msgs)
	}

	c.Compact(sum, -5, c.Messages())
	if got := c.Messages(); len(got) != 4 || got[0].Content() != "SUMMARY" {
		t.Fatalf("negative dropBefore must clamp to 0, got %+v", got)
	}
	c.Compact(sum, 99, nil)
	if got := c.Messages(); len(got) != 1 || got[0].Content() != "SUMMARY" {
		t.Fatalf("overflow dropBefore must clamp to len, got %+v", got)
	}
}

// dropBefore is normalized and then discarded — the method trusts the
// caller's retained tail blindly, so a mismatch silently produces the
// wrong history while the repository's own invariant ("retained must be
// exactly the live tail (dropBefore onward)") is enforced at write time.
// The aggregate owns the same operation: a tail that does not match
// dropBefore must be derived or refused, never applied as-is.
func TestConversationCompactEnforcesDropBefore(t *testing.T) {
	c := mustConv(t)
	for _, txt := range []string{"m1", "m2", "m3", "m4"} {
		m, _ := entity.NewMessage(enum.RoleUser, txt)
		c.Append(m)
	}
	sum, _ := entity.NewMessage(enum.RoleUser, "SUMMARY")
	sum.SetUserType(enum.LLMUserTypeSystem)
	// Caller claims dropBefore=2 but passes a tail starting at 3 — m3 is
	// dropped with no error anywhere in the domain path.
	c.Compact(sum, 2, c.Messages()[3:])
	if got := c.Messages(); len(got) != 3 || got[1].Content() != "m3" {
		t.Fatalf("dropBefore=2 must retain messages[2:], got %+v", got)
	}
}

func TestManualCompactWhileTailExceedsKeep(t *testing.T) {
	c := mustConv(t)
	for _, role := range []enum.Role{enum.RoleUser, enum.RoleAssistant, enum.RoleUser, enum.RoleAssistant} {
		m, _ := entity.NewMessage(role, "previous turn")
		c.Append(m)
	}
	if !c.CanManualCompact(2) {
		t.Fatal("history longer than the keep-tail should be foldable")
	}
	checkpoint, _ := entity.NewMessage(enum.RoleUser, "checkpoint")
	checkpoint.SetUserType(enum.LLMUserTypeSystem)
	c.Compact(checkpoint, 2, c.Messages()[2:])
	// checkpoint + kept tail is still longer than keepTail, so an explicit
	// fold remains available instead of failing the button.
	if !c.CanManualCompact(2) {
		t.Fatal("checkpoint plus a longer tail should stay foldable")
	}
	if c.CanManualCompact(3) {
		t.Fatal("a tail that fits in the keep window is not foldable")
	}
}

func TestConversationAddUsageAccumulates(t *testing.T) {
	c := mustConv(t)
	u1, _ := valueobject.NewUsage(100, 0, 0, 50)
	u2, _ := valueobject.NewUsage(200, 10, 0, 60)
	c.AddUsage(u1)
	c.AddUsage(u2)
	if c.Usage().Total() != 420 {
		t.Fatalf("accumulated total = %d, want 420", c.Usage().Total())
	}
}

func agentRoom(t *testing.T) *Conversation {
	t.Helper()
	c := mustConv(t)
	if err := c.BindAgent("claude"); err != nil {
		t.Fatal(err)
	}
	c.SetAgentSession("sess-1")
	return c
}

func report(t *testing.T, used int) valueobject.AgentContext {
	t.Helper()
	a, err := valueobject.NewAgentContext(used, 200_000, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAgentContextReportBelongsToTheAgentSession(t *testing.T) {
	c := agentRoom(t)
	c.ReportAgentContext(report(t, 1000))
	c.ReportAgentContext(report(t, 2500))
	if c.AgentContext().Used() != 2500 {
		t.Fatalf("a later report must replace the earlier one, got %d", c.AgentContext().Used())
	}

	// The same session keeps its report; a different one starts clean.
	c.SetAgentSession("sess-1")
	if c.AgentContext().IsZero() {
		t.Fatal("re-recording the same session dropped its report")
	}
	c.SetAgentSession("sess-2")
	if !c.AgentContext().IsZero() {
		t.Fatal("a new session kept the old session's report")
	}

	// Anything that forgets the session forgets what it reported.
	for name, forget := range map[string]func(*Conversation){
		"rewrite":     func(c *Conversation) { c.ForgetAgentSession() },
		"other agent": func(c *Conversation) { _ = c.BindAgent("codex") },
		"unbind":      func(c *Conversation) { c.UnbindAgent() },
	} {
		c := agentRoom(t)
		c.ReportAgentContext(report(t, 1000))
		forget(c)
		if !c.AgentContext().IsZero() {
			t.Errorf("%s kept the report", name)
		}
	}
}

func TestModelRoomIgnoresAnAgentReport(t *testing.T) {
	c := mustConv(t)
	c.ReportAgentContext(report(t, 1000))
	if !c.AgentContext().IsZero() {
		t.Fatal("a room with no agent has no agent session to report on")
	}
}

func BenchmarkConversationEstimatedTokens(b *testing.B) {
	for _, outBytes := range []int{32 << 10, 64 << 10} {
		b.Run(fmt.Sprintf("output=%dKiB", outBytes>>10), func(b *testing.B) {
			c, _ := NewConversation("conv")
			for range 200 {
				m, _ := entity.NewMessage(enum.RoleUser, strings.Repeat("x", 2000))
				tc, _ := entity.NewToolCall("id", "name", strings.Repeat("i", 500), enum.TooltypeOrigin)
				tc.SetOutput(strings.Repeat("o", outBytes))
				m.AddToolCall(tc)
				c.Append(m)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_ = c.EstimatedTokens()
			}
		})
	}
}

func titledRoom(t *testing.T, first string) *Conversation {
	t.Helper()
	c := agentRoom(t)
	m, _ := entity.NewMessage(enum.RoleUser, first)
	c.Append(m)
	c.SetTitle(strings.TrimSpace(first))
	return c
}

// A fold's checkpoint is not an owner message, so it never stands in for the
// first line the room was named after.
func TestTitleIsDefaultIgnoresTheCheckpoint(t *testing.T) {
	c := mustConv(t)
	checkpoint, err := entity.NewMessage(enum.RoleUser, "CHECKPOINT: sealed seam")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.SetUserType(enum.LLMUserTypeCompaction)
	c.Append(checkpoint)
	first, err := entity.NewMessage(enum.RoleUser, "hello there")
	if err != nil {
		t.Fatal(err)
	}
	c.Append(first)
	c.SetTitle("hello there")
	if !c.TitleIsDefault() {
		t.Fatal("the checkpoint must not be read as the owner's first line")
	}
}

func TestTitleIsDefaultUntilSomeoneNamesTheRoom(t *testing.T) {
	c := titledRoom(t, "  fix the login bug \n")
	if !c.TitleIsDefault() {
		t.Fatal("the first message, trimmed, is the default title")
	}
	c.SetTitle("Login bug")
	if c.TitleIsDefault() {
		t.Fatal("a renamed room kept reading as default")
	}
	if mustConv(t).TitleIsDefault() {
		t.Fatal("a room with no message has no default title")
	}
}

func TestTitleNeverCarriesAMessageBody(t *testing.T) {
	// The relay-echo bug: a first message that is a whole prompt became
	// the room's title verbatim, and relayHead copied it onward.
	c := titledRoom(t, "first line\nsecond line\n"+strings.Repeat("x", 200))
	if c.Title() != "first line" {
		t.Fatalf("title kept a message body: %q", c.Title())
	}
	if !c.TitleIsDefault() {
		t.Fatal("a normalized default stopped reading as default")
	}
	c.SetTitle("start\n" + strings.Repeat("word ", 40))
	if title := c.Title(); strings.Contains(title, "\n") || len([]rune(title)) > 80 {
		t.Fatalf("title = %q (%d runes)", title, len([]rune(title)))
	}
}

func TestAgentNamesARoomNobodyNamed(t *testing.T) {
	c := titledRoom(t, "fix the login bug")
	if !c.AdoptAgentTitle("  Login\n  bug  ") || c.Title() != "Login bug" || c.AgentTitle() != "Login bug" {
		t.Fatalf("title = %q, agent title = %q", c.Title(), c.AgentTitle())
	}
	// The agent may rename what it named before.
	if !c.AdoptAgentTitle("Login bug: session cookie") || c.Title() != "Login bug: session cookie" {
		t.Fatalf("an agent could not rename its own title: %q", c.Title())
	}
	if c.AdoptAgentTitle("Login bug: session cookie") {
		t.Fatal("the same title reported a change")
	}
	// The owner's name wins over anything the agent says later.
	c.SetTitle("My name for it")
	if c.AdoptAgentTitle("Agent's idea") || c.Title() != "My name for it" {
		t.Fatalf("an agent renamed what the owner named: %q", c.Title())
	}
}

func TestAgentTitleNeedsAnAgentRoomAndAWord(t *testing.T) {
	model := mustConv(t)
	m, _ := entity.NewMessage(enum.RoleUser, "hello")
	model.Append(m)
	model.SetTitle("hello")
	if model.AdoptAgentTitle("Something") {
		t.Fatal("a model room took an agent's title")
	}
	room := titledRoom(t, "hello")
	if room.AdoptAgentTitle(" \n ") || room.Title() != "hello" {
		t.Fatal("a blank title was adopted")
	}
	long := room.AdoptAgentTitle(strings.Repeat("é", 500))
	if !long || len([]rune(room.Title())) != 80 {
		t.Fatalf("a long title was not bounded: %d runes", len([]rune(room.Title())))
	}
}

func TestAgentDefaultsAndAutoStart(t *testing.T) {
	a, err := NewAgent("claude", "Claude", "claude-agent-acp", nil, enum.ApprovalPolicyAsk)
	if err != nil {
		t.Fatal(err)
	}
	if a.AutoStart() || a.Defaults() != nil {
		t.Fatal("a new agent starts on demand with no defaults")
	}
	a.SetAutoStart(true)
	if err := a.SetDefaults(valueobject.ConfigSelections{{ID: "mode", Value: "plan"}, {ID: "thought_level", Value: "high"}}); err != nil {
		t.Fatal(err)
	}
	if v, _ := a.Defaults().Value("mode"); !a.AutoStart() || v != "plan" {
		t.Fatalf("defaults = %v", a.Defaults())
	}
	// The aggregate hands out copies.
	a.Defaults()[0].Value = "tampered"
	if v, _ := a.Defaults().Value("mode"); v != "plan" {
		t.Fatal("defaults were shared with the caller")
	}
	for name, bad := range map[string]valueobject.ConfigSelections{
		"blank id":    {{ID: " ", Value: "x"}},
		"empty value": {{ID: "mode", Value: ""}},
		"long id":     {{ID: strings.Repeat("i", 129), Value: "x"}},
	} {
		if err := a.SetDefaults(bad); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// Stored picks are ordered: a room pick for an id already recorded keeps its
// place, a new id appends, and clearing removes only that entry.
func TestSetAgentOptionKeepsApplyOrder(t *testing.T) {
	c, err := NewConversation("")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.BindAgent("a_x"); err != nil {
		t.Fatal(err)
	}
	for _, p := range [][2]string{{"mode", "plan"}, {"effort", "high"}, {"strict", "true"}, {"mode", "build"}} {
		if err := c.SetAgentOption(p[0], p[1]); err != nil {
			t.Fatal(err)
		}
	}
	got := c.AgentOptions()
	want := valueobject.ConfigSelections{
		{ID: "mode", Value: "build"}, {ID: "effort", Value: "high"}, {ID: "strict", Value: "true"},
	}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("picks = %v, want %v", got, want)
	}
	if err := c.SetAgentOption("effort", ""); err != nil {
		t.Fatal(err)
	}
	if got := c.AgentOptions(); len(got) != 2 || got[0] != want[0] || got[1] != want[2] {
		t.Fatalf("after clear picks = %v", got)
	}
}
