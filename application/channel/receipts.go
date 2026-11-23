package appchannel

import (
	"context"

	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// sentLimit bounds the replies whose receipts are still awaited; the
// oldest are forgotten first.
const sentLimit = 2048

// sentReply ties a message a chat holds to the reply it carried.
type sentReply struct {
	convID, msgID string
	receipt       valueobject.Receipt
}

// rememberSent keeps the chat's ids of a reply so receipts find it. They
// live in memory: a receipt after a restart is dropped.
func (a *App) rememberSent(channel string, ids []string, convID, msgID string) {
	if len(ids) == 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, id := range ids {
		key := pollKey(channel, id)
		if _, ok := a.sent[key]; !ok {
			a.sentOrder = append(a.sentOrder, key)
		}
		a.sent[key] = &sentReply{convID: convID, msgID: msgID}
	}
	for len(a.sentOrder) > sentLimit {
		delete(a.sent, a.sentOrder[0])
		a.sentOrder = a.sentOrder[1:]
	}
}

// HandleReceipt notes how far replies got in a chat: delivered or read.
// A receipt for a message this app did not send, or that adds nothing, is
// dropped.
func (a *App) HandleReceipt(ctx context.Context, channel string, messageIDs []string, receipt valueobject.Receipt) {
	type hit struct{ convID, msgID string }
	var hits []hit
	seen := map[hit]bool{}
	a.mu.Lock()
	for _, id := range messageIDs {
		r := a.sent[pollKey(channel, id)]
		if r == nil || !receipt.After(r.receipt) {
			continue
		}
		r.receipt = receipt
		h := hit{r.convID, r.msgID}
		if !seen[h] {
			seen[h] = true
			hits = append(hits, h)
		}
	}
	a.mu.Unlock()
	for _, h := range hits {
		if err := a.rooms.RecordDelivery(ctx, h.convID, h.msgID, nil, receipt); err != nil {
			a.log.Warn("could not note a receipt", "conversation", h.convID, "error", err)
		}
	}
}
