package acpclient

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestWriteFrameIsNewlineDelimitedJSON(t *testing.T) {
	var buf bytes.Buffer
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if err := writeFrame(&buf, body); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if strings.Contains(got, "Content-Length") {
		t.Fatalf("ACP stdio is newline-delimited JSON; Content-Length framing breaks Gemini CLI: %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Fatalf("frame must end with newline, got %q", got)
	}
	line := strings.TrimSuffix(got, "\n")
	if strings.Contains(line, "\n") {
		t.Fatal("ACP messages must not contain embedded newlines")
	}
	var msg map[string]any
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		t.Fatalf("peer JSON.parse(line) failed: %v (%q)", err, line)
	}
	if msg["method"] != "initialize" {
		t.Fatalf("parsed %+v", msg)
	}
}

func TestReadFrameAcceptsNDJSONAndContentLength(t *testing.T) {
	ndjson := "{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n"
	got, err := readFrame(bufio.NewReader(strings.NewReader(ndjson)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != strings.TrimSpace(ndjson) {
		t.Fatalf("ndjson got %q", got)
	}

	body := []byte(`{"jsonrpc":"2.0","id":2,"result":{}}`)
	cl := "Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + string(body)
	got, err = readFrame(bufio.NewReader(strings.NewReader(cl)))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("content-length got %q", got)
	}
}

func TestWriteFrameGeminiLineParser(t *testing.T) {
	var buf bytes.Buffer
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
	if err := writeFrame(&buf, body); err != nil {
		t.Fatal(err)
	}
	// Gemini CLI ACP splits stdout/stdin on '\n' then JSON.parse(line).
	for _, line := range strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if err := json.Unmarshal([]byte(line), new(map[string]any)); err != nil {
			t.Fatalf("Gemini-style JSON.parse(%q): %v", line, err)
		}
	}
}
