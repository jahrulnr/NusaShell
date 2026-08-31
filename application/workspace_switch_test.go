package application

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

// agentsMDToolbox returns a canned AGENTS.md body for file_read, and empty
// output for every other tool so hydration slots stay hidden in these tests.
type agentsMDToolbox struct {
	body string
	err  error
}

func (t *agentsMDToolbox) ListTools() []ToolInfo { return nil }

func (t *agentsMDToolbox) Execute(_ context.Context, name string, argsJSON []byte) (string, error) {
	if name != "file_read" {
		return "", nil
	}
	var args struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(argsJSON, &args)
	if !strings.HasSuffix(args.Path, "AGENTS.md") {
		return "", fmt.Errorf("missing: %s", args.Path)
	}
	if t.err != nil {
		return "", t.err
	}
	return t.body, nil
}

func workspaceSwitchFixture(oldWS string) *domain.Conversation {
	return &domain.Conversation{
		ID:        "conv_1",
		Title:     "Test",
		Workspace: oldWS,
		Messages: []domain.Message{
			{ID: "m1", Role: domain.RoleUser, Content: "hello", Status: domain.StatusDone},
			{ID: "m2", Role: domain.RoleAssistant, Content: "option C", Status: domain.StatusDone},
		},
	}
}

func pickWorkspace(t *testing.T, app *App, id string) *domain.Conversation {
	t.Helper()
	if _, rpcErr := app.handleConversationsPickWorkspace(contracts.ConversationIDRequest{ID: id}); rpcErr != nil {
		t.Fatalf("pick workspace: %v", rpcErr)
	}
	saved, err := app.Conversations.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func TestHandleConversationsPickWorkspaceQueuesNoticeWithoutInserting(t *testing.T) {
	oldWS := "/media/jahrulnr/storage/workspace/NusaShell-mcp"
	conv := workspaceSwitchFixture(oldWS)
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"conv_1": conv}}
	newWS := t.TempDir()
	app := &App{
		Conversations: store,
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Toolbox:       &agentsMDToolbox{body: "---\nbytes: 12\n---\n\n# Rules\n"},
		WorkspacePicker: WorkspacePickerFunc(func(context.Context) (string, error) {
			return newWS, nil
		}),
	}

	saved := pickWorkspace(t, app, "conv_1")
	if saved.Workspace != newWS {
		t.Fatalf("workspace = %q, want %q", saved.Workspace, newWS)
	}
	if !saved.PendingWorkspaceAnnouncement {
		t.Fatal("pick mid-conversation must queue a workspace-switch notice for the next user turn")
	}
	if saved.WorkspaceSwitchFrom != oldWS {
		t.Fatalf("WorkspaceSwitchFrom = %q, want %q", saved.WorkspaceSwitchFrom, oldWS)
	}
	for _, m := range saved.Messages {
		for _, tc := range m.ToolCalls {
			if tc.Name == domain.AnnouncementToolName {
				t.Fatalf("notice must wait for the next user message, got %+v", tc)
			}
			if tc.Name == "file_read" && !domain.IsHydrationCallID(tc.ID) {
				t.Fatalf("visible AGENTS.md file_read must wait for the next user message, got %+v", tc)
			}
		}
	}
}

func TestHandleConversationsPickWorkspaceEmptyRoomDoesNotQueueNotice(t *testing.T) {
	store := &fakeConvStore{convs: map[string]*domain.Conversation{
		"conv_1": {ID: "conv_1", Title: "Empty"},
	}}
	app := &App{
		Conversations: store,
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		WorkspacePicker: WorkspacePickerFunc(func(context.Context) (string, error) {
			return t.TempDir(), nil
		}),
	}
	saved := pickWorkspace(t, app, "conv_1")
	if saved.PendingWorkspaceAnnouncement {
		t.Fatal("empty room must not queue a workspace-switch notice")
	}
}

func TestHandleConversationsPickWorkspaceSamePathDoesNotQueueNotice(t *testing.T) {
	ws := t.TempDir()
	conv := workspaceSwitchFixture(ws)
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"conv_1": conv}}
	app := &App{
		Conversations: store,
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		WorkspacePicker: WorkspacePickerFunc(func(context.Context) (string, error) {
			return ws, nil
		}),
	}
	saved := pickWorkspace(t, app, "conv_1")
	if saved.PendingWorkspaceAnnouncement {
		t.Fatal("re-picking the same workspace must not queue a notice")
	}
}

// TestAddTurnMessagesInjectsWorkspaceSwitchNotice pins the user-reported
// flow: assistant → pick workspace → user sends → visible announcement +
// AGENTS.md file_read sit immediately after that user, then the assistant
// turn. Same injection point as restart announcements.
func TestAddTurnMessagesInjectsWorkspaceSwitchNotice(t *testing.T) {
	oldWS := "/media/jahrulnr/storage/workspace/NusaShell-mcp"
	newWS := t.TempDir()
	agentsPath := filepath.Join(newWS, "AGENTS.md")
	agentsBody := "---\nbytes: 18\n---\n\n# Host rules\nUse Go.\n"
	conv := workspaceSwitchFixture(oldWS)
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"conv_1": conv}}
	app := &App{
		Conversations: store,
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Toolbox:       &agentsMDToolbox{body: agentsBody},
		WorkspacePicker: WorkspacePickerFunc(func(context.Context) (string, error) {
			return newWS, nil
		}),
	}
	saved := pickWorkspace(t, app, "conv_1")

	app.addTurnMessages(saved,
		domain.Message{ID: "m_user", Role: domain.RoleUser, Content: "Sip, gas implement C", Status: domain.StatusDone},
		domain.Message{ID: "m_asst", Role: domain.RoleAssistant},
	)

	if saved.PendingWorkspaceAnnouncement {
		t.Fatal("pending flag must be one-shot")
	}
	if saved.WorkspaceSwitchFrom != "" {
		t.Fatalf("WorkspaceSwitchFrom must clear after inject, got %q", saved.WorkspaceSwitchFrom)
	}

	// fixture 2 + user + notice + assistant
	if len(saved.Messages) < 4 {
		t.Fatalf("messages = %d, want at least 4", len(saved.Messages))
	}
	userIdx := -1
	for i, m := range saved.Messages {
		if m.ID == "m_user" {
			userIdx = i
			break
		}
	}
	if userIdx < 0 {
		t.Fatal("user message missing")
	}
	if saved.Messages[userIdx].Content != "Sip, gas implement C" {
		t.Fatalf("user content = %q", saved.Messages[userIdx].Content)
	}
	notice := saved.Messages[userIdx+1]
	if isHydrationMessage(notice) {
		t.Fatal("workspace-switch notice must be visible (not a hidden hydration checkpoint)")
	}
	if notice.Role != domain.RoleAssistant {
		t.Fatalf("notice role = %s, want assistant", notice.Role)
	}
	if dto := msgDTO(notice); dto.ID == "" || len(dto.ToolCalls) == 0 {
		t.Fatal("notice must survive msgDTO so the UI and JSON dump show it")
	}
	if saved.Messages[userIdx+2].ID != "m_asst" {
		t.Fatalf("assistant turn must follow the notice, got %+v", saved.Messages[userIdx+2])
	}

	var ann, agents *domain.ToolCall
	for i := range notice.ToolCalls {
		tc := &notice.ToolCalls[i]
		switch {
		case tc.Name == domain.AnnouncementToolName:
			ann = tc
		case tc.Name == "file_read":
			agents = tc
		}
	}
	if ann == nil {
		t.Fatal("missing announcement tool")
	}
	if !domain.IsAnnouncementCallID(ann.ID) {
		t.Fatalf("announcement id %q must use announce- prefix", ann.ID)
	}
	if ann.Status != domain.ToolOK {
		t.Fatalf("announcement status = %s", ann.Status)
	}
	for _, want := range []string{`"type":"workspace_changed"`, oldWS, newWS} {
		if !strings.Contains(ann.Args, want) {
			t.Fatalf("announcement args %s must contain %s", ann.Args, want)
		}
	}
	if !strings.Contains(ann.Output, oldWS) || !strings.Contains(ann.Output, newWS) {
		t.Fatalf("announcement output must name both paths: %q", ann.Output)
	}
	if agents == nil {
		t.Fatal("missing AGENTS.md file_read")
	}
	if domain.IsHydrationCallID(agents.ID) {
		t.Fatal("AGENTS.md file_read must not use the hidden hydrate- prefix")
	}
	var fa struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(agents.Args), &fa); err != nil || fa.Path != agentsPath {
		t.Fatalf("file_read args = %s, want path %q", agents.Args, agentsPath)
	}
	if agents.Output != agentsBody {
		t.Fatalf("file_read output = %q, want real AGENTS.md body", agents.Output)
	}

	app.addTurnMessages(saved,
		domain.Message{ID: "m_user2", Role: domain.RoleUser, Content: "lanjut", Status: domain.StatusDone},
		domain.Message{ID: "m_asst2", Role: domain.RoleAssistant},
	)
	for _, m := range saved.Messages[userIdx+3:] {
		for _, tc := range m.ToolCalls {
			if tc.Name == domain.AnnouncementToolName {
				var parsed struct {
					Type string `json:"type"`
				}
				_ = json.Unmarshal([]byte(tc.Args), &parsed)
				if parsed.Type == "workspace_changed" {
					t.Fatal("second turn must not repeat the workspace-switch notice")
				}
			}
		}
	}
}

func TestAddTurnMessagesWorkspaceSwitchOmitsMissingAgentsMD(t *testing.T) {
	conv := workspaceSwitchFixture("/old/ws")
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"conv_1": conv}}
	app := &App{
		Conversations: store,
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Toolbox:       &agentsMDToolbox{err: fmt.Errorf("open AGENTS.md: no such file")},
		WorkspacePicker: WorkspacePickerFunc(func(context.Context) (string, error) {
			return t.TempDir(), nil
		}),
	}
	saved := pickWorkspace(t, app, "conv_1")
	app.addTurnMessages(saved,
		domain.Message{ID: "m_user", Role: domain.RoleUser, Content: "go", Status: domain.StatusDone},
		domain.Message{ID: "m_asst", Role: domain.RoleAssistant},
	)
	var notice domain.Message
	for _, m := range saved.Messages {
		if m.ID == "m_user" {
			continue
		}
		if m.ID == "m_asst" {
			break
		}
		if len(m.ToolCalls) > 0 {
			notice = m
		}
	}
	if len(notice.ToolCalls) != 1 || notice.ToolCalls[0].Name != domain.AnnouncementToolName {
		t.Fatalf("missing AGENTS.md must leave announcement only, got %+v", notice.ToolCalls)
	}
}
