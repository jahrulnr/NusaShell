package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"nusashell/domain"
)

// modelListTimeout is the maximum time to wait for a model/list response.
const modelListTimeout = 30 * time.Second

// modelListResponse is the JSON-RPC response from model/list.
type modelListResponse struct {
	Result struct {
		Data []struct {
			ID                        string `json:"id"`
			Model                     string `json:"model"`
			DisplayName               string `json:"displayName"`
			Description               string `json:"description"`
			Hidden                    bool   `json:"hidden"`
			DefaultReasoningEffort    string `json:"defaultReasoningEffort"`
			SupportedReasoningEfforts []struct {
				ReasoningEffort string `json:"reasoningEffort"`
			} `json:"supportedReasoningEfforts"`
			IsDefault bool `json:"isDefault"`
		} `json:"data"`
		NextCursor *string `json:"nextCursor"`
	} `json:"result"`
}

// ListModelsViaSubprocess spawns a Codex app-server subprocess and calls
// model/list to fetch the real, current model catalog. This is the Codex
// equivalent of the /models endpoint that other providers expose via HTTP.
//
// The model list is account-aware: some models may be gated by the user's
// ChatGPT subscription tier (Plus, Pro, etc.). The subprocess uses the
// selected NusaShell Codex account in an isolated temporary CODEX_HOME.
func ListModelsViaSubprocess(ctx context.Context, accessToken, accountID string) ([]domain.Model, error) {
	subCtx, cancel := context.WithTimeout(ctx, modelListTimeout)
	defer cancel()

	binPath, err := resolveCodexBinary(ctx)
	if err != nil {
		return nil, fmt.Errorf("codex model list: resolve binary: %w", err)
	}

	dir, err := os.MkdirTemp("", "nusashell-codex-models-*")
	if err != nil {
		return nil, fmt.Errorf("codex model list: create temp dir: %w", err)
	}
	defer os.RemoveAll(dir)
	codexHome := filepath.Join(dir, "codex-home")
	if err := writeModelListAuth(codexHome, accessToken, accountID); err != nil {
		return nil, fmt.Errorf("codex model list: prepare selected account: %w", err)
	}

	cmd := exec.CommandContext(subCtx, binPath, "app-server",
		"-c", "sandbox_mode=danger-full-access",
		"-c", "approval_policy=never",
	)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CODEX_HOME="+codexHome)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("codex model list: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("codex model list: stdout pipe: %w", err)
	}
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("codex model list: start subprocess: %w", err)
	}
	defer cmd.Process.Kill()

	// Handshake: initialize → initialized
	if err := sendRPC(stdin, 1, "initialize", map[string]any{
		"clientInfo": map[string]any{"name": "nusashell", "version": "0.1.0"},
	}); err != nil {
		return nil, fmt.Errorf("codex model list: send initialize: %w", err)
	}

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 32<<20)

	// Wait for initialize response
	if _, err := readRPCResponse(sc, 1); err != nil {
		return nil, fmt.Errorf("codex model list: initialize handshake: %w", err)
	}

	// Send initialized notification
	if err := sendRPCNotif(stdin, "initialized", nil); err != nil {
		return nil, fmt.Errorf("codex model list: send initialized: %w", err)
	}

	// Call model/list
	if err := sendRPC(stdin, 2, "model/list", map[string]any{}); err != nil {
		return nil, fmt.Errorf("codex model list: send model/list: %w", err)
	}

	// Read model/list response
	respLine, err := readRPCResponse(sc, 2)
	if err != nil {
		return nil, fmt.Errorf("codex model list: read response: %w", err)
	}

	var resp modelListResponse
	if err := json.Unmarshal([]byte(respLine), &resp); err != nil {
		return nil, fmt.Errorf("codex model list: parse response: %w", err)
	}

	// Convert to domain.Model. The app-server model/list response does not
	// expose context_window, so the CLI cache is retained only as a discovery
	// hint. The application layer replaces Codex context metadata with the
	// public models.dev value before a direct chat turn when available.
	cacheCtx := loadCodexModelsCache()
	models := make([]domain.Model, 0, len(resp.Result.Data))
	for _, m := range resp.Result.Data {
		if m.Hidden {
			continue
		}
		var efforts []string
		for _, e := range m.SupportedReasoningEfforts {
			efforts = append(efforts, e.ReasoningEffort)
		}
		dm := domain.Model{
			ID:               m.ID,
			Description:      m.Description,
			SupportedEfforts: efforts,
			DefaultEffort:    m.DefaultReasoningEffort,
		}
		if cw, ok := cacheCtx[m.ID]; ok {
			dm.Context = cw
		}
		models = append(models, dm)
	}

	if len(models) == 0 {
		return nil, fmt.Errorf("codex model list: no models returned (check codex login)")
	}

	return models, nil
}

func writeModelListAuth(codexHome, accessToken, accountID string) error {
	if accessToken == "" {
		return fmt.Errorf("access token is empty")
	}
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return err
	}
	auth := codexCLIAuthFile{AuthMode: "chatgpt"}
	auth.Tokens.AccessToken = accessToken
	auth.Tokens.AccountID = accountID
	data, err := json.Marshal(auth)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(codexHome, "auth.json"), data, 0o600)
}
