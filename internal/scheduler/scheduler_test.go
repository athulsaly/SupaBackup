package scheduler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"supabackup/internal/config"
	"supabackup/internal/engine"
	"supabackup/internal/health"
	"supabackup/internal/notify"
)

type mockEngine struct {
	backupErr error
	verifyErr error
}

func (m *mockEngine) Backup(ctx context.Context, job config.JobConfig, targetPath string) error {
	if m.backupErr != nil {
		return m.backupErr
	}
	// Write dummy backup data
	return os.WriteFile(targetPath, []byte("valid-backup-data"), 0644)
}

func (m *mockEngine) Verify(ctx context.Context, targetPath string) error {
	return m.verifyErr
}

func TestScheduler_RunJob_Success(t *testing.T) {
	tempDir := t.TempDir()
	statusFile := filepath.Join(tempDir, "status.json")
	backupDir := filepath.Join(tempDir, "backups")

	job := config.JobConfig{
		Name:          "test-job",
		Engine:        "postgres",
		Schedule:      "* * * * *",
		RetentionDays: 7,
		URL:           "postgres://localhost/db",
	}

	appCfg := &config.AppConfig{
		BackupDir:  backupDir,
		StatusFile: statusFile,
		Jobs:       []config.JobConfig{job},
	}

	tracker, err := health.NewTracker(statusFile, appCfg.Jobs)
	if err != nil {
		t.Fatalf("failed to create tracker: %v", err)
	}

	mockEng := &mockEngine{}
	resolver := func(engineName string) (engine.Engine, error) {
		return mockEng, nil
	}

	sched := NewScheduler(appCfg, tracker, notify.NewNotifier(nil), resolver)

	err = sched.RunJob(context.Background(), job)
	if err != nil {
		t.Fatalf("expected RunJob to succeed, got: %v", err)
	}

	// Verify status updated to success
	st := tracker.GetStatus()
	jobStatus := st.Jobs[job.Name]
	if jobStatus.LastStatus != "success" {
		t.Errorf("expected job status 'success', got %q", jobStatus.LastStatus)
	}
	if jobStatus.LastFileSize <= 0 {
		t.Errorf("expected LastFileSize > 0, got %d", jobStatus.LastFileSize)
	}

	// Verify backup file exists in jobDir
	jobDir := filepath.Join(backupDir, job.Name)
	entries, err := os.ReadDir(jobDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected 1 backup file in %s, got: %v (len %d)", jobDir, err, len(entries))
	}
}

func TestScheduler_RunJob_FailureCleanup(t *testing.T) {
	tempDir := t.TempDir()
	statusFile := filepath.Join(tempDir, "status.json")
	backupDir := filepath.Join(tempDir, "backups")

	job := config.JobConfig{
		Name:          "fail-job",
		Engine:        "postgres",
		Schedule:      "* * * * *",
		RetentionDays: 7,
		URL:           "postgres://localhost/db",
	}

	appCfg := &config.AppConfig{
		BackupDir:  backupDir,
		StatusFile: statusFile,
		Jobs:       []config.JobConfig{job},
	}

	tracker, _ := health.NewTracker(statusFile, appCfg.Jobs)

	mockEng := &mockEngine{
		backupErr: errors.New("database connection refused"),
	}
	resolver := func(engineName string) (engine.Engine, error) {
		return mockEng, nil
	}

	sched := NewScheduler(appCfg, tracker, notify.NewNotifier(nil), resolver)

	err := sched.RunJob(context.Background(), job)
	if err == nil {
		t.Fatalf("expected RunJob to fail, got nil")
	}

	st := tracker.GetStatus()
	if st.Jobs[job.Name].LastStatus != "failure" {
		t.Errorf("expected job status 'failure', got %q", st.Jobs[job.Name].LastStatus)
	}
	if st.Jobs[job.Name].ConsecutiveFailures != 1 {
		t.Errorf("expected ConsecutiveFailures 1, got %d", st.Jobs[job.Name].ConsecutiveFailures)
	}
}

func TestScheduler_StartStop(t *testing.T) {
	tempDir := t.TempDir()
	statusFile := filepath.Join(tempDir, "status.json")

	appCfg := &config.AppConfig{
		BackupDir:  tempDir,
		StatusFile: statusFile,
		Jobs: []config.JobConfig{
			{
				Name:          "cron-job",
				Engine:        "postgres",
				Schedule:      "0 2 * * *",
				RetentionDays: 14,
				URL:           "postgres://localhost/db",
			},
		},
	}

	tracker, _ := health.NewTracker(statusFile, appCfg.Jobs)
	sched := NewScheduler(appCfg, tracker, nil, nil)

	if err := sched.Start(); err != nil {
		t.Fatalf("failed to start scheduler: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	sched.Stop()
}
