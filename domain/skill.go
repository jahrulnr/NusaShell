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
	MaxToolRounds       int
	MaxInputTokens      int // global context window cap (default 200k)
	MaxOutputTokens     int // default max completion tokens (default 64k)
}

// DefaultSettings returns the factory defaults.
func DefaultSettings() Settings {
	return Settings{
		CompactionEnabled:   true,
		CompactionThreshold: 40000,
		PromptCaching:       true,
		MaxToolRounds:       8,
		MaxInputTokens:      200000,
		MaxOutputTokens:     65536,
	}
}

// NormalizeSettings fills values introduced after an existing local settings
// file was written. It preserves intentional false values for toggles.
func NormalizeSettings(settings Settings) Settings {
	if settings.MaxToolRounds < 1 {
		settings.MaxToolRounds = DefaultSettings().MaxToolRounds
	}
	if settings.MaxInputTokens < 1000 {
		settings.MaxInputTokens = DefaultSettings().MaxInputTokens
	}
	if settings.MaxOutputTokens < 256 {
		settings.MaxOutputTokens = DefaultSettings().MaxOutputTokens
	}
	return settings
}
