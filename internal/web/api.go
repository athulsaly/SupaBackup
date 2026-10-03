package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"supabackup/internal/config"
	"supabackup/internal/health"
	"supabackup/internal/scheduler"
)

// ServerContext contains dependencies shared across web handlers.
type ServerContext struct {
	StartTime  time.Time
	Config     *config.AppConfig
	Tracker    *health.Tracker
	Scheduler  *scheduler.Scheduler
	LogBuffer  *health.LogBuffer
	Auth       *AuthManager
	BackupDir  string
	IsWritable bool
}

type JobView struct {
	Name                string     `json:"name"`
	Engine              string     `json:"engine"`
	Schedule            string     `json:"schedule"`
	RetentionDays       int        `json:"retention_days"`
	LastStatus          string     `json:"last_status"`
	LastRunTime         *time.Time `json:"last_run_time,omitempty"`
	LastDurationMs      int64      `json:"last_duration_ms"`
	LastFileSize        int64      `json:"last_file_size,omitempty"`
	LastBackupFile      string     `json:"last_backup_file,omitempty"`
	LastError           string     `json:"last_error,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	NextRunTime         *time.Time `json:"next_run_time,omitempty"`
	IsRunning           bool       `json:"is_running"`
}

type BackupFileView struct {
	Filename string    `json:"filename"`
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"mod_time"`
	Verified bool      `json:"verified"`
}

// HandleHealthz handles GET /healthz.
func (sc *ServerContext) HandleHealthz(w http.ResponseWriter, r *http.Request) {
	uptime := int64(time.Since(sc.StartTime).Seconds())
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"uptime_seconds": uptime,
		"version":        "0.2.0",
	})
}

// HandleGetJobs handles GET /api/jobs.
func (sc *ServerContext) HandleGetJobs(w http.ResponseWriter, r *http.Request) {
	cfg := sc.Scheduler.GetConfig()
	status := sc.Tracker.GetStatus()

	views := make([]JobView, 0, len(cfg.Jobs))
	for _, job := range cfg.Jobs {
		js, hasStatus := status.Jobs[job.Name]
		isRunning := sc.Scheduler.IsJobRunning(job.Name)

		view := JobView{
			Name:          job.Name,
			Engine:        job.Engine,
			Schedule:      job.Schedule,
			RetentionDays: job.RetentionDays,
			IsRunning:     isRunning,
		}

		if hasStatus {
			view.LastStatus = js.LastStatus
			if !js.LastRunTime.IsZero() {
				t := js.LastRunTime
				view.LastRunTime = &t
			}
			view.LastDurationMs = js.LastDurationMs
			view.LastFileSize = js.LastFileSize
			view.LastBackupFile = js.LastBackupFile
			view.LastError = config.RedactURL(js.LastError)
			view.ConsecutiveFailures = js.ConsecutiveFailures
		} else {
			view.LastStatus = "pending"
		}

		if isRunning {
			view.LastStatus = "running"
		}

		next := sc.Scheduler.GetNextRun(job.Name)
		if next != nil && !next.IsZero() {
			view.NextRunTime = next
		}

		views = append(views, view)
	}

	writeJSON(w, http.StatusOK, views)
}

// HandleRunJob handles POST /api/jobs/{name}/run.
func (sc *ServerContext) HandleRunJob(w http.ResponseWriter, r *http.Request, jobName string) {
	cfg := sc.Scheduler.GetConfig()
	var targetJob *config.JobConfig
	for _, j := range cfg.Jobs {
		if j.Name == jobName {
			jobCopy := j
			targetJob = &jobCopy
			break
		}
	}

	if targetJob == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("job %q not found", jobName))
		return
	}

	if sc.Scheduler.IsJobRunning(jobName) {
		writeError(w, http.StatusConflict, fmt.Sprintf("job %q is already running", jobName))
		return
	}

	// Trigger run asynchronously in background
	go func() {
		_ = sc.Scheduler.RunJob(context.Background(), *targetJob)
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{
		"message": "backup job triggered",
		"job":     jobName,
	})
}

// HandleGetBackups handles GET /api/jobs/{name}/backups.
func (sc *ServerContext) HandleGetBackups(w http.ResponseWriter, r *http.Request, jobName string) {
	jobDir := filepath.Join(sc.BackupDir, jobName)
	entries, err := os.ReadDir(jobDir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, []BackupFileView{})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to read backup directory")
		return
	}

	var files []BackupFileView
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, jobName+"_") || !strings.HasSuffix(name, ".dump") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		files = append(files, BackupFileView{
			Filename: name,
			Size:     info.Size(),
			ModTime:  info.ModTime(),
			Verified: info.Size() > 0,
		})
	}

	// Sort newest first
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime.After(files[j].ModTime)
	})

	writeJSON(w, http.StatusOK, files)
}

// HandleDownloadBackup handles GET /api/jobs/{name}/backups/{filename}/download with path traversal protection.
func (sc *ServerContext) HandleDownloadBackup(w http.ResponseWriter, r *http.Request, jobName, filename string) {
	// D-7: Strictly block any path traversal
	baseFilename := filepath.Base(filename)
	if baseFilename != filename || filename == "." || filename == ".." || strings.Contains(filename, "/") || strings.Contains(filename, "\\") {
		writeError(w, http.StatusBadRequest, "invalid filename: path traversal blocked")
		return
	}

	jobDir := filepath.Clean(filepath.Join(sc.BackupDir, jobName))
	targetPath := filepath.Clean(filepath.Join(jobDir, baseFilename))

	rel, err := filepath.Rel(jobDir, targetPath)
	if err != nil || strings.HasPrefix(rel, "..") || rel == "." || strings.Contains(rel, "/") || strings.Contains(rel, "\\") {
		writeError(w, http.StatusBadRequest, "path traversal attempt detected")
		return
	}

	info, err := os.Stat(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "backup file not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to access backup file")
		return
	}

	if info.IsDir() {
		writeError(w, http.StatusBadRequest, "target is a directory")
		return
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", baseFilename))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	http.ServeFile(w, r, targetPath)
}

// HandleGetLogs handles GET /api/jobs/{name}/logs.
func (sc *ServerContext) HandleGetLogs(w http.ResponseWriter, r *http.Request, jobName string) {
	var logs []string
	if sc.LogBuffer != nil {
		logs = sc.LogBuffer.Get(jobName)
	}
	if logs == nil {
		logs = []string{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"job":  jobName,
		"logs": logs,
	})
}

// HandleGetConfig handles GET /api/config.
func (sc *ServerContext) HandleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfgPath := sc.Config.ConfigPath
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to read config file %q: %v", cfgPath, err))
		return
	}

	// Always redact any secret connection strings or passwords
	redactedYAML := config.RedactURL(string(data))

	writeJSON(w, http.StatusOK, map[string]any{
		"config_path": cfgPath,
		"writable":    sc.IsWritable,
		"yaml":        redactedYAML,
	})
}

// HandleSaveConfig handles POST /api/config.
func (sc *ServerContext) HandleSaveConfig(w http.ResponseWriter, r *http.Request) {
	if !sc.IsWritable {
		writeError(w, http.StatusForbidden, "configuration editing is disabled (set DASHBOARD_CONFIG_WRITABLE=true to enable)")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024)) // 1MB limit
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read configuration payload")
		return
	}

	// Validate configuration syntax by writing to temp file and parsing
	tmpFile, err := os.CreateTemp("", "supabackup-validate-*.yaml")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create validation temp file")
		return
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(body); err != nil {
		tmpFile.Close()
		writeError(w, http.StatusInternalServerError, "failed to write validation temp file")
		return
	}
	tmpFile.Close()

	newCfg, err := config.Load(tmpPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid configuration: %v", err))
		return
	}

	// Persist to actual config path
	cfgPath := sc.Config.ConfigPath
	if err := os.WriteFile(cfgPath, body, 0644); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to save configuration: %v", err))
		return
	}

	newCfg.ConfigPath = cfgPath
	sc.Config = newCfg

	// Hot-reload scheduler
	if err := sc.Scheduler.Reload(newCfg); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("configuration saved, but scheduler reload failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "configuration saved and scheduler reloaded successfully",
	})
}

func writeJSON(w http.ResponseWriter, statusCode int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, statusCode int, message string) {
	writeJSON(w, statusCode, map[string]string{
		"error": message,
	})
}
