package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"supabackup/internal/config"
)

// Event contains details about a completed backup run for webhook notification.
type Event struct {
	JobName    string    `json:"job_name"`
	Engine     string    `json:"engine"`
	Status     string    `json:"status"` // "success" or "failure"
	Timestamp  time.Time `json:"timestamp"`
	DurationMs int64     `json:"duration_ms"`
	FileSize   int64     `json:"file_size,omitempty"`
	BackupFile string    `json:"backup_file,omitempty"`
	Error      string    `json:"error,omitempty"`
	Message    string    `json:"message"`
	Text       string    `json:"text"` // Compatible with Slack and Discord incoming webhooks
}

// HTTPClient represents a minimal HTTP client interface.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Notifier dispatches webhook notifications.
type Notifier struct {
	client  HTTPClient
	timeout time.Duration
}

// NewNotifier creates a new webhook notifier.
func NewNotifier(client HTTPClient) *Notifier {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Notifier{
		client:  client,
		timeout: 10 * time.Second,
	}
}

// Send synchronously delivers an event to the configured webhook URL.
func (n *Notifier) Send(ctx context.Context, webhookURL string, event Event) error {
	if webhookURL == "" {
		return nil
	}

	if event.Text == "" {
		if event.Status == "success" {
			event.Text = fmt.Sprintf("[SupaBackup] Job %q (%s) SUCCESS in %dms (size: %d bytes)",
				event.JobName, event.Engine, event.DurationMs, event.FileSize)
		} else {
			sanitizedErr := config.RedactURL(event.Error)
			event.Text = fmt.Sprintf("[SupaBackup] Job %q (%s) FAILED in %dms: %s",
				event.JobName, event.Engine, event.DurationMs, sanitizedErr)
		}
	}
	event.Message = event.Text

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook payload: %w", err)
	}

	reqCtx := ctx
	if reqCtx == nil {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(context.Background(), n.timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, webhookURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("webhook responded with non-2xx status code %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// Dispatch asynchronously fires the webhook in a separate goroutine without blocking.
func (n *Notifier) Dispatch(webhookURL string, event Event) {
	if webhookURL == "" {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), n.timeout)
		defer cancel()
		if err := n.Send(ctx, webhookURL, event); err != nil {
			log.Printf("[notify] webhook error for job %s: %v", event.JobName, err)
		}
	}()
}
