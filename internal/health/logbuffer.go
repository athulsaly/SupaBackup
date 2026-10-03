package health

import (
	"sync"
	"time"

	"supabackup/internal/config"
)

const DefaultLogCapacity = 100

// LogBuffer maintains an in-memory ring buffer of recent log entries per job.
type LogBuffer struct {
	mu       sync.RWMutex
	capacity int
	logs     map[string][]string
}

// NewLogBuffer initializes a LogBuffer with the specified capacity per job.
func NewLogBuffer(capacity int) *LogBuffer {
	if capacity <= 0 {
		capacity = DefaultLogCapacity
	}
	return &LogBuffer{
		capacity: capacity,
		logs:     make(map[string][]string),
	}
}

// Add pushes a log entry for the specified job, passing it through RedactURL.
func (b *LogBuffer) Add(jobName, message string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	sanitized := config.RedactURL(message)
	timestamped := time.Now().Format("2006-01-02 15:04:05") + " " + sanitized

	entries := b.logs[jobName]
	if len(entries) >= b.capacity {
		// Drop oldest entry (shift left)
		entries = entries[1:]
	}
	b.logs[jobName] = append(entries, timestamped)
}

// Get returns a copy of recent log entries for the specified job.
func (b *LogBuffer) Get(jobName string) []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	entries, ok := b.logs[jobName]
	if !ok {
		return []string{}
	}

	result := make([]string, len(entries))
	copy(result, entries)
	return result
}
