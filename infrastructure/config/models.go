package config

import (
	"fmt"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

// ModelsConfig assigns a model to each job the server does on its own
// behalf, and the one a new thread starts on. An unassigned role is off
// (Chat instead falls back to the first agent, then the first provider;
// Threads to the last model picked in that browser).
type ModelsConfig struct {
	// Threads is what a thread the owner starts in the WebUI begins on,
	// on every device, until they pick another: an agent, or a provider
	// and model.
	Threads ModelRole `json:"threads"`
	// Chat answers threads a chat channel (WhatsApp, Telegram) starts: an
	// agent, or a provider and model.
	Chat ModelRole `json:"chat"`
	// Utility does small tasks for the app, such as naming threads.
	Utility ModelRole `json:"utility"`
	// Embedding turns text into vectors for memory.
	Embedding ModelRole `json:"embedding"`
	// Transcription turns speech into text.
	Transcription ModelRole `json:"transcription"`
	// Image makes images from text, or from text and images.
	Image ModelRole `json:"image"`
	// Video makes video from text, images, or sound.
	Video ModelRole `json:"video"`
	// Speech reads text aloud.
	Speech ModelRole `json:"speech"`
}

// ModelRole is one assignment: an agent (Threads and Chat only), or a provider and
// one of its models.
type ModelRole struct {
	Agent    string `json:"agent"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// Effort is the reasoning effort a thread of this role starts on —
	// Threads and Chat only, and only when the role names a model: an
	// agent carries its own effort among its session options. Empty keeps
	// the model's configured effort, then the provider default.
	Effort enum.Effort `json:"effort,omitempty"`
}

// Assigned reports whether the role names something to run: an agent, or
// a provider with a model.
func (r ModelRole) Assigned() bool {
	return r.Agent != "" || (r.Provider != "" && r.Model != "")
}

func (r *ModelRole) normalize() {
	r.Agent = strings.TrimSpace(r.Agent)
	r.Provider = strings.TrimSpace(r.Provider)
	r.Model = strings.TrimSpace(r.Model)
	r.Effort = enum.Effort(strings.TrimSpace(r.Effort.String()))
}

func (c *ModelsConfig) Normalize() {
	c.Threads.normalize()
	c.Chat.normalize()
	c.Utility.normalize()
	c.Embedding.normalize()
	c.Transcription.normalize()
	c.Image.normalize()
	c.Video.normalize()
	c.Speech.normalize()
}

func (c ModelsConfig) validate() error {
	for name, r := range map[string]ModelRole{"threads": c.Threads, "chat": c.Chat} {
		if r.Agent != "" && r.Provider != "" {
			return fmt.Errorf("models.%s names both agent %q and provider %q; keep one", name, r.Agent, r.Provider)
		}
		if r.Effort != "" {
			if _, ok := enum.ParseEffort(r.Effort.String()); !ok {
				return fmt.Errorf("models.%s.effort %q is not a known effort", name, r.Effort)
			}
			// "default" is the schema's placeholder and means "send
			// nothing"; only a real level needs a model to apply to.
			if !r.Effort.IsUnset() {
				if r.Agent != "" {
					return fmt.Errorf("models.%s sets both an agent and an effort; an agent carries its own effort", name)
				}
				if r.Provider == "" || r.Model == "" {
					return fmt.Errorf("models.%s.effort needs a provider and model", name)
				}
			}
		}
	}
	// Only a thread can run on an agent; the other roles call a model
	// directly. None of them takes a reasoning effort.
	for name, r := range map[string]ModelRole{
		"utility": c.Utility, "embedding": c.Embedding, "transcription": c.Transcription,
		"image": c.Image, "video": c.Video, "speech": c.Speech,
	} {
		if r.Agent != "" {
			return fmt.Errorf("models.%s takes a provider and model, not an agent", name)
		}
		if r.Effort != "" && !r.Effort.IsUnset() {
			return fmt.Errorf("models.%s does not take a reasoning effort", name)
		}
	}
	return nil
}
