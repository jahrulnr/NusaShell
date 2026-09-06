// Package learn owns the experience-learning pipeline: experience
// extraction and recording, learning jobs and triggers, the learner LLM turn,
// trajectory, edges/graph, search, learned params, and lifecycle decay. It
// observes conversations via Bus events and writes memories and skills only
// through the memory and skills package ports.
package learn
