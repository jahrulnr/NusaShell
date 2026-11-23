package repoconversation

import (
	"context"
	"errors"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	"path/filepath"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
)

// UpdateToolCallOutput appends a patch record to the line stream — the
// raw output, fixed latency, and any media references land verbatim and resolve by
// callID at read time.
func (r *ConversationRepository) UpdateToolCallOutput(ctx context.Context, convID string, call entity.ToolCall) error {
	if err := validID(convID); err != nil {
		return err
	}
	if !call.HasOutput() {
		return errors.New("cannot persist a pending tool output")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	unlock, err := r.lockStore()
	if err != nil {
		return err
	}
	defer unlock()

	if _, err := r.readMeta(convID); err != nil {
		return err
	}
	// Append-only patch — no read-modify-write, so parallel tool results
	// racing to persist stay safe.
	line := lineRecord{Type: lineToolOutput, CallID: call.ID(), Output: call.Output(), OutputLatency: call.OutputLatency(), Failed: call.Failed()}
	for _, a := range call.OutputAttachments() {
		line.Attachments = append(line.Attachments, attachmentRecord{
			URI:       a.URI(),
			MediaType: a.MediaType(),
			Name:      a.Name(),
			SHA256:    a.SHA256(),
		})
	}
	return appendLine(filepath.Join(r.convDir(convID), activeFile), line)
}

// RecordDelivery appends a delivery patch for msgID: files that went to a
// chat with the reply, and a receipt. Like tool outputs it never rewrites
// stored lines; the reader folds patches onto their message.
func (r *ConversationRepository) RecordDelivery(ctx context.Context, convID, msgID string, files []valueobject.Attachment, receipt valueobject.Receipt) error {
	if err := validID(convID); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	unlock, err := r.lockStore()
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := r.readMeta(convID); err != nil {
		return err
	}
	line := lineRecord{Type: lineDelivery, MessageID: msgID, Receipt: string(receipt), Attachments: encodeAttachments(files)}
	return appendLine(filepath.Join(r.convDir(convID), activeFile), line)
}
