package config

import "fmt"

// SkillsConfig turns skill improvement on or off. "observe" logs one line per
// eligible finished turn and writes nothing else. "suggest" is not accepted
// yet: it comes with phase 1, the skill proposal flow, and until then the file
// refuses it rather than offering a mode the build cannot honor. The mode is
// read once at boot. Changing it takes a restart, because the observer and the
// edit evidence hook are only registered when it is "observe". Off registers
// nothing.
type SkillsConfig struct {
	Improvement string `json:"improvement" default:"off"`
}

func (c *SkillsConfig) Normalize() { ApplyDefaults(c) }

func (c SkillsConfig) validate() error {
	switch c.Improvement {
	case "off", "observe":
		return nil
	}
	return fmt.Errorf("skills.improvement must be off or observe, got %q", c.Improvement)
}
