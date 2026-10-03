package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNotifier_SendSuccess(t *testing.T) {
	var receivedEvent Event
	var receivedContentType string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedContentType = r.Header.Get("Content-Type")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := json.Unmarshal(body, &receivedEvent); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok": true}`))
	}))
	defer server.Close()

	notifier := NewNotifier(nil)
	event := Event{
		JobName:    "supa-prod",
		Engine:     "postgres",
		Status:     "success",
		Timestamp:  time.Now(),
		DurationMs: 1200,
		FileSize:   1048576,
		BackupFile: "supa-prod_2026-10-03_02-00.dump",
	}

	err := notifier.Send(context.Background(), server.URL, event)
	if err != nil {
		t.Fatalf("expected successful send, got: %v", err)
	}

	if receivedContentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", receivedContentType)
	}
	if receivedEvent.JobName != "supa-prod" || receivedEvent.Status != "success" {
		t.Errorf("unexpected event payload: %+v", receivedEvent)
	}
	if receivedEvent.Text == "" {
		t.Errorf("expected non-empty Text field for Slack/Discord")
	}
}

func TestNotifier_EmptyURL(t *testing.T) {
	notifier := NewNotifier(nil)
	err := notifier.Send(context.Background(), "", Event{JobName: "test"})
	if err != nil {
		t.Errorf("expected nil error on empty URL, got: %v", err)
	}
}

func TestNotifier_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	notifier := NewNotifier(nil)
	err := notifier.Send(context.Background(), server.URL, Event{JobName: "test"})
	if err == nil {
		t.Fatalf("expected error on 500 response, got nil")
	}
}
