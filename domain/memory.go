package domain

import (
	"strings"
	"time"
	"unicode"
)

// Memory has two human-owned always-injected documents (user.md, soul.md)
// plus structured MemoryRecords under growth/memories.jsonl. Agents and
// consolidators never write the two documents.

// UserTokenCap is the soft token budget for the user tier (user.md).
const UserTokenCap = 1000

// UserCharCap is the character approximation of UserTokenCap.
const UserCharCap = UserTokenCap * 4

// AgentTokenCap is the soft token budget for the agent tier (soul.md).
const AgentTokenCap = 1000

// AgentCharCap is the character approximation of AgentTokenCap.
const AgentCharCap = AgentTokenCap * 4

const (
	MemoryTierUser   = "user"
	MemoryTierAgent  = "agent"
	MemoryTierRecord = "record"
)

// DocumentEntry is one body stored in user.md or soul.md.
type DocumentEntry struct {
	ID        string
	Content   string
	Source    string
	UpdatedAt time.Time
}

// MemoryDocument is the full human-owned memory document.
type MemoryDocument struct {
	Entries   []DocumentEntry
	UpdatedAt time.Time
}

// NormalizeMemoryContent returns the canonical form used for exact memory
// duplicate checks. It normalizes line endings, removes trailing whitespace
// from each line, and trims the document boundary without changing internal
// indentation or case-sensitive content such as paths, symbols, and commands.
func NormalizeMemoryContent(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRightFunc(line, unicode.IsSpace)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
