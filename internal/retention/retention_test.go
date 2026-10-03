package retention

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPrune_RetentionDays(t *testing.T) {
	tempDir := t.TempDir()
	jobName := "supabase-main"
	jobDir := filepath.Join(tempDir, jobName)
	if err := os.MkdirAll(jobDir, 0755); err != nil {
		t.Fatalf("failed to create jobDir: %v", err)
	}

	// Current reference time: 2026-10-15 12:00
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.Local)

	// Files to create:
	// 1. 2 days old (keep)
	// 2. 10 days old (keep)
	// 3. 15 days old (delete if retention is 14 days)
	// 4. 30 days old (delete)
	// 5. File belonging to another job (keep)
	files := []struct {
		name         string
		expectedKeep bool
	}{
		{"supabase-main_2026-10-13_12-00.dump", true},
		{"supabase-main_2026-10-05_12-00.dump", true},
		{"supabase-main_2026-09-30_12-00.dump", false},
		{"supabase-main_2026-09-15_12-00.dump", false},
		{"other-job_2026-09-01_12-00.dump", true},
	}

	for _, f := range files {
		path := filepath.Join(jobDir, f.name)
		if err := os.WriteFile(path, []byte("data"), 0644); err != nil {
			t.Fatalf("failed to write test file %s: %v", f.name, err)
		}
	}

	deleted, err := Prune(jobDir, jobName, 14, now)
	if err != nil {
		t.Fatalf("Prune failed: %v", err)
	}

	if len(deleted) != 2 {
		t.Errorf("expected 2 deleted files, got %d (%v)", len(deleted), deleted)
	}

	for _, f := range files {
		path := filepath.Join(jobDir, f.name)
		_, statErr := os.Stat(path)
		exists := statErr == nil

		if f.expectedKeep && !exists {
			t.Errorf("expected file %s to be kept, but it was deleted", f.name)
		}
		if !f.expectedKeep && exists {
			t.Errorf("expected file %s to be deleted, but it still exists", f.name)
		}
	}
}

func TestPrune_NonExistentDir(t *testing.T) {
	deleted, err := Prune("/path/does/not/exist", "test", 14, time.Now())
	if err != nil {
		t.Errorf("expected nil error on missing dir, got: %v", err)
	}
	if len(deleted) != 0 {
		t.Errorf("expected 0 deleted files, got: %d", len(deleted))
	}
}
