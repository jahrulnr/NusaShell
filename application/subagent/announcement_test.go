package subagent

import (
	"fmt"
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

type announcementAgentStore struct {
	items map[string]*domain.AcpAgent
}

func (s *announcementAgentStore) List() []*domain.AcpAgent {
	out := make([]*domain.AcpAgent, 0, len(s.items))
	for _, agent := range s.items {
		out = append(out, agent)
	}
	return out
}

func (s *announcementAgentStore) Get(id string) (*domain.AcpAgent, error) {
	agent := s.items[id]
	if agent == nil {
		return nil, fmt.Errorf("agent %q not found", id)
	}
	return agent, nil
}

func (s *announcementAgentStore) Save(agent *domain.AcpAgent) error {
	s.items[agent.ID] = agent
	return nil
}

func (s *announcementAgentStore) Delete(id string) error {
	delete(s.items, id)
	return nil
}

func TestHandleAgentsSaveReportsConcreteEnabledState(t *testing.T) {
	cases := []struct {
		name       string
		initial    *domain.AcpAgent
		enabled    bool
		wantAction string
	}{
		{name: "codex", initial: &domain.AcpAgent{ID: "a1", Name: "codex", Command: "codex", Enabled: true}, enabled: false, wantAction: "disabled"},
		{name: "devin", initial: nil, enabled: true, wantAction: "enabled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &announcementAgentStore{items: map[string]*domain.AcpAgent{}}
			if tc.initial != nil {
				store.items[tc.initial.ID] = tc.initial
			}
			var gotName, gotAction string
			svc := New(Deps{
				Agents: store,
				OnAgentsChanged: func(name, action string) {
					gotName, gotAction = name, action
				},
			})
			enabled := tc.enabled
			req := contracts.AcpAgentSaveRequest{ID: "a1", Name: tc.name, Command: tc.name, Enabled: &enabled}
			if tc.initial == nil {
				req.ID = ""
			}
			if _, rpcErr := svc.HandleAgentsSave(req); rpcErr != nil {
				t.Fatalf("HandleAgentsSave: %v", rpcErr)
			}
			if gotName != tc.name || gotAction != tc.wantAction {
				t.Fatalf("change = %q/%q, want %q/%q", gotName, gotAction, tc.name, tc.wantAction)
			}
		})
	}
}

func TestHandleAgentsDeleteReportsAgentName(t *testing.T) {
	store := &announcementAgentStore{items: map[string]*domain.AcpAgent{
		"a1": {ID: "a1", Name: "codex", Command: "codex", Enabled: true},
	}}
	var gotName, gotAction string
	svc := New(Deps{
		Agents: store,
		OnAgentsChanged: func(name, action string) {
			gotName, gotAction = name, action
		},
	})
	if _, rpcErr := svc.HandleAgentsDelete(contracts.AcpAgentIDRequest{ID: "a1"}); rpcErr != nil {
		t.Fatalf("HandleAgentsDelete: %v", rpcErr)
	}
	if gotName != "codex" || gotAction != "deleted" {
		t.Fatalf("change = %q/%q, want codex/deleted", gotName, gotAction)
	}
}
