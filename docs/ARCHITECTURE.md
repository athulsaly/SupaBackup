# SupaBackup Architecture

## 1. Overview and Technology Stack
SupaBackup is a lightweight, standalone daemon designed to automatically back up cloud-hosted databases (PostgreSQL, MySQL/MariaDB) to a local TrueNAS dataset.

* **Language:** **Go (Golang)**. Go is chosen because it produces a single, statically linked binary, ensuring the final Docker image remains exceptionally small. It also boasts excellent standard libraries for concurrency (needed for scheduling), HTTP (webhooks, health checks), and YAML parsing.
* **Base Image:** **Alpine Linux**. Alpine keeps the image minimal. The image will install `postgresql17-client` (ensuring `pg_dump` >= 17) and `mariadb-client` (must verify the dump works against MySQL 8). 
* **Privileges:** The container will support `PUID` and `PGID` environment variables. By default, it will drop privileges using `su-exec` or a dedicated entrypoint script to run the backup process as the specified non-root user (defaulting to TrueNAS `apps` UID/GID 568), ensuring the TrueNAS dataset permissions remain pristine.

## 2. Module Layout
The Go project will follow a standard layout:
* `cmd/supabackup/`: Application entrypoint (main).
* `internal/config/`: YAML parsing, environment variable substitution, and `*_FILE` secret resolution.
* `internal/scheduler/`: Cron-based job scheduling and concurrency management.
* `internal/engine/`: The database backup plug-in implementations.
* `internal/retention/`: File lifecycle management and cleanup logic.
* `internal/health/`: Status tracking and Docker HEALTHCHECK endpoints.
* `internal/notify/`: Webhook integrations for success/failure alerts.

## 3. Engine Plug-in Interface
To support multiple database types, backup logic is abstracted into an `Engine` interface:

```go
type Engine interface {
    // Backup executes the dump and writes the output to targetPath
    Backup(ctx context.Context, job Config, targetPath string) error
    
    // Verify checks the integrity of the backup (e.g., size check, pg_restore --list)
    Verify(ctx context.Context, targetPath string) error
}
```
* **Postgres Engine:** Uses `pg_dump` with `--format=custom`. Verification uses `pg_restore --list`.
* **MySQL Engine:** Uses `mysqldump`. Verification relies on file size checks and basic EOF marker checks.
* **Mongo Engine:** Stub implementation for MVP; designed to eventually wrap `mongodump`.

## 4. Configuration Schema
Jobs are defined in a YAML configuration file (`/etc/supabackup/config.yaml`), with sensitive fields sourced exclusively from environment variables or `*_FILE` equivalents.

```yaml
jobs:
  - name: "supabase-main"
    engine: "postgres"
    schedule: "0 2 * * *" # Daily at 2 AM
    url_env: SUPABASE_DB_URL # Engine looks up $SUPABASE_DB_URL or $SUPABASE_DB_URL_FILE
    retention_days: 14
    webhook_url_env: SLACK_WEBHOOK_URL # Optional webhook

  - name: "mysql-legacy"
    engine: "mysql"
    schedule: "0 4 * * 0" # Weekly on Sunday
    url_env: MYSQL_DB_URL
    retention_days: 30
```

## 5. Subsystems

### Scheduler
Uses a robust Go cron library (like `robfig/cron`) to parse standard cron expressions. Each job runs in its own goroutine to prevent long-running backups from blocking others.

### Retention Policy
After a successful backup, the retention module scans the job's target directory. It parses timestamps from the filenames (e.g., `<job-name>_<timestamp>.dump`), sorts them, and deletes files older than the configured `retention_days`.

### Verification
Every completed backup triggers a verification phase:
1. **Size Check:** Fails if the file is empty (0 bytes).
2. **Engine-specific Check:** The Postgres engine executes `pg_restore --list <file>`. If it fails, the backup is marked invalid and an alert is triggered.

### Status and Health
The daemon maintains a state map of the last run status for each job. This state is dumped to a JSON file (e.g., `/var/run/supabackup/status.json`). The Docker `HEALTHCHECK` instruction runs a minimal script (or a sub-command `supabackup check`) that reads this file and returns non-zero if any recent jobs have persistently failed.

### Webhooks
An asynchronous notifier queues events for job success or failure and dispatches HTTP POST requests to the configured endpoints (useful for Discord, Slack, or generic webhooks).

## 6. Volume Layout (TrueNAS Dataset)
The TrueNAS dataset is bind-mounted to `/backups` inside the container. The storage layout is organized by job:

```text
/backups/
├── supabase-main/
│   ├── supabase-main_2026-10-01_02-00.dump
│   └── supabase-main_2026-10-02_02-00.dump
└── mysql-legacy/
    └── mysql-legacy_2026-09-27_04-00.dump
```

## 7. Test Strategy Outline (for Angela)
* **Unit Tests:** 
  - `config`: Validate YAML parsing, fallback to environment variables, and reading from `_FILE` paths.
  - `retention`: Mock the filesystem to test date parsing and deletion logic based on days.
  - `scheduler`: Verify cron expression parsing and trigger frequencies.
* **Integration Tests (Testcontainers):**
  - Spin up ephemeral Postgres and MySQL containers.
  - Seed them with dummy data.
  - Run the `supabackup` engine implementations against these containers.
  - Verify that the resulting dump files are created, non-empty, and successfully pass engine-specific verification (`pg_restore --list`).
