package domain

import "time"

type Skill struct {
	ID          string
	Name        string
	Description string
	Content     string
	UpdatedAt   time.Time
}

type MemoryEntry struct {
	ID        string
	Content   string
	Tags      []string
	CreatedAt time.Time
}

type MCPServer struct {
	ID      string
	Name    string
	Command string
	Args    []string
	Env     map[string]string
	Enabled bool
}

type LogEntry struct {
	ID      string
	Time    time.Time
	Level   string // debug | info | warn | error
	Source  string
	Message string
}

type Settings struct {
	CompactionEnabled   bool
	CompactionThreshold int
	PromptCaching       bool
}

// DefaultSettings returns the factory defaults.
func DefaultSettings() Settings {
	return Settings{
		CompactionEnabled:   true,
		CompactionThreshold: 40000,
		PromptCaching:       true,
	}
}
