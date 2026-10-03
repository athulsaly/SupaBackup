# SupaBackup Dashboard Architecture

## 1. Overview
SupaBackup v0.2.0 introduces an embedded, lightweight web dashboard served by the same Go binary. It provides a visual overview of backup jobs, manual trigger capabilities, and log visibility without relying on a separate container or Node.js/npm toolchain.

## 2. Package Layout (`internal/web`)
The web server logic will reside in `internal/web` with the following structure:
* `server.go`: HTTP server initialization, graceful shutdown, and port binding (`DASHBOARD_PORT`, default 8080).
* `routes.go`: Router configuration, applying middlewares (auth, CSRF, rate limit).
* `api.go`: JSON API handlers for the frontend.
* `auth.go`: Session management, password validation, and CSRF protection.
* `assets/`: Directory containing static files (HTML, CSS, Vanilla JS / vendored HTMX) bundled via `go:embed`.

## 3. Routes and the JSON API
* **UI Routes:**
  * `GET /`: Serves the embedded HTML application (overview, config, logs).
  * `GET /login`, `POST /login`: Authentication forms and handlers.
* **API Routes:**
  * `GET /healthz`: Returns overall daemon health status.
  * `GET /api/jobs`: Returns the status of all jobs (read from `internal/health/health.go`'s `DaemonStatus`).
  * `GET /api/jobs/{name}/backups`: Lists backup files for a specific job.
  * `GET /api/jobs/{name}/backups/{filename}/download`: Initiates a file download.
  * `POST /api/jobs/{name}/run`: Triggers a manual "run-now" backup.
  * `GET /api/jobs/{name}/logs`: Retrieves the recent logs for a job.
  * `GET /api/config`: Retrieves the current loaded configuration (secrets redacted).
  * `POST /api/config`: Validates and saves new config, then hot-reloads the scheduler (only if `DASHBOARD_CONFIG_WRITABLE=true`).

## 4. Authentication and Security
The dashboard is strictly opt-in. It will only start if `DASHBOARD_PASSWORD` (or `DASHBOARD_PASSWORD_FILE`) is present.
* **Authentication:** Login uses a constant-time string comparison (`subtle.ConstantTimeCompare`) against the configured password.
* **Rate Limiting:** Failed login attempts to `POST /login` will be rate-limited (e.g., using `x/time/rate`) to prevent brute-force attacks.
* **Session Management:** Successful logins generate a secure, randomly generated session token stored in an `HttpOnly`, `SameSite=Strict` cookie.
* **CSRF Protection:** All mutating endpoints (`POST`) require a CSRF token passed via headers or form values.

## 5. Run-State Registry and Log Ring Buffer
The existing `internal/health.Tracker` acts as the run-state registry.
* **Status Updates:** The UI queries the `Tracker.GetStatus()` data, exposing `LastRunTime`, `LastStatus` (pending, running, success, failure), `LastDurationMs`, and `LastError`.
* **Log Ring Buffer:** We will augment the `Tracker` (or introduce a new `Logger`) with an in-memory ring buffer (e.g., storing the last 100 log lines per job).
* **Hooking & Redaction:** Existing log lines in `internal/scheduler/scheduler.go` (e.g., during `RecordStart`, `RecordSuccess`, `handleFailure`) will be dual-written to `log.Printf` and the job's ring buffer. All lines pushed to the ring buffer will pass through `config.RedactURL` to ensure no connection strings or secrets leak.

## 6. Run-Now Concurrency
When a user calls `POST /api/jobs/{name}/run`, the API will check the `Tracker`. If `JobStatus.LastStatus == "running"`, the request is immediately rejected with HTTP 409 Conflict. If not running, the endpoint spawns a goroutine to invoke the engine's `Backup()` method (the same logic used by the cron scheduler) and updates the status to `running`, providing live feedback to the dashboard.

## 7. File Listing and Download (Path-Traversal Protection)
The API will list `.dump` files by scanning the job's specific target directory (`/backups/{name}/`).
* **Download Security:** The download endpoint accepts `{filename}`. To prevent path traversal (e.g., passing `../../etc/passwd`), the handler will sanitize the input using `filepath.Base(filename)` and verify that the fully resolved path strictly resides inside the job's designated backup directory before calling `http.ServeFile`. No secret environment variables will ever be returned in file metadata.

## 8. Config View and Hot Reload
* **View:** `GET /api/config` serializes the current in-memory struct to YAML, ensuring `ConnectionURI` and `WebhookURL` fields are masked using `RedactURL`.
* **Hot Reload:** If `DASHBOARD_CONFIG_WRITABLE` is true, the `POST /api/config` handler writes the new YAML to disk, calls a new `scheduler.Reload()` method (which stops the existing `robfig/cron` instance, rebuilds the job list, and starts a fresh cron instance), and resets the `Tracker`.

## 9. `go:embed` Assets
No external build steps (like `npm` or `webpack`) are used. The `internal/web/assets` directory will contain handwritten HTML, CSS, and vendored JS (such as HTMX). The Go compiler bundles these using `//go:embed assets/*`, allowing `http.FS` to serve them seamlessly. This approach maintains the Docker image's minimal Alpine footprint.

## 10. Compose Port Change
The TrueNAS application configuration and `docker-compose.yaml` will be updated to expose `DASHBOARD_PORT` (default `8080`). This port is served by the container and mapped to the host or TrueNAS app portal.
