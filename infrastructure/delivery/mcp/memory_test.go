package deliverymcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	appmemory "github.com/jahrulnr/nusashell/application/memory"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/redact"
	repomemory "github.com/jahrulnr/nusashell/infrastructure/repository/memory"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/migration"
)

type oneRoom struct{}

func (oneRoom) Info(_ context.Context, id string) (drepo.ConversationInfo, error) {
	return drepo.ConversationInfo{ID: id}, nil
}

// An agent reaches memory only through this server: save, read the index
// with an empty recall, read a body by name, mark, forget.
func TestMemoryCatalogRoundTrip(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "n.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := Memory(appmemory.New(appmemory.Deps{Memories: repomemory.New(db), Rooms: oneRoom{}, Redact: redact.String}))
	for _, name := range []string{"save", "recall", "mark", "forget"} {
		if !cat.Has(name) {
			t.Fatalf("missing tool %s", name)
		}
	}
	ctx := context.Background()
	call := func(tool, args string) string {
		t.Helper()
		res, err := cat.Call(ctx, "c_1", tool, json.RawMessage(args))
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		return res.Content[0].Text
	}
	if out := call("save", `{"name":"tabs","type":"feedback","description":"Owner indents Go with tabs","body":"gofmt."}`); !strings.Contains(out, `"created": true`) {
		t.Fatalf("save: %s", out)
	}
	if out := call("recall", `{}`); !strings.Contains(out, "- tabs [global] — Owner indents Go with tabs") {
		t.Fatalf("index: %s", out)
	}
	if out := call("recall", `{"names":["tabs","nope"]}`); !strings.Contains(out, `"body": "gofmt."`) || !strings.Contains(out, `"nope"`) {
		t.Fatalf("by name: %s", out)
	}
	if out := call("mark", `{"name":"tabs","state":"stale","reason":"owner switched to spaces?"}`); !strings.Contains(out, `"status": "stale"`) {
		t.Fatalf("mark: %s", out)
	}
	if out := call("forget", `{"name":"tabs","reason":"never true"}`); !strings.Contains(out, `"status": "archived"`) {
		t.Fatalf("forget: %s", out)
	}
	if _, err := cat.Call(ctx, "c_1", "forget", json.RawMessage(`{"name":"tabs","reason":"again"}`)); err == nil {
		t.Fatal("forgot a forgotten memory")
	}
}
