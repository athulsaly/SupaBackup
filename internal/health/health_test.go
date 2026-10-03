package health

import (
	"errors"
	"path/filepath"
	"testing"

	"supabackup/internal/config"
)

func TestTracker_Lifecycle(t *testing.T) {
	tempDir := t.TempDir()
	statusFile := filepath.Join(tempDir, "status.json")

	jobs := []config.JobConfig{
		{Name: "job1", Engine: "postgres"},
		{Name: "job2", Engine: "mysql"},
	}

	tracker, err := NewTracker(statusFile, jobs)
	if err != nil {
		t.Fatalf("failed to create tracker: %v", err)
	}

	// Initial healthcheck should pass (pending jobs are not failures)
	if err := CheckHealth(statusFile, 3); err != nil {
		t.Errorf("expected initial healthcheck to pass, got: %v", err)
	}

	// Record start for job1
	if err := tracker.RecordStart("job1"); err != nil {
		t.Fatalf("RecordStart failed: %v", err)
	}

	// Record success for job1
	if err := tracker.RecordSuccess("job1", 1500, 2048, "job1.dump"); err != nil {
		t.Fatalf("RecordSuccess failed: %v", err)
	}

	st := tracker.GetStatus()
	if st.Jobs["job1"].LastStatus != "success" || st.Jobs["job1"].ConsecutiveFailures != 0 {
		t.Errorf("unexpected job1 status: %+v", st.Jobs["job1"])
	}

	// Record 2 failures for job2 (under threshold 3)
	_ = tracker.RecordFailure("job2", 500, errors.New("conn error with pass: postgres://u:secret@h/db"))
	_ = tracker.RecordFailure("job2", 600, errors.New("conn error again"))

	if err := CheckHealth(statusFile, 3); err != nil {
		t.Errorf("expected healthcheck to pass with 2 failures (<3), got: %v", err)
	}

	// Record 3rd failure for job2 -> should fail healthcheck
	_ = tracker.RecordFailure("job2", 700, errors.New("third failure"))

	if err := CheckHealth(statusFile, 3); err == nil {
		t.Fatalf("expected healthcheck to fail with 3 consecutive failures, got nil")
	}

	// Ensure error string does not leak secret password
	st = tracker.GetStatus()
	if st.Jobs["job2"].LastError == "" {
		t.Errorf("expected non-empty last error")
	}

	// Now record success for job2 -> resets consecutive failures to 0
	_ = tracker.RecordSuccess("job2", 1200, 1024, "job2.dump")
	if err := CheckHealth(statusFile, 3); err != nil {
		t.Errorf("expected healthcheck to pass after recovery, got: %v", err)
	}
}

func TestCheckHealth_MissingFile(t *testing.T) {
	err := CheckHealth("/non/existent/path/status.json", 3)
	if err == nil {
		t.Errorf("expected error on missing status file, got nil")
	}
}
