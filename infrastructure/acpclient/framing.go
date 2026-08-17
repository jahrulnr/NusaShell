package acpclient

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func writeFrame(w io.Writer, body []byte) error {
	// ACP stdio is newline-delimited JSON-RPC (not LSP Content-Length).
	// Gemini CLI, Claude Code, and the official spec JSON.parse each line.
	if _, err := w.Write(body); err != nil {
		return err
	}
	_, err := w.Write([]byte("\n"))
	return err
}

func readFrame(r *bufio.Reader) ([]byte, error) {
	// Skip blank lines and stray log text until a JSON-RPC frame starts.
	for {
		prefix, err := r.Peek(1)
		if err != nil {
			return nil, err
		}
		if prefix[0] == '{' {
			line, err := r.ReadBytes('\n')
			if err != nil && len(bytes.TrimSpace(line)) == 0 {
				return nil, err
			}
			return bytes.TrimSpace(line), nil
		}
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		lower := strings.ToLower(strings.TrimSpace(line))
		if strings.HasPrefix(lower, "content-length:") {
			n, err := strconv.Atoi(strings.TrimSpace(line[len("Content-Length:"):]))
			if err != nil {
				return nil, fmt.Errorf("bad content-length: %w", err)
			}
			// Consume remaining headers until blank line.
			for {
				h, err := r.ReadString('\n')
				if err != nil {
					return nil, err
				}
				if h == "\r\n" || h == "\n" {
					break
				}
			}
			if n < 0 || n > 16*1024*1024 {
				return nil, fmt.Errorf("content-length out of range: %d", n)
			}
			buf := make([]byte, n)
			if _, err := io.ReadFull(r, buf); err != nil {
				return nil, err
			}
			return buf, nil
		}
		// Ignore non-protocol stdout (agent logs).
	}
}
