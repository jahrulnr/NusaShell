package repoconversation

import (
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// A delivery patch folds onto its message when the history loads: files
// add up without repeats, a receipt only moves forward, and a rewrite that
// carries the message keeps what it recorded.
func TestRecordDelivery(t *testing.T) {
	repo := newRepo(t)
	ctx := t.Context()
	if err := repo.Save(ctx, newConv(t, "c1")); err != nil {
		t.Fatal(err)
	}
	appendMsgs(t, repo, "c1", "u1", "u2")
	ids := msgIDs(t, repo, "c1")
	file, err := valueobject.NewAttachment("upload://up_1", "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	file.SetName("notes.txt")
	for _, step := range []struct {
		files   []valueobject.Attachment
		receipt valueobject.Receipt
	}{
		{[]valueobject.Attachment{file}, valueobject.ReceiptNone},
		{nil, valueobject.ReceiptRead},
		{[]valueobject.Attachment{file}, valueobject.ReceiptDelivered},
	} {
		if err := repo.RecordDelivery(ctx, "c1", ids[0], step.files, step.receipt); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.RecordDelivery(ctx, "c1", "msg_gone", nil, valueobject.ReceiptRead); err != nil {
		t.Fatal(err) // a patch for a message no longer here stays inert
	}
	all, err := repo.LoadAt(ctx, "c1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := all[0]; len(got.SentFiles()) != 1 || got.SentFiles()[0].Name() != "notes.txt" || got.Receipt() != valueobject.ReceiptRead {
		t.Fatalf("delivery = %v %q", got.SentFiles(), got.Receipt())
	}
	if all[1].Receipt() != valueobject.ReceiptNone || len(all[1].SentFiles()) != 0 {
		t.Fatal("the patch landed on another message")
	}
	// Rewriting the later line re-encodes the kept one with its delivery.
	if err := repo.RewriteFrom(ctx, "c1", ids[1], "u2*"); err != nil {
		t.Fatal(err)
	}
	if all, _ = repo.LoadAt(ctx, "c1", 0, 0); all[0].Receipt() != valueobject.ReceiptRead || len(all[0].SentFiles()) != 1 {
		t.Fatalf("after rewrite = %v %q", all[0].SentFiles(), all[0].Receipt())
	}
}
