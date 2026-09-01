package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"nusashell/domain"
	clock "nusashell/pkg/time"
)

// HTTPNotifier sends workflow run completion notifications as JSON POST
// requests to an external webhook URL. It implements application.RunNotifier.
type HTTPNotifier struct {
	Client *http.Client
}

func NewHTTPNotifier() *HTTPNotifier {
	return &HTTPNotifier{Client: &http.Client{Timeout: 300 * time.Second}}
}

type webhookPayload struct {
	RunID      string `json:"run_id"`
	Workflow   string `json:"workflow"`
	Status     string `json:"status"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	Jobs       int    `json:"jobs"`
	Failed     int    `json:"failed"`
	Success    int    `json:"success"`
}

func (n *HTTPNotifier) NotifyRunCompleted(ctx context.Context, url string, run *domain.WorkflowRun) error {
	if n == nil || n.Client == nil {
		return fmt.Errorf("notifier is not configured")
	}
	sum := run.Summary()
	payload := webhookPayload{
		RunID:    run.ID,
		Workflow: run.Definition.Name,
		Status:   string(run.Status),
		Jobs:     sum.Total,
		Failed:   sum.Failed,
		Success:  sum.Success,
	}
	if !run.StartedAt.IsZero() {
		payload.StartedAt = clock.NewTime(run.StartedAt).Format(time.RFC3339)
	}
	if !run.FinishedAt.IsZero() {
		payload.FinishedAt = clock.NewTime(run.FinishedAt).Format(time.RFC3339)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "NusaShell/1.0")
	resp, err := n.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook %s returned %s", url, resp.Status)
	}
	return nil
}
