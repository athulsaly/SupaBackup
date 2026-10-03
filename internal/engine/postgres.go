package engine

import (
	"bytes"
	"context"
	"fmt"

	"supabackup/internal/config"
)

// PostgresEngine implements database dumps and verification for PostgreSQL databases.
type PostgresEngine struct {
	runner CommandRunner
}

// NewPostgresEngine constructs a new PostgresEngine with the provided command runner.
func NewPostgresEngine(runner CommandRunner) *PostgresEngine {
	if runner == nil {
		runner = DefaultCommandRunner
	}
	return &PostgresEngine{runner: runner}
}

// Backup executes pg_dump with custom format to targetPath.
func (p *PostgresEngine) Backup(ctx context.Context, job config.JobConfig, targetPath string) error {
	// Reference method: pg_dump "$DB_URL" --format=custom --file="$TARGET_PATH"
	args := []string{
		job.URL,
		"--format=custom",
		"--file=" + targetPath,
	}

	cmd := p.runner(ctx, "pg_dump", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		sanitizedErr := config.RedactURL(stderr.String())
		return fmt.Errorf("pg_dump failed: %w (stderr: %s)", err, sanitizedErr)
	}

	return nil
}

// Verify validates that the backup file is non-empty and readable via pg_restore --list.
func (p *PostgresEngine) Verify(ctx context.Context, targetPath string) error {
	if err := CheckFileSize(targetPath); err != nil {
		return err
	}

	cmd := p.runner(ctx, "pg_restore", "--list", targetPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_restore --list verification failed: %w (stderr: %s)", err, stderr.String())
	}

	return nil
}
