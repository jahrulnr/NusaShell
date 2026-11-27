package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	repochannel "github.com/jahrulnr/nusashell/infrastructure/repository/channel"
	reposecret "github.com/jahrulnr/nusashell/infrastructure/repository/secret"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/migration"
)

// botAPI is a scripted Bot API: getUpdates hands out queued updates once.
type botAPI struct {
	mu      sync.Mutex
	queue   []tgUpdate
	sent    []map[string]any
	actions int
	files   map[string][]byte // file_id → bytes, served by getFile and /file/
	calls   []apiCall         // every call but polling, typing and sendMessage
	refuse  *apiError         // when set, sendMessage answers this Bot API error
}

// apiCall is one recorded Bot API call; a multipart upload records its
// form fields and the uploaded file's name and bytes.
type apiCall struct {
	method string
	params map[string]any
	file   string
	data   string
}

func (b *botAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if i := strings.Index(r.URL.Path, "/file/bot"); i >= 0 {
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		b.mu.Lock()
		data, ok := b.files[id]
		b.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
		return
	}
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	params := map[string]any{}
	call := apiCall{method: method, params: params}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		_ = r.ParseMultipartForm(1 << 20)
		for k, v := range r.MultipartForm.Value {
			params[k] = v[0]
		}
		for _, fh := range r.MultipartForm.File {
			f, _ := fh[0].Open()
			raw, _ := io.ReadAll(f)
			call.file, call.data = fh[0].Filename, string(raw)
		}
	} else {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &params)
	}
	reply := func(v any) {
		raw, _ := json.Marshal(v)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": json.RawMessage(raw)})
	}
	switch method {
	case "getMe":
		reply(tgUser{ID: 999, IsBot: true, Username: "nusa_bot"})
	case "getUpdates":
		b.mu.Lock()
		q := b.queue
		b.queue = nil
		b.mu.Unlock()
		if len(q) == 0 {
			select {
			case <-r.Context().Done():
			case <-time.After(200 * time.Millisecond):
			}
		}
		reply(q)
	case "sendMessage":
		b.mu.Lock()
		ref := b.refuse
		if ref == nil {
			b.sent = append(b.sent, params)
		}
		b.mu.Unlock()
		if ref != nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": ref.code, "description": ref.desc})
			return
		}
		reply(tgMessage{MessageID: 1})
	case "sendChatAction":
		b.mu.Lock()
		b.actions++
		b.mu.Unlock()
		reply(true)
	case "getFile":
		id, _ := params["file_id"].(string)
		b.mu.Lock()
		data, ok := b.files[id]
		b.mu.Unlock()
		if !ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 400, "description": "file not found"})
			return
		}
		reply(tgFile{FileID: id, FileSize: int64(len(data)), FilePath: "docs/" + id})
	default:
		b.mu.Lock()
		b.calls = append(b.calls, call)
		b.mu.Unlock()
		if strings.HasPrefix(method, "send") {
			reply(tgMessage{MessageID: 1})
			return
		}
		reply(true)
	}
}

func (b *botAPI) take(method string) []apiCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out, rest []apiCall
	for _, c := range b.calls {
		if c.method == method {
			out = append(out, c)
		} else {
			rest = append(rest, c)
		}
	}
	b.calls = rest
	return out
}

type inbox struct {
	mu       sync.Mutex
	got      []dservice.InboundMessage
	mentions []dservice.ChannelMention
	queries  []string
}

func (i *inbox) HandleInbound(_ context.Context, in dservice.InboundMessage) {
	i.mu.Lock()
	i.got = append(i.got, in)
	i.mu.Unlock()
}

func (i *inbox) Mentions(_ context.Context, query string) []dservice.ChannelMention {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.queries = append(i.queries, query)
	return i.mentions
}

func (i *inbox) HandleReceipt(context.Context, string, []string, valueobject.Receipt) {}

func (i *inbox) messages() []dservice.InboundMessage {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]dservice.InboundMessage(nil), i.got...)
}

func tgUpd(id int64, chat tgChat, from int64, text string) tgUpdate {
	return tgUpdate{UpdateID: id, Message: &tgMessage{MessageID: id, Chat: chat, From: &tgUser{ID: from, FirstName: "Ana"}, Date: time.Now().Unix(), Text: text}}
}

func TestTelegramGatesAndReplies(t *testing.T) {
	ctx := t.Context()
	api := &botAPI{}
	srv := httptest.NewServer(api)
	defer srv.Close()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "app.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	secrets, _ := reposecret.New(":memory:")
	defer secrets.Close()
	ch := New(repochannel.New(db), secrets, nil)
	ch.base = srv.URL
	in := &inbox{}
	if err := ch.Start(ctx, in); err != nil {
		t.Fatal(err)
	}
	if st := ch.Status(); st.State != "unconfigured" {
		t.Fatalf("state without a token = %q", st.State)
	}
	if err := ch.UpdateSettings(ctx, json.RawMessage(`{"token":"not-a-token"}`)); err == nil {
		t.Fatal("a malformed token must be refused")
	}
	doc := `{"token":"123456:ABCDEFGHIJKLMNOPQRSTUVWXYZ","allowedUserIds":["42"],"allowedGroupIds":["-100"],"groupPolicy":"mention"}`
	if err := ch.UpdateSettings(ctx, json.RawMessage(doc)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return ch.Status().State == "connected" })
	raw, _ := ch.Settings(ctx)
	if strings.Contains(string(raw), "ABCDEFGHIJ") || !strings.Contains(string(raw), `"tokenSet":true`) {
		t.Fatalf("settings leak or miss the token state: %s", raw)
	}

	private := tgChat{ID: 42, Type: "private"}
	group := tgChat{ID: -100, Type: "supergroup", Title: "Team"}
	api.mu.Lock()
	api.queue = []tgUpdate{
		tgUpd(1, private, 42, "hello"),
		tgUpd(2, tgChat{ID: 7, Type: "private"}, 7, "let me in"), // not allowlisted
		tgUpd(3, group, 42, "chatter without a mention"),
		tgUpd(4, group, 55, "@nusa_bot summarize this"), // allowlisted group, any member
	}
	api.mu.Unlock()
	waitFor(t, func() bool { return len(in.messages()) == 2 })
	got := in.messages()
	if got[0].Text != "hello" || got[0].Chat.ChatID() != "42" || got[0].Group || got[0].MessageID != "42:1" {
		t.Fatalf("private = %+v", got[0])
	}
	if got[1].Text != "summarize this" || !got[1].Group || got[1].ChatName != "Team" {
		t.Fatalf("group = %+v", got[1])
	}
	if b := ch.Status().Blocked; len(b) != 1 || b[0].SenderID != "7" {
		t.Fatalf("blocked = %+v", b)
	}

	chat, _ := valueobject.NewChatRef("telegram", "42", "")
	stop := ch.Typing(ctx, chat)
	waitFor(t, func() bool { api.mu.Lock(); defer api.mu.Unlock(); return api.actions > 0 })
	stop()
	if ids, err := ch.Send(ctx, chat, "**Done** with `a < b`"); err != nil || len(ids) != 1 {
		t.Fatalf("send = %q, %v", ids, err)
	}
	api.mu.Lock()
	sent := api.sent[len(api.sent)-1]
	api.mu.Unlock()
	if sent["text"] != "<b>Done</b> with <code>a &lt; b</code>" || sent["parse_mode"] != "HTML" {
		t.Fatalf("sent = %v", sent)
	}
	// Add and remove change the allowlists in place; junk is refused.
	if err := ch.UpdateSettings(ctx, json.RawMessage(`{"addUserIds":[" 43 "],"removeUserIds":["42"],"removeGroupIds":["-100"]}`)); err != nil {
		t.Fatal(err)
	}
	if raw, _ = ch.Settings(ctx); !strings.Contains(string(raw), `"allowedUserIds":["43"]`) || !strings.Contains(string(raw), `"allowedGroupIds":[]`) {
		t.Fatalf("after add/remove: %s", raw)
	}
	for _, bad := range []string{`{"addUserIds":["@someone"]}`, `{"addGroupIds":["100"]}`, `{"groupPolicy":"never"}`} {
		if err := ch.UpdateSettings(ctx, json.RawMessage(bad)); err == nil {
			t.Fatalf("%s must be refused", bad)
		}
	}
	if err := ch.Logout(ctx); err != nil || ch.Status().State != "unconfigured" {
		t.Fatalf("logout: %v, state %q", err, ch.Status().State)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached in time")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSplitTextKeepsLines(t *testing.T) {
	text := strings.TrimSpace(strings.Repeat("line of text\n", 50))
	parts := splitText(text, 100)
	for _, p := range parts {
		if utf16Units(p) > 100 {
			t.Fatalf("part too long: %d", utf16Units(p))
		}
	}
	if strings.Join(parts, "\n") != text {
		t.Fatal("split lost text")
	}
}

func TestQuoteOf(t *testing.T) {
	cases := []struct {
		name string
		m    *tgMessage
		want string
	}{
		{"no reply", nil, ""},
		{"text", &tgMessage{Text: " shall we? "}, "shall we?"},
		{"caption", &tgMessage{Caption: "the chart", Photo: []tgPhotoSize{{}}}, "the chart"},
		{"photo", &tgMessage{Photo: []tgPhotoSize{{}}}, "an image"},
		{"voice", &tgMessage{Voice: &tgMedia{}}, "a voice message"},
		{"animation", &tgMessage{Animation: &tgMedia{}}, "a video"},
		{"sticker", &tgMessage{Sticker: &tgMedia{}}, "a sticker"},
		{"something else", &tgMessage{}, "a message"},
	}
	for _, c := range cases {
		if got := quoteOf(c.m); got != c.want {
			t.Errorf("%s: quoteOf = %q, want %q", c.name, got, c.want)
		}
	}
}
