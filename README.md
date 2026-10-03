# SupaBackup

**SupaBackup** is an automated, lightweight Docker backup agent designed specifically for **TrueNAS SCALE** (and any Docker host) to reliably back up cloud-hosted databases to local storage datasets.

It features first-class support for **Supabase (PostgreSQL)**, generic PostgreSQL (Neon, AWS RDS, GCP Cloud SQL), and **MySQL / MariaDB**.

---

## Features

- **Automated Cron Scheduling**: Native built-in cron scheduler per job (no host cron dependencies).
- **PostgreSQL 17+ Client**: Ships with `postgresql17-client` to support modern PostgreSQL versions (15, 16, 17) used by Supabase.
- **Custom Dump Format**: Produces compressed PostgreSQL custom dumps (`--format=custom`) and verifies them via `pg_restore --list`.
- **MySQL / MariaDB Support**: Uses `mysqldump` with non-locking transactions (`--single-transaction --quick`).
- **Retention Lifecycle**: Automatically purges expired backups per job based on configured `retention_days`.
- **TrueNAS SCALE Permissions**: Runs as a non-root user matching TrueNAS `apps` user permissions (`PUID=568`, `PGID=568`).
- **Secret Protection**: Accepts database credentials via environment variables or `*_FILE` secret mounts; passwords are redacted and never logged.
- **Health Checks & Alerts**: Docker `HEALTHCHECK` backed by a local `/var/run/supabackup/status.json` file and optional Slack/Discord webhook notifications.

---

## TrueNAS SCALE Deployment Guide

### 1. Create Storage Dataset
1. In TrueNAS SCALE, navigate to **Datasets**.
2. Create a new dataset under your storage pool, e.g.:
   ```text
   /mnt/tank/supabase-backups
   ```
3. Set the dataset permissions so the built-in TrueNAS `apps` user (UID `568`, GID `568`) has read/write access.

### 2. Prepare Configuration
Create a configuration directory and file on your TrueNAS dataset, e.g. at `/mnt/tank/supabase-backups/config/config.yaml`:

```yaml
jobs:
  - name: "supabase-prod"
    engine: "postgres"
    schedule: "0 2 * * *" # Daily at 02:00 UTC
    retention_days: 14
    url_env: "SUPABASE_DB_URL"
    webhook_url_env: "SLACK_WEBHOOK_URL"

  - name: "mysql-app"
    engine: "mysql"
    schedule: "0 4 * * 0" # Weekly on Sunday at 04:00 UTC
    retention_days: 30
    url_env: "MYSQL_DB_URL"
```

### 3. Deploy via TrueNAS SCALE "Install via YAML"
In TrueNAS SCALE, go to **Apps** > **Discover Apps** > **Custom App** (or **Install via YAML**) and paste:

```yaml
services:
  supabackup:
    image: ghcr.io/athulsaly/supabackup:latest
    container_name: supabackup
    restart: unless-stopped
    environment:
      - PUID=568
      - PGID=568
      - TZ=UTC
      - CONFIG_PATH=/etc/supabackup/config.yaml
      - BACKUP_DIR=/backups
      - SUPABASE_DB_URL=${SUPABASE_DB_URL:-}
      - MYSQL_DB_URL=${MYSQL_DB_URL:-}
      - SLACK_WEBHOOK_URL=${SLACK_WEBHOOK_URL:-}
      - DISCORD_WEBHOOK_URL=${DISCORD_WEBHOOK_URL:-}
    volumes:
      - /mnt/tank/supabase-backups:/backups
      - /mnt/tank/supabase-backups/config/config.yaml:/etc/supabackup/config.yaml:ro
    healthcheck:
      test: ["CMD", "/bin/supabackup", "check"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 10s
```

### 4. Local Build Fallback (TrueNAS Shell / Air-Gapped)
If you prefer building the container image directly on your TrueNAS host shell instead of pulling from GHCR:
```bash
# 1. Open Shell in TrueNAS Web UI or SSH into host
# 2. Clone the repository
git clone https://github.com/athulsaly/SupaBackup.git
cd SupaBackup

# 3. Build the Docker image locally
docker build -t supabackup:latest .

# 4. In docker-compose.yaml, replace image with supabackup:latest:
#    image: supabackup:latest
```

---

## Supabase Connection Strings

When configuring backups for Supabase, **use the Session Pooler connection string on port 5432**.

### Recommended Connection URI
```text
postgresql://postgres.[PROJECT-REF]:[PASSWORD]@aws-0-us-east-1.pooler.supabase.com:5432/postgres
```

> **Why Session Pooler on port 5432?**
> - **Port 5432 (Session Mode)**: Fully supports session-level PostgreSQL parameters and commands required by `pg_dump`.
> - **Port 6543 (Transaction Mode)**: Transaction poolers do NOT support the session commands and locking behavior required by `pg_dump`.
> - **IPv4 Compatibility**: The Supabase pooler hostnames resolve cleanly to IPv4 addresses on homelab and TrueNAS networks.

---

## Secret Management

Credentials can be passed via direct environment variables or using `*_FILE` paths for Docker / Kubernetes secret mounts:

| Environment Variable | Secret File Variable | Description |
|----------------------|----------------------|-------------|
| `SUPABASE_DB_URL`    | `SUPABASE_DB_URL_FILE` | Connection string for Supabase |
| `MYSQL_DB_URL`       | `MYSQL_DB_URL_FILE`    | Connection string for MySQL |
| `SLACK_WEBHOOK_URL`  | `SLACK_WEBHOOK_URL_FILE` | Webhook URL for alerts |

When using `*_FILE`, SupaBackup reads and trims the secret file at container startup. All logged URLs and error reports automatically redact passwords (e.g. `postgresql://user:***@host:5432/db`).

---

## Volume & Storage Layout

Backups are partitioned by job in the target volume:

```text
/backups/
├── supabase-prod/
│   ├── supabase-prod_2026-10-01_02-00.dump
│   └── supabase-prod_2026-10-02_02-00.dump
└── mysql-app/
    └── mysql-app_2026-09-27_04-00.dump
```

---

## Manual Restore Guide

SupaBackup produces standard database dumps that can be restored with native tools at any time.

### Restoring PostgreSQL / Supabase (`pg_restore`)

#### 1. Inspect Backup Contents
Verify the archive headers and view the list of dumped database objects:
```bash
pg_restore --list /backups/supabase-prod/supabase-prod_2026-10-03_02-00.dump
```

#### 2. Full Database Restore

> [!NOTE]
> When using PostgreSQL custom format dumps (`--format=custom`), ownership and privilege metadata are captured in the archive. To prevent restore errors when targeting cloud-hosted databases without superuser rights (e.g. Supabase, Neon, AWS RDS), always supply `--no-owner` and `--no-privileges` at `pg_restore` time.

To restore into a database instance:
```bash
pg_restore \
  --dbname="$RESTORE_TARGET_URL" \
  --clean \
  --if-exists \
  --no-owner \
  --no-privileges \
  --verbose \
  /backups/supabase-prod/supabase-prod_2026-10-03_02-00.dump
```

#### 3. Single Schema or Table Restore
To restore only a specific table:
```bash
pg_restore \
  --dbname="$RESTORE_TARGET_URL" \
  --table=my_table \
  --data-only \
  /backups/supabase-prod/supabase-prod_2026-10-03_02-00.dump
```

### Restoring MySQL / MariaDB

To restore a MySQL backup:
```bash
mysql -h db.internal -u dbuser -p target_database < /backups/mysql-app/mysql-app_2026-10-03_04-00.dump
```

---

## Health Checks & CLI Usage

The `supabackup` binary includes subcommands for container management:

```bash
# Start backup daemon (default)
supabackup run

# Run healthcheck (evaluates consecutive failures from status.json)
supabackup check

# Execute an immediate backup run for a job without waiting for cron
supabackup run-now --job=supabase-prod
```
