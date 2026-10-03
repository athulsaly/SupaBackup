# Product Requirements Document (PRD): SupaBackup Web Dashboard (v0.2.0)

## 1. Overview
As an addendum to the core SupaBackup engine, v0.2.0 introduces a lightweight, embedded web dashboard. It allows TrueNAS and Docker users to visually monitor their backup jobs, view run histories, download backup files, and trigger on-demand runs without needing to access the terminal or raw JSON logs. 

## 2. User Stories
- As a user, I want to securely log into the dashboard so my backup configuration and files are protected from unauthorized access.
- As a user, I want an overview of all my configured backup jobs, showing their schedule, last run status, and next scheduled run.
- As a user, I want to see a list of individual backup files for a specific job and download them directly from my browser.
- As a user, I want to manually trigger a backup job ("Run now") to take an immediate snapshot before making risky database changes.
- As an administrator, I want to view recent logs for a job in the UI to troubleshoot failures without exec-ing into the container.
- As a power user, I want to view (and optionally edit) my configuration YAML via the dashboard to quickly add new jobs.

## 3. Screen List
1. **Login Screen:** A simple password-only authentication form.
2. **Dashboard / Jobs Overview:** The main landing page listing all jobs in a tabular or card format, showing engine, schedule, last run status (time, result, duration, size, redacted error), and a "Run Now" button.
3. **Job Details & Backups:** A drill-down view for a specific job listing generated backup files (name, size, date, validation status) with a download button, plus an embedded view of recent logs.
4. **Configuration View:** A screen displaying the current YAML config (with secrets redacted). If editing is enabled, this becomes a form/editor to save and apply config changes.

## 4. Acceptance Criteria

- **D-1 (Authentication):** The dashboard must only start if `DASHBOARD_PASSWORD` (or `*_FILE`) is set. It must use a login form setting a secure (HttpOnly, SameSite=Strict) session cookie.
- **D-2 (Security):** All POST requests must require a valid CSRF token. The login must use constant-time password comparison and enforce rate limiting.
- **D-3 (Port Configuration):** The web server must listen on `DASHBOARD_PORT` (default 8080) and be reachable through standard reverse proxies.
- **D-4 (Jobs Overview):** The UI must list all jobs with their respective engine, cron schedule, and the status/timestamp of the last and next runs.
- **D-5 (Run on Demand):** Users must be able to trigger a "Run Now" action for a job. The system must reject the request if the job is already actively running.
- **D-6 (File Listing & Download):** The UI must list backup files per job with metadata (name, size, date, validation result). Users must be able to download these files.
- **D-7 (Path Traversal Protection):** File downloads must be strictly constrained to the respective job's backup directory. Path traversal attempts must be blocked.
- **D-8 (Logs):** The dashboard must display a recent logs view (from an in-memory ring buffer) per job. All secrets in these logs must be passed through a RedactURL filter.
- **D-9 (Config View/Edit):** The dashboard must show a read-only configuration view with redacted secrets. If `DASHBOARD_CONFIG_WRITABLE=true`, the UI must allow editing, validate the YAML before saving, and hot-reload the scheduler upon success.
- **D-10 (Health Endpoints):** The server must expose a `GET /healthz` endpoint for external health checks, alongside `GET /api/*` endpoints providing JSON data to the UI.
- **D-11 (Data Privacy):** The HTML, API payloads, and downloaded file metadata must never expose secrets or credentials.

## 5. Non-Goals
- Multi-user authentication or RBAC (Role-Based Access Control).
- Deleting backup files via the UI.
- Restoring databases via the UI.
- HTTPS termination within the container.
- Complex SPA frameworks (Node/npm build steps are prohibited; plain HTML/CSS/vanilla JS or vendored htmx only).
