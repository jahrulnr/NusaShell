package jsonstore

import "nusashell/domain"

// Sub-store adapters expose the application port method names. The Store
// itself implements ConversationStore (List/Get/Save/Delete).

type Providers struct{ S *Store }

func (p *Providers) List() []*domain.Provider { return p.S.ListProviders() }
func (p *Providers) Get(id string) (*domain.Provider, error) {
	return p.S.GetProvider(id)
}
func (p *Providers) Save(v *domain.Provider) error { return p.S.SaveProvider(v) }
func (p *Providers) Delete(id string) error        { return p.S.DeleteProvider(id) }

type Skills struct{ S *Store }

func (k *Skills) List() []*domain.Skill { return k.S.ListSkills() }
func (k *Skills) Get(id string) (*domain.Skill, error) {
	return k.S.GetSkill(id)
}
func (k *Skills) Save(v *domain.Skill) error { return k.S.SaveSkill(v) }
func (k *Skills) Delete(id string) error     { return k.S.DeleteSkill(id) }

type Memory struct{ S *Store }

func (m *Memory) List() []*domain.MemoryEntry { return m.S.ListMemories() }
func (m *Memory) Save(v *domain.MemoryEntry) error {
	return m.S.SaveMemory(v)
}
func (m *Memory) Delete(id string) error { return m.S.DeleteMemory(id) }

type LearningEdges struct{ S *Store }

func (l *LearningEdges) List() []*domain.LearningEdge { return l.S.ListLearningEdges() }
func (l *LearningEdges) Save(v *domain.LearningEdge) error {
	return l.S.SaveLearningEdge(v)
}
func (l *LearningEdges) Delete(id string) error { return l.S.DeleteLearningEdge(id) }

type MCP struct{ S *Store }

func (m *MCP) List() []*domain.MCPServer { return m.S.ListMCP() }
func (m *MCP) Get(id string) (*domain.MCPServer, error) {
	return m.S.GetMCP(id)
}
func (m *MCP) Save(v *domain.MCPServer) error { return m.S.SaveMCP(v) }
func (m *MCP) Delete(id string) error         { return m.S.DeleteMCP(id) }

type Logs struct{ S *Store }

func (l *Logs) Append(e *domain.LogEntry) { l.S.AppendLog(e) }
func (l *Logs) List(level string, limit int) []*domain.LogEntry {
	return l.S.ListLogs(level, limit)
}
func (l *Logs) Clear() { l.S.ClearLogs() }

type Settings struct{ S *Store }

func (st *Settings) Get() domain.Settings { return st.S.GetSettings() }
func (st *Settings) Set(v domain.Settings) error {
	return st.S.SetSettings(v)
}
