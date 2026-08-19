package domain

import "time"

// CanvasArtifact is an interactive HTML/CSS/JS document the agent produces
// via the artifact_create / artifact_update tools. It is rendered in a
// sandboxed iframe in the UI so the agent can ship prototypes, minigames,
// dashboards, and visualizations without touching the host page.
type CanvasArtifact struct {
	ID        string    `json:"id"`
	Title     string    `json:"title,omitempty"`
	HTML      string    `json:"html,omitempty"`
	CSS       string    `json:"css,omitempty"`
	JS        string    `json:"js,omitempty"`
	Width     int       `json:"width,omitempty"`
	Height    int       `json:"height,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
