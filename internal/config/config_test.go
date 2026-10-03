package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigLoad_Success(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	yamlContent := `
jobs:
  - name: "supabase-main"
    engine: "postgres"
    schedule: "0 2 * * *"
    retention_days: 14
    url_env: "TEST_DB_URL"
    webhook_url_env: "TEST_WEBHOOK_URL"
  - name: "mysql-legacy"
    engine: "mysql"
    schedule: "0 4 * * 0"
    retention_days: 30
    url_env: "TEST_MYSQL_URL"
`
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	secretFile := filepath.Join(tempDir, "mysql_secret.txt")
	if err := os.WriteFile(secretFile, []byte("mysql://root:secretpass@127.0.0.1:3306/mydb\n"), 0600); err != nil {
		t.Fatalf("failed to write secret file: %v", err)
	}

	t.Setenv("TEST_DB_URL", "postgresql://postgres:supasecret@localhost:5432/postgres")
	t.Setenv("TEST_WEBHOOK_URL", "https://discord.com/api/webhooks/test")
	t.Setenv("TEST_MYSQL_URL_FILE", secretFile)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("expected Load to succeed, got: %v", err)
	}

	if cfg.PUID != DefaultPUID {
		t.Errorf("expected default PUID %d, got %d", DefaultPUID, cfg.PUID)
	}
	if cfg.PGID != DefaultPGID {
		t.Errorf("expected default PGID %d, got %d", DefaultPGID, cfg.PGID)
	}
	if len(cfg.Jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(cfg.Jobs))
	}

	job1 := cfg.Jobs[0]
	if job1.Name != "supabase-main" || job1.Engine != "postgres" {
		t.Errorf("unexpected job1 metadata: %+v", job1)
	}
	if job1.URL != "postgresql://postgres:supasecret@localhost:5432/postgres" {
		t.Errorf("unexpected job1 URL: %s", job1.URL)
	}
	if job1.WebhookURL != "https://discord.com/api/webhooks/test" {
		t.Errorf("unexpected job1 WebhookURL: %s", job1.WebhookURL)
	}

	job2 := cfg.Jobs[1]
	if job2.Name != "mysql-legacy" || job2.Engine != "mysql" {
		t.Errorf("unexpected job2 metadata: %+v", job2)
	}
	if job2.URL != "mysql://root:secretpass@127.0.0.1:3306/mydb" {
		t.Errorf("unexpected job2 URL from secret file: %s", job2.URL)
	}
}

func TestConfigLoad_ValidationErrors(t *testing.T) {
	tempDir := t.TempDir()

	testCases := []struct {
		name        string
		yaml        string
		env         map[string]string
		expectError string
	}{
		{
			name:        "empty jobs",
			yaml:        "jobs: []",
			expectError: "at least one backup job must be configured",
		},
		{
			name: "invalid job name",
			yaml: `
jobs:
  - name: "bad name with spaces"
    engine: "postgres"
    schedule: "0 2 * * *"
    url_env: "DB_URL"
`,
			expectError: "name must contain only alphanumeric characters",
		},
		{
			name: "duplicate job name",
			yaml: `
jobs:
  - name: "job1"
    engine: "postgres"
    schedule: "0 2 * * *"
    url_env: "DB_URL"
  - name: "job1"
    engine: "mysql"
    schedule: "0 2 * * *"
    url_env: "DB_URL"
`,
			env:         map[string]string{"DB_URL": "postgres://localhost/db"},
			expectError: "duplicate job name",
		},
		{
			name: "unsupported engine",
			yaml: `
jobs:
  - name: "job1"
    engine: "sqlite"
    schedule: "0 2 * * *"
    url_env: "DB_URL"
`,
			expectError: "unsupported engine",
		},
		{
			name: "missing secret",
			yaml: `
jobs:
  - name: "job1"
    engine: "postgres"
    schedule: "0 2 * * *"
    url_env: "MISSING_ENV_URL"
`,
			expectError: "resolved to empty value",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(tempDir, tc.name+".yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0644); err != nil {
				t.Fatalf("failed to write test file: %v", err)
			}

			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			_, err := Load(path)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.expectError)
			}
		})
	}
}

func TestRedactURL(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{
			input:    "postgresql://postgres.myref:supersecretpassword@aws-0-us-east-1.pooler.supabase.com:5432/postgres",
			expected: "postgresql://postgres.myref:***@aws-0-us-east-1.pooler.supabase.com:5432/postgres",
		},
		{
			input:    "mysql://user:mypassword@db.example.com:3306/production",
			expected: "mysql://user:***@db.example.com:3306/production",
		},
		{
			input:    "postgres://localhost:5432/db",
			expected: "postgres://localhost:5432/db",
		},
		{
			input:    "",
			expected: "",
		},
	}

	for _, tc := range testCases {
		got := RedactURL(tc.input)
		if got != tc.expected {
			t.Errorf("RedactURL(%q) = %q, expected %q", tc.input, got, tc.expected)
		}
	}
}
