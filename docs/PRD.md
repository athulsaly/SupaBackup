# Product Requirements Document (PRD): SupaBackup

## 1. Overview
SupaBackup is a lightweight Docker container designed to run on TrueNAS SCALE (or any Docker host) as an automatic backup agent for cloud-hosted databases. It targets Supabase (Postgres) as a first-class citizen, alongside support for other cloud databases like Neon, RDS, and MySQL/MariaDB.

## 2. Personas
- **The TrueNAS Homelabber:** A user who self-hosts storage and wants to leverage their local NAS (TrueNAS SCALE) to keep secure, automated, off-cloud backups of their cloud database projects.
- **The Indie Hacker/Developer:** A developer using Supabase or other managed databases who needs a zero-maintenance, set-and-forget backup solution without paying for expensive cloud retention tiers.

## 3. User Stories
- As a user, I want to deploy a single Docker container on TrueNAS that periodically backs up my Supabase database to a local dataset.
- As a user, I want to configure multiple backup jobs (e.g., for different projects or database engines) within a single container.
- As a security-conscious user, I want to ensure my database credentials are provided securely via environment variables and never logged or exposed.
- As an administrator, I want old backups to be automatically pruned based on a retention policy so that I don't run out of disk space.
- As an administrator, I want to be notified via a webhook if a backup fails or succeeds, and I want a healthcheck mechanism to monitor the container's status.

## 4. Acceptance Criteria
1. **Engine Support:** The MVP must support PostgreSQL (Supabase, Neon, RDS, etc.) and MySQL/MariaDB. MongoDB is designed for but optional.
2. **Postgres Client Version:** The container image must ship with a `pg_dump` client version equal to or newer than Supabase's current Postgres version (PG 15/17).
3. **Container Base:** The image must be small, based on Alpine or Debian-slim, and run as a non-root user (with configurable `PUID` and `PGID`, defaulting to 568 for TrueNAS apps).
4. **Configuration:** Multiple backup jobs must be configurable via a YAML file, with secrets sourced exclusively from environment variables or `*_FILE` files. No secrets in logs.
5. **Scheduling:** The application must include a built-in scheduler (cron expressions) for triggering backups independently of the host's cron system.
6. **Backup Output:** Backup files must be named using the job name and timestamp (e.g., `<job-name>_$(date +%F_%H-%M).dump`). For Postgres, the format must be `custom`.
7. **Validation:** Each generated backup must be validated to ensure it is not empty. For Postgres, it should ideally be checked with `pg_restore --list`. Non-zero exits must be triggered on failure.
8. **Retention Policy:** The system must delete backup files older than `RETENTION_DAYS` (default 14) on a per-job basis.
9. **Monitoring & Health:** The container must provide a Docker HEALTHCHECK, maintain a status JSON/file, and optionally send a webhook notification on success/failure.
10. **Delivery & Deployment:** A Dockerfile, `docker-compose.yaml` (for TrueNAS "Install via YAML" support), and README instructions for TrueNAS deployment must be provided. Tests and a CI build workflow must be included.

## 5. Config Surface

### YAML Configuration (`config.yaml`)
```yaml
jobs:
  - name: supabase-prod
    engine: postgres
    schedule: "0 2 * * *" # Daily at 2 AM
    retention_days: 14
    url_env: SUPABASE_DB_URL # Name of env var holding connection string
    webhook_url_env: SLACK_WEBHOOK_URL # Optional webhook

  - name: internal-mysql
    engine: mysql
    schedule: "0 3 * * *"
    retention_days: 7
    url_env: MYSQL_DB_URL
```

### Environment Variables
- `PUID`: User ID to run the process (default: `568`).
- `PGID`: Group ID to run the process (default: `568`).
- `CONFIG_PATH`: Path to the YAML configuration file (default: `/etc/supabackup/config.yaml`).
- `BACKUP_DIR`: Directory where backups are saved (default: `/backups`).
- `*_FILE`: Any variable can have a `_FILE` counterpart (e.g., `SUPABASE_DB_URL_FILE`) to read secrets from Docker secrets/mounted files.
- Arbitrary secret variables referenced by the `url_env` or `webhook_url_env` in the YAML (e.g., `SUPABASE_DB_URL`, `MYSQL_DB_URL`).

## 6. Non-Goals (Out of Scope for MVP)
- **Web UI:** No graphical interface for configuring or monitoring jobs.
- **Restore Automation:** The tool will not automate the restore process (restores will be documented manual steps using `pg_restore` or equivalent).
- **Off-site Sync:** Uploading backups to S3, Google Drive, or other remote storage is not included. The container assumes local storage mapping.
