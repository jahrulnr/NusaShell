package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"nusashell/domain"
	"nusashell/infrastructure/ai/codex/runtime"
)

// CodexBinary is the fallback Codex CLI executable name used when no
// managed runtime is available. Can be overridden for testing.
var CodexBinary = "codex"

// compactTimeout is the maximum time to wait for a compaction operation.
const compactTimeout = 120 * time.Second

// runtimeManager lazily initializes a runtime.Manager for auto-downloading
// the Codex binary. Package-level singleton to avoid re-downloading.
var (
	runtimeManager     *runtime.Manager
	runtimeManagerOnce sync.Once
	runtimeManagerErr  error
)

func getRuntimeManager() (*runtime.Manager, error) {
	runtimeManagerOnce.Do(func() {
		runtimeManager, runtimeManagerErr = runtime.NewManager()
	})
	return runtimeManager, runtimeManagerErr
}

// resolveCodexBinary returns the path to a usable Codex binary.
// It first checks for a NusaShell-managed runtime (auto-downloaded),
// then falls back to "codex" in PATH.
//
// skipRuntimeManager is a test hook to bypass the runtime manager and
// use CodexBinary directly without attempting any download.
var skipRuntimeManager bool

func resolveCodexBinary(ctx context.Context) (string, error) {
	if !skipRuntimeManager {
		mgr, err := getRuntimeManager()
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

// compactViaSubprocess compacts a conversation by spawning a Codex app-server
// subprocess and sending thread/compact/start via JSON-RPC.
//
// This is the only working way to trigger Codex's server-side compaction.
// The HTTP /responses/compact endpoint is not exposed to clients — it's
// internal to the Codex CLI. The app-server JSON-RPC protocol is the public
// interface.
//
// Flow:
//  1. Spawn `codex app-server` subprocess
//  2. Initialize handshake (initialize → initialized → thread/start)
//  3. Replay conversation as turn/start messages
//  4. Send thread/compact/start
//  5. Wait for turn/completed
//  6. Kill subprocess
//
// The encrypted compaction blob is stored internally by Codex and is NOT
// exposed to the client. Context preservation happens automatically when
// subsequent turns are sent on the same thread. Since NusaShell uses its own
// HTTP adapter for regular requests (not the app-server subprocess), the
// compaction blob is not portable across the two approaches.
//
// Therefore, this method returns a summary string only. The blob is not
// stored on the conversation — subsequent NusaShell turns will not benefit
// from the server-side compaction blob. This is a known limitation.
//
// On any error, the caller falls back to client-side compaction.
func compactViaSubprocess(ctx context.Context, c *domain.Conversation, model, accessToken, accountID string) (string, error) {
	if len(c.Messages) <= 1 {
		return "", nil
	}

	subCtx, cancel := context.WithTimeout(ctx, compactTimeout)
	defer cancel()

	// Resolve binary: try managed runtime first, fall back to PATH
	binPath, err := resolveCodexBinary(ctx)
	if err != nil {
		return "", fmt.Errorf("codex compact: resolve binary: %w", err)
	}

	dir, err := os.MkdirTemp("", "nusashell-codex-compact-*")
	if err != nil {
		return "", fmt.Errorf("codex compact: create temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	// Compact only needs to read the conversation and produce a summary.
	// Use read-only sandbox instead of danger-full-access to limit blast
	// radius if the subprocess is compromised. The approval policy is
	// never because there is no human to approve in a background compact.
	cmd := exec.CommandContext(subCtx, binPath, "app-server",
		"-c", "sandbox_mode=read-only",
		"-c", "approval_policy=never",
	)
	cmd.Dir = dir
	// Inject NusaShell's OAuth token so the subprocess authenticates as
	// the same account instead of relying on Codex CLI's own auth.json.
	// This prevents cross-account access on multi-user systems and ensures
	// the compact request counts against the correct account's usage.
	if accessToken != "" {
		cmd.Env = append(os.Environ(),
			"OPENAI_CODEX_ACCESS_TOKEN="+accessToken,
		)
		if accountID != "" {
			cmd.Env = append(cmd.Env, "OPENAI_CODEX_ACCOUNT_ID="+accountID)
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("codex compact: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("codex compact: stdout pipe: %w", err)
	}
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("codex compact: start subprocess: %w", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 32<<20)

	// 1. initialize
	if err := sendRPC(stdin, 1, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "nusashell",
			"version": "0.1.0",
		},
	}); err != nil {
		return "", fmt.Errorf("codex compact: send initialize: %w", err)
	}
	if _, err := readRPCResponse(sc, 1); err != nil {
		return "", fmt.Errorf("codex compact: initialize: %w", err)
	}

	// 2. initialized notification
	if err := sendRPCNotif(stdin, "initialized", nil); err != nil {
		return "", fmt.Errorf("codex compact: send initialized: %w", err)
	}

	// 3. thread/start
	if err := sendRPC(stdin, 2, "thread/start", map[string]any{}); err != nil {
		return "", fmt.Errorf("codex compact: send thread/start: %w", err)
	}
	threadResp, err := readRPCResponse(sc, 2)
	if err != nil {
		return "", fmt.Errorf("codex compact: thread/start: %w", err)
	}
	var thread struct {
		Result struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(threadResp), &thread); err != nil {
		return "", fmt.Errorf("codex compact: parse thread: %w", err)
	}
	threadID := thread.Result.Thread.ID
	if threadID == "" {
		return "", fmt.Errorf("codex compact: no thread ID in response")
	}

	// 4. Replay conversation as a single turn
	// Build input from conversation messages
	input := buildTurnInput(c)
	if len(input) > 0 {
		if err := sendRPC(stdin, 100, "turn/start", map[string]any{
			"threadId": threadID,
			"input":    input,
			"effort":   "low",
		}); err != nil {
			return "", fmt.Errorf("codex compact: send turn/start: %w", err)
		}
		// Wait for turn/completed
		if err := waitTurnCompleted(sc, 100); err != nil {
			return "", fmt.Errorf("codex compact: replay turn: %w", err)
		}
	}

	// 5. thread/compact/start
	if err := sendRPC(stdin, 200, "thread/compact/start", map[string]any{
		"threadId": threadID,
	}); err != nil {
		return "", fmt.Errorf("codex compact: send compact/start: %w", err)
	}

	// 6. Wait for compact turn/completed
	summary, err := waitCompactCompleted(sc, 200)
	if err != nil {
		return "", fmt.Errorf("codex compact: %w", err)
	}

	if summary == "" {
		summary = "[Server-side compaction completed via Codex app-server]"
	}
	return summary, nil
}

// buildTurnInput converts conversation messages to Codex turn/start input
// items. User and assistant text are replayed so the compact summary can see
// both the request and the work that was done. Tool calls and system markers
// are skipped — Codex turn/start accepts text items, not our tool protocol.
func buildTurnInput(c *domain.Conversation) []map[string]any {
	var out []map[string]any
	for _, m := range c.Messages {
		switch m.Role {
		case domain.RoleUser, domain.RoleAssistant:
			if strings.TrimSpace(m.Content) != "" {
				out = append(out, map[string]any{
					"type": "text",
					"text": m.Content,
				})
			}
		}
	}
	return out
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

// waitTurnCompleted reads notifications until turn/completed is received.
func waitTurnCompleted(sc *bufio.Scanner, turnID int) error {
	for {
		if !sc.Scan() {
			if err := sc.Err(); err != nil {
				return fmt.Errorf("read stdout: %w", err)
			}
			return fmt.Errorf("codex subprocess exited during turn")
		}
		line := sc.Text()
		var m struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		json.Unmarshal([]byte(line), &m)
		if m.ID == turnID {
			// turn/start response received — continue waiting for turn/completed
			continue
		}
		if m.Method == "turn/completed" {
			return nil
		}
	}
}

// waitCompactCompleted reads notifications during compaction and returns
// any agent message text as the summary.
func waitCompactCompleted(sc *bufio.Scanner, compactID int) (string, error) {
	var summaryParts []string
	for {
		if !sc.Scan() {
			if err := sc.Err(); err != nil {
				return "", fmt.Errorf("read stdout: %w", err)
			}
			return "", fmt.Errorf("codex subprocess exited during compaction")
		}
		line := sc.Text()
		var m struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		json.Unmarshal([]byte(line), &m)

		if m.ID == compactID {
			if m.Error != nil {
				return "", fmt.Errorf("compact error: %s", m.Error.Message)
			}
			// compact/start response received — continue waiting
			continue
		}

		if m.Method == "item/completed" {
			// Check if this is an agent message with text
			var p struct {
				Item struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"item"`
			}
			if json.Unmarshal(m.Params, &p) == nil && p.Item.Type == "agentMessage" && p.Item.Text != "" {
				summaryParts = append(summaryParts, p.Item.Text)
			}
		}

		if m.Method == "turn/completed" {
			break
		}
	}
	return strings.Join(summaryParts, "\n\n"), nil
}
