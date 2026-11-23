// Package mcp speaks the Model Context Protocol over one byte stream: the
// JSON-RPC framing, a tool server (Serve), and a tool client (Client). It
// owns the wire only — which tools exist and what they do belong to the
// callers that build a Server or consume a Client.
//
// Messages are newline-delimited JSON (the MCP stdio transport). A server
// also accepts Content-Length framed requests and answers in the same
// framing, since some agents still send them.
package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ProtocolVersion is the MCP revision this package speaks.
const ProtocolVersion = "2024-11-05"

// maxMessage caps one framed message.
const maxMessage = 16 << 20

// Tool is one entry of tools/list.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Content is one block of a tool result: text, or an image as base64.
type Content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// Result is a tools/call result. IsError marks a failure the caller should
// read, as opposed to a protocol error.
type Result struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError"`
}

// TextResult is a successful result with one text block.
func TextResult(text string) Result {
	return Result{Content: []Content{{Type: "text", Text: text}}}
}

// ErrorResult is a failed call the caller can read.
func ErrorResult(msg string) Result {
	return Result{Content: []Content{{Type: "text", Text: msg}}, IsError: true}
}

// Text joins the result's text blocks.
func (r Result) Text() string {
	var parts []string
	for _, c := range r.Content {
		if c.Type == "text" && c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

type message struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *rpcError        `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("mcp error %d: %s", e.Code, e.Message) }

// readMessage returns the next message body and whether it came Content-Length
// framed. Blank lines between messages are skipped.
func readMessage(r *bufio.Reader) ([]byte, bool, error) {
	var line string
	for {
		raw, err := r.ReadString('\n')
		line = strings.TrimSpace(raw)
		if line != "" {
			break
		}
		if err != nil {
			return nil, false, err
		}
	}
	if strings.HasPrefix(line, "{") {
		if len(line) > maxMessage {
			return nil, false, fmt.Errorf("mcp message too large")
		}
		return []byte(line), false, nil
	}
	length := -1
	for line != "" {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "content-length") {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil || n < 0 || n > maxMessage {
				return nil, false, fmt.Errorf("bad Content-Length %q", v)
			}
			length = n
		}
		raw, err := r.ReadString('\n')
		if err != nil {
			return nil, false, err
		}
		line = strings.TrimSpace(raw)
	}
	if length < 0 {
		return nil, false, fmt.Errorf("missing Content-Length")
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, false, err
	}
	return buf, true, nil
}

func writeMessage(w *bufio.Writer, v any, framed bool) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if framed {
		if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
			return err
		}
	}
	if _, err := w.Write(body); err != nil {
		return err
	}
	if !framed {
		if err := w.WriteByte('\n'); err != nil {
			return err
		}
	}
	return w.Flush()
}
