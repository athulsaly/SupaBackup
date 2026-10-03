package health

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"supabackup/internal/config"
)

// Default failure threshold before marking container unhealthy
const DefaultMaxConsecutiveFailures = 3

// JobStatus stores the latest execution state for an individual backup job.
type JobStatus struct {
	JobName             string    `json:"job_name"`
	Engine              string    `json:"engine"`
	LastRunTime         time.Time `json:"last_run_time"`
	LastStatus          string    `json:"last_status"` // "pending", "running", "success", "failure"
	LastDurationMs      int64     `json:"last_duration_ms"`
	LastFileSize        int64     `json:"last_file_size,omitempty"`
	LastBackupFile      string    `json:"last_backup_file,omitempty"`
	LastError           string    `json:"last_error,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
}

// DaemonStatus captures the entire daemon's runtime status.
type DaemonStatus struct {
	StartTime  time.Time             `json:"start_time"`
	LastUpdate time.Time             `json:"last_update"`
	Jobs       map[string]*JobStatus `json:"jobs"`
}

// Tracker manages recording job states and saving status.json.
type Tracker struct {
	mu       sync.RWMutex
	filePath string
	status   DaemonStatus
}

// NewTracker creates a new Tracker and persists the initial status.
func NewTracker(filePath string, jobs []config.JobConfig) (*Tracker, error) {
	now := time.Now()
	jobMap := make(map[string]*JobStatus, len(jobs))

	for _, job := range jobs {
		jobMap[job.Name] = &JobStatus{
			JobName:    job.Name,
			Engine:     job.Engine,
			LastStatus: "pending",
		}
	}

	t := &Tracker{
		filePath: filePath,
		status: DaemonStatus{
			StartTime:  now,
			LastUpdate: now,
			Jobs:       jobMap,
		},
	}

	if err := t.persist(); err != nil {
		return nil, fmt.Errorf("failed to write initial status file %q: %w", filePath, err)
	}

	return t, nil
}

// RecordStart marks a job as currently running.
func (t *Tracker) RecordStart(jobName string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	js, ok := t.status.Jobs[jobName]
	if !ok {
		return fmt.Errorf("job %q not found in tracker", jobName)
	}

	js.LastStatus = "running"
	js.LastRunTime = time.Now()
	t.status.LastUpdate = time.Now()

	return t.persist()
}

// RecordSuccess records a successful job execution and resets consecutive failures.
func (t *Tracker) RecordSuccess(jobName string, durationMs int64, fileSize int64, backupFile string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	js, ok := t.status.Jobs[jobName]
	if !ok {
		return fmt.Errorf("job %q not found in tracker", jobName)
	}

	js.LastStatus = "success"
	js.LastDurationMs = durationMs
	js.LastFileSize = fileSize
	js.LastBackupFile = backupFile
	js.LastError = ""
	js.ConsecutiveFailures = 0
	t.status.LastUpdate = time.Now()

	return t.persist()
}

// RecordFailure records a failed job execution and increments consecutive failures.
func (t *Tracker) RecordFailure(jobName string, durationMs int64, err error) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	js, ok := t.status.Jobs[jobName]
	if !ok {
		return fmt.Errorf("job %q not found in tracker", jobName)
	}

	js.LastStatus = "failure"
	js.LastDurationMs = durationMs
	if err != nil {
		js.LastError = config.RedactURL(err.Error())
	}
	js.ConsecutiveFailures++
	t.status.LastUpdate = time.Now()

	return t.persist()
}

// GetStatus returns a snapshot copy of the current status.
func (t *Tracker) GetStatus() DaemonStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()

	copyStatus := DaemonStatus{
		StartTime:  t.status.StartTime,
		LastUpdate: t.status.LastUpdate,
		Jobs:       make(map[string]*JobStatus, len(t.status.Jobs)),
	}

	for k, v := range t.status.Jobs {
		jobCopy := *v
		copyStatus.Jobs[k] = &jobCopy
	}

	return copyStatus
}

func (t *Tracker) persist() error {
	dir := filepath.Dir(t.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create status directory %q: %w", dir, err)
	}

	data, err := json.MarshalIndent(t.status, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal status JSON: %w", err)
	}

	tmpFile := t.filePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp status file %q: %w", tmpFile, err)
	}

	if err := os.Rename(tmpFile, t.filePath); err != nil {
		return fmt.Errorf("failed to replace status file %q: %w", t.filePath, err)
	}

	return nil
}

// SyncJobs synchronizes the tracked jobs map with updated job configs after a reload.
func (t *Tracker) SyncJobs(jobs []config.JobConfig) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	active := make(map[string]bool, len(jobs))
	for _, j := range jobs {
		active[j.Name] = true
		if existing, exists := t.status.Jobs[j.Name]; exists {
			existing.Engine = j.Engine
		} else {
			t.status.Jobs[j.Name] = &JobStatus{
				JobName:    j.Name,
				Engine:     j.Engine,
				LastStatus: "pending",
			}
		}
	}

	for name := range t.status.Jobs {
		if !active[name] {
			delete(t.status.Jobs, name)
		}
	}

	t.status.LastUpdate = time.Now()
	return t.persist()
}

// CheckHealth reads the status file and checks if any jobs have persistently failed.
func CheckHealth(statusFilePath string, maxConsecutiveFailures int) error {
	if maxConsecutiveFailures <= 0 {
		maxConsecutiveFailures = DefaultMaxConsecutiveFailures
	}

	data, err := os.ReadFile(statusFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("status file %q does not exist", statusFilePath)
		}
		return fmt.Errorf("failed to read status file: %w", err)
	}

	var status DaemonStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return fmt.Errorf("failed to parse status file JSON: %w", err)
	}

	for jobName, job := range status.Jobs {
		if job.ConsecutiveFailures >= maxConsecutiveFailures {
			return fmt.Errorf("job %q unhealthy: %d consecutive failures (last error: %s)",
				jobName, job.ConsecutiveFailures, job.LastError)
		}
	}

	return nil
}

