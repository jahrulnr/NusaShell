// Package events translates NusaShell's shared WebSocket event envelope into
// the small state vocabulary understood by the desktop pet.
package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"nusashell-pets/internal/state"
)

type envelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// Decode maps a server-pushed event to a pet state event. The second return
// value is false for valid but unrelated events, allowing the same /ws stream
// to carry transcript and UI events without making the pet react to them.
func Decode(data []byte) (state.Event, bool, error) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return state.Event{}, false, fmt.Errorf("events: decode envelope: %w", err)
	}
	if env.Type == "" {
		return state.Event{}, false, fmt.Errorf("events: envelope type is empty")
	}

	var next state.PetState
	switch env.Type {
	case "agent.turn.started":
		next = state.StateThinking
	case "agent.tool.started", "agent.compacting", "agent.provider.retry":
		next = state.StateReasoning
	case "agent.tool.completed", "agent.compacted", "agent.compaction.failed", "agent.ask.answered", "agent.ask.cancelled":
		next = state.StateThinking
	case "agent.ask.pending":
		next = state.StateWaiting
	case "agent.auto_continue", "agent.steer.queued", "agent.steer.applied", "agent.steer.cancelled":
		next = state.StateThinking
	case "agent.turn.done":
		next = state.StateDone
	case "agent.turn.error":
		next = state.StateError
	default:
		return state.Event{}, false, nil
	}

	ev := state.Event{State: next, Message: payloadMessage(env.Payload)}
	switch env.Type {
	case "agent.tool.started":
		ev.Title = "Executing…"
		var tool struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(env.Payload, &tool)
		ev.Message = "Running a tool…"
		if name := strings.TrimSpace(tool.Name); name != "" {
			ev.Message = name + "(...)"
		}
	case "agent.compacting":
		ev.Title = "Compacting…"
		ev.Message = "Making room for the next step…"
	case "agent.provider.retry":
		ev.Title = "Retrying…"
	}
	return ev, true, nil
}

func payloadMessage(payload json.RawMessage) string {
	var fields struct {
		Message  string `json:"message"`
		Error    string `json:"error"`
		Question string `json:"question"`
	}
	if len(payload) == 0 || json.Unmarshal(payload, &fields) != nil {
		return ""
	}
	if fields.Message != "" {
		return fields.Message
	}
	if fields.Error != "" {
		return fields.Error
	}
	return fields.Question
}
