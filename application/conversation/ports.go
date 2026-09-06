package conversation

import (
	"context"

	"nusashell/contracts"
	"nusashell/domain"
)

// Store is the persistence port for conversation JSON files.
type Store interface {
	List() []*domain.Conversation
	Get(id string) (*domain.Conversation, error)
	Save(c *domain.Conversation) error
	Delete(id string) error
	GetChunk(id string, index int) ([]domain.Message, error)
}

// Todos is the per-conversation checklist used by room RPC and delete cascade.
type Todos interface {
	Get(conversationID string) []domain.TodoItem
	GetBrief(conversationID string) string
	Set(conversationID string, items []domain.TodoItem)
	Clear(conversationID string)
}

// Attachments removes sidecar files when a conversation is deleted.
type Attachments interface {
	Remove(conversationID string) error
}

// Acp stops live subagent runs for a conversation being deleted.
type Acp interface {
	List(conversationID string) []*domain.AcpRun
	Stop(runID string) error
}

// DirListing is the workspace folder-browser result. Entries is never nil
// on the wire; the handler coerces a nil slice to empty.
type DirListing struct {
	Path      string
	Parent    string
	Entries   []contracts.WorkspaceDirEntry
	Truncated bool
}

// Browser lists host directories for the in-app workspace picker.
type Browser interface {
	ListDirs(ctx context.Context, path string) (DirListing, error)
	EnsureDir(ctx context.Context, path string) error
}

// Logger records a structured application log line.
type Logger func(level, source, format string, args ...any)

// TurnLocker serializes set-workspace with turn persistence. The returned
// function must unlock.
type TurnLocker func(conversationID string) (unlock func())

// RunCanceller cancels the in-flight agent turn for a conversation, if any.
type RunCanceller func(conversationID string)

// PeerAnnouncer delivers an inter-room peer_message announcement.
type PeerAnnouncer func(targetID, fromID, content string)

// Deps is the narrow wiring for New. Feature packages never receive *App.
type Deps struct {
	Store       Store
	Todos       Todos
	Attachments Attachments
	Acp         Acp
	Browser     Browser
	Log         Logger
	LockTurn    TurnLocker
	CancelRun   RunCanceller
	Announce    PeerAnnouncer
}

// Service owns conversation RPC, todos, workspace listing, and room messaging.
type Service struct {
	store       Store
	todos       Todos
	attachments Attachments
	acp         Acp
	browser     Browser
	log         Logger
	lockTurn    TurnLocker
	cancelRun   RunCanceller
	announce    PeerAnnouncer
}

// New builds a conversation Service from Deps.
func New(d Deps) *Service {
	return &Service{
		store:       d.Store,
		todos:       d.Todos,
		attachments: d.Attachments,
		acp:         d.Acp,
		browser:     d.Browser,
		log:         d.Log,
		lockTurn:    d.LockTurn,
		cancelRun:   d.CancelRun,
		announce:    d.Announce,
	}
}

func (s *Service) info(format string, args ...any) {
	s.write("info", format, args...)
}

func (s *Service) warn(format string, args ...any) {
	s.write("warn", format, args...)
}

func (s *Service) write(level, format string, args ...any) {
	if s.log != nil {
		s.log(level, "agent", format, args...)
	}
}
