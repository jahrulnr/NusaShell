package jsonstore

import (
	"fmt"

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

// SkillsAdapter adds file-level skill reads for stores without a real
// skills directory on disk (legacy jsonstore path). Reads are unsupported —
// they return a clear error so the toolbox falls back to Content.
func (k *Skills) ReadFile(id, path string, offset, maxChars int) (*domain.SkillFile, error) {
	return nil, fmt.Errorf("skill file reads are not supported by this store")
}
func (k *Skills) Files(id string) ([]domain.SkillFileEntry, error) {
	return nil, fmt.Errorf("skill file listing is not supported by this store")
}
