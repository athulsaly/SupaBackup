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

	mu          sync.Mutex
	runningJobs map[string]bool
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
	}
}

// Start schedules all configured jobs and begins cron execution.
func (s *Scheduler) Start() error {
	for _, job := range s.config.Jobs {
		jobCopy := job // capture loop variable

		log.Printf("[scheduler] registering job %q (engine: %s, schedule: %q, retention: %d days)",
			jobCopy.Name, jobCopy.Engine, jobCopy.Schedule, jobCopy.RetentionDays)

		_, err := s.cron.AddFunc(jobCopy.Schedule, func() {
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
	}

	s.cron.Start()
	log.Printf("[scheduler] cron scheduler started with %d job(s)", len(s.config.Jobs))
	return nil
}

// Stop terminates the cron scheduler.
func (s *Scheduler) Stop() context.Context {
	return s.cron.Stop()
}

// RunJob executes a single backup run for the specified job.
func (s *Scheduler) RunJob(ctx context.Context, job config.JobConfig) error {
	s.mu.Lock()
	if s.runningJobs[job.Name] {
		s.mu.Unlock()
		log.Printf("[scheduler] job %q is already running; skipping scheduled run", job.Name)
		return fmt.Errorf("job %q is already running", job.Name)
	}
	s.runningJobs[job.Name] = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.runningJobs, job.Name)
		s.mu.Unlock()
	}()

	log.Printf("[scheduler] starting backup job %q (engine: %s)", job.Name, job.Engine)
	_ = s.tracker.RecordStart(job.Name)

	now := time.Now()
	startTime := now

	jobDir := filepath.Join(s.config.BackupDir, job.Name)
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
	log.Printf("[scheduler] backup job %q SUCCESS in %dms (file: %s, size: %d bytes)",
		job.Name, durationMs, filename, fileSize)

	// 4. Retention cleanup
	deleted, pruneErr := retention.Prune(jobDir, job.Name, job.RetentionDays, now)
	if pruneErr != nil {
		log.Printf("[retention] warning: pruning failed for job %q: %v", job.Name, pruneErr)
	} else if len(deleted) > 0 {
		log.Printf("[retention] pruned %d expired backup(s) for job %q", len(deleted), job.Name)
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
	log.Printf("[scheduler] backup job %q FAILED in %dms: %s", job.Name, durationMs, sanitizedErr)

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
