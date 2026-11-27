package config

import "fmt"

// SkillsConfig turns skill improvement on or off. "off" registers nothing.
// "observe" logs one line per eligible finished turn that shows a signal, and
// writes nothing else.
// "suggest" runs the proposal pass and serves the Skills page. The mode is
// read once at boot. Changing it takes a restart, because the hooks and the
// page are registered only for the mode that was read.
//
// CatalogMaxBytes is the catalog's byte budget. The owner sets it. Suggest
// copies it onto the pass and onto an accept. Off and observe do not read it.
type SkillsConfig struct {
	Improvement     string `json:"improvement" default:"off"`
	CatalogMaxBytes int    `json:"catalog_max_bytes" default:"32768"`
}

func (c *SkillsConfig) Normalize() { ApplyDefaults(c) }

func (c SkillsConfig) validate() error {
	switch c.Improvement {
	case "off", "observe", "suggest":
	default:
		return fmt.Errorf("skills.improvement must be off, observe, or suggest, got %q", c.Improvement)
	}
	if c.CatalogMaxBytes <= 0 {
		return fmt.Errorf("skills.catalog_max_bytes must be positive, got %d", c.CatalogMaxBytes)
	}
	return nil
}
