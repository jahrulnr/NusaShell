package config

import (
	"fmt"
	"time"
)

// MinAgentIdleTimeout is the shortest idle timeout allowed. The server looks
// for idle agent processes once a minute, so a shorter value would claim a
// precision it does not have, and a process stopped between two messages of
// one conversation costs a restart.
const MinAgentIdleTimeout = time.Minute

// AgentsConfig tunes the processes of the external agents.
type AgentsConfig struct {
	// IdleTimeout is how long an agent's process may run no turn before the
	// server stops it. The next message starts it again and restores the
	// thread's session, so the owner does not notice. An agent kept resident
	// (its own "start with the server" setting) is never stopped.
	IdleTimeout Duration `json:"idle_timeout" default:"5m"`
}

// Normalize clamps a negative timeout back to unconfigured, then fills zeros
// from the `default` tags.
func (c *AgentsConfig) Normalize() {
	if c.IdleTimeout < 0 {
		c.IdleTimeout = 0
	}
	ApplyDefaults(c)
}

func (c AgentsConfig) validate() error {
	if time.Duration(c.IdleTimeout) < MinAgentIdleTimeout {
		return fmt.Errorf("agents.idle_timeout must be at least %s, got %s", MinAgentIdleTimeout, time.Duration(c.IdleTimeout))
	}
	return nil
}
