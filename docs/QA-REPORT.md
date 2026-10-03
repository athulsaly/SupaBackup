# SupaBackup QA Verification Report (WFK-5 Phase 2)

**Evaluator:** Angela (`angela-murqgucl`), Head of QA & DevOps  
**Target Commit:** `bfefa84` (Kevin's MVP implementation on `feat/mvp`)  
**QA Working Branch:** `qa/ci`  
**Evaluation Date:** 2026-10-03  
**Overall Verdict:** **FAILED (8 Passed, 2 Failed with Actionable Bugs)**  

---

## 1. Executive Summary & Quality Gate Scorecard

The SupaBackup MVP application implemented by Kevin was rebased onto `qa/ci` and subjected to the automated test harness, static verification, unit tests, and TrueNAS SCALE compose compliance checks.

* **Acceptance Criteria Passed:** 8 / 10
* **Acceptance Criteria Failed:** 2 / 10 (AC-1, AC-10)
* **Bugs Discovered:** 2 Bugs (1 High/Blocker, 1 Medium) + 1 Recommended Warning

| PRD AC # | Requirement | Status | Summary |
| :--- | :--- | :--- | :--- |
| **AC-1** | Engine Support (PostgreSQL, MySQL/MariaDB, MongoDB design) | ❌ **FAIL** | PostgreSQL backup & `pg_restore` verified clean. MySQL failed due to missing `caching_sha2_password` library in Alpine image. Mongo interface stub verified. |
| **AC-2** | Postgres Client Version (`pg_dump` >= 17) | ✅ **PASS** | Container ships `pg_dump (PostgreSQL) 17.11`. |
| **AC-3** | Minimal Alpine Base & Non-Root Execution (PUID/PGID 568) | ✅ **PASS** | Alpine 3.21 base, image footprint is 67.8 MB (<150MB target). `entrypoint.sh` correctly drops privileges via `su-exec` to 568:568. |
| **AC-4** | Multi-Job YAML Config & Secret Redaction | ✅ **PASS** | Multi-job parsing, `url_env`, and `*_FILE` secret resolution verified. Passwords masked in logs (`TestRedactURL`). |
| **AC-5** | Built-in Cron Scheduler (no host cron) | ✅ **PASS** | `robfig/cron/v3` scheduler verified with unit and start/stop concurrency tests. |
| **AC-6** | Backup Naming & Custom Output Format | ✅ **PASS** | Generates `<job-name>_YYYY-MM-DD_HH-mm.dump`. Custom format verified for Postgres. |
| **AC-7** | Validation & Non-Zero Exit on Failure | ✅ **PASS** | Verification checks file size and `pg_restore --list`. On engine failure, partial files are removed and process exits with non-zero status. |
| **AC-8** | Retention Policy Pruning | ✅ **PASS** | Automatically prunes expired dumps (>14 days) while preserving valid recent backups. |
| **AC-9** | Health Monitoring & Webhooks | ✅ **PASS** | `/var/run/supabackup/status.json` written on run; Docker `HEALTHCHECK` implemented via `/bin/supabackup check`; webhook notifier tested. |
| **AC-10** | TrueNAS Deployment & Compose Delivery | ❌ **FAIL** | `Dockerfile` and `README.md` are excellent. Root `docker-compose.yaml` failed validation due to hardcoded plaintext password on line 18. |

---

## 2. Exact Commands Executed

### A. Environment Setup & Rebase
```bash
cd /Users/athul/Projects/SupaBackup-qa
git rebase feat/mvp
```

### B. Docker Image Build & Smoke Checks
```bash
# 1. Build image
docker build -t supabackup:local .

# 2. Check Postgres client version
docker run --rm --entrypoint pg_dump supabackup:local --version
# Output: pg_dump (PostgreSQL) 17.11

# 3. Check MariaDB client version
docker run --rm --entrypoint mariadb-dump supabackup:local --version
# Output: mariadb-dump from 11.4.12-MariaDB, client 10.19 for Linux (aarch64)

# 4. Check image size
docker image inspect supabackup:local --format='{{.Size}}'
# Output: 71088836 bytes (~67.8 MB)
```

### C. Go Unit Test Suite
```bash
docker run --rm -v /Users/athul/Projects/SupaBackup-qa:/src -w /src golang:1.24-alpine go test -v ./...
# Result: PASS (all 6 packages: config, engine, health, notify, retention, scheduler)
```

### D. End-to-End Integration Test Harness
```bash
./test/run-integration-tests.sh
# Result: 14 Passed, 2 Failed
```

### E. TrueNAS SCALE Compose Specification Linting
```bash
./scripts/validate-truenas-compose.sh docker-compose.yaml
# Result: FAILED with 1 error(s) and 1 warning(s)
```

---

## 3. Discovered Bugs & Remediation Details

### 🐛 Bug 1: Missing `mariadb-connector-c` in Dockerfile (Breaks MySQL 8 Authentication)
* **File:** `Dockerfile` (Lines 18–24)
* **Severity:** **High (Blocker for MySQL Backups)**
* **Related Criteria:** PRD AC-1, ARCHITECTURE.md Section 1, REVIEW-ARCH.md Item 3
* **Description:**  
  The `Dockerfile` installs `mariadb-client`, but omits `mariadb-connector-c`. On Alpine Linux, the client authentication plugin `caching_sha2_password.so` is packaged inside `mariadb-connector-c`, not `mariadb-client`. Because MySQL 8+ defaults to `caching_sha2_password`, any attempt to connect to standard MySQL 8 servers causes `mysqldump` to crash immediately.
* **Reproduction Steps:**
  1. Build image: `docker build -t supabackup:local .`
  2. Launch MySQL 8 container with standard user:
     `docker run -d --name test-mysql -e MYSQL_DATABASE=testdb -e MYSQL_USER=testuser -e MYSQL_PASSWORD=testpass -e MYSQL_ROOT_PASSWORD=rootpass mysql:8.4`
  3. Execute SupaBackup MySQL job against `test-mysql`.
* **Observed Error (stderr):**
  ```text
  mysqldump: Got error: 1045: "Plugin caching_sha2_password could not be loaded: Error loading shared library /usr/lib/mariadb/plugin/caching_sha2_password.so: No such file or directory" when trying to connect
  ```
* **Expected Result:**  
  `mysqldump` connects, authenticates via `caching_sha2_password`, and creates the SQL dump.
* **Recommended Fix for Kevin:**  
  In `Dockerfile`, add `mariadb-connector-c` to the `apk add` package list:
  ```dockerfile
  RUN apk add --no-cache \
      postgresql17-client \
      mariadb-client \
      mariadb-connector-c \
      su-exec \
      tzdata \
      ca-certificates
  ```

---

### 🐛 Bug 2: Hardcoded Plaintext Password in Root `docker-compose.yaml`
* **File:** `docker-compose.yaml` (Line 18)
* **Severity:** **Medium (Security Compliance)**
* **Related Criteria:** PRD AC-4, AC-10
* **Description:**  
  Line 18 of the delivered `docker-compose.yaml` contains an active plaintext connection string with a hardcoded password:
  ```yaml
  - MYSQL_DB_URL=mysql://dbuser:mypassword@mysql.internal:3306/production
  ```
  This violates the security requirement that credentials must never be hardcoded into compose templates and must instead be passed via environment variables (e.g. `${MYSQL_DB_URL}`) or `.env` files.
* **Reproduction Steps:**
  ```bash
  ./scripts/validate-truenas-compose.sh docker-compose.yaml
  ```
* **Observed Output:**
  ```text
  [FAIL] Hardcoded plaintext database connection string detected in compose file. Use environment variables or *_FILE secrets.
  ```
* **Expected Result:**  
  All sensitive variables must use variable interpolation or reference secret files.
* **Recommended Fix for Kevin:**  
  Update line 18 in `docker-compose.yaml` to match `SUPABASE_DB_URL`:
  ```yaml
  - MYSQL_DB_URL=${MYSQL_DB_URL:-}
  ```

---

### ⚠️ Warning 1: Missing Compose-Level `healthcheck:` Block in `docker-compose.yaml`
* **File:** `docker-compose.yaml`
* **Severity:** **Low (Usability in TrueNAS SCALE UI)**
* **Description:**  
  TrueNAS SCALE Custom Apps UI utilizes compose-level `healthcheck:` declarations to render the service status badge. While the `Dockerfile` includes a `HEALTHCHECK`, defining it in `docker-compose.yaml` ensures consistent monitoring in TrueNAS.
* **Recommended Fix for Kevin:**  
  Add to `docker-compose.yaml`:
  ```yaml
      healthcheck:
        test: ["CMD", "/bin/supabackup", "check"]
        interval: 30s
        timeout: 5s
        retries: 3
  ```

---

## 4. Test Harness Enhancements Committed on `qa/ci`
In accordance with QA responsibilities, the following improvements were committed to `qa/ci`:
1. **`test/run-integration-tests.sh`**:
   - Converted assertion framework to continue across jobs so failures in one engine (MySQL) do not prevent validating healthy engines (Postgres), recording full pass/fail tallies.
   - Updated container invocation to use the `/bin/supabackup run-now` subcommand for immediate synchronous execution during integration testing.
2. **`scripts/validate-truenas-compose.sh`**:
   - Enhanced regex to support both Docker Compose mapping syntax (`PUID: 568`) and list syntax (`- PUID=568`).
