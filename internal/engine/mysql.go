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

// MySQLEngine implements database dumps and verification for MySQL and MariaDB databases.
type MySQLEngine struct {
	runner CommandRunner
}

// NewMySQLEngine constructs a new MySQLEngine with the provided command runner.
func NewMySQLEngine(runner CommandRunner) *MySQLEngine {
	if runner == nil {
		runner = DefaultCommandRunner
	}
	return &MySQLEngine{runner: runner}
}

// Backup executes mysqldump against the target MySQL/MariaDB database.
func (m *MySQLEngine) Backup(ctx context.Context, job config.JobConfig, targetPath string) error {
	args, password, err := buildMySQLArgs(job.URL, targetPath)
	if err != nil {
		return fmt.Errorf("failed to prepare mysqldump parameters: %w", err)
	}

	cmd := m.runner(ctx, "mysqldump", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if password != "" {
		cmd.Env = append(os.Environ(), "MYSQL_PWD="+password)
	}

	if err := cmd.Run(); err != nil {
		sanitizedErr := config.RedactURL(stderr.String())
		return fmt.Errorf("mysqldump failed: %w (stderr: %s)", err, sanitizedErr)
	}

	return nil
}

// Verify validates that the backup file is non-empty and contains valid dump markers.
func (m *MySQLEngine) Verify(ctx context.Context, targetPath string) error {
	if err := CheckFileSize(targetPath); err != nil {
		return err
	}

	// Basic content sanity check: read last 4KB to check for dump completion or valid SQL
	file, err := os.Open(targetPath)
	if err != nil {
		return fmt.Errorf("failed to open backup file for verification: %w", err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat backup file: %w", err)
	}

	bufSize := int64(4096)
	if stat.Size() < bufSize {
		bufSize = stat.Size()
	}

	buf := make([]byte, bufSize)
	offset := stat.Size() - bufSize
	if _, err := file.ReadAt(buf, offset); err != nil {
		return fmt.Errorf("failed to read backup file trailer: %w", err)
	}

	trailer := string(buf)
	// Check for common markers or SQL comments
	if !strings.Contains(trailer, "Dump completed") && !strings.Contains(trailer, ";") && !strings.Contains(trailer, "--") {
		return fmt.Errorf("backup file does not appear to contain valid SQL dump trailer")
	}

	return nil
}

func buildMySQLArgs(rawURL, targetPath string) ([]string, string, error) {
	var host, port, user, password, dbName string

	if strings.HasPrefix(rawURL, "mysql://") || strings.HasPrefix(rawURL, "mariadb://") {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return nil, "", fmt.Errorf("invalid MySQL URL: %w", err)
		}

		host = parsed.Hostname()
		port = parsed.Port()
		if parsed.User != nil {
			user = parsed.User.Username()
			password, _ = parsed.User.Password()
		}
		dbName = strings.TrimPrefix(parsed.Path, "/")
	} else if strings.Contains(rawURL, "@tcp(") {
		// DSN format: username:password@tcp(host:port)/dbname
		parts := strings.Split(rawURL, "@tcp(")
		if len(parts) == 2 {
			userPass := parts[0]
			if strings.Contains(userPass, ":") {
				up := strings.SplitN(userPass, ":", 2)
				user = up[0]
				password = up[1]
			} else {
				user = userPass
			}

			rest := parts[1]
			idx := strings.Index(rest, ")")
			if idx > 0 {
				hostPort := rest[:idx]
				if strings.Contains(hostPort, ":") {
					hp := strings.SplitN(hostPort, ":", 2)
					host = hp[0]
					port = hp[1]
				} else {
					host = hostPort
				}
				dbPart := rest[idx+1:]
				dbName = strings.TrimPrefix(dbPart, "/")
				if qIdx := strings.Index(dbName, "?"); qIdx >= 0 {
					dbName = dbName[:qIdx]
				}
			}
		}
	} else {
		// Fallback: treated as database name or simple argument
		dbName = rawURL
	}

	if dbName == "" {
		return nil, "", fmt.Errorf("database name could not be determined from MySQL connection string")
	}

	args := []string{
		"--single-transaction",
		"--quick",
		"--result-file=" + targetPath,
	}

	if host != "" {
		args = append(args, "-h", host)
	}
	if port != "" {
		args = append(args, "-P", port)
	} else if host != "" {
		args = append(args, "-P", "3306")
	}
	if user != "" {
		args = append(args, "-u", user)
	}

	args = append(args, dbName)

	return args, password, nil
}
