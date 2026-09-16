package agent

import (
	"errors"
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

func peerWakeService(t *testing.T, conversation *domain.Conversation) (*Service, *lifecycleConvStore, func() bool) {
	t.Helper()
	store := &lifecycleConvStore{byID: map[string]*domain.Conversation{conversation.ID: conversation}}
	provider := &domain.Provider{ID: "provider", Kind: domain.ProviderChat, Enabled: true}
	var launched func()
	service := New(Deps{
		Conversations: store,
		ResolveModel: func(model string) (*domain.Provider, string, string, *contracts.RPCError) {
			if model != "model" {
				return nil, "", "", &contracts.RPCError{Code: contracts.CodeValidation, Message: "unexpected model"}
			}
			return provider, model, "key", nil
		},
		Go: func(_ string, fn func()) { launched = fn },
	})
	return service, store, func() bool { return launched != nil }
}

func TestDeliverPeerMessageWakesIdleConversation(t *testing.T) {
	conv := &domain.Conversation{
		ID: "conv_target", Model: "model", Status: "idle",
		Messages: []domain.Message{{ID: "u1", Role: domain.RoleUser, Content: "start", Status: domain.StatusDone}},
	}
	service, store, launched := peerWakeService(t, conv)

	if err := service.DeliverPeerMessage("conv_target", "conv_source", "Please review the result."); err != nil {
		t.Fatalf("DeliverPeerMessage: %v", err)
	}

	saved, err := store.Get("conv_target")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "running" {
		t.Fatalf("status = %q, want running", saved.Status)
	}
	if len(saved.PendingAnnouncements) != 0 {
		t.Fatalf("pending announcements = %+v, want drained before wake", saved.PendingAnnouncements)
	}
	if len(saved.Messages) != 3 {
		t.Fatalf("messages = %d, want user + announcement + assistant placeholder", len(saved.Messages))
	}
	if saved.Messages[1].Role != domain.RoleAssistant || len(saved.Messages[1].ToolCalls) != 1 {
		t.Fatalf("message[1] = %+v, want announcement assistant message", saved.Messages[1])
	}
	if saved.Messages[1].ToolCalls[0].Name != domain.AnnouncementToolName || saved.Messages[1].ToolCalls[0].Output == "" {
		t.Fatalf("announcement tool call = %+v", saved.Messages[1].ToolCalls[0])
	}
	if saved.Messages[2].Role != domain.RoleAssistant {
		t.Fatalf("message[2] role = %q, want assistant placeholder", saved.Messages[2].Role)
	}
	if !launched() {
		t.Fatal("idle peer message must launch an agent turn")
	}
	if service.ActiveRunForConversation("conv_target") == nil {
		t.Fatal("idle peer message must register an active run")
	}
}

func TestDeliverPeerMessageQueuesWhileConversationIsActive(t *testing.T) {
	conv := &domain.Conversation{
		ID: "conv_target", Model: "model", Status: "running",
		Messages: []domain.Message{{ID: "u1", Role: domain.RoleUser, Content: "start", Status: domain.StatusDone}},
	}
	service, store, launched := peerWakeService(t, conv)
	service.runsMu.Lock()
	service.runs["run-existing"] = &TurnRun{ID: "run-existing", ConversationID: "conv_target"}
	service.runsMu.Unlock()

	if err := service.DeliverPeerMessage("conv_target", "conv_source", "Wait for the next boundary."); err != nil {
		t.Fatalf("DeliverPeerMessage: %v", err)
	}

	saved, err := store.Get("conv_target")
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.PendingAnnouncements) != 1 || saved.PendingAnnouncements[0].Type != "peer_message" {
		t.Fatalf("pending announcements = %+v, want one peer_message", saved.PendingAnnouncements)
	}
	if launched() {
		t.Fatal("active peer message must not launch a second run")
	}
}

func TestDeliverPeerMessageReportsQueuePersistenceFailure(t *testing.T) {
	store := &lifecycleConvStore{
		byID: map[string]*domain.Conversation{
			"conv_target": {
				ID: "conv_target", Status: "idle",
				Messages: []domain.Message{{ID: "u1", Role: domain.RoleUser, Content: "seed", Status: domain.StatusDone}},
			},
		},
		err: errors.New("disk full"),
	}
	service := New(Deps{Conversations: store})

	if err := service.DeliverPeerMessage("conv_target", "conv_source", "must be durable"); err == nil {
		t.Fatal("DeliverPeerMessage must report queue persistence failure")
	}
}

func TestDeliverPeerMessageRejectsEmptyConversation(t *testing.T) {
	conv := &domain.Conversation{ID: "conv_empty", Status: "idle"}
	service, store, launched := peerWakeService(t, conv)

	if err := service.DeliverPeerMessage("conv_empty", "conv_source", "do not persist me"); err == nil {
		t.Fatal("peer delivery to an empty draft must be rejected")
	}
	saved, err := store.Get("conv_empty")
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.PendingAnnouncements) != 0 || len(saved.Messages) != 0 {
		t.Fatalf("empty conversation mutated: %+v", saved)
	}
	if launched() {
		t.Fatal("empty conversation must not wake an agent run")
	}
}

func TestWakePendingPeerMessageAfterRunEnds(t *testing.T) {
	conv := &domain.Conversation{
		ID: "conv_target", Model: "model", Status: "running",
		Messages: []domain.Message{{ID: "u1", Role: domain.RoleUser, Content: "start", Status: domain.StatusDone}},
	}
	service, store, launched := peerWakeService(t, conv)
	service.runsMu.Lock()
	service.runs["run-existing"] = &TurnRun{ID: "run-existing", ConversationID: "conv_target"}
	service.runsMu.Unlock()

	if err := service.DeliverPeerMessage("conv_target", "conv_source", "Arrived at the terminal boundary."); err != nil {
		t.Fatalf("DeliverPeerMessage: %v", err)
	}
	service.runsMu.Lock()
	delete(service.runs, "run-existing")
	service.runsMu.Unlock()
	service.WakePendingPeerMessage("conv_target")

	saved, err := store.Get("conv_target")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "running" || len(saved.PendingAnnouncements) != 0 {
		t.Fatalf("conversation after wake = status=%q pending=%+v", saved.Status, saved.PendingAnnouncements)
	}
	if !launched() {
		t.Fatal("pending peer message must wake after the previous run ends")
	}
}

func TestResolveConversationProviderPrefersQualifiedConversationModel(t *testing.T) {
	codex := &domain.Provider{ID: "codex", Name: "Codex", Kind: domain.ProviderCodex, Enabled: true}
	openAI := &domain.Provider{ID: "openai", Name: "OpenAI", Kind: domain.ProviderResponses, Enabled: true}
	service := New(Deps{
		ResolveModel: func(model string) (*domain.Provider, string, string, *contracts.RPCError) {
			switch model {
			case "codex:gpt-5.6-terra":
				return codex, "gpt-5.6-terra", "codex-token", nil
			case "gpt-5.6-terra":
				return openAI, "gpt-5.6-terra", "openai-token", nil
			default:
				return nil, "", "", &contracts.RPCError{Code: contracts.CodeValidation, Message: "unknown model"}
			}
		},
	})

	conversation := &domain.Conversation{
		ID:    "conv-codex",
		Model: "codex:gpt-5.6-terra",
		Messages: []domain.Message{
			{Role: domain.RoleAssistant, Model: "gpt-5.6-terra", Status: domain.StatusDone},
		},
	}

	provider, model, key, _, err := service.ResolveConversationProvider(conversation)
	if err != nil {
		t.Fatalf("ResolveConversationProvider: %v", err)
	}
	if provider != codex {
		t.Fatalf("provider = %v, want Codex", provider)
	}
	if model != "gpt-5.6-terra" {
		t.Fatalf("model = %q, want bare Codex model", model)
	}
	if key != "codex-token" {
		t.Fatalf("key = %q, want Codex credential", key)
	}
}
