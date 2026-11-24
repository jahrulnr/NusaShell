package appbrowser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"unicode/utf8"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Recordings: the owner (or the agent) records a thread's browser tab — a
// timeline of page events, navigations, network lines, downloads, and the
// agent's tool calls — and reads it back, live or finished.

const (
	timelineDefault = 200
	timelineMax     = 1000
	artifactText    = 256 << 10
)

// StartRecording records the thread's tab.
func (a *App) StartRecording(ctx context.Context, convID, env string) (drepo.RecordingMeta, error) {
	if a.recordings == nil {
		return drepo.RecordingMeta{}, apperror.New(503, "recordings are not available", nil)
	}
	s, err := a.session(ctx, convID)
	if err != nil {
		return drepo.RecordingMeta{}, err
	}
	a.mu.Lock()
	if w := a.live[convID]; w != nil {
		a.mu.Unlock()
		return w.Meta(), apperror.New(409, "a recording is already running in this thread", nil)
	}
	a.mu.Unlock()
	w, err := a.recordings.Start(convID+"/"+s.Profile(), convID, s.Profile(), env, func(text string) string { return a.Mask(convID, text) })
	if err != nil {
		return drepo.RecordingMeta{}, err
	}
	a.mu.Lock()
	a.live[convID] = w
	a.mu.Unlock()
	s.Record(w)
	meta := w.Meta()
	s.SetRecording(&dservice.BrowserRecording{ID: meta.ID, Status: meta.Status})
	return meta, nil
}

func (a *App) liveRecording(convID string) (drepo.RecordingWriter, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w := a.live[convID]
	if w == nil {
		return nil, apperror.New(409, "no recording is running in this thread", nil)
	}
	return w, nil
}

// PauseRecording and ResumeRecording hold and continue the live recording.
func (a *App) PauseRecording(convID string) error  { return a.recordingStep(convID, true) }
func (a *App) ResumeRecording(convID string) error { return a.recordingStep(convID, false) }
func (a *App) recordingStep(convID string, pause bool) error {
	w, err := a.liveRecording(convID)
	if err != nil {
		return err
	}
	if pause {
		err = w.Pause()
	} else {
		err = w.Resume()
	}
	if err != nil {
		return apperror.New(409, err.Error(), err)
	}
	if s, ok := a.browser.Session(convID); ok {
		meta := w.Meta()
		s.SetRecording(&dservice.BrowserRecording{ID: meta.ID, Status: meta.Status})
	}
	return nil
}

// StopRecording finalizes the live recording, summary included.
func (a *App) StopRecording(convID string) (drepo.RecordingMeta, error) {
	w, err := a.liveRecording(convID)
	if err != nil {
		return drepo.RecordingMeta{}, err
	}
	meta, err := w.Stop()
	a.mu.Lock()
	if a.live[convID] == w {
		delete(a.live, convID)
	}
	a.mu.Unlock()
	if s, ok := a.browser.Session(convID); ok {
		s.SetRecording(nil)
	}
	if meta == nil {
		return drepo.RecordingMeta{}, err
	}
	return *meta, err
}

// NoteTool puts an agent's browser tool call on the live recording.
func (a *App) NoteTool(convID, tool string, args json.RawMessage) {
	a.mu.Lock()
	w := a.live[convID]
	a.mu.Unlock()
	if w == nil {
		return
	}
	var data any = map[string]any{"name": tool}
	if len(args) > 0 && json.Valid(args) {
		data = map[string]any{"name": tool, "args": args}
	}
	w.Append("agent", "tool", data)
}

// Recordings lists a thread's recordings, newest first.
func (a *App) Recordings(convID string) []drepo.RecordingMeta {
	if a.recordings == nil {
		return []drepo.RecordingMeta{}
	}
	return a.recordings.List(convID)
}

// Recording reads one recording of the thread (its newest when id is
// empty).
func (a *App) Recording(convID, id string) (drepo.RecordingReader, error) {
	if a.recordings == nil {
		return nil, apperror.NewNotFoundError("recording", nil)
	}
	if id == "" {
		list := a.recordings.List(convID)
		if len(list) == 0 {
			return nil, apperror.NewNotFoundError("recording: this thread was never recorded", nil)
		}
		id = list[0].ID
	}
	r, err := a.recordings.Get(id)
	if err != nil || (convID != "" && r.Meta().ConversationID != convID) {
		return nil, apperror.NewNotFoundError("recording "+id, err)
	}
	return r, nil
}

// RecordingStatus reports the newest recording of the thread.
func (a *App) RecordingStatus(convID string) (Result, error) {
	r, err := a.Recording(convID, "")
	if err != nil {
		return Result{Text: "this thread has no recording"}, nil
	}
	return Result{Text: jsonText(r.Meta())}, nil
}

// RecordingSummary is the newest finished recording's summary.
func (a *App) RecordingSummary(convID string) (Result, error) {
	r, err := a.Recording(convID, "")
	if err != nil {
		return Result{}, err
	}
	sum, err := r.Summary()
	if err != nil {
		if st := r.Meta().Status; st == "recording" || st == "paused" {
			return Result{Text: "still recording: stop it first for a summary"}, nil
		}
		return Result{}, err
	}
	return Result{Text: sum}, nil
}

// Timeline pages the newest recording's events.
func (a *App) Timeline(convID string, after int64, limit int, types []string) (Result, error) {
	r, err := a.Recording(convID, "")
	if err != nil {
		return Result{}, err
	}
	if limit <= 0 {
		limit = timelineDefault
	}
	events, next, done := r.Events(after, min(limit, timelineMax), types)
	if events == nil {
		events = []drepo.RecordingEvent{}
	}
	return Result{Text: jsonText(map[string]any{"events": events, "next": next, "done": done})}, nil
}

// Artifact reads a file of the newest recording: images come back as
// images, text capped.
func (a *App) Artifact(convID, name string) (Result, error) {
	if strings.TrimSpace(name) == "" {
		return Result{}, apperror.New(400, "name is required", nil)
	}
	r, err := a.Recording(convID, "")
	if err != nil {
		return Result{}, err
	}
	data, mime, err := r.Artifact(name)
	if err != nil {
		return Result{}, apperror.NewNotFoundError("artifact "+name, err)
	}
	if strings.HasPrefix(mime, "image/") {
		return Result{Text: name + " (" + mime + ")", Image: data, ImageType: mime}, nil
	}
	if !utf8.Valid(data) {
		return Result{Text: name + " is binary (" + mime + "), " + base64.StdEncoding.EncodeToString(data[:min(len(data), 64)]) + "…"}, nil
	}
	if len(data) > artifactText {
		data = append(data[:artifactText], []byte("\n[truncated]")...)
	}
	return Result{Text: string(data)}, nil
}
