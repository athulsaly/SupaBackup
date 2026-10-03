package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"supabackup/internal/config"
)

// Engine defines the plug-in interface for database backup implementations.
type Engine interface {
	// Backup executes the dump and writes the output to targetPath
	Backup(ctx context.Context, job config.JobConfig, targetPath string) error

	// Verify checks the integrity of the backup (e.g., size check, pg_restore --list)
	Verify(ctx context.Context, targetPath string) error
}

// CommandRunner abstracts exec.CommandContext for testing and execution.
type CommandRunner func(ctx context.Context, name string, args ...string) *exec.Cmd

var DefaultCommandRunner CommandRunner = exec.CommandContext

// Get returns the matching Engine implementation for the given engine name.
func Get(engineName string) (Engine, error) {
	switch engineName {
	case "postgres":
		return NewPostgresEngine(DefaultCommandRunner), nil
	case "mysql":
		return NewMySQLEngine(DefaultCommandRunner), nil
	case "mongo":
		return NewMongoEngine(), nil
	default:
		return nil, fmt.Errorf("unsupported database engine: %q", engineName)
	}
}

// CheckFileSize confirms that the backup file exists and has size > 0.
func CheckFileSize(targetPath string) error {
	info, err := os.Stat(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("backup file %q was not created", targetPath)
		}
		return fmt.Errorf("failed to stat backup file %q: %w", targetPath, err)
	}

	if info.Size() == 0 {
		return fmt.Errorf("backup file %q is empty (0 bytes)", targetPath)
	}

	return nil
}
