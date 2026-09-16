package application

import (
	"fmt"
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

type announcementAcpStore struct {
	items map[string]*domain.AcpAgent
}

func (s *announcementAcpStore) List() []*domain.AcpAgent {
	out := make([]*domain.AcpAgent, 0, len(s.items))
	for _, agent := range s.items {
		out = append(out, agent)
	}
	return out
}

func (s *announcementAcpStore) Get(id string) (*domain.AcpAgent, error) {
	agent := s.items[id]
	if agent == nil {
		return nil, fmt.Errorf("agent %q not found", id)
	}
	return agent, nil
}

func (s *announcementAcpStore) Save(agent *domain.AcpAgent) error {
	s.items[agent.ID] = agent
	return nil
}

func (s *announcementAcpStore) Delete(id string) error {
	delete(s.items, id)
	return nil
}

func TestAcpConfigAnnouncementIncludesNameAndStatus(t *testing.T) {
	conv := durableAnnouncementConversation()
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		AcpAgents: &announcementAcpStore{items: map[string]*domain.AcpAgent{
			"a1": {ID: "a1", Name: "codex", Command: "codex", Enabled: true},
		}},
		Bus:  NewBus(),
		Logs: &fakeLogStore{},
	}
	enabled := false
	if _, rpcErr := app.handleAcpAgentsSave(contracts.AcpAgentSaveRequest{
		ID: "a1", Name: "codex", Command: "codex", Enabled: &enabled,
	}); rpcErr != nil {
		t.Fatalf("handleAcpAgentsSave: %v", rpcErr)
	}
	if got := conv.PendingAnnouncements[0].Message; got != "subagent codex has disabled. Re-read the affected tool descriptions and instructions." {
		t.Fatalf("announcement = %q", got)
	}
}

func TestMemoryAnnouncementIncludesPrimaryDocumentPath(t *testing.T) {
	conv := durableAnnouncementConversation()
	user := &userUpdateStore{path: "/data/memory/user.md"}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		User:          user,
		Bus:           NewBus(),
		Logs:          &fakeLogStore{},
	}
	if _, rpcErr := app.handleMemoryUserUpdate(contracts.MemoryUserUpdateRequest{Content: "new"}); rpcErr != nil {
		t.Fatalf("handleMemoryUserUpdate: %v", rpcErr)
	}
	if got := conv.PendingAnnouncements[0].Message; got != "user.md has changed, read /data/memory/user.md to see primary memory" {
		t.Fatalf("announcement = %q", got)
	}
}

func TestAgentMemoryAnnouncementUsesSoulPath(t *testing.T) {
	conv := durableAnnouncementConversation()
	agent := &userUpdateStore{path: "/data/memory/soul.md"}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		Agent:         agent,
		Bus:           NewBus(),
		Logs:          &fakeLogStore{},
	}
	if _, rpcErr := app.handleMemoryAgentUpdate(contracts.MemoryAgentUpdateRequest{Content: "new conventions"}); rpcErr != nil {
		t.Fatalf("handleMemoryAgentUpdate: %v", rpcErr)
	}
	if got := conv.PendingAnnouncements[0].Message; got != "soul.md has changed, read /data/memory/soul.md to see primary memory" {
		t.Fatalf("announcement = %q", got)
	}
}

func TestSkillAnnouncementIncludesName(t *testing.T) {
	conv := durableAnnouncementConversation()
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		Skills:        &fakeSkillStore{items: map[string]*domain.Skill{}},
		Bus:           NewBus(),
		Logs:          &fakeLogStore{},
	}
	if _, rpcErr := app.handleSkillsSave(contracts.SkillSaveRequest{Name: "tool-mapping", Content: "# Tool mapping"}); rpcErr != nil {
		t.Fatalf("handleSkillsSave: %v", rpcErr)
	}
	if got := conv.PendingAnnouncements[0].Message; got != "skill tool-mapping has changed, re-read if you are using this skill" {
		t.Fatalf("announcement = %q", got)
	}
}
