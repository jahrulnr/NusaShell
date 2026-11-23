package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Spawn starts a stdio server and returns its stdin/stdout as one stream.
// Closing the stream ends the process. Its stderr is discarded into a
// small tail kept for errors.
func Spawn(command string, args []string, env map[string]string) (io.ReadWriteCloser, error) {
	cmd := exec.Command(command, args...)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	tail := &tailBuffer{max: 4096}
	cmd.Stderr = tail
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", command, err)
	}
	return &process{cmd: cmd, in: stdin, out: stdout, tail: tail}, nil
}

type process struct {
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  io.ReadCloser
	tail *tailBuffer
	once sync.Once
}

func (p *process) Read(b []byte) (int, error) {
	n, err := p.out.Read(b)
	if err == io.EOF {
		if t := strings.TrimSpace(p.tail.String()); t != "" {
			return n, fmt.Errorf("the server exited: %s", t)
		}
	}
	return n, err
}

func (p *process) Write(b []byte) (int, error) { return p.in.Write(b) }

func (p *process) Close() error {
	p.once.Do(func() {
		_ = p.in.Close()
		done := make(chan struct{})
		go func() { _ = p.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = p.cmd.Process.Kill()
			<-done
		}
	})
	return nil
}

type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(b), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// remote turns an HTTP transport into the newline-delimited stream Client
// reads: each written line is a message to send, each message the server
// sends comes out as a line.
type remote struct {
	url     string
	headers map[string]string
	http    *http.Client
	legacy  bool // the older SSE transport

	pr *io.PipeReader
	pw *io.PipeWriter

	wmu     sync.Mutex // the pipe writer
	pending []byte     // a partial written line

	mu       sync.Mutex
	session  string // Mcp-Session-Id
	endpoint string // legacy: where messages are posted
	ready    chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
}

// Dial connects to an MCP server over streamable HTTP, or over the older
// SSE transport when sse is set.
func Dial(rawURL string, headers map[string]string, sse bool) (io.ReadWriteCloser, error) {
	if _, err := url.Parse(rawURL); err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	r := &remote{url: rawURL, headers: headers, http: &http.Client{}, legacy: sse, pr: pr, pw: pw, ready: make(chan struct{}), ctx: ctx, cancel: cancel}
	if !sse {
		close(r.ready)
		return r, nil
	}
	if err := r.openSSE(); err != nil {
		cancel()
		return nil, err
	}
	return r, nil
}

func (r *remote) Read(b []byte) (int, error) { return r.pr.Read(b) }

func (r *remote) Close() error {
	r.cancel()
	_ = r.pw.Close()
	return nil
}

func (r *remote) Write(b []byte) (int, error) {
	r.mu.Lock()
	r.pending = append(r.pending, b...)
	var lines [][]byte
	for {
		i := bytes.IndexByte(r.pending, '\n')
		if i < 0 {
			break
		}
		if line := bytes.TrimSpace(r.pending[:i]); len(line) > 0 {
			lines = append(lines, append([]byte(nil), line...))
		}
		r.pending = r.pending[i+1:]
	}
	r.mu.Unlock()
	for _, line := range lines {
		go r.post(line)
	}
	return len(b), nil
}

// emit hands one server message to the reader.
func (r *remote) emit(msg []byte) {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	_, _ = r.pw.Write(append(bytes.TrimSpace(msg), '\n'))
}

// fail answers a request that could not be posted, so its caller does not
// wait forever.
func (r *remote) fail(line []byte, err error) {
	var m struct {
		ID *json.RawMessage `json:"id"`
	}
	if json.Unmarshal(line, &m) != nil || m.ID == nil {
		return
	}
	out, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": -32000, "message": err.Error()}})
	r.emit(out)
}

func (r *remote) post(line []byte) {
	select {
	case <-r.ready:
	case <-r.ctx.Done():
		return
	}
	r.mu.Lock()
	target, session := r.url, r.session
	if r.legacy {
		target = r.endpoint
	}
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.ctx, 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(line))
	if err != nil {
		r.fail(line, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	for k, v := range r.headers {
		req.Header.Set(k, v)
	}
	res, err := r.http.Do(req)
	if err != nil {
		r.fail(line, err)
		return
	}
	defer res.Body.Close()
	if id := res.Header.Get("Mcp-Session-Id"); id != "" {
		r.mu.Lock()
		r.session = id
		r.mu.Unlock()
	}
	if res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		r.fail(line, fmt.Errorf("MCP server answered %s: %s", res.Status, strings.TrimSpace(string(body))))
		return
	}
	if r.legacy || res.StatusCode == http.StatusAccepted {
		return // answers come over the event stream
	}
	if strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		readEvents(res.Body, func(event, data string) {
			if event == "" || event == "message" {
				r.emit([]byte(data))
			}
		})
		return
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxMessage))
	if err != nil {
		r.fail(line, err)
		return
	}
	body = bytes.TrimSpace(body)
	if len(body) > 0 && body[0] == '[' {
		var batch []json.RawMessage
		if json.Unmarshal(body, &batch) == nil {
			for _, m := range batch {
				r.emit(m)
			}
			return
		}
	}
	if len(body) > 0 {
		r.emit(body)
	}
}

// openSSE opens the legacy event stream; its first "endpoint" event says
// where to post.
func (r *remote) openSSE() error {
	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range r.headers {
		req.Header.Set(k, v)
	}
	res, err := r.http.Do(req)
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		res.Body.Close()
		return fmt.Errorf("MCP server answered %s", res.Status)
	}
	got := make(chan error, 1)
	go func() {
		defer res.Body.Close()
		defer r.pw.CloseWithError(errors.New("the MCP event stream ended"))
		once := sync.Once{}
		readEvents(res.Body, func(event, data string) {
			switch event {
			case "endpoint":
				base, _ := url.Parse(r.url)
				ref, err := url.Parse(strings.TrimSpace(data))
				if err != nil {
					once.Do(func() { got <- err })
					return
				}
				r.mu.Lock()
				r.endpoint = base.ResolveReference(ref).String()
				r.mu.Unlock()
				once.Do(func() { close(r.ready); got <- nil })
			case "", "message":
				r.emit([]byte(data))
			}
		})
		once.Do(func() { got <- errors.New("the MCP event stream ended before naming an endpoint") })
	}()
	select {
	case err := <-got:
		return err
	case <-time.After(30 * time.Second):
		r.cancel()
		return errors.New("the MCP server sent no endpoint")
	}
}

// readEvents parses a text/event-stream, calling fn for each event.
func readEvents(body io.Reader, fn func(event, data string)) {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 64<<10), maxMessage)
	var event string
	var data []string
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if len(data) > 0 {
				fn(event, strings.Join(data, "\n"))
			}
			event, data = "", nil
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(line[6:])
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(line[5:], " "))
		}
	}
	if len(data) > 0 {
		fn(event, strings.Join(data, "\n"))
	}
}
