package retention

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Standard timestamp format matching $(date +%F_%H-%M)
const (
	TimestampFormatMinutes = "2006-01-02_15-04"
	TimestampFormatSeconds = "2006-01-02_15-04-05"
)

var timestampRegex = regexp.MustCompile(`_(\d{4}-\d{2}-\d{2}_\d{2}-\d{2}(?:-\d{2})?)`)

// Prune scans the job's backup directory and removes files older than retentionDays relative to now.
// It returns the list of deleted file paths.
func Prune(jobDir string, jobName string, retentionDays int, now time.Time) ([]string, error) {
	if retentionDays <= 0 {
		return nil, nil
	}

	entries, err := os.ReadDir(jobDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read directory %q for retention pruning: %w", jobDir, err)
	}

	cutoff := now.AddDate(0, 0, -retentionDays)
	var deleted []string

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()
		// Only consider files for this job
		if !strings.HasPrefix(filename, jobName+"_") {
			continue
		}

		fullPath := filepath.Join(jobDir, filename)
		fileTime, err := extractFileTime(filename)
		if err != nil {
			// If filename timestamp parsing fails, check file ModTime
			fileInfo, statErr := entry.Info()
			if statErr != nil {
				continue
			}
			fileTime = fileInfo.ModTime()
		}

		if fileTime.Before(cutoff) {
			if err := os.Remove(fullPath); err != nil {
				return deleted, fmt.Errorf("failed to remove expired backup %q: %w", fullPath, err)
			}
			deleted = append(deleted, fullPath)
		}
	}

	return deleted, nil
}

func extractFileTime(filename string) (time.Time, error) {
	matches := timestampRegex.FindStringSubmatch(filename)
	if len(matches) < 2 {
		return time.Time{}, fmt.Errorf("no timestamp found in filename: %s", filename)
	}

	tsStr := matches[1]
	if len(tsStr) == len(TimestampFormatMinutes) {
		t, err := time.ParseInLocation(TimestampFormatMinutes, tsStr, time.Local)
		if err == nil {
			return t, nil
		}
	} else if len(tsStr) == len(TimestampFormatSeconds) {
		t, err := time.ParseInLocation(TimestampFormatSeconds, tsStr, time.Local)
		if err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unrecognized timestamp format in %q", filename)
}
