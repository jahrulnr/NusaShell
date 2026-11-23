package admin

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	appauth "github.com/jahrulnr/nusashell/application/auth"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	repoauth "github.com/jahrulnr/nusashell/infrastructure/repository/auth"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/infrastructure/service/password"
	"github.com/jahrulnr/nusashell/migration"
)

// fakeServer answers like a server that asks no login, records what the
// CLI sent, and replies with reply (or a problem when status is set).
type fakeServer struct {
	mu     sync.Mutex
	method string
	path   string
	query  string
	body   map[string]any
	status int
	reply  string
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/auth/status" {
		w.Write([]byte(`{"required":false}`))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.method, f.path, f.query, f.body = r.Method, r.URL.Path, r.URL.RawQuery, nil
	raw, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(raw, &f.body)
	if f.status != 0 {
		w.WriteHeader(f.status)
		w.Write([]byte(`{"title":"Bad Request","detail":"WhatsApp is not connected"}`))
		return
	}
	w.Write([]byte(f.reply))
}

func runChannel(t *testing.T, srv *httptest.Server, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	args = append(args, "--addr", strings.TrimPrefix(srv.URL, "http://"))
	code := Channel(args, strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

func TestChannelCommands(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	f := &fakeServer{reply: `{"ok":true}`}
	srv := httptest.NewServer(f)
	defer srv.Close()

	code, out, errOut := runChannel(t, srv, "", "whatsapp", "set", "--allow-add", "+62 812", "--allow-add", "1", "--groups", "",
		"--send-pacing", "0", "--enabled=false", "--retention-days", "30")
	if code != 0 || f.method != "PUT" || f.path != "/api/channels/whatsapp/settings" || !strings.Contains(out, `"ok": true`) {
		t.Fatalf("set: %d %q %q %s %s", code, out, errOut, f.method, f.path)
	}
	want := map[string]any{"addNumbers": []any{"+62 812", "1"}, "allowedGroups": []any{}, "sendPacing": float64(0),
		"enabled": false, "retentionDays": float64(30)}
	if got, _ := json.Marshal(f.body); string(got) != string(must(json.Marshal(want))) {
		t.Fatalf("set body = %s", got)
	}
	if code, _, _ = runChannel(t, srv, "123:tok\n", "telegram", "set", "--group-remove", "-100", "--token-stdin"); code != 0 ||
		f.body["removeGroupIds"].([]any)[0] != "-100" || f.body["token"] != "123:tok" {
		t.Fatalf("telegram set body = %v", f.body)
	}
	if code, _, _ = runChannel(t, srv, "", "whatsapp", "set"); code != 2 {
		t.Fatal("set without flags must be a usage error")
	}
	if code, _, _ = runChannel(t, srv, "", "telegram", "set", "--queue-cap", "3"); code != 2 {
		t.Fatal("a WhatsApp-only flag on Telegram must be a usage error")
	}

	// A file goes as data with a type from its name; flags may follow text.
	dir := t.TempDir()
	pic := filepath.Join(dir, "shot.png")
	os.WriteFile(pic, []byte("\x89PNG\r\n\x1a\nxx"), 0o600)
	if code, _, errOut = runChannel(t, srv, "", "whatsapp", "send", "--to", "6281", "--file", pic, "--caption", "look"); code != 0 {
		t.Fatalf("send file: %s", errOut)
	}
	if f.path != "/api/channels/whatsapp/send" || f.body["type"] != "image" || f.body["mime"] != "image/png" ||
		f.body["name"] != "shot.png" || f.body["caption"] != "look" || f.body["path"] != nil ||
		f.body["data"] != base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nxx")) {
		t.Fatalf("send file body = %v", f.body)
	}
	if code, _, _ = runChannel(t, srv, `{"target":"g@g.us","type":"poll","text":"Q","options":["a","b"]}`, "whatsapp", "send"); code != 0 ||
		f.body["type"] != "poll" {
		t.Fatalf("stdin send body = %v", f.body)
	}
	if code, _, _ = runChannel(t, srv, "", "whatsapp", "send", "--to", "6281"); code != 2 {
		t.Fatal("send --to without content must be a usage error")
	}

	if code, _, _ = runChannel(t, srv, "", "whatsapp", "search", "rapat besok", "-n", "5", "--chat", "1@s.whatsapp.net", "--raw"); code != 0 ||
		f.path != "/api/channels/whatsapp/messages" || f.query != "chat=1%40s.whatsapp.net&limit=5&q=rapat+besok&raw=1" {
		t.Fatalf("search query = %s %s", f.path, f.query)
	}
	if code, _, _ = runChannel(t, srv, "", "whatsapp", "search", "two", "words"); code != 2 {
		t.Fatal("search text must be one argument")
	}
	if code, _, _ = runChannel(t, srv, "", "whatsapp", "user", "62@s.whatsapp.net"); code != 0 || f.query != "id=62%40s.whatsapp.net" {
		t.Fatalf("user query = %s", f.query)
	}

	// The server's detail reaches the owner.
	f.status = 400
	if code, _, errOut = runChannel(t, srv, "", "whatsapp", "groups"); code != 1 || !strings.Contains(errOut, "WhatsApp is not connected") {
		t.Fatalf("problem: %d %q", code, errOut)
	}
}

func TestChannelWithoutServer(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	var out, errb bytes.Buffer
	if code := Channel([]string{"list", "--addr", "127.0.0.1:1"}, strings.NewReader(""), &out, &errb); code != 1 ||
		!strings.Contains(errb.String(), "start it with `nusashell serve`") {
		t.Fatalf("%d %q", code, errb.String())
	}
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}

// When the server asks for a login, the command opens a short session in
// the application database, sends its cookie and CSRF token, and ends it.
func TestChannelOpensAndEndsItsSession(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	os.WriteFile(cfgPath, []byte(`{"storage":{"dir":"`+filepath.ToSlash(filepath.Join(dir, "data"))+`"}}`), 0o600)
	t.Setenv("CONFIG_PATH", cfgPath)
	var cookie, csrf, origin string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/status" {
			w.Write([]byte(`{"required":true}`))
			return
		}
		if c, err := r.Cookie("nusashell_session"); err == nil {
			cookie = c.Value
		}
		csrf, origin = r.Header.Get("X-CSRF-Token"), r.Header.Get("Origin")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	if code, _, errOut := runChannel(t, srv, "", "whatsapp", "set", "--enabled"); code != 0 {
		t.Fatal(errOut)
	}
	if cookie == "" || csrf == "" || origin != srv.URL {
		t.Fatalf("cookie %q csrf %q origin %q", cookie, csrf, origin)
	}
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(dir, "data", "nusashell.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	auth, _ := appauth.New(repoauth.New(db), password.Bcrypt{}, enum.AuthModeOn, false)
	if _, err := auth.Authenticate(ctx, cookie); err == nil {
		t.Fatal("the command's session outlived it")
	}
}
