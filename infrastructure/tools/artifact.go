package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nusashell/application"
	"nusashell/domain"
)

// artifactMaxBytes caps the total size of an artifact's html+css+js to keep
// tool output within the agent's token budget. 256KB ≈ 64k tokens.
const artifactMaxBytes = 256 * 1024

// executeArtifact dispatches artifact_create / artifact_update / artifact_read
// / artifact_list / artifact_delete. Returns (output, true, nil) when the
// tool name is an artifact tool; ( "", false, nil ) otherwise.
func (t *Toolbox) executeArtifact(ctx context.Context, name string, argsJSON []byte) (string, bool, error) {
	switch {
	case name == "artifact_create":
		out, err := t.artifactCreate(ctx, argsJSON)
		return out, true, err
	case name == "artifact_update":
		out, err := t.artifactUpdate(ctx, argsJSON)
		return out, true, err
	case name == "artifact_read":
		out, err := t.artifactRead(ctx, argsJSON)
		return out, true, err
	case name == "artifact_list":
		out, err := t.artifactList(ctx)
		return out, true, err
	case name == "artifact_delete":
		out, err := t.artifactDelete(ctx, argsJSON)
		return out, true, err
	default:
		return "", false, nil
	}
}

func (t *Toolbox) artifactCreate(ctx context.Context, argsJSON []byte) (string, error) {
	if t.Artifacts == nil {
		return "", fmt.Errorf("artifact tools are not available in this runtime")
	}
	convID := application.ConversationIDFromContext(ctx)
	if convID == "" {
		return "", fmt.Errorf("artifact_create requires a running conversation context")
	}
	var args struct {
		HTML   string `json:"html"`
		CSS    string `json:"css"`
		JS     string `json:"js"`
		Title  string `json:"title"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(args.HTML) == "" && strings.TrimSpace(args.CSS) == "" && strings.TrimSpace(args.JS) == "" {
		return "", fmt.Errorf("at least one of html, css, or js is required")
	}
	if total := len(args.HTML) + len(args.CSS) + len(args.JS); total > artifactMaxBytes {
		return "", fmt.Errorf("artifact too large: %d bytes (max %d); split into smaller updates or reuse CDNs for large libraries", total, artifactMaxBytes)
	}
	now := time.Now().UTC()
	art := &domain.CanvasArtifact{
		ID:        domain.NewID("art"),
		Title:     strings.TrimSpace(args.Title),
		HTML:      args.HTML,
		CSS:       args.CSS,
		JS:        args.JS,
		Width:     args.Width,
		Height:    args.Height,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if art.Title == "" {
		art.Title = "Artifact"
	}
	if err := t.Artifacts.Save(convID, art); err != nil {
		return "", fmt.Errorf("artifact_create: %w", err)
	}
	return artifactResultJSON(art), nil
}

func (t *Toolbox) artifactUpdate(ctx context.Context, argsJSON []byte) (string, error) {
	if t.Artifacts == nil {
		return "", fmt.Errorf("artifact tools are not available in this runtime")
	}
	convID := application.ConversationIDFromContext(ctx)
	if convID == "" {
		return "", fmt.Errorf("artifact_update requires a running conversation context")
	}
	var args struct {
		ID    string  `json:"id"`
		HTML  *string `json:"html"`
		CSS   *string `json:"css"`
		JS    *string `json:"js"`
		Title *string `json:"title"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(args.ID) == "" {
		return "", fmt.Errorf("id is required")
	}
	existing := t.Artifacts.Get(convID, args.ID)
	if existing == nil {
		return "", fmt.Errorf("artifact %q not found", args.ID)
	}
	if args.HTML != nil {
		existing.HTML = *args.HTML
	}
	if args.CSS != nil {
		existing.CSS = *args.CSS
	}
	if args.JS != nil {
		existing.JS = *args.JS
	}
	if args.Title != nil {
		existing.Title = strings.TrimSpace(*args.Title)
	}
	if total := len(existing.HTML) + len(existing.CSS) + len(existing.JS); total > artifactMaxBytes {
		return "", fmt.Errorf("artifact too large after update: %d bytes (max %d)", total, artifactMaxBytes)
	}
	existing.UpdatedAt = time.Now().UTC()
	if err := t.Artifacts.Save(convID, existing); err != nil {
		return "", fmt.Errorf("artifact_update: %w", err)
	}
	return artifactResultJSON(existing), nil
}

func (t *Toolbox) artifactRead(ctx context.Context, argsJSON []byte) (string, error) {
	if t.Artifacts == nil {
		return "", fmt.Errorf("artifact tools are not available in this runtime")
	}
	convID := application.ConversationIDFromContext(ctx)
	if convID == "" {
		return "", fmt.Errorf("artifact_read requires a running conversation context")
	}
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(args.ID) == "" {
		return "", fmt.Errorf("id is required")
	}
	art := t.Artifacts.Get(convID, args.ID)
	if art == nil {
		return "", fmt.Errorf("artifact %q not found", args.ID)
	}
	return artifactResultJSON(art), nil
}

func (t *Toolbox) artifactList(ctx context.Context) (string, error) {
	if t.Artifacts == nil {
		return "", fmt.Errorf("artifact tools are not available in this runtime")
	}
	convID := application.ConversationIDFromContext(ctx)
	if convID == "" {
		return "", fmt.Errorf("artifact_list requires a running conversation context")
	}
	arts := t.Artifacts.List(convID)
	type entry struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		UpdatedAt string `json:"updated_at"`
	}
	out := make([]entry, 0, len(arts))
	for _, a := range arts {
		out = append(out, entry{ID: a.ID, Title: a.Title, UpdatedAt: a.UpdatedAt.Format(time.RFC3339)})
	}
	b, _ := json.Marshal(map[string]any{"count": len(out), "artifacts": out})
	return string(b), nil
}

func (t *Toolbox) artifactDelete(ctx context.Context, argsJSON []byte) (string, error) {
	if t.Artifacts == nil {
		return "", fmt.Errorf("artifact tools are not available in this runtime")
	}
	convID := application.ConversationIDFromContext(ctx)
	if convID == "" {
		return "", fmt.Errorf("artifact_delete requires a running conversation context")
	}
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(args.ID) == "" {
		return "", fmt.Errorf("id is required")
	}
	if err := t.Artifacts.Delete(convID, args.ID); err != nil {
		return "", fmt.Errorf("artifact_delete: %w", err)
	}
	return fmt.Sprintf(`{"status":"deleted","id":%q}`, args.ID), nil
}

// artifactResultJSON serializes an artifact as the tool output the UI parses
// to render the artifact card. Shape: { "artifact": { id, title, html, css,
// js, width, height } }.
func artifactResultJSON(a *domain.CanvasArtifact) string {
	type artOut struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		HTML   string `json:"html"`
		CSS    string `json:"css"`
		JS     string `json:"js"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	}
	b, _ := json.Marshal(map[string]any{"artifact": artOut{
		ID:     a.ID,
		Title:  a.Title,
		HTML:   a.HTML,
		CSS:    a.CSS,
		JS:     a.JS,
		Width:  a.Width,
		Height: a.Height,
	}})
	return string(b)
}
