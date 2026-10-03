package engine

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

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

// Backup executes pg_dump with custom format to targetPath, keeping secrets out of CLI args.
func (p *PostgresEngine) Backup(ctx context.Context, job config.JobConfig, targetPath string) error {
	args, password, sslMode, err := buildPostgresArgs(job, targetPath)
	if err != nil {
		return fmt.Errorf("failed to prepare pg_dump parameters: %w", err)
	}

	cmd := p.runner(ctx, "pg_dump", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	cmd.Env = os.Environ()
	if password != "" {
		cmd.Env = append(cmd.Env, "PGPASSWORD="+password)
	}
	if sslMode != "" {
		cmd.Env = append(cmd.Env, "PGSSLMODE="+sslMode)
	}

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
		sanitizedErr := config.RedactURL(stderr.String())
		return fmt.Errorf("pg_restore --list verification failed: %w (stderr: %s)", err, sanitizedErr)
	}

	return nil
}

func buildPostgresArgs(job config.JobConfig, targetPath string) ([]string, string, string, error) {
	var host, port, user, password, dbName, sslMode string
	rawURL := job.URL

	if strings.HasPrefix(rawURL, "postgres://") || strings.HasPrefix(rawURL, "postgresql://") {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return nil, "", "", fmt.Errorf("invalid PostgreSQL URL: %w", err)
		}

		host = parsed.Hostname()
		port = parsed.Port()
		if parsed.User != nil {
			user = parsed.User.Username()
			password, _ = parsed.User.Password()
		}
		dbName = strings.TrimPrefix(parsed.Path, "/")
		if q := parsed.Query(); len(q) > 0 {
			sslMode = q.Get("sslmode")
		}
	} else if strings.Contains(rawURL, "=") {
		// Key-value connection string format: host=... port=... user=... password=... dbname=...
		for _, part := range strings.Fields(rawURL) {
			kv := strings.SplitN(part, "=", 2)
			if len(kv) == 2 {
				k, v := strings.ToLower(kv[0]), kv[1]
				switch k {
				case "host":
					host = v
				case "port":
					port = v
				case "user":
					user = v
				case "password":
					password = v
				case "dbname":
					dbName = v
				case "sslmode":
					sslMode = v
				}
			}
		}
	} else {
		dbName = rawURL
	}

	args := []string{
		"--format=custom",
		"--file=" + targetPath,
	}

	if host != "" {
		args = append(args, "-h", host)
	}
	if port != "" {
		args = append(args, "-p", port)
	}
	if user != "" {
		args = append(args, "-U", user)
	}
	if dbName != "" {
		args = append(args, "-d", dbName)
	}

	// Add optional schema exclusions
	for _, schema := range job.ExcludeSchemas {
		schema = strings.TrimSpace(schema)
		if schema != "" {
			args = append(args, "--exclude-schema="+schema)
		}
	}

	return args, password, sslMode, nil
}
