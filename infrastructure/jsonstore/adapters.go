package jsonstore

import (
	"nusashell/domain"
)

// Sub-store adapters expose the application port method names. The Store
// itself implements ConversationStore (List/Get/Save/Delete).

type Providers struct{ S *Store }

func (p *Providers) List() []*domain.Provider { return p.S.ListProviders() }
func (p *Providers) Get(id string) (*domain.Provider, error) {
	return p.S.GetProvider(id)
}
func (p *Providers) Save(v *domain.Provider) error { return p.S.SaveProvider(v) }
func (p *Providers) Delete(id string) error        { return p.S.DeleteProvider(id) }

type AcpAgents struct{ S *Store }

func (a *AcpAgents) List() []*domain.AcpAgent { return a.S.ListAcpAgents() }
func (a *AcpAgents) Get(id string) (*domain.AcpAgent, error) {
	return a.S.GetAcpAgent(id)
}
func (a *AcpAgents) Save(v *domain.AcpAgent) error { return a.S.SaveAcpAgent(v) }
func (a *AcpAgents) Delete(id string) error        { return a.S.DeleteAcpAgent(id) }

type Memory struct{ S *Store }

func (m *Memory) List() []*domain.MemoryEntry { return m.S.ListMemories() }
func (m *Memory) Save(v *domain.MemoryEntry) error {
	return m.S.SaveMemory(v)
}
func (m *Memory) Delete(id string) error { return m.S.DeleteMemory(id) }
func (m *Memory) Replace(target, oldText, content string) error {
	return m.S.ReplaceMemory(target, oldText, content)
}

type LearningEdges struct{ S *Store }

func (l *LearningEdges) List() []*domain.LearningEdge { return l.S.ListLearningEdges() }
func (l *LearningEdges) Save(v *domain.LearningEdge) error {
	return l.S.SaveLearningEdge(v)
}
func (l *LearningEdges) Delete(id string) error { return l.S.DeleteLearningEdge(id) }

type LearnedParams struct{ S *Store }

func (l *LearnedParams) Load() *domain.LearnedParamRegistry {
	return l.S.LoadLearnedParams()
}
func (l *LearnedParams) Save(r *domain.LearnedParamRegistry) error {
	return l.S.SaveLearnedParams(r)
}

type ModelOverrides struct{ S *Store }

func (m *ModelOverrides) Load() *domain.ModelOverrideRegistry {
	return m.S.LoadModelOverrides()
}
func (m *ModelOverrides) Save(r *domain.ModelOverrideRegistry) error {
	return m.S.SaveModelOverrides(r)
}

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
