package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Default configuration constants
const (
	DefaultPUID        = 568
	DefaultPGID        = 568
	DefaultConfigPath  = "/etc/supabackup/config.yaml"
	DefaultBackupDir   = "/backups"
	DefaultStatusFile  = "/var/run/supabackup/status.json"
	DefaultRetentionDays = 14
)

// JobConfig defines the configuration for a single database backup job.
type JobConfig struct {
	Name           string `yaml:"name"`
	Engine         string `yaml:"engine"`
	Schedule       string `yaml:"schedule"`
	RetentionDays  int    `yaml:"retention_days"`
	URLEnv         string `yaml:"url_env"`
	WebhookURLEnv  string `yaml:"webhook_url_env,omitempty"`

	// Resolved runtime values (never serialized to YAML or logged with secrets)
	URL        string `yaml:"-"`
	WebhookURL string `yaml:"-"`
}

// AppConfig is the top-level application configuration.
type AppConfig struct {
	PUID       int         `yaml:"puid"`
	PGID       int         `yaml:"pgid"`
	BackupDir  string      `yaml:"backup_dir"`
	ConfigPath string      `yaml:"config_path"`
	StatusFile string      `yaml:"status_file"`
	Jobs       []JobConfig `yaml:"jobs"`
}

var jobNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_\-]+$`)
var passwordRegex = regexp.MustCompile(`://([^:]+):([^@]+)@`)

// Load reads configuration from the given path (or CONFIG_PATH env), resolves environment variables and *_FILE secrets.
func Load(explicitPath string) (*AppConfig, error) {
	configPath := explicitPath
	if configPath == "" {
		configPath = os.Getenv("CONFIG_PATH")
	}
	if configPath == "" {
		configPath = DefaultConfigPath
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", configPath, err)
	}

	var cfg AppConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse YAML config: %w", err)
	}

	cfg.ConfigPath = configPath

	// Environment variable overrides & defaults for AppConfig
	cfg.PUID = resolveIntEnv("PUID", cfg.PUID, DefaultPUID)
	cfg.PGID = resolveIntEnv("PGID", cfg.PGID, DefaultPGID)

	if backupDir := os.Getenv("BACKUP_DIR"); backupDir != "" {
		cfg.BackupDir = backupDir
	} else if cfg.BackupDir == "" {
		cfg.BackupDir = DefaultBackupDir
	}

	if statusFile := os.Getenv("STATUS_FILE"); statusFile != "" {
		cfg.StatusFile = statusFile
	} else if cfg.StatusFile == "" {
		cfg.StatusFile = DefaultStatusFile
	}

	if len(cfg.Jobs) == 0 {
		return nil, fmt.Errorf("config error: at least one backup job must be configured")
	}

	seenNames := make(map[string]bool)
	for i := range cfg.Jobs {
		job := &cfg.Jobs[i]

		if job.Name == "" {
			return nil, fmt.Errorf("job[%d]: name is required", i)
		}
		if !jobNameRegex.MatchString(job.Name) {
			return nil, fmt.Errorf("job[%d] (%s): name must contain only alphanumeric characters, dashes, and underscores", i, job.Name)
		}
		if seenNames[job.Name] {
			return nil, fmt.Errorf("job[%d]: duplicate job name %q", i, job.Name)
		}
		seenNames[job.Name] = true

		job.Engine = strings.ToLower(strings.TrimSpace(job.Engine))
		switch job.Engine {
		case "postgres", "postgresql":
			job.Engine = "postgres"
		case "mysql", "mariadb":
			job.Engine = "mysql"
		case "mongo", "mongodb":
			job.Engine = "mongo"
		default:
			return nil, fmt.Errorf("job %q: unsupported engine %q (supported: postgres, mysql, mongo)", job.Name, job.Engine)
		}

		if job.Schedule == "" {
			return nil, fmt.Errorf("job %q: schedule is required", job.Name)
		}

		if job.RetentionDays <= 0 {
			job.RetentionDays = DefaultRetentionDays
		}

		if job.URLEnv == "" {
			return nil, fmt.Errorf("job %q: url_env is required", job.Name)
		}

		// Resolve DB URL from environment or *_FILE
		dbURL, err := ResolveSecret(job.URLEnv)
		if err != nil {
			return nil, fmt.Errorf("job %q: failed to resolve secret for url_env %q: %w", job.Name, job.URLEnv, err)
		}
		if dbURL == "" {
			return nil, fmt.Errorf("job %q: url_env %q (or %s_FILE) resolved to empty value", job.Name, job.URLEnv, job.URLEnv)
		}
		job.URL = dbURL

		// Resolve Webhook URL if configured
		if job.WebhookURLEnv != "" {
			webhookURL, err := ResolveSecret(job.WebhookURLEnv)
			if err != nil {
				return nil, fmt.Errorf("job %q: failed to resolve secret for webhook_url_env %q: %w", job.Name, job.WebhookURLEnv, err)
			}
			job.WebhookURL = webhookURL
		}
	}

	return &cfg, nil
}

// ResolveSecret resolves an environment variable, falling back to <ENV_VAR>_FILE for secret files.
func ResolveSecret(envVarName string) (string, error) {
	if envVarName == "" {
		return "", nil
	}

	if val := os.Getenv(envVarName); val != "" {
		return strings.TrimSpace(val), nil
	}

	filePath := os.Getenv(envVarName + "_FILE")
	if filePath != "" {
		cleanedPath := filepath.Clean(filePath)
		content, err := os.ReadFile(cleanedPath)
		if err != nil {
			return "", fmt.Errorf("unable to read secret file %q: %w", filePath, err)
		}
		return strings.TrimSpace(string(content)), nil
	}

	return "", nil
}

func resolveIntEnv(envName string, configVal, defaultVal int) int {
	if envVal := os.Getenv(envName); envVal != "" {
		if parsed, err := strconv.Atoi(envVal); err == nil && parsed > 0 {
			return parsed
		}
	}
	if configVal > 0 {
		return configVal
	}
	return defaultVal
}

// RedactURL strips passwords and tokens from connection strings and URLs for safe logging.
func RedactURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}

	return passwordRegex.ReplaceAllString(rawURL, `://$1:***@`)
}
