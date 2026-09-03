// Package config parses the CharPack config.json that drives the pet overlay.
//
// The config declares the pet name, the shrink-to-fit bounding box, the
// WebSocket URL of the NusaShell backend, the Electron launcher path, and
// either the preferred hatch-pet v2 spritesheet or a legacy image/state map.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Defaults mirror myCat's CharPack defaults; only the bounding box is shrunk.
const (
	DefaultMaxWidth  = 200
	DefaultMaxHeight = 400
	DefaultWSURL     = "ws://127.0.0.1:9999/ws"
	// DefaultShapeAlphaCutoff keeps every non-transparent source pixel in the
	// X11 shape. Antialiased edge pixels remain visible without including the
	// transparent canvas around the mascot.
	DefaultShapeAlphaCutoff uint8 = 1
)

// StateConfig maps a legacy pet state name to a GIF file and a random replay gap.
// Every is [minSeconds, maxSeconds]; [0,0] means play once and hold.
type StateConfig struct {
	GIF   string     `json:"gif"`
	Every [2]float64 `json:"every"`
}

// Config is the parsed config.json.
type Config struct {
	Name             string                 `json:"name"`
	MaxWidth         int                    `json:"max_width"`
	MaxHeight        int                    `json:"max_height"`
	WSURL            string                 `json:"ws_url"`
	ElectronPath     string                 `json:"electron_path"`
	SpriteSheet      string                 `json:"spritesheet"`
	Image            string                 `json:"image"`
	ClickThrough     bool                   `json:"click_through"`
	ShapeAlphaCutoff uint8                  `json:"shape_alpha_cutoff"`
	BubbleEnabled    *bool                  `json:"bubble_enabled"`
	BubbleFont       string                 `json:"bubble_font"`
	States           map[string]StateConfig `json:"states"`
}

// Bubbles reports whether the head speech bubble is enabled. Defaults to
// enabled when the config omits bubble_enabled.
func (c *Config) Bubbles() bool {
	return c.BubbleEnabled == nil || *c.BubbleEnabled
}

// Load reads and validates config.json from path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return cfg, nil
}

// Parse decodes config bytes and applies defaults + validation.
func Parse(data []byte) (*Config, error) {
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	if c.Name == "" {
		c.Name = "nusa-shell-pet"
	}
	if c.MaxWidth <= 0 {
		c.MaxWidth = DefaultMaxWidth
	}
	if c.MaxHeight <= 0 {
		c.MaxHeight = DefaultMaxHeight
	}
	if c.WSURL == "" {
		c.WSURL = DefaultWSURL
	}
	if c.ShapeAlphaCutoff == 0 {
		c.ShapeAlphaCutoff = DefaultShapeAlphaCutoff
	}
	if strings.TrimSpace(c.SpriteSheet) == "" && strings.TrimSpace(c.Image) == "" && len(c.States) == 0 {
		return nil, fmt.Errorf("assets: spritesheet, image, or states required")
	}
	for name, st := range c.States {
		if st.GIF == "" {
			return nil, fmt.Errorf("states.%s: gif is empty", name)
		}
		if st.Every[0] < 0 || st.Every[1] < 0 || st.Every[1] < st.Every[0] {
			return nil, fmt.Errorf("states.%s: invalid every [%v,%v]", name, st.Every[0], st.Every[1])
		}
	}
	return &c, nil
}
