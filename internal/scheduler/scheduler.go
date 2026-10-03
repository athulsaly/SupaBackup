package scheduler

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"supabackup/internal/config"
	"supabackup/internal/engine"
	"supabackup/internal/health"
	"supabackup/internal/notify"
	"supabackup/internal/retention"
)

// EngineResolver resolves an engine implementation by name.
type EngineResolver func(engineName string) (engine.Engine, error)

// Scheduler coordinates cron scheduling, backup executions, health tracking, and retention pruning.
type Scheduler struct {
	cron           *cron.Cron
	config         *config.AppConfig
	tracker        *health.Tracker
	notifier       *notify.Notifier
	engineResolver EngineResolver
	logBuffer      *health.LogBuffer

	mu          sync.Mutex
	runningJobs map[string]bool
	entryIDs    map[string]cron.EntryID
}

// NewScheduler creates a new Scheduler.
func NewScheduler(cfg *config.AppConfig, tracker *health.Tracker, notifier *notify.Notifier, resolver EngineResolver) *Scheduler {
	if resolver == nil {
		resolver = engine.Get
	}
	if notifier == nil {
		notifier = notify.NewNotifier(nil)
	}

	return &Scheduler{
		cron:           cron.New(),
		config:         cfg,
		tracker:        tracker,
		notifier:       notifier,
		engineResolver: resolver,
		runningJobs:    make(map[string]bool),
		entryIDs:       make(map[string]cron.EntryID),
	}
}

// SetLogBuffer attaches an in-memory LogBuffer for tracking recent logs per job.
func (s *Scheduler) SetLogBuffer(buffer *health.LogBuffer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logBuffer = buffer
}

// GetLogBuffer returns the attached LogBuffer.
func (s *Scheduler) GetLogBuffer() *health.LogBuffer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logBuffer
}

// GetConfig returns a pointer to the current active configuration.
func (s *Scheduler) GetConfig() *config.AppConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config
}

// Start schedules all configured jobs and begins cron execution.
func (s *Scheduler) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.startLocked()
}

func (s *Scheduler) startLocked() error {
	for _, job := range s.config.Jobs {
		jobCopy := job // capture loop variable

		log.Printf("[scheduler] registering job %q (engine: %s, schedule: %q, retention: %d days)",
			jobCopy.Name, jobCopy.Engine, jobCopy.Schedule, jobCopy.RetentionDays)

		id, err := s.cron.AddFunc(jobCopy.Schedule, func() {
			go func() {
				ctx := context.Background()
				if err := s.RunJob(ctx, jobCopy); err != nil {
					log.Printf("[scheduler] job %q finished with error: %v", jobCopy.Name, err)
				}
			}()
		})
		if err != nil {
			return fmt.Errorf("failed to schedule job %q with cron expression %q: %w", jobCopy.Name, jobCopy.Schedule, err)
		}
		s.entryIDs[jobCopy.Name] = id
	}

	s.cron.Start()
	log.Printf("[scheduler] cron scheduler started with %d job(s)", len(s.config.Jobs))
	return nil
}

// Stop terminates the cron scheduler.
func (s *Scheduler) Stop() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cron.Stop()
}

// Reload stops the current cron scheduler, reapplies new configuration jobs, and starts anew.
func (s *Scheduler) Reload(newCfg *config.AppConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	log.Printf("[scheduler] reloading scheduler with %d new job configuration(s)...", len(newCfg.Jobs))
	s.cron.Stop()

	s.cron = cron.New()
	s.config = newCfg
	s.entryIDs = make(map[string]cron.EntryID)

	if s.tracker != nil {
		if err := s.tracker.SyncJobs(newCfg.Jobs); err != nil {
			log.Printf("[scheduler] warning: tracker sync failed during reload: %v", err)
		}
	}

	return s.startLocked()
}

// IsJobRunning reports whether the specified job is actively executing a backup.
func (s *Scheduler) IsJobRunning(jobName string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runningJobs[jobName]
}

// GetNextRun returns the next scheduled run time for the specified job.
func (s *Scheduler) GetNextRun(jobName string) *time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.entryIDs[jobName]
	if !ok {
		return nil
	}

	entry := s.cron.Entry(id)
	next := entry.Next
	return &next
}

// RunJob executes a single backup run for the specified job.
func (s *Scheduler) RunJob(ctx context.Context, job config.JobConfig) error {
	s.mu.Lock()
	if s.runningJobs[job.Name] {
		s.mu.Unlock()
		s.logJob(job.Name, "[scheduler] job %q is already running; skipping run", job.Name)
		return fmt.Errorf("job %q is already running", job.Name)
	}
	s.runningJobs[job.Name] = true
	// Snapshot active configuration under lock so this run finishes safely on its snapshot
	cfgSnapshot := s.config
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.runningJobs, job.Name)
		s.mu.Unlock()
	}()

	s.logJob(job.Name, "[scheduler] starting backup job %q (engine: %s)", job.Name, job.Engine)
	_ = s.tracker.RecordStart(job.Name)

	now := time.Now()
	startTime := now

	backupDir := ""
	if cfgSnapshot != nil {
		backupDir = cfgSnapshot.BackupDir
	}
	jobDir := filepath.Join(backupDir, job.Name)
	if err := os.MkdirAll(jobDir, 0755); err != nil {
		runErr := fmt.Errorf("failed to create backup directory %q: %w", jobDir, err)
		s.handleFailure(job, startTime, runErr, "")
		return runErr
	}

	filename := fmt.Sprintf("%s_%s.dump", job.Name, now.Format(retention.TimestampFormatMinutes))
	targetPath := filepath.Join(jobDir, filename)

	eng, err := s.engineResolver(job.Engine)
	if err != nil {
		runErr := fmt.Errorf("failed to get engine %q: %w", job.Engine, err)
		s.handleFailure(job, startTime, runErr, targetPath)
		return runErr
	}

	// 1. Perform database dump
	if err := eng.Backup(ctx, job, targetPath); err != nil {
		s.handleFailure(job, startTime, err, targetPath)
		return err
	}

	// 2. Verify backup
	if err := eng.Verify(ctx, targetPath); err != nil {
		s.handleFailure(job, startTime, err, targetPath)
		return err
	}

	// 3. Success processing
	durationMs := time.Since(startTime).Milliseconds()
	fi, statErr := os.Stat(targetPath)
	fileSize := int64(0)
	if statErr == nil {
		fileSize = fi.Size()
	}

	_ = s.tracker.RecordSuccess(job.Name, durationMs, fileSize, filename)
	s.logJob(job.Name, "[scheduler] backup job %q SUCCESS in %dms (file: %s, size: %d bytes)",
		job.Name, durationMs, filename, fileSize)

	// 4. Retention cleanup
	deleted, pruneErr := retention.Prune(jobDir, job.Name, job.RetentionDays, now)
	if pruneErr != nil {
		s.logJob(job.Name, "[retention] warning: pruning failed for job %q: %v", job.Name, pruneErr)
	} else if len(deleted) > 0 {
		s.logJob(job.Name, "[retention] pruned %d expired backup(s) for job %q", len(deleted), job.Name)
	}

	// 5. Success webhook notification
	if job.WebhookURL != "" {
		s.notifier.Dispatch(job.WebhookURL, notify.Event{
			JobName:    job.Name,
			Engine:     job.Engine,
			Status:     "success",
			Timestamp:  now,
			DurationMs: durationMs,
			FileSize:   fileSize,
			BackupFile: filename,
		})
	}

	return nil
}

func (s *Scheduler) handleFailure(job config.JobConfig, startTime time.Time, err error, targetPath string) {
	durationMs := time.Since(startTime).Milliseconds()

	// Clean up partial / empty failed dump file if created
	if targetPath != "" {
		_ = os.Remove(targetPath)
	}

	_ = s.tracker.RecordFailure(job.Name, durationMs, err)

	sanitizedErr := config.RedactURL(err.Error())
	s.logJob(job.Name, "[scheduler] backup job %q FAILED in %dms: %s", job.Name, durationMs, sanitizedErr)

	if job.WebhookURL != "" {
		s.notifier.Dispatch(job.WebhookURL, notify.Event{
			JobName:    job.Name,
			Engine:     job.Engine,
			Status:     "failure",
			Timestamp:  startTime,
			DurationMs: durationMs,
			Error:      sanitizedErr,
		})
	}
}

func (s *Scheduler) logJob(jobName, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Print(msg)

	s.mu.Lock()
	buf := s.logBuffer
	s.mu.Unlock()

	if buf != nil && jobName != "" {
		buf.Add(jobName, msg)
	}
}
