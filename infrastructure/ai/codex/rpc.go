package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"nusashell/infrastructure/ai/codex/runtime"
)

// CodexBinary is the fallback Codex CLI executable name used when no
// managed runtime is available. Can be overridden for testing.
var CodexBinary = "codex"

// runtimeManager lazily initializes a runtime.Manager for auto-downloading
// the Codex binary. Package-level singleton to avoid re-downloading.
var (
	runtimeManager     *runtime.Manager
	runtimeManagerOnce sync.Once
	runtimeManagerErr  error
)

// skipRuntimeManager is a test hook to bypass the runtime manager and
// use CodexBinary directly without attempting any download.
var skipRuntimeManager bool

// resolveCodexBinary returns the path to a usable Codex binary.
// It first checks for a NusaShell-managed runtime (auto-downloaded),
// then falls back to "codex" in PATH.
func resolveCodexBinary(ctx context.Context) (string, error) {
	if !skipRuntimeManager {
		runtimeManagerOnce.Do(func() {
			runtimeManager, runtimeManagerErr = runtime.NewManager()
		})
		mgr, err := runtimeManager, runtimeManagerErr
		if err == nil {
			binPath, err := mgr.EnsureBinary(ctx)
			if err == nil {
				return binPath, nil
			}
			// Fall through to PATH lookup if managed runtime fails
		}
	}
	return CodexBinary, nil
}

// jsonrpcRequest is a JSON-RPC 2.0 request.
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonrpcNotification is a JSON-RPC 2.0 notification (no ID).
type jsonrpcNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// sendRPC writes a JSON-RPC request to the subprocess stdin.
func sendRPC(w io.Writer, id int, method string, params any) error {
	var p json.RawMessage
	if params != nil {
		var err error
		if p, err = json.Marshal(params); err != nil {
			return err
		}
	}
	req := jsonrpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: p}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

// sendRPCNotif writes a JSON-RPC notification (no ID) to stdin.
func sendRPCNotif(w io.Writer, method string, params any) error {
	var p json.RawMessage
	if params != nil {
		var err error
		if p, err = json.Marshal(params); err != nil {
			return err
		}
	}
	req := jsonrpcNotification{JSONRPC: "2.0", Method: method, Params: p}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

// readRPCResponse reads lines from the scanner until it finds a response
// with the matching ID. Notifications are skipped.
func readRPCResponse(sc *bufio.Scanner, wantID int) (string, error) {
	for {
		if !sc.Scan() {
			if err := sc.Err(); err != nil {
				return "", fmt.Errorf("read stdout: %w", err)
			}
			return "", fmt.Errorf("codex subprocess exited unexpectedly")
		}
		line := sc.Text()
		var r struct {
			ID    int `json:"id"`
			Error *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue // skip non-JSON lines
		}
		if r.ID == wantID {
			if r.Error != nil {
				return "", fmt.Errorf("RPC error %d: %s", r.Error.Code, r.Error.Message)
			}
			return line, nil
		}
		// Skip notifications and other responses
	}
}
