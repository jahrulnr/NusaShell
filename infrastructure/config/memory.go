package config

import "fmt"

// MemoryConfig turns memory on or off, and says whether a thread's sealed
// history is read for memory suggestions. Enabled is "on" or "off" — a
// string, since an explicit false would read as unset and fall back to
// the default. Off: threads get no memory index and the memory tools
// refuse; what is stored stays.
type MemoryConfig struct {
	Enabled string `json:"enabled" default:"on"`
	// Consolidation is "suggest" (after a compaction the utility model
	// reads what the owner said and suggests memories for the owner to
	// review) or "off".
	Consolidation string `json:"consolidation" default:"suggest"`
}

func (c *MemoryConfig) Normalize() { ApplyDefaults(c) }

func (c MemoryConfig) validate() error {
	switch c.Enabled {
	case "on", "off":
	default:
		return fmt.Errorf("memory.enabled must be on or off, got %q", c.Enabled)
	}
	switch c.Consolidation {
	case "suggest", "off":
	default:
		return fmt.Errorf("memory.consolidation must be suggest or off, got %q", c.Consolidation)
	}
	return nil
}

// On reports whether memory is in use.
func (c MemoryConfig) On() bool { return c.Enabled != "off" }
